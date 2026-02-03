package xconfigvault

import (
	"testing"
	"time"
)

func TestParsePath(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantPath   string
		wantKey    string
		wantErr    bool
		errMessage string
	}{
		{
			name:     "simple path with key",
			path:     "secret/myapp#password",
			wantPath: "secret/myapp",
			wantKey:  "password",
			wantErr:  false,
		},
		{
			name:     "nested path with key",
			path:     "kv/data/prod/database#conn_string",
			wantPath: "kv/data/prod/database",
			wantKey:  "conn_string",
			wantErr:  false,
		},
		{
			name:     "path with spaces (trimmed)",
			path:     "  secret/myapp  #  api_key  ",
			wantPath: "secret/myapp",
			wantKey:  "api_key",
			wantErr:  false,
		},
		{
			name:       "missing key separator",
			path:       "secret/myapp",
			wantErr:    true,
			errMessage: "invalid secret path format",
		},
		{
			name:       "empty path",
			path:       "#key",
			wantErr:    true,
			errMessage: "invalid secret path format",
		},
		{
			name:       "empty key",
			path:       "secret/myapp#",
			wantErr:    true,
			errMessage: "invalid secret path format",
		},
		{
			name:       "empty string",
			path:       "",
			wantErr:    true,
			errMessage: "invalid secret path format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotKey, err := parsePath(tt.path)

			if tt.wantErr {
				if err == nil {
					t.Errorf("parsePath() error = nil, wantErr = true")
					return
				}
				return
			}

			if err != nil {
				t.Errorf("parsePath() unexpected error = %v", err)
				return
			}

			if gotPath != tt.wantPath {
				t.Errorf("parsePath() path = %q, want %q", gotPath, tt.wantPath)
			}

			if gotKey != tt.wantKey {
				t.Errorf("parsePath() key = %q, want %q", gotKey, tt.wantKey)
			}
		})
	}
}

func TestConvertToStringMap(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
		want map[string]string
	}{
		{
			name: "string values",
			data: map[string]any{
				"key1": "value1",
				"key2": "value2",
			},
			want: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
		},
		{
			name: "mixed types",
			data: map[string]any{
				"string": "hello",
				"int":    42,
				"float":  3.14,
				"bool":   true,
			},
			want: map[string]string{
				"string": "hello",
				"int":    "42",
				"float":  "3.14",
				"bool":   "true",
			},
		},
		{
			name: "empty map",
			data: map[string]any{},
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertToStringMap(tt.data)

			if len(got) != len(tt.want) {
				t.Errorf("convertToStringMap() len = %d, want %d", len(got), len(tt.want))
				return
			}

			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("convertToStringMap()[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := &Config{}
	cfg.defaults()

	if cfg.DefaultMount != "secret" {
		t.Errorf("defaults() DefaultMount = %q, want %q", cfg.DefaultMount, "secret")
	}

	if cfg.KVVersion != 2 {
		t.Errorf("defaults() KVVersion = %d, want %d", cfg.KVVersion, 2)
	}

	if cfg.Cache == nil {
		t.Fatal("defaults() Cache = nil, want non-nil")
	}

	if !cfg.Cache.Enabled {
		t.Error("defaults() Cache.Enabled = false, want true")
	}

	if cfg.Cache.TTL != 5*time.Minute {
		t.Errorf("defaults() Cache.TTL = %v, want %v", cfg.Cache.TTL, 5*time.Minute)
	}

	if cfg.Cache.RefreshInterval != 1*time.Minute {
		t.Errorf("defaults() Cache.RefreshInterval = %v, want %v", cfg.Cache.RefreshInterval, 1*time.Minute)
	}
}

func TestSecretCache(t *testing.T) {
	t.Run("enabled cache", func(t *testing.T) {
		cache := newSecretCache(&CacheConfig{
			Enabled: true,
			TTL:     1 * time.Hour,
		})

		// Test set and get
		cache.set("path1", "value1", nil, 1)

		value, ok := cache.get("path1")
		if !ok {
			t.Error("get() ok = false, want true")
		}
		if value != "value1" {
			t.Errorf("get() value = %q, want %q", value, "value1")
		}

		// Test non-existent key
		_, ok = cache.get("nonexistent")
		if ok {
			t.Error("get() ok = true for non-existent key, want false")
		}

		// Test delete
		cache.delete("path1")
		_, ok = cache.get("path1")
		if ok {
			t.Error("get() ok = true after delete, want false")
		}
	})

	t.Run("disabled cache", func(t *testing.T) {
		cache := newSecretCache(&CacheConfig{
			Enabled: false,
		})

		cache.set("path1", "value1", nil, 1)

		_, ok := cache.get("path1")
		if ok {
			t.Error("get() ok = true for disabled cache, want false")
		}
	})

	t.Run("expired entry", func(t *testing.T) {
		cache := newSecretCache(&CacheConfig{
			Enabled: true,
			TTL:     1 * time.Millisecond,
		})

		cache.set("path1", "value1", nil, 1)

		// Wait for expiration
		time.Sleep(5 * time.Millisecond)

		_, ok := cache.get("path1")
		if ok {
			t.Error("get() ok = true for expired entry, want false")
		}

		if !cache.expired("path1") {
			t.Error("expired() = false, want true")
		}
	})

	t.Run("clear cache", func(t *testing.T) {
		cache := newSecretCache(&CacheConfig{
			Enabled: true,
			TTL:     1 * time.Hour,
		})

		cache.set("path1", "value1", nil, 1)
		cache.set("path2", "value2", nil, 1)

		cache.clear()

		if len(cache.paths()) != 0 {
			t.Errorf("paths() len = %d after clear, want 0", len(cache.paths()))
		}
	})
}

func TestVaultError(t *testing.T) {
	t.Run("error with path", func(t *testing.T) {
		err := &VaultError{
			Op:   "read",
			Path: "secret/myapp",
			Err:  ErrSecretNotFound,
		}

		expected := "vault read secret/myapp: vault: secret not found"
		if err.Error() != expected {
			t.Errorf("Error() = %q, want %q", err.Error(), expected)
		}
	})

	t.Run("error without path", func(t *testing.T) {
		err := &VaultError{
			Op:  "auth",
			Err: ErrAuthFailed,
		}

		expected := "vault auth: vault: authentication failed"
		if err.Error() != expected {
			t.Errorf("Error() = %q, want %q", err.Error(), expected)
		}
	})

	t.Run("unwrap error", func(t *testing.T) {
		err := &VaultError{
			Op:  "read",
			Err: ErrSecretNotFound,
		}

		if err.Unwrap() != ErrSecretNotFound {
			t.Error("Unwrap() did not return underlying error")
		}
	})
}

func TestAuthMethodNames(t *testing.T) {
	tests := []struct {
		auth AuthMethod
		want string
	}{
		{WithToken("token"), "token"},
		{WithAppRole("role", "secret"), "approle"},
		{WithKubernetes("role"), "kubernetes"},
		{WithUserPass("user", "pass"), "userpass"},
		{WithLDAP("user", "pass"), "ldap"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.auth.Name(); got != tt.want {
				t.Errorf("Name() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewClientValidation(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		_, err := New(nil)
		if err == nil {
			t.Error("New(nil) should return error")
		}
	})

	t.Run("missing address", func(t *testing.T) {
		_, err := New(&Config{
			Auth: WithToken("token"),
		})
		if err == nil {
			t.Error("New() with empty address should return error")
		}
	})

	t.Run("missing auth", func(t *testing.T) {
		_, err := New(&Config{
			Address: "http://localhost:8200",
		})
		if err == nil {
			t.Error("New() without auth should return error")
		}
	})
}
