package xconfigvault

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/sxwebdev/xconfig/flat"
	"github.com/sxwebdev/xconfig/plugins"
)

const vaultTag = "vault"

var ErrInvalidSecretValue = errors.New("vault: invalid secret value")

// SecretValueError identifies a field that rejected a Vault value without
// retaining or exposing the secret itself.
type SecretValueError struct {
	FieldName string
	Key       string
	reason    string
	cause     error
}

type redactedCause struct {
	message  string
	original error
}

func (e *redactedCause) Error() string { return e.message }

func (e *redactedCause) Is(target error) bool { return errors.Is(e.original, target) }

func (e *SecretValueError) Error() string {
	message := fmt.Sprintf("vault: field %s rejected value from key %s", e.FieldName, e.Key)
	if e.reason != "" {
		return message + ": " + e.reason
	}
	return message
}

func (e *SecretValueError) Unwrap() error { return e.cause }

func init() {
	plugins.RegisterTag(vaultTag)
}

// PluginOption configures a VaultPlugin.
type PluginOption func(*VaultPlugin)

// WithEnvPrefix sets the env-style prefix used when expanding slice and map
// containers from Vault secret keys. Should match xconfig.WithEnvPrefix —
// e.g. WithEnvPrefix("MYAPP") makes the plugin look for keys like
// MYAPP_NODES_0_HOST.
//
// In most cases this is detected automatically from the env plugin's
// stamped metadata at Visit time; pass this option only when the conf
// has no top-level scalar fields visible at Visit time (e.g. when every
// top-level field is an empty slice or map) and auto-detection cannot
// recover the prefix.
func WithEnvPrefix(prefix string) PluginOption {
	return func(p *VaultPlugin) { p.envPrefix = prefix }
}

// VaultPlugin is an xconfig plugin that batch-loads secrets from Vault.
// It implements plugins.Walker (for slice/map expansion driven by Vault keys),
// plugins.Visitor (for initial registration), and plugins.Refreshable (for
// background config updates).
//
// Fields tagged with vault:"true" are sourced from the configured SecretPath.
// The vault key is derived from the field's EnvName (UPPER_SNAKE_CASE).
// VaultPlugin runs last in the plugin chain and has maximum priority over
// all other sources (env, flags, defaults, files).
//
// Slice-of-struct and map fields are populated automatically: keys present
// in the Vault secret like ITEMS_0_HOST, SERVERS_PRIMARY_PASSWORD grow the
// containers and create entries the same way the env plugin does.
type VaultPlugin struct {
	client     *Client
	secretPath string
	ctx        context.Context // used by Parse(); set at construction via Plugin(ctx)
	conf       any
	envPrefix  string // global env prefix (matches xconfig.WithEnvPrefix); used when expanding slice/map containers

	mu sync.Mutex
}

// Plugin returns a new VaultPlugin for use with xconfig.WithPlugins().
// The provided context is used for the initial Parse() call.
// The Refresh() method receives its own context from xconfig's StartRefresh
// and is not affected by this context.
//
// Optional PluginOption arguments configure the plugin — see WithEnvPrefix.
func (c *Client) Plugin(ctx context.Context, opts ...PluginOption) *VaultPlugin {
	p := &VaultPlugin{
		client:     c,
		secretPath: c.config.SecretPath,
		ctx:        ctx,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Walk captures the conf reference so Parse can re-flatten and expand
// slice/map fields based on Vault secret keys (mirroring the env plugin).
func (p *VaultPlugin) Walk(conf any) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.conf = conf
	return nil
}

// Visit collects fields tagged with vault:"true" from the initial flat view
// and, when not configured explicitly, auto-detects the env prefix from the
// env plugin's already-stamped metadata. Parse and Refresh rebuild their field
// maps after container expansion instead of retaining caller-owned fields.
func (p *VaultPlugin) Visit(fields flat.Fields) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.envPrefix == "" {
		p.envPrefix = detectEnvPrefix(fields)
	}
	return nil
}

// detectEnvPrefix infers the global env prefix from the env plugin's stamped
// metadata. It compares Meta()["env"] (prefixed) against EnvName() (unprefixed)
// for a top-level scalar with no env tag override. Returns "" if no field is
// suitable for detection (which is fine when no prefix is configured).
func detectEnvPrefix(fields flat.Fields) string {
	for _, f := range fields {
		meta := f.Meta()["env"]
		if meta == "" {
			continue
		}
		if t, ok := f.Tag("env"); ok && t != "" {
			continue
		}
		if pt := f.ParentTag(); pt != "" {
			if t, ok := pt.Lookup("env"); ok && t != "" {
				continue
			}
		}
		if strings.Contains(f.Name(), ".") {
			continue
		}
		unprefixed := f.EnvName()
		if meta == unprefixed {
			return ""
		}
		if strings.HasSuffix(meta, "_"+unprefixed) {
			return strings.TrimSuffix(meta, "_"+unprefixed)
		}
	}
	return ""
}

// Parse fetches secrets from the configured SecretPath, uses the secret keys
// to expand slice/map containers (so e.g. SERVERS_PRIMARY_PASSWORD creates
// Servers["PRIMARY"]), then sets values on every vault-tagged field.
func (p *VaultPlugin) Parse() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	secrets, err := p.client.GetMap(p.ctx, p.secretPath)
	if err != nil {
		return fmt.Errorf("vault: failed to load secrets from %s: %w", p.secretPath, err)
	}

	p.client.emitEvent(EventSecretsFetched, nil)

	if err := p.applySecretsLocked(secrets); err != nil {
		return err
	}

	return nil
}

// Refresh re-fetches secrets from Vault and applies them to xconfig's private
// target. Invalid individual values remain last-known-good and are returned as
// redacted warnings while valid sibling values continue to update.
func (p *VaultPlugin) Refresh(ctx context.Context, target any) (plugins.RefreshOutcome, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.client.InvalidateCache(p.secretPath)

	secrets, err := p.client.GetMap(ctx, p.secretPath)
	if err != nil {
		p.client.emitEvent(EventVaultUnreachable, err)
		return plugins.RefreshOutcome{}, err
	}

	return p.applyRefreshSecrets(target, secrets)
}

func (p *VaultPlugin) applyRefreshSecrets(target any, secrets map[string]string) (plugins.RefreshOutcome, error) {
	// Re-expand containers in case new map keys / slice indices appeared in
	// Vault since the last refresh.
	keys := mapKeys(secrets)
	nameMap, err := flat.ExpandContainersFromKeys(target, p.envPrefix, keys)
	if err != nil {
		return plugins.RefreshOutcome{}, err
	}

	fields, err := flat.View(target)
	if err != nil {
		return plugins.RefreshOutcome{}, err
	}
	keyMap := collectVaultFields(fields, nameMap)

	outcome := plugins.RefreshOutcome{}
	for _, key := range slices.Sorted(maps.Keys(keyMap)) {
		f := keyMap[key]
		newVal, ok := secrets[key]
		if !ok {
			continue
		}
		changed, err := f.SetChanged(newVal)
		if err != nil {
			outcome.Warnings = append(outcome.Warnings, newSecretValueError(f.Name(), key, err))
			continue
		}
		if changed {
			outcome.Changes = append(outcome.Changes, plugins.FieldChange{FieldName: f.Name()})
		}
	}

	return outcome, nil
}

// applySecretsLocked expands slice/map containers based on the secret keys,
// then sets values on every vault-tagged leaf field. Callers must hold p.mu.
func (p *VaultPlugin) applySecretsLocked(secrets map[string]string) error {
	keys := mapKeys(secrets)
	nameMap, err := flat.ExpandContainersFromKeys(p.conf, p.envPrefix, keys)
	if err != nil {
		return err
	}

	fields, err := flat.View(p.conf)
	if err != nil {
		return err
	}
	keyMap := collectVaultFields(fields, nameMap)

	// Sorted so that a rejected value always aborts on the same key, leaving the
	// same fields applied, instead of varying with map iteration order.
	for _, key := range slices.Sorted(maps.Keys(keyMap)) {
		value, ok := secrets[key]
		if !ok {
			continue
		}
		f := keyMap[key]
		if err := f.Set(value); err != nil {
			return newSecretValueError(f.Name(), key, err)
		}
	}

	return nil
}

// newSecretValueError builds a redacted error for a rejected value. The
// underlying message may embed the secret, so only reasons known to be
// value-free are reported; the original error stays reachable through
// errors.Is without its message ever being printed.
func newSecretValueError(fieldName, key string, err error) error {
	reason := "invalid value"
	switch {
	case errors.Is(err, strconv.ErrSyntax):
		reason = strconv.ErrSyntax.Error()
	case errors.Is(err, strconv.ErrRange):
		reason = strconv.ErrRange.Error()
	}
	return &SecretValueError{
		FieldName: fieldName,
		Key:       key,
		reason:    reason,
		cause: errors.Join(ErrInvalidSecretValue, &redactedCause{
			message:  reason,
			original: err,
		}),
	}
}

// collectVaultFields builds a map ENV_NAME → field for every leaf tagged
// `vault:"true"`. Lookup order for the env name:
//  1. nameMap entry from ExpandContainersFromKeys (respects env-tag overrides
//     inside slice/map elements);
//  2. the env plugin's stamped metadata (`f.Meta()["env"]`);
//  3. `f.EnvName()` derived from the field path.
func collectVaultFields(fields flat.Fields, nameMap map[string]string) map[string]flat.Field {
	out := make(map[string]flat.Field, len(fields))
	for _, f := range fields {
		tagVal, ok := f.Tag(vaultTag)
		if !ok || tagVal != "true" {
			continue
		}
		var key string
		if nameMap != nil {
			key = nameMap[f.Name()]
		}
		if key == "" {
			key = f.Meta()["env"]
		}
		if key == "" {
			key = f.EnvName()
		}
		out[key] = f
	}
	return out
}

func mapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
