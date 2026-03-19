# xconfigvault — HashiCorp Vault Integration

## Table of Contents

- [Overview](#overview)
- [Installation](#installation)
- [Configuration](#configuration)
- [Authentication methods](#authentication-methods)
- [Secret path format](#secret-path-format)
- [Caching](#caching)
- [TLS configuration](#tls-configuration)
- [Environment-based setup](#environment-based-setup)
- [Watcher (secret rotation)](#watcher)
- [Integration with xconfig](#integration-with-xconfig)
- [Error handling](#error-handling)

## Overview

Package `sourcers/xconfigvault` provides a HashiCorp Vault client that implements the
`secret.Sourcer` interface (`func(string) (string, error)`). It supports KV v1 and v2
engines, multiple auth methods, caching, TLS, and secret watching.

Import: `github.com/sxwebdev/xconfig/sourcers/xconfigvault`

## Installation

Requires the `hashicorp` build tag or explicit dependency:

```bash
go get github.com/sxwebdev/xconfig/sourcers/xconfigvault
```

This pulls in `github.com/hashicorp/vault-client-go`.

## Configuration

```go
type Config struct {
    Address      string       // Vault server address (required)
    Namespace    string       // Vault namespace (Enterprise)
    TLS          *TLSConfig   // TLS settings
    Auth         AuthMethod   // Authentication method (required)
    Cache        *CacheConfig // Caching behavior (defaults to enabled, 5m TTL)
    DefaultMount string       // Default mount path (defaults to "secret")
    KVVersion    int          // KV engine version: 1 or 2 (defaults to 2)
}
```

## Authentication methods

Create auth methods using constructor functions:

| Method     | Constructor                               | Description                     |
| ---------- | ----------------------------------------- | ------------------------------- |
| Token      | `WithToken(token string)`                 | Static token auth               |
| AppRole    | `WithAppRole(roleID, secretID string)`    | AppRole auth                    |
| Kubernetes | `WithKubernetes(role, jwt string)`        | Kubernetes service account auth |
| UserPass   | `WithUserPass(username, password string)` | Username/password auth          |
| LDAP       | `WithLDAP(username, password string)`     | LDAP auth                       |

All methods implement the `AuthMethod` interface:

```go
type AuthMethod interface {
    Login(ctx context.Context, client *vault.Client) error
}
```

## Secret path format

Paths use the format: `mount/path/to/secret#key`

- `secret/myapp/config#db_password` — mount=secret, path=myapp/config, key=db_password
- `myapp/config#db_password` — uses DefaultMount, path=myapp/config, key=db_password

For KV v2, do NOT include "data/" in the path — it is added automatically.

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

The watcher monitors secrets for changes and triggers callbacks. Use `client.Watch()`
to start watching specific paths. The watcher runs in a background goroutine and checks
for version changes based on the configured `RefreshInterval`.

## Integration with xconfig

```go
import (
    "github.com/sxwebdev/xconfig"
    "github.com/sxwebdev/xconfig/plugins/secret"
    "github.com/sxwebdev/xconfig/sourcers/xconfigvault"
)

type Config struct {
    DBPassword string `secret:"secret/myapp/config#db_password"`
    APIKey     string `secret:"secret/myapp/config#api_key"`
}

func main() {
    vaultClient, err := xconfigvault.New(&xconfigvault.Config{
        Address: "https://vault.example.com:8200",
        Auth:    xconfigvault.WithToken("s.mytoken"),
    })
    if err != nil {
        log.Fatal(err)
    }
    defer vaultClient.Close()

    cfg := &Config{}
    _, err = xconfig.Load(cfg, xconfig.WithPlugins(
        secret.New(vaultClient.Sourcer()),
    ))
    if err != nil {
        log.Fatal(err)
    }
}
```

## Error handling

Sentinel errors in `xconfigvault`:

| Error                 | Meaning                                  |
| --------------------- | ---------------------------------------- |
| `ErrNoAuthMethod`     | Config.Auth is nil                       |
| `ErrClientClosed`     | Client.Close() was already called        |
| `ErrInvalidPath`      | Path doesn't match `path#key` format     |
| `ErrKeyNotFound`      | Key not found in secret data             |
| `ErrSecretNotFound`   | Secret path not found in Vault           |
| `ErrPermissionDenied` | Token lacks permission for the operation |
| `ErrVaultUnreachable` | Cannot connect to Vault server           |
| `ErrTokenExpired`     | Authentication token has expired         |

All errors are wrapped with operation and path context via `VaultError` type.
