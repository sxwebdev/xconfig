package xconfigvault

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/sxwebdev/xconfig/flat"
	"github.com/sxwebdev/xconfig/plugins"
)

const vaultTag = "vault"

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
	fields     flat.Fields
	keyMap     map[string]flat.Field // ENV_NAME -> field

	mu          sync.RWMutex
	lastSecrets map[string]string
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
	p.conf = conf
	return nil
}

// Visit collects fields tagged with vault:"true" from the initial flat view
// and, when not configured explicitly, auto-detects the env prefix from the
// env plugin's already-stamped metadata. Parse() rebuilds the keyMap after
// expansion to pick up newly created entries.
func (p *VaultPlugin) Visit(fields flat.Fields) error {
	p.fields = fields
	p.keyMap = collectVaultFields(fields, nil)
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
	secrets, err := p.client.GetMap(p.ctx, p.secretPath)
	if err != nil {
		return fmt.Errorf("vault: failed to load secrets from %s: %w", p.secretPath, err)
	}

	p.client.emitEvent(EventSecretsFetched, nil)

	if err := p.applySecrets(secrets); err != nil {
		return err
	}

	p.mu.Lock()
	p.lastSecrets = secrets
	p.mu.Unlock()

	return nil
}

// Refresh re-fetches secrets from Vault and updates changed fields.
// Returns a list of changes with full field paths (e.g. "Database.Postgres.Password").
// Implements plugins.Refreshable.
func (p *VaultPlugin) Refresh(ctx context.Context) ([]plugins.FieldChange, error) {
	p.client.InvalidateCache(p.secretPath)

	secrets, err := p.client.GetMap(ctx, p.secretPath)
	if err != nil {
		p.client.emitEvent(EventVaultUnreachable, err)
		return nil, err
	}

	// Re-expand containers in case new map keys / slice indices appeared in
	// Vault since the last refresh.
	keys := mapKeys(secrets)
	nameMap, err := flat.ExpandContainersFromKeys(p.conf, p.envPrefix, keys)
	if err != nil {
		return nil, err
	}

	fields, err := flat.View(p.conf)
	if err != nil {
		return nil, err
	}
	keyMap := collectVaultFields(fields, nameMap)

	p.mu.Lock()
	defer p.mu.Unlock()

	var changes []plugins.FieldChange
	for key, f := range keyMap {
		newVal := secrets[key]
		oldVal := p.lastSecrets[key]
		if newVal == oldVal {
			continue
		}
		if err := f.Set(newVal); err != nil {
			continue
		}
		changes = append(changes, plugins.FieldChange{
			FieldName: f.Name(),
			OldValue:  oldVal,
			NewValue:  newVal,
		})
	}

	p.lastSecrets = secrets
	p.fields = fields
	p.keyMap = keyMap
	return changes, nil
}

// applySecrets expands slice/map containers based on the secret keys, then
// sets values on every vault-tagged leaf field. Used by both Parse() and
// (indirectly) Refresh().
func (p *VaultPlugin) applySecrets(secrets map[string]string) error {
	keys := mapKeys(secrets)
	nameMap, err := flat.ExpandContainersFromKeys(p.conf, p.envPrefix, keys)
	if err != nil {
		return err
	}

	fields, err := flat.View(p.conf)
	if err != nil {
		return err
	}
	p.fields = fields
	p.keyMap = collectVaultFields(fields, nameMap)

	for key, f := range p.keyMap {
		value, ok := secrets[key]
		if !ok || value == "" {
			continue
		}
		if err := f.Set(value); err != nil {
			return fmt.Errorf("vault: failed to set field %s from key %s: %w", f.Name(), key, err)
		}
	}

	return nil
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
