# xConfig

[![Go Reference](https://pkg.go.dev/badge/github.com/sxwebdev/xconfig.svg)](https://pkg.go.dev/github.com/sxwebdev/xconfig)
[![Go Report Card](https://goreportcard.com/badge/github.com/sxwebdev/xconfig)](https://goreportcard.com/report/github.com/sxwebdev/xconfig)

A lightweight, zero-dependency, and highly extensible configuration management library for Go applications.

## Features

- **Zero Dependencies** - No external dependencies in the core library
- **Plugin-Based Architecture** - Mix and match only the configuration sources you need
- **Type-Safe** - Strongly typed configuration with struct tags
- **Multiple Sources** - Support for defaults, environment variables, command-line flags, and config files
- **Nested Structures** - Full support for nested configuration structs
- **Rich Type Support** - All basic Go types, `time.Duration`, and custom types via `encoding.TextUnmarshaler`
- **Validation** - Built-in validation support through plugins
- **Documentation Generation** - Auto-generate markdown documentation for your configuration

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
        Password string `secret:"DB_PASSWORD" usage:"Database password"`
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
    ".json": json.Unmarshal,
    // Add more formats: ".yaml": yaml.Unmarshal, ".toml": toml.Unmarshal, etc.
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

### Secret Management

Use the `secret` tag for sensitive data that should be loaded from a secret provider:

```go
import (
    "github.com/sxwebdev/xconfig"
    "github.com/sxwebdev/xconfig/plugins/secret"
)

type Config struct {
    DBPassword string `secret:"DATABASE_PASSWORD"`
    APIToken   string `secret:"API_TOKEN"`
}

// Custom secret provider (e.g., AWS Secrets Manager, HashiCorp Vault, etc.)
secretProvider := func(name string) (string, error) {
    // Implement your secret fetching logic here
    switch name {
    case "DATABASE_PASSWORD":
        return fetchFromVault(name)
    case "API_TOKEN":
        return fetchFromAWS(name)
    default:
        return "", fmt.Errorf("secret not found: %s", name)
    }
}

cfg := &Config{}
_, err := xconfig.Load(cfg, xconfig.WithPlugins(secret.New(secretProvider)))
```

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
    xconfig.WithSkipDefaults(),      // Don't load from 'default' tags
    xconfig.WithSkipEnv(),            // Don't load from environment
    xconfig.WithSkipFlags(),          // Don't load from command-line flags
    xconfig.WithSkipCustomDefaults(), // Don't call SetDefaults()
)
```

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
| `secret`  | Secret identifier for secret provider | `secret:"DB_PASSWORD"`  |
| `usage`   | Description for documentation/help    | `usage:"Server port"`   |
| `xconfig` | Override field name in flat structure | `xconfig:"custom_name"` |

## Available Plugins

| Plugin             | Description                                             |
| ------------------ | ------------------------------------------------------- |
| **defaults**       | Load values from `default` struct tags                  |
| **customdefaults** | Call `SetDefaults()` method if implemented              |
| **env**            | Load values from environment variables                  |
| **flag**           | Load values from command-line flags                     |
| **loader**         | Load values from configuration files (JSON, YAML, etc.) |
| **secret**         | Load sensitive values from secret providers             |
| **validate**       | Validate configuration after loading                    |

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
