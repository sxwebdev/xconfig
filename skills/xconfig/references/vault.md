# xconfigvault — HashiCorp Vault Integration

## Table of Contents

- [Overview](#overview)
- [Installation](#installation)
- [Configuration](#configuration)
- [Authentication methods](#authentication-methods)
- [VaultPlugin (recommended)](#vaultplugin)
- [Token renewal](#token-renewal)
- [Auto-retry](#auto-retry)
- [Metrics callback](#metrics-callback)
- [Background refresh](#background-refresh)
- [Secret path format](#secret-path-format)
- [Caching](#caching)
- [TLS configuration](#tls-configuration)
- [Environment-based setup](#environment-based-setup)
- [Watcher (secret rotation)](#watcher)
- [Standalone usage](#standalone-usage)
- [Error handling](#error-handling)
- [Integration testing](#integration-testing)

## Overview

Package `sourcers/xconfigvault` provides a HashiCorp Vault integration for xconfig. It includes:

- **VaultPlugin** — an xconfig plugin (Visitor + Refreshable) that batch-loads secrets from Vault
- **Token renewal** — background goroutine that keeps tokens alive via renew-self or re-login
- **Auto-retry** — automatic retry with token refresh on 401/403 errors
- **Metrics callback** — operational events for monitoring and alerting
- **Background refresh** — detects secret rotation and updates config in real-time

Import: `github.com/sxwebdev/xconfig/sourcers/xconfigvault`

## Installation

```bash
go get github.com/sxwebdev/xconfig/sourcers/xconfigvault
```

This pulls in `github.com/hashicorp/vault-client-go` and `github.com/sxwebdev/xconfig`.

## Configuration

```go
type Config struct {
    Address      string           // Vault server address (required)
    Namespace    string           // Vault namespace (Enterprise)
    TLS          *TLSConfig       // TLS settings
    Auth         AuthMethod       // Authentication method (required)
    Cache        *CacheConfig     // Caching behavior (defaults: enabled, 5m TTL)
    DefaultMount string           // Default mount path (defaults to "secret")
    KVVersion    int              // KV engine version: 1 or 2 (defaults to 2)
    SecretPath   string           // KV path for batch loading (e.g., "kv/myservice/config")
    Metrics      MetricsCallback  // Optional operational event callback
    Renew        *RenewConfig     // Token renewal settings (defaults: 0.8 fraction, 60s check)
}
```

### RenewConfig

```go
type RenewConfig struct {
    Fraction            float64       // Renew at this fraction of lease (default: 0.8)
    NearExpiryThreshold time.Duration // Refresh if within this time of expiry (default: 5m)
    CheckInterval       time.Duration // Background check interval (default: 60s)
    MaxBackoff          time.Duration // Max retry backoff (default: 30s)
}
```

## Authentication methods

All methods implement the `AuthMethod` interface:

```go
type AuthMethod interface {
    Login(ctx context.Context, client *vault.Client) error
    Relogin(ctx context.Context, client *vault.Client) error
    Name() string
}
```

| Method     | Constructor                                          | Relogin |
| ---------- | ---------------------------------------------------- | ------- |
| Token      | `WithToken(token)`                                   | Error (static) |
| AppRole    | `WithAppRole(roleID, secretID)`                      | Re-login |
| Kubernetes | `WithKubernetes(role)` / `WithKubernetesPath(r,j,m)` | Re-login |
| UserPass   | `WithUserPass(username, password)`                   | Re-login |
| LDAP       | `WithLDAP(username, password)`                       | Re-login |

All non-token methods support `Relogin()` for automatic token recovery.

## VaultPlugin

The recommended way to use Vault with xconfig. VaultPlugin implements `plugins.Visitor` and
`plugins.Refreshable`, providing batch loading and background refresh.

### Struct tags

Use `vault:"true"` to mark fields that should be sourced from Vault:

```go
type Config struct {
    Host       string `env:"HOST" default:"localhost"`
    Port       int    `env:"PORT" default:"8080"`
    DBPassword string `vault:"true" env:"DB_PASSWORD" secret:"true"`
    APIKey     string `vault:"true" env:"API_KEY" secret:"true"`
    RedisHost  string `vault:"true" env:"REDIS_HOST"`
}
```

The vault key is derived from the `env` tag value if present, otherwise from `EnvName()`
(UPPER_SNAKE_CASE of the field name). Vault keys should match env var names.

### Usage

```go
client, err := xconfigvault.New(&xconfigvault.Config{
    Address:    os.Getenv("VAULT_ADDR"),
    Auth:       xconfigvault.WithKubernetes("my-service-role"),
    SecretPath: "kv/myservice/config",
    Metrics:    metricsCallback,
})
defer client.Close()

var cfg Config
xc, err := xconfig.Load(&cfg, xconfig.WithPlugins(client.Plugin()))
```

VaultPlugin runs last in the plugin chain and has **maximum priority** over all other sources.

### How it works

1. **Visit phase**: Collects all fields tagged `vault:"true"` and maps their env name to the field.
2. **Parse phase**: Calls `Client.GetMap(ctx, secretPath)` once (single HTTP request) and sets all matching fields via `field.Set()`.
3. **Refresh**: Re-fetches secrets, compares with previous values, sets changed fields, and returns `[]plugins.FieldChange`.

## Token renewal

The client starts a background goroutine that manages token lifecycle:

1. **Renew-self**: When the token is in the renewal window (last 20% of lease by default), calls `/auth/token/renew-self`.
2. **Re-login**: If renew-self fails, falls back to full re-authentication via `AuthMethod.Relogin()`.
3. **Exponential backoff**: Retries with 1s, 2s, 4s, ..., up to `MaxBackoff` (30s default).
4. **Coalescing**: Multiple concurrent refresh requests are deduplicated.

Token renewal is automatic — no user action required after `New()`.

## Auto-retry

All `Get()` and `GetMap()` calls use `fetchWithRetry`:

- On `ErrPermissionDenied` or `ErrTokenExpired`: triggers token refresh via renewer, then retries.
- Up to 3 retry attempts with exponential backoff.
- Each retry emits an `EventRetryAttempt` metric event.

## Metrics callback

Implement `MetricsCallback` to receive operational events:

```go
type MetricsCallback interface {
    OnEvent(event Event)
}

// Or use the function adapter:
metrics := xconfigvault.MetricsFunc(func(e xconfigvault.Event) {
    promCounter.WithLabelValues("myservice", string(e.Type)).Inc()
    if e.Error != nil {
        slog.Error("vault event", "type", e.Type, "err", e.Error)
    }
})
```

### Event types

| EventType              | When                                  |
| ---------------------- | ------------------------------------- |
| `EventAuthSuccess`     | Initial authentication succeeded      |
| `EventAuthFailed`      | Initial authentication failed         |
| `EventTokenRenewed`    | Token renew-self succeeded            |
| `EventTokenRenewFailed`| Token renew-self failed               |
| `EventReloginSuccess`  | Full re-authentication succeeded      |
| `EventReloginFailed`   | Full re-authentication failed         |
| `EventVaultUnreachable`| Cannot connect to Vault               |
| `EventSecretsFetched`  | Secrets batch-loaded successfully     |
| `EventRetryAttempt`    | Retry attempt after retryable error   |

## Background refresh

Use `Config.StartRefresh()` to detect secret rotation in Vault:

```go
xc.StartRefresh(ctx, 1*time.Minute, func(changes []plugins.FieldChange) {
    for _, c := range changes {
        slog.Info("config changed",
            "field", c.FieldName,  // "Database.Postgres.Password"
            "old", c.OldValue,
            "new", c.NewValue,
        )
        if c.FieldName == "Database.Postgres.Password" {
            reconnectDB(c.NewValue)
        }
    }
})
defer xc.StopRefresh()
```

The refresh invalidates the cache, fetches fresh secrets, and updates only changed fields.
`FieldChange.FieldName` is the full flat field path (e.g., `Database.Postgres.Password`).

## Secret path format

For standalone `Get()` calls, paths use the format: `mount/path/to/secret#key`

- `secret/myapp/config#db_password` — mount=secret, path=myapp/config, key=db_password
- `myapp/config#db_password` — uses DefaultMount, path=myapp/config, key=db_password

For KV v2, do NOT include "data/" in the path — it is added automatically.

For VaultPlugin, the `SecretPath` in Config is used (without `#key`). Keys are derived from field tags.

## Caching

Default cache config (enabled automatically):

```go
type CacheConfig struct {
    Enabled         bool          // Default: true
    TTL             time.Duration // Default: 5 minutes
    RefreshInterval time.Duration // Default: 1 minute
    RefreshAhead    bool          // Default: true (pre-emptive refresh)
}
```

Cache methods on `*Client`:

- `ClearCache()` — clear entire cache
- `InvalidateCache(path)` — invalidate specific path

## TLS configuration

```go
type TLSConfig struct {
    CACert     string // Path to CA certificate PEM file
    CAPath     string // Path to directory of CA certificate PEM files
    ClientCert string // Path to client certificate PEM file
    ClientKey  string // Path to client private key PEM file
    ServerName string // Server name for TLS verification
    Insecure   bool   // Disable TLS verification (not for production)
}
```

## Environment-based setup

`NewFromEnv()` reads from standard Vault environment variables:

| Variable            | Description            |
| ------------------- | ---------------------- |
| `VAULT_ADDR`        | Vault server address   |
| `VAULT_TOKEN`       | Authentication token   |
| `VAULT_NAMESPACE`   | Vault namespace        |
| `VAULT_CACERT`      | Path to CA certificate |
| `VAULT_SKIP_VERIFY` | Skip TLS verification  |

```go
client, err := xconfigvault.NewFromEnv()
```

## Watcher

The watcher monitors specific secrets for changes and triggers callbacks. Use `client.Watch()`
to start watching paths. The watcher runs in a background goroutine and checks for changes
based on the configured `RefreshInterval`.

For most use cases, prefer VaultPlugin + `StartRefresh()` over the low-level watcher.

## Standalone usage

The Vault client can be used without xconfig for direct secret access:

```go
client, err := xconfigvault.New(&xconfigvault.Config{
    Address: "https://vault:8200",
    Auth:    xconfigvault.WithToken("s.xxx"),
})

// Single key
val, err := client.Get(ctx, "secret/myapp/config#db_password")

// All keys from a path
secrets, err := client.GetMap(ctx, "secret/myapp/config")

// Sourcer for plugins/secret (legacy)
sourcer := client.Sourcer()
```

## Error handling

Sentinel errors in `xconfigvault`:

| Error                 | Meaning                                  |
| --------------------- | ---------------------------------------- |
| `ErrNoAuthMethod`     | Config.Auth is nil                       |
| `ErrAuthFailed`       | Authentication or re-authentication failed |
| `ErrClientClosed`     | Client.Close() was already called        |
| `ErrInvalidPath`      | Path doesn't match `path#key` format     |
| `ErrKeyNotFound`      | Key not found in secret data             |
| `ErrSecretNotFound`   | Secret path not found in Vault           |
| `ErrPermissionDenied` | Token lacks permission (triggers retry)  |
| `ErrVaultUnreachable` | Cannot connect to Vault server           |
| `ErrTokenExpired`     | Token has expired (triggers retry)       |

All errors are wrapped with operation and path context via `VaultError` type.

## Integration testing

Integration tests use Docker Compose with a real Vault dev server:

```bash
cd sourcers/xconfigvault
make integration-test
```

This starts Vault 1.21, runs all tests (build tag `integration`), and stops the container.
Tests cover: auth, batch loading, token renewal, secret rotation, auto-retry, metrics, priority, E2E.
