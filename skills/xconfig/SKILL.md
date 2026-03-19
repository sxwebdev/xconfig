---
name: xconfig
description: >-
  Go configuration management library (github.com/sxwebdev/xconfig) with plugin-based
  architecture, struct tags, and multi-source loading. Use this skill whenever working in
  the xconfig codebase — editing plugins, decoders, flat field processing, loaders, or
  tests. Also triggers when code imports "xconfig", "sxwebdev/xconfig", or references
  xconfig.Load, xconfig.Custom, flat.View, flat.Fields, plugins.Plugin, plugins.Walker,
  plugins.Visitor, loader.NewLoader, secret.New, validate.New, defaults, customdefaults,
  env, flag plugins, xconfigyaml, xconfigdotenv, xconfigvault, or GenerateMarkdown. Applies
  when the user mentions Go config management, struct tag configuration, environment variable
  loading, config file parsing, secret providers, config validation, or documentation generation
  in the context of this library.
user-invocable: false
---

# xconfig — Go Configuration Management Library

## Overview

xconfig (`github.com/sxwebdev/xconfig`) is a lightweight, zero-dependency, plugin-based
configuration management library for Go. It loads configuration into typed Go structs from
multiple sources (defaults, env vars, flags, config files, secrets) with a deterministic
priority order.

The core design principle is **composability**: each config source is a plugin that implements
either `Walker` (receives the whole struct) or `Visitor` (receives flattened fields). The
`Load()` convenience function wires up the standard plugins; `Custom()` gives full control.

## Architecture

### Package layout

```
xconfig.go          — Config interface, Custom(), config struct, Parse()
load.go             — Load() convenience function, wires standard plugins
options.go          — Option type and With* functional options
usage.go            — Usage() text output
markdown.go         — GenerateMarkdown() table output
doc.go              — Package-level godoc

flat/
  flat.go           — View() flattens nested structs into Fields slice
  field.go          — Field interface + field impl (Set, IsZero, type coercion)

plugins/
  plugins.go        — Plugin, Walker, Visitor interfaces; RegisterTag()
  defaults/         — Reads `default` struct tag, sets zero-valued fields
  customdefaults/   — Calls SetDefaults() if implemented
  env/              — Reads `env` struct tag, loads from os.Getenv
  flag/             — Reads `flag` struct tag, registers stdlib flag.FlagSet
  loader/           — File-based loading (JSON, YAML, dotenv, etc.)
  secret/           — Reads `secret` struct tag, calls Sourcer function
  validate/         — Calls Validate() method or custom validator function

decoders/
  xconfigyaml/      — YAML decoder (go-yaml)
  xconfigdotenv/    — .env file decoder (godotenv)
  xconfigjson/      — JSON decoder (encoding/json)

sourcers/
  xconfigvault/     — HashiCorp Vault secret sourcer with caching & TLS
```

### Loading order

`Load()` registers plugins in this order (each can be skipped via `WithSkip*` options):

1. **defaults (meta-only)** — registers `default` tag metadata for usage/docs
2. **customdefaults** — calls `SetDefaults()` if the struct implements it
3. **loader plugins** — unmarshal config files into the struct
4. **defaults (with rescan)** — applies `default` tag values to zero fields (including map entries created by loader)
5. **env** — overrides from environment variables
6. **flag** — overrides from CLI flags
7. **user plugins** — any plugins passed via `WithPlugins()`

Later sources override earlier ones. This means: flags > env > defaults > files > SetDefaults().

### Struct tags

| Tag        | Plugin   | Purpose                               | Example                 |
| ---------- | -------- | ------------------------------------- | ----------------------- |
| `default`  | defaults | Default value                         | `default:"8080"`        |
| `env`      | env      | Environment variable name             | `env:"PORT"`            |
| `flag`     | flag     | CLI flag name                         | `flag:"port"`           |
| `secret`   | secret   | Secret identifier                     | `secret:"DB_PASSWORD"`  |
| `usage`    | usage    | Help/doc description                  | `usage:"Server port"`   |
| `xconfig`  | flat     | Override field name in flat structure | `xconfig:"custom_name"` |
| `validate` | validate | Validation rules (go-playground)      | `validate:"required"`   |
| `required` | markdown | Mark field as required in docs        | `required:"true"`       |
| `example`  | markdown | Example value for docs                | `example:"https://..."` |

### Flat fields

`flat.View(structPtr)` walks a struct recursively and returns `flat.Fields` — a slice of
`flat.Field` interfaces. Each field has:

- `Name()` — dot-separated path (e.g., `Database.Host`)
- `EnvName()` — auto-generated env var name (e.g., `DATABASE_HOST`)
- `Tag(key)` — reads struct tag
- `Meta()` — mutable metadata map used by plugins to communicate
- `Set(string)` — type-coercing setter (string, bool, int*, uint*, float\*, Duration, slices, TextUnmarshaler)
- `IsZero()` — checks if field has zero value

Nested structs are flattened with dot prefixes. Anonymous structs are transparent (no prefix).
Maps with struct values are supported: each map key becomes a prefix segment.

## Instructions

### Adding a new plugin

1. Create a package under `plugins/` (e.g., `plugins/myplugin/`).
2. Implement either `plugins.Visitor` (flat field access) or `plugins.Walker` (raw struct access).
3. If the plugin uses a struct tag, call `plugins.RegisterTag("mytag")` in `init()` to prevent tag collisions.
4. `Visit(fields)` / `Walk(conf)` is the setup phase — store references but do not mutate config yet.
5. `Parse()` is the execution phase — read sources and call `field.Set(value)` to apply values.
6. Register via `xconfig.WithPlugins(myplugin.New())` or add to `load.go` if it should be standard.

### Adding a new decoder

1. Create a package under `decoders/` (e.g., `decoders/xconfigtoml/`).
2. Export an `Unmarshal([]byte, any) error` function or a `Decoder` with `Unmarshal` method.
3. Register with `loader.NewLoader(map[string]loader.Unmarshal{"toml": myDecoder.Unmarshal})`.
4. The decoder is pure unmarshaling — it does not interact with the plugin system directly.

### Adding a new secret sourcer

1. Create a package under `sourcers/` (e.g., `sourcers/xconfigaws/`).
2. Implement `func(string) (string, error)` — the `secret.Sourcer` signature.
3. Wire via `xconfig.WithPlugins(secret.New(mySourcer))`.
4. See `sourcers/xconfigvault/` for a full implementation with caching, TLS, and auth methods.

### Working with the loader

```go
l, err := loader.NewLoader(map[string]loader.Unmarshal{
    "json": json.Unmarshal,
    "yaml": xconfigyaml.New().Unmarshal,
    "env":  xconfigdotenv.New().Unmarshal,
})
l.AddFile("config.yaml", true)  // optional=true means file may not exist
l.AddFile("config.json", false) // optional=false means file must exist

_, err = xconfig.Load(cfg, xconfig.WithLoader(l))
```

### Unknown fields validation

Enable `WithDisallowUnknownFields()` to fail on unknown fields in config files.
Use `xconfig.GetUnknownFields(c)` to retrieve unknown fields without failing.
The loader tracks `PresentFields()` — which leaf fields were explicitly set in files.

### Vault integration

```go
import "github.com/sxwebdev/xconfig/sourcers/xconfigvault"

client, err := xconfigvault.New(&xconfigvault.Config{
    Address:      "https://vault.example.com:8200",
    Auth:         xconfigvault.WithToken("s.xxx"),
    DefaultMount: "secret",
    KVVersion:    2,
})

_, err = xconfig.Load(cfg, xconfig.WithPlugins(secret.New(client.Sourcer())))
```

Vault client supports: token auth, AppRole, Kubernetes, UserPass, LDAP. See `references/vault.md`.

### Writing tests

- Use `xconfig.WithSkipFlags()` in tests to avoid flag registration conflicts.
- Use `xconfig.WithSkipEnv()` when testing file/default loading in isolation.
- For file-based tests, create temp files and use `loader.AddFile()`.
- Integration tests live in `tests/integration/`.

## Examples

**Example 1: Basic config loading**

Input: User asks "add a Redis config section with host, port, and password"

Output:

```go
type Config struct {
    // ... existing fields ...
    Redis struct {
        Host     string `default:"localhost" env:"REDIS_HOST" flag:"redis-host" usage:"Redis host"`
        Port     int    `default:"6379" env:"REDIS_PORT" flag:"redis-port" usage:"Redis port"`
        Password string `secret:"REDIS_PASSWORD" usage:"Redis password"`
    }
}
```

**Example 2: Custom plugin**

Input: User asks "create a plugin that loads config from a remote HTTP endpoint"

Output:

```go
package httpconfig

import (
    "encoding/json"
    "io"
    "net/http"

    "github.com/sxwebdev/xconfig/plugins"
)

type httpPlugin struct {
    url  string
    conf any
}

func New(url string) plugins.Plugin {
    return &httpPlugin{url: url}
}

func (p *httpPlugin) Walk(conf any) error {
    p.conf = conf
    return nil
}

func (p *httpPlugin) Parse() error {
    resp, err := http.Get(p.url)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    data, err := io.ReadAll(resp.Body)
    if err != nil {
        return err
    }

    return json.Unmarshal(data, p.conf)
}
```

## Key principles

- **Plugin order matters.** Later plugins override earlier ones. Understand the loading chain before modifying `load.go` to avoid subtle precedence bugs.
- **Visitor vs Walker.** Use Visitor when you need flat field access with metadata (env, flags, secrets). Use Walker when you need the raw struct (file loading, custom defaults). A plugin can implement both.
- **Zero-value semantics.** The defaults plugin only sets fields that are zero-valued. If a file loader sets a field, defaults won't overwrite it. This is intentional — don't change this behavior.
- **Tag registration prevents collisions.** Always call `plugins.RegisterTag()` in `init()` for new tags. This panics at startup if two plugins claim the same tag, catching errors early.
- **Map sync callback.** Map struct values are copied for addressability. After `field.Set()`, the `mapSync` callback writes the copy back to the map. Never remove this mechanism — it's required for map-based config to work.
- **Decoders are pure.** Decoders only unmarshal bytes into structs. They don't participate in the plugin lifecycle. Keep them stateless and simple.
