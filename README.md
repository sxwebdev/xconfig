# xConfig

[![Go Reference](https://pkg.go.dev/badge/github.com/sxwebdev/xconfig.svg)](https://pkg.go.dev/github.com/sxwebdev/xconfig)
[![Go Version](https://img.shields.io/badge/go-1.25-blue)](https://go.dev/)
[![Go Report Card](https://goreportcard.com/badge/github.com/sxwebdev/xconfig)](https://goreportcard.com/report/github.com/sxwebdev/xconfig)
[![License](https://img.shields.io/github/license/sxwebdev/xconfig)](LICENSE)

A lightweight, zero-dependency, and highly extensible configuration management library for Go applications.

## Features

- **Zero Dependencies** - No external dependencies in the core library
- **Plugin-Based Architecture** - Mix and match only the configuration sources you need
- **Type-Safe** - Strongly typed configuration with struct tags
- **Multiple Sources** - Support for defaults, environment variables, command-line flags, and config files
- **HashiCorp Vault** - Native integration with batch loading, token renewal, auto-retry, and metrics
- **Background Refresh** - Real-time config updates without restart via `Refreshable` plugins
- **Nested Structures** - Full support for nested configuration structs
- **Rich Type Support** - All basic Go types, `time.Duration`, and custom types via `encoding.TextUnmarshaler`
- **Validation** - Built-in validation support through plugins
- **Documentation Generation** - Auto-generate markdown documentation for your configuration

## AI Agent Skills

This repository includes [AI agent skills](https://github.com/sxwebdev/skills) with documentation and usage examples for all packages. Install them with the [skills](https://github.com/sxwebdev/skills) CLI:

```bash
go install github.com/sxwebdev/skills/cmd/skills@latest
skills init
skills repo add sxwebdev/xconfig
```

## Installation

```bash
go get github.com/sxwebdev/xconfig
```

## Quick Start

```go
package main

import (
    "fmt"
    "log"

    "github.com/sxwebdev/xconfig"
)

type Config struct {
    Host     string `default:"localhost" env:"HOST" flag:"host" usage:"Server host address"`
    Port     int    `default:"8080" env:"PORT" flag:"port" usage:"Server port"`
    Debug    bool   `default:"false" env:"DEBUG" flag:"debug" usage:"Enable debug mode"`
    Database struct {
        Host     string `default:"localhost" env:"DB_HOST" usage:"Database host"`
        Port     int    `default:"5432" env:"DB_PORT" usage:"Database port"`
        Name     string `default:"myapp" env:"DB_NAME" usage:"Database name"`
        Password string `vault:"true" env:"DB_PASSWORD" secret:"true" usage:"Database password"`
    }
}

func main() {
    cfg := &Config{}

    // Load configuration from defaults, env vars, and flags
    _, err := xconfig.Load(cfg)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Server: %s:%d\n", cfg.Host, cfg.Port)
    fmt.Printf("Database: %s:%d/%s\n", cfg.Database.Host, cfg.Database.Port, cfg.Database.Name)
}
```

## Usage

### Basic Configuration Loading

The `Load` function provides the most common configuration pattern, automatically enabling:

1. Default values from struct tags
2. Custom defaults via `SetDefaults()` method
3. Configuration files (if provided)
4. Environment variables
5. Command-line flags
6. Custom plugins (Vault, etc.) — highest priority

```go
type AppConfig struct {
    APIKey    string `default:"dev-key" env:"API_KEY" flag:"api-key"`
    Timeout   int    `default:"30" env:"TIMEOUT" flag:"timeout"`
    EnableLog bool   `default:"true" env:"ENABLE_LOG" flag:"enable-log"`
}

cfg := &AppConfig{}
_, err := xconfig.Load(cfg)
```

### Loading from Configuration Files

xConfig supports multiple configuration file formats through decoders:

```go
import (
    "encoding/json"

    "github.com/sxwebdev/xconfig"
    "github.com/sxwebdev/xconfig/plugins/loader"
    "github.com/sxwebdev/xconfig/decoders/xconfigyaml"
    "github.com/sxwebdev/xconfig/decoders/xconfigdotenv"
)

type Config struct {
    Server struct {
        Host string `json:"host"`
        Port int    `json:"port"`
    } `json:"server"`
}

cfg := &Config{}

// Create loader with JSON decoder
l, err := loader.NewLoader(map[string]loader.Unmarshal{
    "json": json.Unmarshal,
    "yaml": xconfigyaml.New().Unmarshal,
    "env":  xconfigdotenv.New().Unmarshal,
})
if err != nil {
    log.Fatal(err)
}

// Add configuration file (optional=false means file must exist)
err = l.AddFile("config.json", false)
if err != nil {
    log.Fatal(err)
}

_, err = xconfig.Load(cfg, xconfig.WithLoader(l))
```

### Environment Variables with Prefix

```go
type Config struct {
    APIKey string `env:"API_KEY"`
    Secret string `env:"SECRET"`
}

cfg := &Config{}

// All env vars will be prefixed with "MYAPP_"
// So it will look for: MYAPP_API_KEY and MYAPP_SECRET
_, err := xconfig.Load(cfg, xconfig.WithEnvPrefix("MYAPP"))
```

### Slices and Maps from Environment Variables

The env plugin can populate slices of structs and maps directly from environment
variables — including nil/empty containers. Slice elements are addressed by
their numeric index; map entries by their key.

#### Slice of struct

```go
type Item struct {
    Key1 string
    Key2 string
}

type Config struct {
    Items []Item
}

// ITEMS_0_KEY_1=a, ITEMS_0_KEY_2=b, ITEMS_1_KEY_1=c
//   → Items: [{Key1: "a", Key2: "b"}, {Key1: "c"}]
```

The slice grows automatically to fit the largest index found in the
environment. `[]*Item` (pointer elements) is also supported — empty slots are
allocated as `&Item{}`.

#### Map of primitive

```go
type Config struct {
    Tags     map[string]string
    Counts   map[string]int
    Timeouts map[string]time.Duration
}

// TAGS_FOO=v1, TAGS_BAR=v2  → Tags:     {FOO: "v1", BAR: "v2"}
// COUNTS_A=10, COUNTS_B=42  → Counts:   {A: 10, B: 42}
// TIMEOUTS_FAST=5s          → Timeouts: {FAST: 5*time.Second}
```

The suffix after the map's env prefix becomes the map key verbatim — case is
preserved. Pre-populated entries are kept; matching env vars override them.

#### Map of struct

```go
type Server struct {
    Host string
    Port int
}

type Config struct {
    Servers map[string]Server
}

// SERVERS_PRIMARY_HOST=10.0.0.1, SERVERS_PRIMARY_PORT=5432, SERVERS_BACKUP_HOST=10.0.0.2
//   → Servers: {
//       PRIMARY: {Host: "10.0.0.1", Port: 5432},
//       BACKUP:  {Host: "10.0.0.2"},
//     }
```

The plugin discovers map keys by matching the suffix of each env var against
the inner struct's field names (longest match wins). `map[string]*Server` is
supported the same way — entries are allocated as `&Server{}`.

#### `env` tag inside a slice/map element

When a field inside a slice element or map value carries its own `env:"..."`
tag, it acts as a **per-segment override** — the surrounding slice index or
map key is preserved so different elements don't collide:

```go
type Item struct {
    Key1 string
    Port int `env:"P"`
}

type Config struct {
    Items []Item
}

// ITEMS_0_KEY_1=a, ITEMS_0_P=1000, ITEMS_1_P=2000
//   → Items: [{Key1: "a", Port: 1000}, {Port: 2000}]
```

At the top level the `env:"..."` tag still anchors the full env name (only the
global `WithEnvPrefix` is prepended) — so `Database.Host env:"DB_HOST"`
continues to read from `DB_HOST`, not `DATABASE_DB_HOST`.

### Custom Defaults with SetDefaults

Implement the `SetDefaults()` method to programmatically set default values:

```go
type Config struct {
    Host string
    Port int
    URLs []string
}

func (c *Config) SetDefaults() {
    c.Host = "localhost"
    c.Port = 8080
    c.URLs = []string{"https://api.example.com", "https://backup.example.com"}
}

cfg := &Config{}
_, err := xconfig.Load(cfg)
// cfg.Host will be "localhost" unless overridden by env or flags
```

### HashiCorp Vault Integration

Use the `vault` tag to load secrets from HashiCorp Vault with automatic token renewal,
batch loading, auto-retry on 401/403, and metrics callback:

```go
import (
    "github.com/sxwebdev/xconfig"
    "github.com/sxwebdev/xconfig/sourcers/xconfigvault"
)

type Config struct {
    Host       string `default:"localhost" env:"HOST"`
    DBPassword string `vault:"true" env:"DB_PASSWORD" secret:"true"`
    APIKey     string `vault:"true" env:"API_KEY" secret:"true"`
}

// Create Vault client (supports Token, AppRole, Kubernetes, UserPass, LDAP auth)
vaultClient, err := xconfigvault.New(&xconfigvault.Config{
    Address:    os.Getenv("VAULT_ADDR"),
    Auth:       xconfigvault.WithKubernetes("my-service-role"),
    SecretPath: "kv/myservice/config",
    Metrics:    xconfigvault.MetricsFunc(func(e xconfigvault.Event) {
        // Monitor auth failures, retries, etc.
        promCounter.WithLabelValues(string(e.Type)).Inc()
    }),
})
if err != nil {
    log.Fatal(err)
}
defer vaultClient.Close()

cfg := &Config{}
xc, err := xconfig.Load(cfg, xconfig.WithPlugins(vaultClient.Plugin()))
```

The Vault plugin:

- Runs last in the plugin chain (maximum priority over env, flags, defaults)
- Batch-loads all secrets in a single HTTP request
- Automatically renews tokens in the background
- Retries on 401/403 with token refresh
- Emits operational events via `MetricsCallback`
- Populates slice-of-struct and map fields from secret keys the same way the
  env plugin does — e.g. secret keys `ITEMS_0_PASSWORD`, `SERVERS_PRIMARY_PASSWORD`
  grow the containers and create entries automatically. Pointer element types
  (`[]*T`, `map[string]*T`) are supported.

The `vault:"true"` tag marks a field to be sourced from Vault. The `secret:"true"` tag is
independent — it marks a field as sensitive (for masking in logs/docs).

### Background Config Refresh

Plugins implementing `Refreshable` support runtime updates without mutating the
configuration struct returned by `Load`. Keep the active configuration in an
`atomic.Pointer` and replace it with an owned snapshot after every successful refresh:

```go
var active atomic.Pointer[Config]
initial, err := xconfig.Snapshot[Config](xc)
if err != nil {
    return err
}
active.Store(&initial)

results, err := xc.StartRefresh(ctx, time.Minute)
if err != nil {
    return err
}
defer xc.StopRefresh()

go func() {
    for result := range results {
        if result.Err != nil {
            log.Printf("config refresh failed: %v", result.Err)
        }
        for _, warning := range result.Warnings {
            log.Printf("config refresh warning: %v", warning)
        }
        if !result.Published {
            continue
        }
        latest, err := xconfig.Snapshot[Config](xc)
        if err != nil {
            log.Printf("config snapshot failed: %v", err)
            continue
        }
        active.Store(&latest)
        for _, change := range result.Changes {
            log.Printf("config field changed: %s", change.FieldName)
        }
    }
}()
```

`FieldChange.FieldName` contains the full field path (e.g., `Database.Postgres.Password`).
Change events intentionally contain no old or new values, preventing secrets from
leaking into logs and metrics. `Snapshot` owns maps (keys included), slices, and
config-data pointers such as `*big.Int` or `*bytes.Buffer`; package boundaries do not
change clone behavior. Because keys are copied too, a key containing pointers is a
distinct key in the copy. Mark intentionally shared, concurrency-safe runtime
dependencies such as `*http.Client`, `*os.File`, or `*slog.LevelVar` with
`xconfig_shared:"true"` to retain their identity.
Immutable value types keep their identity automatically and need no tag: inside a struct
without exported fields (`time.Time`, `netip.Addr`, `unique.Handle`, ...) the interned or
sentinel pointers are shared rather than cloned, so `Is4()` and `==` keep working, while
such a type's slices and maps are still cloned. The immutable stdlib pointers
`*time.Location` and `*regexp.Regexp` are shared for the same reason.
The struct originally passed to `Load` contains the initial configuration and is never
modified by refresh. The configuration is captured when `Parse` succeeds, so every caller
sees the same value regardless of when it first calls `Snapshot`.

Refresh notifications use a bounded latest-state queue and never block the refresh loop.
`RefreshResult.Dropped` reports coalesced notifications; readers should always obtain the
latest snapshot after a result with `Published=true`. Async coalescing retains at most 16
warnings plus the first and latest errors, so an ignored channel has bounded memory use.

Services that already own a lifecycle loop (for example MX services) can call
`result := xc.Refresh(ctx)` directly, handle `result.Err` and `result.Warnings`, then
publish a new snapshot when `result.Published` is true.
Any plugin implementing `plugins.Refreshable` participates in the refresh cycle automatically.
`StartRefresh` reports `ErrNoRefreshablePlugins` when no registered plugin supports refresh,
so a loop that could never emit anything fails immediately instead of running silently.

### Secret Management (Legacy)

The `secret` plugin loads sensitive data from a custom provider function:

```go
import "github.com/sxwebdev/xconfig/plugins/secret"

type Config struct {
    DBPassword string `secret:"DATABASE_PASSWORD"`
}

secretProvider := func(name string) (string, error) {
    return fetchFromVault(name)
}

cfg := &Config{}
_, err := xconfig.Load(cfg, xconfig.WithPlugins(secret.New(secretProvider)))
```

For new projects, prefer the [Vault plugin](#hashicorp-vault-integration) which provides
batch loading, token renewal, and background refresh out of the box.

### Validation

Add validation to ensure your configuration meets requirements:

```go
import (
    "fmt"
    "github.com/sxwebdev/xconfig"
    "github.com/sxwebdev/xconfig/plugins/validate"
)

type Config struct {
    Port int    `default:"8080"`
    Host string `default:"localhost"`
}

// Implement Validate method
func (c *Config) Validate() error {
    if c.Port < 1 || c.Port > 65535 {
        return fmt.Errorf("port must be between 1 and 65535")
    }
    if c.Host == "" {
        return fmt.Errorf("host cannot be empty")
    }
    return nil
}

cfg := &Config{}

// Validation happens automatically after loading
_, err := xconfig.Load(cfg)
if err != nil {
    log.Fatal(err) // Will fail if validation fails
}
```

You can also use external validators:

```go
import (
    "github.com/go-playground/validator/v10"
    "github.com/sxwebdev/xconfig/plugins/validate"
)

type Config struct {
    Email string `validate:"required,email"`
    Age   int    `validate:"gte=0,lte=130"`
}

cfg := &Config{}

v := validator.New()
_, err := xconfig.Load(cfg, xconfig.WithPlugins(
    validate.New(func(a any) error {
        return v.Struct(a)
    }),
))
```

### Selective Plugin Loading

Control which plugins are enabled:

```go
cfg := &Config{}

// Skip certain plugins
_, err := xconfig.Load(cfg,
    xconfig.WithSkipDefaults(),         // Don't load from 'default' tags
    xconfig.WithSkipEnv(),              // Don't load from environment
    xconfig.WithSkipFlags(),            // Don't load from command-line flags
    xconfig.WithSkipCustomDefaults(),   // Don't call SetDefaults()
    xconfig.WithDisallowUnknownFields(), // Fail if config files contain unknown fields
)
```

**Unknown Fields Validation**: Enable `WithDisallowUnknownFields()` to detect typos and configuration errors in JSON/YAML files. When enabled, loading will fail if any fields in the config files don't match your struct definition. Use `xconfig.GetUnknownFields()` to retrieve unknown fields without failing.

### Documentation Generation

Generate markdown documentation for your configuration:

```go
type Config struct {
    Host   string `default:"localhost" usage:"Server host address"`
    Port   int    `default:"8080" usage:"Server port number"`
    APIKey string `secret:"API_KEY" usage:"API authentication key"`
}

cfg := &Config{}

markdown, err := xconfig.GenerateMarkdown(cfg)
if err != nil {
    log.Fatal(err)
}

// Save to file
os.WriteFile("CONFIG.md", []byte(markdown), 0644)
```

### Usage Information

Get runtime configuration information:

```go
cfg := &Config{}
c, err := xconfig.Load(cfg)
if err != nil {
    log.Fatal(err)
}

usage, err := c.Usage()
if err != nil {
    log.Fatal(err)
}

fmt.Println(usage)
```

## Available Struct Tags

| Tag       | Description                           | Example                 |
| --------- | ------------------------------------- | ----------------------- |
| `default` | Default value for the field           | `default:"8080"`        |
| `env`     | Environment variable name             | `env:"PORT"`            |
| `flag`    | Command-line flag name                | `flag:"port"`           |
| `secret`  | Marks field as sensitive (metadata)   | `secret:"true"`         |
| `vault`   | Field sourced from HashiCorp Vault    | `vault:"true"`          |
| `usage`   | Description for documentation/help    | `usage:"Server port"`   |
| `xconfig` | Override field name in flat structure | `xconfig:"custom_name"` |
| `xconfig_shared` | Keep a concurrency-safe dependency shared in snapshots | `xconfig_shared:"true"` |

## Available Plugins

| Plugin             | Description                                                   |
| ------------------ | ------------------------------------------------------------- |
| **defaults**       | Load values from `default` struct tags                        |
| **customdefaults** | Call `SetDefaults()` method if implemented                    |
| **env**            | Load values from environment variables                        |
| **flag**           | Load values from command-line flags                           |
| **loader**         | Load from configuration files (JSON, YAML, etc.)              |
| **secret**         | Mark fields as sensitive, load from custom providers          |
| **validate**       | Validate configuration after loading                          |
| **xconfigvault**   | HashiCorp Vault: batch loading, token renewal, retry, refresh |

## Custom Plugins

Create your own plugins by implementing the `Plugin` interface with either `Walker` or `Visitor`:

```go
import (
    "github.com/sxwebdev/xconfig/flat"
    "github.com/sxwebdev/xconfig/plugins"
)

type myPlugin struct {
    fields flat.Fields
}

// Visitor interface - called once with all fields
func (p *myPlugin) Visit(fields flat.Fields) error {
    p.fields = fields
    // Setup phase: register metadata, validate structure, etc.
    return nil
}

// Parse is called to actually load configuration
func (p *myPlugin) Parse() error {
    for _, field := range p.fields {
        // Load configuration for each field
    }
    return nil
}

// Use your custom plugin
cfg := &Config{}
_, err := xconfig.Custom(cfg, &myPlugin{})
```

## Applying Defaults Programmatically

Use `xconfig.ApplyDefaults` when data arrives through something other than
`xconfig.Load` — for example, a slice decoded directly with `yaml.Unmarshal`.
It walks the value and applies every `default:"..."` struct tag it finds,
leaving non-zero fields untouched.

```go
type Group struct {
    IsEnabled bool   `yaml:"is_enabled" default:"true"`
    Name      string `yaml:"name"`
    Port      int    `yaml:"port" default:"8080"`
}

var file struct {
    Groups []Group `yaml:"groups"`
}
if err := yaml.Unmarshal(data, &file); err != nil {
    return err
}

// Fills IsEnabled=true / Port=8080 for elements where YAML omitted them.
if err := xconfig.ApplyDefaults(&file.Groups); err != nil {
    return err
}
```

`ApplyDefaults` accepts a pointer to a struct, a slice of structs (`*[]T`), or
a slice of struct pointers (`*[]*T`). It also works through `xconfig.Load` /
`xconfig.Custom` automatically — `default` tags inside `[]Struct` fields are
applied the same way they are for nested structs and maps.

**Note on `bool` and other scalars.** Go's `yaml.Unmarshal` cannot distinguish
a field that was explicitly set to its zero value (e.g. `is_enabled: false`)
from a field that was simply absent — both end up as `false`. When that
distinction matters, declare the field as a pointer type (`*bool`) so the
absent case is represented by `nil`.

## Supported Types

- All basic Go types: `string`, `bool`, `int`, `int8`, `int16`, `int32`, `int64`, `uint`, `uint8`, `uint16`, `uint32`, `uint64`, `float32`, `float64`
- `time.Duration`
- Slices of supported types: `[]string`, `[]int`, etc.
- Any type implementing `encoding.TextUnmarshaler`

## Examples

See the [examples](https://github.com/sxwebdev/xconfig/tree/master/examples) directory for more complete examples.

## License

MIT License
