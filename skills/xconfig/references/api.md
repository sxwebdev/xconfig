# xconfig API Reference

## Table of Contents

- [Core functions](#core-functions)
- [Options](#options)
- [Config interface](#config-interface)
- [flat package](#flat-package)
- [Plugin interfaces](#plugin-interfaces)
- [Built-in plugins](#built-in-plugins)
- [Decoders](#decoders)
- [Supported types](#supported-types)

## Core functions

### `xconfig.Load(conf any, opts ...Option) (Config, error)`

Creates a config manager with standard plugins (defaults, custom defaults, files, env, flags),
parses all sources, and returns the `Config` handle. This is the primary entry point.

```go
cfg := &MyConfig{}
c, err := xconfig.Load(cfg)
c, err := xconfig.Load(cfg, xconfig.WithEnvPrefix("MYAPP"))
c, err := xconfig.Load(cfg, xconfig.WithLoader(l), xconfig.WithPlugins(secret.New(sourcer)))
```

### `xconfig.Custom(conf any, ps ...plugins.Plugin) (Config, error)`

Creates a config manager with only the explicitly provided plugins. Does NOT call `Parse()` —
you must call `c.Parse()` yourself.

```go
c, err := xconfig.Custom(cfg, defaults.New(), env.New(""))
if err != nil { ... }
err = c.Parse()
```

### `xconfig.GenerateMarkdown(cfg any, opts ...Option) (string, error)`

Loads config (same as `Load`) and generates a markdown table documenting all fields with
their env names, defaults, usage, required/secret status, and examples.

### `xconfig.GetUnknownFields(c Config) map[string][]string`

Returns unknown fields found in config files (map of filepath to field paths).
Only populated when a loader is configured.

## Options

| Option                        | Effect                                      |
| ----------------------------- | ------------------------------------------- |
| `WithSkipDefaults()`          | Skip `default` tag processing               |
| `WithSkipCustomDefaults()`    | Skip `SetDefaults()` call                   |
| `WithSkipFiles()`             | Skip file loading                           |
| `WithSkipEnv()`               | Skip environment variable loading           |
| `WithSkipFlags()`             | Skip CLI flag registration and parsing      |
| `WithEnvPrefix(prefix)`       | Prefix all env var lookups (e.g., `MYAPP_`) |
| `WithLoader(loader)`          | Use a custom file loader                    |
| `WithPlugins(plugins...)`     | Append custom plugins after standard ones   |
| `WithDisallowUnknownFields()` | Fail if config files contain unknown fields |

## Config interface

```go
type Config interface {
    Parse() error                // Execute all plugins in order
    Usage() (string, error)      // Generate text usage information
    Options() *options           // Access configured options
    Fields() flat.Fields         // Access flattened fields
    StartRefresh(ctx context.Context, interval time.Duration, onChange func([]plugins.FieldChange))
    StopRefresh()
}
```

`StartRefresh` starts a background goroutine that periodically calls `Refresh(ctx)` on all
plugins implementing `plugins.Refreshable`. The `onChange` callback is invoked with changed
fields (full paths like `Database.Postgres.Password`). Call `StopRefresh()` for graceful shutdown.

## flat package

### `flat.View(s any) (Fields, error)`

Flattens a struct pointer into a slice of `Field` interfaces. Supports nested structs,
anonymous structs, and maps with struct values.

### `flat.Field` interface

```go
type Field interface {
    Name() string                    // Dot-separated path: "Database.Host"
    EnvName() string                 // Auto-generated: "DATABASE_HOST"
    Tag(key string) (string, bool)   // Read struct tag
    ParentTag() reflect.StructTag    // Parent struct's tags
    Meta() map[string]string         // Mutable plugin metadata
    String() string                  // Default tag value
    Set(value string) error          // Type-coercing setter
    IsZero() bool                    // Check if zero-valued
    FieldValue() reflect.Value       // Underlying reflect.Value
    FieldType() reflect.StructField  // Underlying reflect.StructField
}
```

## Plugin interfaces

```go
// Base interface — all plugins must implement Parse()
type Plugin interface {
    Parse() error
}

// Walker receives the raw struct — used by file loaders
type Walker interface {
    Plugin
    Walk(config any) error
}

// Visitor receives flattened fields — used by env, flags, secrets
type Visitor interface {
    Plugin
    Visit(fields flat.Fields) error
}

// Refreshable — plugin supports background config refresh
type Refreshable interface {
    Plugin
    Refresh(ctx context.Context) ([]FieldChange, error)
}

// FieldChange describes a config field change detected during refresh
type FieldChange struct {
    FieldName string // Full path: "Database.Postgres.Password"
    OldValue  string
    NewValue  string
}
```

A plugin can implement multiple interfaces (Walker + Visitor, Visitor + Refreshable, etc.).

## Built-in plugins

### defaults (`plugins/defaults`)

- `defaults.New()` — reads `default` tag, sets zero-valued fields
- `defaults.NewMetaOnly()` — only registers metadata (no field mutation)
- `defaults.NewWithRescan(loader)` — rescans struct after loading (catches map entries)

### customdefaults (`plugins/customdefaults`)

- `customdefaults.New()` — calls `SetDefaults()` if the struct implements it

### env (`plugins/env`)

- `env.New(prefix string)` — loads from env vars; prefix is prepended with `_`

### flag (`plugins/flag`)

- `flag.Standard()` — registers fields with `flag` tag in `flag.CommandLine`

### loader (`plugins/loader`)

- `loader.NewLoader(decoders map[string]Unmarshal) (*Loader, error)` — create with decoder map
- `loader.AddFile(path string, optional bool) error` — add config file
- `loader.AddFiles(paths []string, optional bool) error` — add multiple files
- `loader.RegisterDecoder(format string, decoder Unmarshal) error` — register decoder
- `loader.DisallowUnknownFields(bool)` — enable strict mode
- `loader.GetUnknownFields() map[string][]string` — get unknown fields
- `loader.PresentFields() map[string]struct{}` — get explicitly set fields
- `loader.NewReader(src io.Reader, unmarshal Unmarshal) Plugin` — load from reader

### secret (`plugins/secret`)

- `secret.New(sourcer Sourcer) Plugin` — sourcer is `func(string) (string, error)`
- Fields with empty `secret:""` tag auto-generate name from field path (uppercased, dots→underscores)

### validate (`plugins/validate`)

- `validate.New(fn func(any) error) Plugin` — custom validator function
- Also auto-calls `Validate()` method if the struct implements it

## Decoders

### xconfigyaml

```go
import "github.com/sxwebdev/xconfig/decoders/xconfigyaml"
decoder := xconfigyaml.New()
// decoder.Unmarshal(data, v) or use decoder.Unmarshal as loader.Unmarshal
```

Uses `github.com/goccy/go-yaml`.

### xconfigdotenv

```go
import "github.com/sxwebdev/xconfig/decoders/xconfigdotenv"
decoder := xconfigdotenv.New()
```

Uses `github.com/joho/godotenv`. Maps `.env` keys to struct fields by normalizing names
(lowercase, strip underscores). Supports nested structs, pointers, maps, and slices.

### xconfigjson

```go
import "github.com/sxwebdev/xconfig/decoders/xconfigjson"
// Uses encoding/json — standard library
```

## Supported types

The `field.Set(string)` method handles:

- `string` — direct assignment
- `bool` — via `strconv.ParseBool`
- `int`, `int8`, `int16`, `int32`, `int64` — via `strconv.ParseInt`
- `uint`, `uint8`, `uint16`, `uint32`, `uint64` — via `strconv.ParseUint`
- `float32`, `float64` — via `strconv.ParseFloat`
- `time.Duration` — via `time.ParseDuration`
- Slices of above types — comma-separated values
- Any `encoding.TextUnmarshaler` — via `UnmarshalText`
