package xconfigvault

import (
	"context"
	"fmt"
	"sync"

	"github.com/sxwebdev/xconfig/flat"
	"github.com/sxwebdev/xconfig/plugins"
)

const vaultTag = "vault"

func init() {
	plugins.RegisterTag(vaultTag)
}

// VaultPlugin is an xconfig plugin that batch-loads secrets from Vault.
// It implements plugins.Visitor for initial loading and plugins.Refreshable
// for background config updates.
//
// Fields tagged with vault:"true" are sourced from the configured SecretPath.
// The vault key is derived from the field's EnvName (UPPER_SNAKE_CASE).
// VaultPlugin runs last in the plugin chain and has maximum priority over
// all other sources (env, flags, defaults, files).
type VaultPlugin struct {
	client     *Client
	secretPath string
	fields     flat.Fields
	keyMap     map[string]flat.Field // ENV_NAME -> field

	mu          sync.RWMutex
	lastSecrets map[string]string
}

// Plugin returns a new VaultPlugin for use with xconfig.WithPlugins().
func (c *Client) Plugin() *VaultPlugin {
	return &VaultPlugin{
		client:     c,
		secretPath: c.config.SecretPath,
	}
}

// Visit collects all fields tagged with vault:"true" and maps their EnvName
// to the field for batch loading. Implements plugins.Visitor.
func (p *VaultPlugin) Visit(fields flat.Fields) error {
	p.fields = fields
	p.keyMap = make(map[string]flat.Field)

	for _, f := range fields {
		tagVal, ok := f.Tag(vaultTag)
		if !ok || tagVal != "true" {
			continue
		}
		// Use env plugin's resolved name if available (supports env:"CUSTOM_NAME"),
		// otherwise fall back to EnvName() derived from the field name.
		key := f.Meta()["env"]
		if key == "" {
			key = f.EnvName()
		}
		p.keyMap[key] = f
	}

	return nil
}

// Parse batch-loads all secrets from the configured SecretPath in a single
// request and sets the values on matching fields. Implements plugins.Plugin.
func (p *VaultPlugin) Parse() error {
	if len(p.keyMap) == 0 {
		return nil
	}

	secrets, err := p.client.GetMap(context.Background(), p.secretPath)
	if err != nil {
		return fmt.Errorf("vault: failed to load secrets from %s: %w", p.secretPath, err)
	}

	p.client.emitEvent(EventSecretsFetched, nil)

	p.mu.Lock()
	p.lastSecrets = secrets
	p.mu.Unlock()

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

// Refresh re-fetches secrets from Vault and updates changed fields.
// Returns a list of changes with full field paths (e.g. "Database.Postgres.Password").
// Implements plugins.Refreshable.
func (p *VaultPlugin) Refresh(ctx context.Context) ([]plugins.FieldChange, error) {
	if len(p.keyMap) == 0 {
		return nil, nil
	}

	p.client.InvalidateCache(p.secretPath)

	secrets, err := p.client.GetMap(ctx, p.secretPath)
	if err != nil {
		p.client.emitEvent(EventVaultUnreachable, err)
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	var changes []plugins.FieldChange
	for key, f := range p.keyMap {
		newVal := secrets[key]
		oldVal := p.lastSecrets[key]
		if newVal != oldVal {
			if err := f.Set(newVal); err != nil {
				continue
			}
			changes = append(changes, plugins.FieldChange{
				FieldName: f.Name(),
				OldValue:  oldVal,
				NewValue:  newVal,
			})
		}
	}

	p.lastSecrets = secrets
	return changes, nil
}
