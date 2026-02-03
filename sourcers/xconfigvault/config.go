// Package xconfigvault provides HashiCorp Vault integration for xconfig.
package xconfigvault

import "time"

// Config holds Vault client configuration.
type Config struct {
	// Address is the Vault server address (e.g., "https://vault.example.com:8200").
	// Can be set via VAULT_ADDR environment variable.
	Address string

	// Namespace is the Vault namespace (Enterprise feature).
	// Can be set via VAULT_NAMESPACE environment variable.
	Namespace string

	// TLS configures TLS settings for Vault connection.
	TLS *TLSConfig

	// Auth configures the authentication method.
	Auth AuthMethod

	// Cache configures secret caching behavior.
	Cache *CacheConfig

	// DefaultMount is the default secrets engine mount path.
	// Defaults to "secret" if not specified.
	DefaultMount string

	// KVVersion specifies KV secrets engine version (1 or 2).
	// Defaults to 2 if not specified.
	KVVersion int
}

// TLSConfig holds TLS configuration for Vault connection.
type TLSConfig struct {
	// CACert is the path to a PEM-encoded CA certificate file.
	CACert string

	// CAPath is the path to a directory of PEM-encoded CA certificate files.
	CAPath string

	// ClientCert is the path to a PEM-encoded client certificate.
	ClientCert string

	// ClientKey is the path to a PEM-encoded client key.
	ClientKey string

	// ServerName is the server name to use for TLS verification.
	ServerName string

	// Insecure disables TLS verification.
	// Not recommended for production use.
	Insecure bool
}

// CacheConfig configures secret caching behavior.
type CacheConfig struct {
	// Enabled enables/disables caching.
	// Defaults to true.
	Enabled bool

	// TTL is the default cache TTL.
	// Defaults to 5 minutes.
	TTL time.Duration

	// RefreshInterval is how often to check for secret changes.
	// Defaults to 1 minute.
	RefreshInterval time.Duration

	// RefreshAhead enables pre-emptive refresh before TTL expiry.
	// Defaults to true.
	RefreshAhead bool
}

// DefaultCacheConfig returns the default cache configuration.
func DefaultCacheConfig() *CacheConfig {
	return &CacheConfig{
		Enabled:         true,
		TTL:             5 * time.Minute,
		RefreshInterval: 1 * time.Minute,
		RefreshAhead:    true,
	}
}

func (c *Config) defaults() {
	if c.DefaultMount == "" {
		c.DefaultMount = "secret"
	}
	if c.KVVersion == 0 {
		c.KVVersion = 2
	}
	if c.Cache == nil {
		c.Cache = DefaultCacheConfig()
	}
}
