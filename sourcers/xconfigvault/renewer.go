package xconfigvault

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/vault-client-go"
	"github.com/hashicorp/vault-client-go/schema"
)

const (
	defaultRenewFraction      = 0.8
	defaultNearExpiryMinutes  = 5
	defaultCheckIntervalSec   = 60
	defaultMaxBackoffSec      = 30
	loginDeadline             = 2 * time.Minute
	renewDeadline             = 1 * time.Minute
	initialBackoff            = 1 * time.Second
)

// tokenRenewer manages token lifecycle: background renewal, re-login, and coalescing.
type tokenRenewer struct {
	vaultClient *vault.Client
	auth        AuthMethod
	metrics     MetricsCallback
	cfg         *RenewConfig

	mu       sync.RWMutex
	renewing bool
	expiry    time.Time
	lease     time.Duration
	renewable bool

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newTokenRenewer(client *vault.Client, auth AuthMethod, metrics MetricsCallback, cfg *RenewConfig) *tokenRenewer {
	if cfg == nil {
		cfg = DefaultRenewConfig()
	}
	return &tokenRenewer{
		vaultClient: client,
		auth:        auth,
		metrics:     metrics,
		cfg:         cfg,
	}
}

// start begins the background token renewal loop.
func (r *tokenRenewer) start(ctx context.Context) {
	ctx, r.cancel = context.WithCancel(ctx)

	// Lookup current token to initialize lease info.
	r.initTokenInfo(ctx)

	r.wg.Add(1)
	go r.loop(ctx)
}

// stop cancels the renewal loop and waits for it to finish.
func (r *tokenRenewer) stop() {
	if r.cancel != nil {
		r.cancel()
		r.wg.Wait()
	}
}

// initTokenInfo fetches the current token's TTL and renewable status.
func (r *tokenRenewer) initTokenInfo(ctx context.Context) {
	resp, err := r.vaultClient.Auth.TokenLookUpSelf(ctx)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	ttl := toFloat64(resp.Data["ttl"])
	if ttl > 0 {
		r.lease = time.Duration(ttl) * time.Second
		r.expiry = time.Now().Add(time.Duration(ttl*r.cfg.Fraction) * time.Second)
	}
	if ren, ok := resp.Data["renewable"].(bool); ok {
		r.renewable = ren
	}
}

// toFloat64 converts a JSON number value to float64.
// Handles float64, json.Number, int, and int64.
func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func (r *tokenRenewer) loop(ctx context.Context) {
	defer r.wg.Done()
	ticker := time.NewTicker(r.cfg.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

func (r *tokenRenewer) tick(ctx context.Context) {
	r.mu.RLock()
	expiry := r.expiry
	lease := r.lease
	renewable := r.renewable
	r.mu.RUnlock()

	if expiry.IsZero() {
		return
	}

	timeUntilExpiry := time.Until(expiry)

	// Determine if we're in the renew window (last portion of the lease).
	renewWindow := time.Duration(float64(lease) * (1 - r.cfg.Fraction))
	inRenewWindow := renewable && lease > 0 && timeUntilExpiry < renewWindow

	// Cap nearExpiryThreshold for short leases.
	nearThreshold := r.cfg.NearExpiryThreshold
	fractionWindow := time.Duration(float64(lease) * r.cfg.Fraction)
	if fractionWindow > 0 && nearThreshold > fractionWindow/2 {
		nearThreshold = fractionWindow / 2
	}
	needRefresh := timeUntilExpiry <= 0 || timeUntilExpiry < nearThreshold

	if inRenewWindow {
		if err := r.withBackoff(ctx, r.tryRenewSelf, renewDeadline); err != nil {
			r.emitEvent(EventTokenRenewFailed, err)
			// Fallback to full re-login.
			if err := r.relogin(ctx); err != nil {
				r.emitEvent(EventReloginFailed, err)
			}
		}
	} else if needRefresh {
		if err := r.relogin(ctx); err != nil {
			r.emitEvent(EventReloginFailed, err)
		}
	}
}

// refreshNow forces an immediate token refresh.
// If a refresh is already in progress, it returns immediately (the retry loop
// in fetchWithRetry will handle subsequent attempts).
func (r *tokenRenewer) refreshNow(ctx context.Context) error {
	r.mu.Lock()

	// If token is still valid, no need to refresh.
	if !r.expiry.IsZero() && time.Until(r.expiry) > r.cfg.NearExpiryThreshold {
		r.mu.Unlock()
		return nil
	}

	// If another refresh is in progress, skip — let the caller retry.
	if r.renewing {
		r.mu.Unlock()
		return nil
	}

	r.renewing = true
	r.mu.Unlock()

	err := r.relogin(ctx)

	r.mu.Lock()
	r.renewing = false
	r.mu.Unlock()

	return err
}

// tryRenewSelf attempts to renew the current token via the renew-self endpoint.
func (r *tokenRenewer) tryRenewSelf(ctx context.Context) error {
	resp, err := r.vaultClient.Auth.TokenRenewSelf(ctx, schema.TokenRenewSelfRequest{})
	if err != nil {
		return fmt.Errorf("renew-self: %w", err)
	}

	if resp.Auth == nil || resp.Auth.LeaseDuration == 0 {
		return fmt.Errorf("renew-self: invalid response, no lease duration")
	}

	newLease := time.Duration(resp.Auth.LeaseDuration) * time.Second
	newExpiry := time.Now().Add(time.Duration(float64(newLease) * r.cfg.Fraction))

	r.mu.Lock()
	r.expiry = newExpiry
	r.lease = newLease
	r.renewable = resp.Auth.Renewable
	r.mu.Unlock()

	r.emitEvent(EventTokenRenewed, nil)
	return nil
}

// relogin performs a full re-authentication.
func (r *tokenRenewer) relogin(ctx context.Context) error {
	err := r.withBackoff(ctx, func(ctx context.Context) error {
		return r.auth.Relogin(ctx, r.vaultClient)
	}, loginDeadline)
	if err != nil {
		return err
	}

	// After re-login, fetch new token info.
	r.initTokenInfo(ctx)
	r.emitEvent(EventReloginSuccess, nil)
	return nil
}

// withBackoff retries fn with exponential backoff until deadline.
func (r *tokenRenewer) withBackoff(ctx context.Context, fn func(context.Context) error, deadline time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	backoff := initialBackoff
	var lastErr error

	for {
		lastErr = fn(ctx)
		if lastErr == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return lastErr
		case <-time.After(backoff):
		}

		backoff = min(backoff*2, r.cfg.MaxBackoff)
	}
}

func (r *tokenRenewer) emitEvent(typ EventType, err error) {
	if r.metrics == nil {
		return
	}
	r.metrics.OnEvent(Event{
		Type:      typ,
		Error:     err,
		Timestamp: time.Now(),
	})
}
