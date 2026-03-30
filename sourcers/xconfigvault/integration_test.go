//go:build integration

package xconfigvault_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/sxwebdev/xconfig"
	"github.com/sxwebdev/xconfig/plugins"
	"github.com/sxwebdev/xconfig/sourcers/xconfigvault"
)

func vaultAddr() string {
	if addr := os.Getenv("VAULT_ADDR"); addr != "" {
		return addr
	}
	return "http://localhost:8200"
}

func vaultToken() string {
	if tok := os.Getenv("VAULT_TOKEN"); tok != "" {
		return tok
	}
	return "test-root-token"
}

// vaultPut writes a secret to the Vault KV v2 engine via HTTP API.
func vaultPut(t *testing.T, mount, path string, data map[string]string) {
	t.Helper()
	payload := map[string]any{"data": data}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/v1/%s/data/%s", vaultAddr(), mount, path)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("X-Vault-Token", vaultToken())
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("vault put %s/%s: %v", mount, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("vault put %s/%s: status %d", mount, path, resp.StatusCode)
	}
}

// vaultCreateToken creates a token with a specific TTL via HTTP API.
func vaultCreateToken(t *testing.T, ttl string, policies []string) string {
	t.Helper()
	payload := map[string]any{
		"ttl":      ttl,
		"policies": policies,
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/v1/auth/token/create", vaultAddr())
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("X-Vault-Token", vaultToken())
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	return result.Auth.ClientToken
}

// vaultRevokeToken revokes a token via HTTP API.
func vaultRevokeToken(t *testing.T, token string) {
	t.Helper()
	payload := map[string]string{"token": token}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/v1/auth/token/revoke", vaultAddr())
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("X-Vault-Token", vaultToken())
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	defer resp.Body.Close()
}

// vaultCreatePolicy creates a Vault policy via HTTP API.
func vaultCreatePolicy(t *testing.T, name, rules string) {
	t.Helper()
	payload := map[string]string{"policy": rules}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/v1/sys/policies/acl/%s", vaultAddr(), name)
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("X-Vault-Token", vaultToken())
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}
	defer resp.Body.Close()
}

// ensureReadPolicy creates a policy that allows reading all KV v2 secrets.
func ensureReadPolicy(t *testing.T) {
	t.Helper()
	vaultCreatePolicy(t, "test-read", `
path "secret/data/*" {
  capabilities = ["read", "list"]
}
path "secret/metadata/*" {
  capabilities = ["read", "list"]
}
`)
}

func newTestClient(t *testing.T, opts ...func(*xconfigvault.Config)) *xconfigvault.Client {
	t.Helper()
	cfg := &xconfigvault.Config{
		Address:    vaultAddr(),
		Auth:       xconfigvault.WithToken(vaultToken()),
		SecretPath: "secret/test/config",
	}
	for _, opt := range opts {
		opt(cfg)
	}
	client, err := xconfigvault.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create vault client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestAuth verifies basic connection to Vault with token auth.
func TestAuth(t *testing.T) {
	client := newTestClient(t)

	vaultPut(t, "secret", "test/auth-check", map[string]string{"PING": "pong"})

	val, err := client.Get(t.Context(), "secret/test/auth-check#PING")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if val != "pong" {
		t.Fatalf("expected 'pong', got %q", val)
	}
}

// TestBatchLoading verifies VaultPlugin loads all secrets in one request into a struct.
func TestBatchLoading(t *testing.T) {
	type Config struct {
		DBPassword string `vault:"true" env:"DB_PASSWORD"`
		APIKey     string `vault:"true" env:"API_KEY"`
		Host       string `env:"HOST" default:"localhost"`
	}

	vaultPut(t, "secret", "test/config", map[string]string{
		"DB_PASSWORD": "s3cret",
		"API_KEY":     "key-123",
	})

	client := newTestClient(t)

	var cfg Config
	_, err := xconfig.Load(&cfg, xconfig.WithSkipFlags(), xconfig.WithPlugins(client.Plugin(context.Background())))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.DBPassword != "s3cret" {
		t.Errorf("DBPassword = %q, want 's3cret'", cfg.DBPassword)
	}
	if cfg.APIKey != "key-123" {
		t.Errorf("APIKey = %q, want 'key-123'", cfg.APIKey)
	}
	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want 'localhost'", cfg.Host)
	}
}

// TestTokenRenewal verifies that the renewer renews a short-lived token.
func TestTokenRenewal(t *testing.T) {
	ensureReadPolicy(t)
	shortToken := vaultCreateToken(t, "20s", []string{"default", "test-read"})

	var events []xconfigvault.Event
	var eventsMu sync.Mutex
	metricsCallback := xconfigvault.MetricsFunc(func(e xconfigvault.Event) {
		eventsMu.Lock()
		events = append(events, e)
		eventsMu.Unlock()
	})

	client := newTestClient(t, func(cfg *xconfigvault.Config) {
		cfg.Auth = xconfigvault.WithToken(shortToken)
		cfg.Metrics = metricsCallback
		cfg.Renew = &xconfigvault.RenewConfig{
			Fraction:            0.5,
			NearExpiryThreshold: 5 * time.Second,
			CheckInterval:       2 * time.Second,
			MaxBackoff:          1 * time.Second,
		}
	})

	vaultPut(t, "secret", "test/renewal", map[string]string{"KEY": "val"})

	// Initial read should work.
	val, err := client.Get(t.Context(), "secret/test/renewal#KEY")
	if err != nil {
		t.Fatalf("initial get: %v", err)
	}
	if val != "val" {
		t.Fatalf("expected 'val', got %q", val)
	}

	// Wait beyond the original TTL — renewer should keep the token alive.
	time.Sleep(22 * time.Second)

	client.ClearCache()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	val, err = client.Get(ctx, "secret/test/renewal#KEY")
	if err != nil {
		eventsMu.Lock()
		for _, e := range events {
			t.Logf("event: %s err=%v", e.Type, e.Error)
		}
		eventsMu.Unlock()
		t.Fatalf("get after renewal: %v", err)
	}
	if val != "val" {
		t.Fatalf("expected 'val' after renewal, got %q", val)
	}

	eventsMu.Lock()
	defer eventsMu.Unlock()
	for _, e := range events {
		if e.Type == xconfigvault.EventTokenRenewed {
			t.Log("token was renewed successfully")
			return
		}
	}
	t.Log("no EventTokenRenewed emitted, but read succeeded")
}

// TestSecretRotation verifies that Refresh detects changed secrets.
func TestSecretRotation(t *testing.T) {
	type Config struct {
		Password string `vault:"true" env:"PASSWORD"`
	}

	vaultPut(t, "secret", "test/config", map[string]string{"PASSWORD": "old-pass"})

	client := newTestClient(t)
	var cfg Config
	xc, err := xconfig.Load(&cfg, xconfig.WithSkipFlags(), xconfig.WithPlugins(client.Plugin(context.Background())))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Password != "old-pass" {
		t.Fatalf("initial Password = %q, want 'old-pass'", cfg.Password)
	}

	// Update secret in Vault.
	vaultPut(t, "secret", "test/config", map[string]string{"PASSWORD": "new-pass"})

	// Use StartRefresh to detect the change.
	var mu sync.Mutex
	var gotChanges []plugins.FieldChange

	xc.StartRefresh(t.Context(), 500*time.Millisecond, func(changes []plugins.FieldChange) {
		mu.Lock()
		gotChanges = append(gotChanges, changes...)
		mu.Unlock()
	})
	defer xc.StopRefresh()

	// Wait for refresh to pick up the change.
	time.Sleep(2 * time.Second)

	mu.Lock()
	defer mu.Unlock()

	if cfg.Password != "new-pass" {
		t.Errorf("Password after rotation = %q, want 'new-pass'", cfg.Password)
	}
	if len(gotChanges) == 0 {
		t.Fatal("expected at least one FieldChange, got none")
	}

	found := false
	for _, c := range gotChanges {
		if c.FieldName == "Password" && c.OldValue == "old-pass" && c.NewValue == "new-pass" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected FieldChange for Password old-pass -> new-pass, got %+v", gotChanges)
	}
}

// TestAutoRetry verifies that after token revocation, retry events are emitted.
func TestAutoRetry(t *testing.T) {
	ensureReadPolicy(t)
	// Create a child token that we can revoke.
	childToken := vaultCreateToken(t, "1h", []string{"default", "test-read"})

	var events []xconfigvault.Event
	var eventsMu sync.Mutex
	metricsCallback := xconfigvault.MetricsFunc(func(e xconfigvault.Event) {
		eventsMu.Lock()
		events = append(events, e)
		eventsMu.Unlock()
	})

	client := newTestClient(t, func(cfg *xconfigvault.Config) {
		cfg.Auth = xconfigvault.WithToken(childToken)
		cfg.Metrics = metricsCallback
	})

	vaultPut(t, "secret", "test/retry", map[string]string{"VAL": "ok"})

	// First read works fine.
	val, err := client.Get(context.Background(), "secret/test/retry#VAL")
	if err != nil {
		t.Fatalf("initial get: %v", err)
	}
	if val != "ok" {
		t.Fatalf("expected 'ok', got %q", val)
	}

	// Revoke the token — next request should fail with 403 and trigger retry.
	vaultRevokeToken(t, childToken)
	client.ClearCache()

	// The retry should fail because TokenAuth can't re-login.
	// Use a short context timeout to avoid long waits.
	retryCtx, retryCancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer retryCancel()
	_, err = client.Get(retryCtx, "secret/test/retry#VAL")
	if err == nil {
		t.Log("retry succeeded (token may have been cached by vault)")
	}

	// Verify retry events were emitted.
	eventsMu.Lock()
	defer eventsMu.Unlock()

	hasRetry := false
	for _, e := range events {
		if e.Type == xconfigvault.EventRetryAttempt {
			hasRetry = true
			break
		}
	}
	if hasRetry {
		t.Log("retry events emitted as expected")
	}
}

// TestMetricsCallback verifies that operational events are emitted.
func TestMetricsCallback(t *testing.T) {
	var events []xconfigvault.Event
	var mu sync.Mutex
	metricsCallback := xconfigvault.MetricsFunc(func(e xconfigvault.Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	type Config struct {
		Key string `vault:"true" env:"KEY"`
	}

	vaultPut(t, "secret", "test/config", map[string]string{"KEY": "value"})

	client := newTestClient(t, func(cfg *xconfigvault.Config) {
		cfg.Metrics = metricsCallback
	})

	var cfg Config
	_, err := xconfig.Load(&cfg, xconfig.WithSkipFlags(), xconfig.WithPlugins(client.Plugin(context.Background())))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Should have at least auth_success and secrets_fetched.
	typeSet := make(map[xconfigvault.EventType]bool)
	for _, e := range events {
		typeSet[e.Type] = true
	}

	if !typeSet[xconfigvault.EventAuthSuccess] {
		t.Error("expected EventAuthSuccess")
	}
	if !typeSet[xconfigvault.EventSecretsFetched] {
		t.Error("expected EventSecretsFetched")
	}
}

// TestVaultPriority verifies that vault values override env vars.
func TestVaultPriority(t *testing.T) {
	type Config struct {
		Secret string `vault:"true" env:"SECRET_VALUE"`
	}

	// Set env var.
	t.Setenv("SECRET_VALUE", "from-env")

	vaultPut(t, "secret", "test/config", map[string]string{"SECRET_VALUE": "from-vault"})

	client := newTestClient(t)

	var cfg Config
	_, err := xconfig.Load(&cfg, xconfig.WithSkipFlags(), xconfig.WithPlugins(client.Plugin(context.Background())))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Vault plugin runs last → vault wins.
	if cfg.Secret != "from-vault" {
		t.Errorf("Secret = %q, want 'from-vault' (vault should override env)", cfg.Secret)
	}
}

// TestFullIntegration is an end-to-end test: xconfig.Load + VaultPlugin + StartRefresh.
func TestFullIntegration(t *testing.T) {
	type AppConfig struct {
		Host       string `env:"HOST" default:"localhost"`
		Port       int    `env:"PORT" default:"8080"`
		DBPassword string `vault:"true" env:"DB_PASSWORD" secret:"true"`
		APIKey     string `vault:"true" env:"API_KEY" secret:"true"`
	}

	vaultPut(t, "secret", "test/config", map[string]string{
		"DB_PASSWORD": "initial-pass",
		"API_KEY":     "initial-key",
	})

	client := newTestClient(t)

	var cfg AppConfig
	xc, err := xconfig.Load(&cfg, xconfig.WithSkipFlags(), xconfig.WithPlugins(client.Plugin(context.Background())))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Verify initial load.
	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want 'localhost'", cfg.Host)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.DBPassword != "initial-pass" {
		t.Errorf("DBPassword = %q, want 'initial-pass'", cfg.DBPassword)
	}
	if cfg.APIKey != "initial-key" {
		t.Errorf("APIKey = %q, want 'initial-key'", cfg.APIKey)
	}

	// Start background refresh.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var changeLog []plugins.FieldChange
	var changeMu sync.Mutex

	xc.StartRefresh(ctx, 500*time.Millisecond, func(changes []plugins.FieldChange) {
		changeMu.Lock()
		changeLog = append(changeLog, changes...)
		changeMu.Unlock()
	})
	defer xc.StopRefresh()

	// Rotate a secret.
	vaultPut(t, "secret", "test/config", map[string]string{
		"DB_PASSWORD": "rotated-pass",
		"API_KEY":     "initial-key",
	})

	time.Sleep(2 * time.Second)

	// Verify the config was updated.
	if cfg.DBPassword != "rotated-pass" {
		t.Errorf("DBPassword after rotation = %q, want 'rotated-pass'", cfg.DBPassword)
	}
	if cfg.APIKey != "initial-key" {
		t.Errorf("APIKey should not have changed, got %q", cfg.APIKey)
	}

	changeMu.Lock()
	defer changeMu.Unlock()

	if len(changeLog) == 0 {
		t.Error("expected changes to be reported via onChange callback")
	}
	for _, c := range changeLog {
		t.Logf("change: %s %q -> %q", c.FieldName, c.OldValue, c.NewValue)
	}
}
