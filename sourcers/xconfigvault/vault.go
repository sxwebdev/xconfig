package xconfigvault

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/hashicorp/vault-client-go"
)

// Client is the Vault secrets client.
type Client struct {
	client  *vault.Client
	config  *Config
	cache   *secretCache
	watcher *secretWatcher

	mu          sync.RWMutex
	renewCancel context.CancelFunc
	closed      bool
}

// New creates a new Vault client with the given configuration.
func New(cfg *Config) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}

	if cfg.Address == "" {
		return nil, fmt.Errorf("vault address is required")
	}

	if cfg.Auth == nil {
		return nil, ErrNoAuthMethod
	}

	cfg.defaults()

	// Build vault client options
	opts := []vault.ClientOption{
		vault.WithAddress(cfg.Address),
	}

	// Configure TLS if provided
	if cfg.TLS != nil {
		tlsConfig, err := buildTLSConfig(cfg.TLS)
		if err != nil {
			return nil, fmt.Errorf("failed to configure TLS: %w", err)
		}
		httpClient := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
		}
		opts = append(opts, vault.WithHTTPClient(httpClient))
	}

	// Create vault client
	vaultClient, err := vault.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create vault client: %w", err)
	}

	// Set namespace if provided
	if cfg.Namespace != "" {
		if err := vaultClient.SetNamespace(cfg.Namespace); err != nil {
			return nil, fmt.Errorf("failed to set namespace: %w", err)
		}
	}

	// Authenticate
	ctx := context.Background()
	if err := cfg.Auth.Login(ctx, vaultClient); err != nil {
		return nil, err
	}

	c := &Client{
		client: vaultClient,
		config: cfg,
		cache:  newSecretCache(cfg.Cache),
	}

	return c, nil
}

// NewFromEnv creates a Vault client configured from environment variables.
// Environment variables:
//   - VAULT_ADDR: Vault server address
//   - VAULT_TOKEN: Authentication token (if using token auth)
//   - VAULT_NAMESPACE: Vault namespace
//   - VAULT_CACERT: Path to CA certificate
//   - VAULT_SKIP_VERIFY: Skip TLS verification ("true" or "1")
func NewFromEnv() (*Client, error) {
	addr := os.Getenv("VAULT_ADDR")
	if addr == "" {
		return nil, fmt.Errorf("VAULT_ADDR environment variable is required")
	}

	token := os.Getenv("VAULT_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("VAULT_TOKEN environment variable is required")
	}

	cfg := &Config{
		Address:   addr,
		Namespace: os.Getenv("VAULT_NAMESPACE"),
		Auth:      WithToken(token),
	}

	// Configure TLS from environment
	caCert := os.Getenv("VAULT_CACERT")
	skipVerify := os.Getenv("VAULT_SKIP_VERIFY")
	if caCert != "" || skipVerify == "true" || skipVerify == "1" {
		cfg.TLS = &TLSConfig{
			CACert:   caCert,
			Insecure: skipVerify == "true" || skipVerify == "1",
		}
	}

	return New(cfg)
}

// Close gracefully shuts down the client, stopping all background workers.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true

	// Stop token renewal
	if c.renewCancel != nil {
		c.renewCancel()
	}

	// Stop watcher
	if c.watcher != nil {
		c.watcher.stop()
	}

	// Clear cache
	c.cache.clear()

	return nil
}

// Get retrieves a secret value from Vault.
// Path format: "mount/path/to/secret#key" or "path/to/secret#key" (uses DefaultMount)
// For KV v2, the path should not include "data/" - it will be added automatically.
func (c *Client) Get(ctx context.Context, path string) (string, error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return "", ErrClientClosed
	}
	c.mu.RUnlock()

	// Parse path
	secretPath, key, err := parsePath(path)
	if err != nil {
		return "", err
	}

	// Check cache first
	cacheKey := path
	if value, ok := c.cache.get(cacheKey); ok {
		return value, nil
	}

	// Fetch from Vault
	data, version, err := c.fetchSecret(ctx, secretPath)
	if err != nil {
		return "", err
	}

	// Extract the key
	value, ok := data[key]
	if !ok {
		return "", newVaultError("get", path, ErrKeyNotFound)
	}

	valueStr, ok := value.(string)
	if !ok {
		return "", newVaultError("get", path, fmt.Errorf("value for key %q is not a string", key))
	}

	// Cache the result
	c.cache.set(cacheKey, valueStr, data, version)

	return valueStr, nil
}

// GetMap retrieves all key-value pairs from a secret path.
func (c *Client) GetMap(ctx context.Context, path string) (map[string]string, error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return nil, ErrClientClosed
	}
	c.mu.RUnlock()

	// Check cache first
	if data, ok := c.cache.getData(path); ok {
		return convertToStringMap(data), nil
	}

	// Fetch from Vault
	data, version, err := c.fetchSecret(ctx, path)
	if err != nil {
		return nil, err
	}

	// Cache the result
	c.cache.set(path, "", data, version)

	return convertToStringMap(data), nil
}

// Sourcer returns a secret.Sourcer function compatible with xconfig's secret plugin.
// The sourcer expects paths in format "mount/path#key" or "path#key".
func (c *Client) Sourcer() func(string) (string, error) {
	return func(name string) (string, error) {
		return c.Get(context.Background(), name)
	}
}

// SourcerWithContext returns a Sourcer that uses the provided context.
func (c *Client) SourcerWithContext(ctx context.Context) func(string) (string, error) {
	return func(name string) (string, error) {
		return c.Get(ctx, name)
	}
}

// fetchSecret fetches a secret from Vault.
func (c *Client) fetchSecret(ctx context.Context, path string) (map[string]any, int, error) {
	mount, secretPath := c.splitMountPath(path)

	var data map[string]any
	var version int

	if c.config.KVVersion == 2 {
		resp, err := c.client.Secrets.KvV2Read(ctx, secretPath, vault.WithMountPath(mount))
		if err != nil {
			return nil, 0, c.wrapVaultError("read", path, err)
		}
		if resp.Data.Data == nil {
			return nil, 0, newVaultError("read", path, ErrSecretNotFound)
		}
		data = resp.Data.Data
		if v, ok := resp.Data.Metadata["version"].(float64); ok {
			version = int(v)
		}
	} else {
		resp, err := c.client.Secrets.KvV1Read(ctx, secretPath, vault.WithMountPath(mount))
		if err != nil {
			return nil, 0, c.wrapVaultError("read", path, err)
		}
		if resp.Data == nil {
			return nil, 0, newVaultError("read", path, ErrSecretNotFound)
		}
		data = resp.Data
	}

	return data, version, nil
}

// splitMountPath splits a path into mount and secret path.
// If no mount is detected, uses DefaultMount.
func (c *Client) splitMountPath(path string) (mount, secretPath string) {
	// Check if path starts with known mount pattern
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 2 {
		mount = parts[0]
		secretPath = parts[1]
	} else {
		mount = c.config.DefaultMount
		secretPath = path
	}
	return
}

// wrapVaultError wraps a Vault error with context.
func (c *Client) wrapVaultError(op, path string, err error) error {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	if strings.Contains(errStr, "permission denied") {
		return newVaultError(op, path, ErrPermissionDenied)
	}
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "i/o timeout") {
		return newVaultError(op, path, ErrVaultUnreachable)
	}
	if strings.Contains(errStr, "invalid token") ||
		strings.Contains(errStr, "token expired") {
		return newVaultError(op, path, ErrTokenExpired)
	}

	return newVaultError(op, path, err)
}

// parsePath parses a secret path in format "path#key" or "mount/path#key".
func parsePath(path string) (secretPath, key string, err error) {
	parts := strings.SplitN(path, "#", 2)
	if len(parts) != 2 {
		return "", "", newVaultError("parse", path, ErrInvalidPath)
	}

	secretPath = strings.TrimSpace(parts[0])
	key = strings.TrimSpace(parts[1])

	if secretPath == "" || key == "" {
		return "", "", newVaultError("parse", path, ErrInvalidPath)
	}

	return secretPath, key, nil
}

// convertToStringMap converts map[string]any to map[string]string.
func convertToStringMap(data map[string]any) map[string]string {
	result := make(map[string]string, len(data))
	for k, v := range data {
		if str, ok := v.(string); ok {
			result[k] = str
		} else {
			result[k] = fmt.Sprintf("%v", v)
		}
	}
	return result
}

// buildTLSConfig builds a TLS configuration from the provided options.
func buildTLSConfig(cfg *TLSConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.Insecure,
		ServerName:         cfg.ServerName,
	}

	// Load CA cert
	if cfg.CACert != "" {
		caCert, err := os.ReadFile(cfg.CACert)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA cert: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA cert")
		}

		tlsConfig.RootCAs = caCertPool
	}

	// Load CA path (directory of certs)
	if cfg.CAPath != "" {
		caCertPool := x509.NewCertPool()

		entries, err := os.ReadDir(cfg.CAPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA path: %w", err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			caCert, err := os.ReadFile(cfg.CAPath + "/" + entry.Name())
			if err != nil {
				continue
			}

			caCertPool.AppendCertsFromPEM(caCert)
		}

		tlsConfig.RootCAs = caCertPool
	}

	// Load client cert and key
	if cfg.ClientCert != "" && cfg.ClientKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("failed to load client cert/key: %w", err)
		}

		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}

// ClearCache clears the secret cache.
func (c *Client) ClearCache() {
	c.cache.clear()
}

// InvalidateCache invalidates a specific path in the cache.
func (c *Client) InvalidateCache(path string) {
	c.cache.delete(path)
}
