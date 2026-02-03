package xconfigvault

import (
	"sync"
	"time"
)

// cachedSecret holds a cached secret value with metadata.
type cachedSecret struct {
	value     string
	data      map[string]any // full secret data
	fetchedAt time.Time
	expiresAt time.Time
	version   int // for KV v2
}

// secretCache provides thread-safe caching of secrets.
type secretCache struct {
	entries map[string]*cachedSecret
	mu      sync.RWMutex
	ttl     time.Duration
	enabled bool
}

// newSecretCache creates a new secret cache with the given configuration.
func newSecretCache(cfg *CacheConfig) *secretCache {
	if cfg == nil {
		cfg = DefaultCacheConfig()
	}

	return &secretCache{
		entries: make(map[string]*cachedSecret),
		ttl:     cfg.TTL,
		enabled: cfg.Enabled,
	}
}

// get retrieves a cached secret if it exists and hasn't expired.
func (c *secretCache) get(path string) (string, bool) {
	if !c.enabled {
		return "", false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[path]
	if !ok {
		return "", false
	}

	if time.Now().After(entry.expiresAt) {
		return "", false
	}

	return entry.value, true
}

// getData retrieves cached secret data if it exists and hasn't expired.
func (c *secretCache) getData(path string) (map[string]any, bool) {
	if !c.enabled {
		return nil, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[path]
	if !ok {
		return nil, false
	}

	if time.Now().After(entry.expiresAt) {
		return nil, false
	}

	return entry.data, true
}

// set stores a secret in the cache.
func (c *secretCache) set(path, value string, data map[string]any, version int) {
	if !c.enabled {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	c.entries[path] = &cachedSecret{
		value:     value,
		data:      data,
		fetchedAt: now,
		expiresAt: now.Add(c.ttl),
		version:   version,
	}
}

// delete removes a secret from the cache.
func (c *secretCache) delete(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, path)
}

// clear removes all secrets from the cache.
func (c *secretCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*cachedSecret)
}

// expired checks if a cached secret has expired.
func (c *secretCache) expired(path string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[path]
	if !ok {
		return true
	}

	return time.Now().After(entry.expiresAt)
}

// getEntry returns the full cache entry for a path.
func (c *secretCache) getEntry(path string) (*cachedSecret, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[path]
	if !ok {
		return nil, false
	}

	// Return a copy to prevent data races
	return &cachedSecret{
		value:     entry.value,
		data:      entry.data,
		fetchedAt: entry.fetchedAt,
		expiresAt: entry.expiresAt,
		version:   entry.version,
	}, true
}

// paths returns all cached paths.
func (c *secretCache) paths() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	paths := make([]string, 0, len(c.entries))
	for path := range c.entries {
		paths = append(paths, path)
	}
	return paths
}
