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

The `vault:"true"` tag marks a field to be sourced from Vault. The `secret:"true"` tag is
independent — it marks a field as sensitive (for masking in logs/docs).

### Background Config Refresh

Plugins implementing `Refreshable` support real-time config updates without restart:

```go
xc.StartRefresh(ctx, 1*time.Minute, func(changes []plugins.FieldChange) {
    for _, c := range changes {
        log.Printf("config changed: %s %q -> %q", c.FieldName, c.OldValue, c.NewValue)
        if c.FieldName == "Database.Password" {
            reconnectDB(c.NewValue)
        }
    }
})
defer xc.StopRefresh()
```

`FieldChange.FieldName` contains the full field path (e.g., `Database.Postgres.Password`).
Any plugin implementing `plugins.Refreshable` participates in the refresh cycle automatically.

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

## Supported Types

- All basic Go types: `string`, `bool`, `int`, `int8`, `int16`, `int32`, `int64`, `uint`, `uint8`, `uint16`, `uint32`, `uint64`, `float32`, `float64`
- `time.Duration`
- Slices of supported types: `[]string`, `[]int`, etc.
- Any type implementing `encoding.TextUnmarshaler`

## Examples

See the [examples](https://github.com/sxwebdev/xconfig/tree/master/examples) directory for more complete examples.

## License

MIT License
