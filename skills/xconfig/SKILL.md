---
name: xconfig
description: >-
  Go configuration management library (github.com/sxwebdev/xconfig) with plugin-based
  architecture, struct tags, and multi-source loading. Use this skill whenever working in
  the xconfig codebase — editing plugins, decoders, flat field processing, loaders, or
  tests. Also triggers when code imports "xconfig", "sxwebdev/xconfig", or references
  xconfig.Load, xconfig.Custom, flat.View, flat.Fields, plugins.Plugin, plugins.Walker,
  plugins.Visitor, plugins.Refreshable, plugins.FieldChange, loader.NewLoader, secret.New,
  validate.New, defaults, customdefaults, env, flag plugins, xconfigyaml, xconfigdotenv,
  xconfigvault, VaultPlugin, MetricsCallback, StartRefresh, StopRefresh, or GenerateMarkdown.
  Applies when the user mentions Go config management, struct tag configuration, environment
  variable loading, config file parsing, secret providers, vault integration, token renewal,
  background config refresh, config validation, or documentation generation in the context
  of this library.
user-invocable: true
license: MIT
metadata:
  author: "sxwebdev"
  version: "1.1.0"
  repo: "github.com/sxwebdev/xconfig"
allowed-tools: Read Edit Write Glob Grep Agent AskUserQuestion
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
  plugins.go        — Plugin, Walker, Visitor, Refreshable interfaces; RegisterTag(); FieldChange
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
  xconfigvault/     — HashiCorp Vault plugin with batch loading, token renewal,
                      auto-retry, metrics callback, VaultPlugin (Visitor+Refreshable)
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

Later sources override earlier ones. This means: vault > flags > env > defaults > files > SetDefaults().

### Background refresh

Plugins implementing `plugins.Refreshable` support background config updates. Call
`Config.StartRefresh(ctx, interval, onChange)` after `Load()` to periodically re-fetch
values from external sources (Vault, Consul, etcd, etc.). The `onChange` callback receives
`[]plugins.FieldChange` with full field paths (e.g., `Database.Postgres.Password`).

### Struct tags

| Tag        | Plugin       | Purpose                               | Example                 |
| ---------- | ------------ | ------------------------------------- | ----------------------- |
| `default`  | defaults     | Default value                         | `default:"8080"`        |
| `env`      | env          | Environment variable name             | `env:"PORT"`            |
| `flag`     | flag         | CLI flag name                         | `flag:"port"`           |
| `secret`   | secret       | Marks field as secret (metadata)      | `secret:"true"`         |
| `vault`    | xconfigvault | Field sourced from Vault              | `vault:"true"`          |
| `usage`    | usage        | Help/doc description                  | `usage:"Server port"`   |
| `xconfig`  | flat         | Override field name in flat structure | `xconfig:"custom_name"` |
| `validate` | validate     | Validation rules (go-playground)      | `validate:"required"`   |
| `required` | markdown     | Mark field as required in docs        | `required:"true"`       |
| `example`  | markdown     | Example value for docs                | `example:"https://..."` |

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
Maps are walked by key — `map[string]<struct>`, `map[string]*<struct>`, and primitive
`map[string]<scalar>` are all supported. Slice elements are walked by numeric index.
Modifications to map entries are synced back through a `mapSync` callback so `field.Set`
persists across map-copy semantics.

### Env-driven slice/map population

The env plugin (`plugins/env`) is both a `Walker` and a `Visitor`. In `Parse()` it
reflectively walks the conf, scans `os.Environ()` for keys matching slice/map prefixes,
and grows slices / creates map entries before re-flattening and applying values. This
means env vars can populate even nil/empty containers:

- `[]Struct` / `[]*Struct` — env keys `<PREFIX>_<N>_<FIELD>` grow the slice to `N+1`.
- `map[string]<scalar>` — env keys `<PREFIX>_<KEY>` create entries with key as-is from env (case preserved).
- `map[string]<struct>` / `map[string]*<struct>` — keys discovered by longest-suffix match against the inner struct's leaf field env names.

An `env:"..."` tag on a field nested inside a slice element or map value acts as a
per-segment override (the surrounding index/key is preserved). At the top level the tag
anchors the full env name, prepended only with the global plugin prefix.

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

### Adding a new sourcer plugin

1. Create a package under `sourcers/` (e.g., `sourcers/xconfigconsul/`).
2. Register a unique struct tag via `plugins.RegisterTag("consul")` in `init()`.
3. Implement `plugins.Visitor` (Visit collects tagged fields, Parse batch-loads values).
4. Optionally implement `plugins.Refreshable` for background config updates.
5. Wire via `xconfig.WithPlugins(mySourcer.Plugin())`.
6. See `sourcers/xconfigvault/` for a full implementation with batch loading, token renewal,
   auto-retry, metrics callback, and Refreshable support.

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

type Config struct {
    Host       string `env:"HOST" default:"localhost"`
    DBPassword string `vault:"true" env:"DB_PASSWORD" secret:"true"`
    APIKey     string `vault:"true" env:"API_KEY" secret:"true"`
}

client, err := xconfigvault.New(&xconfigvault.Config{
    Address:    "https://vault.example.com:8200",
    Auth:       xconfigvault.WithKubernetes("my-service-role"),
    SecretPath: "kv/myservice/config",
    Metrics:    xconfigvault.MetricsFunc(func(e xconfigvault.Event) {
        promCounter.WithLabelValues(string(e.Type)).Inc()
    }),
})
defer client.Close()

var cfg Config
xc, err := xconfig.Load(&cfg, xconfig.WithPlugins(client.Plugin()))

// Background refresh — detects secret rotation in Vault
xc.StartRefresh(ctx, 1*time.Minute, func(changes []plugins.FieldChange) {
    for _, c := range changes {
        slog.Info("config changed", "field", c.FieldName)
    }
})
defer xc.StopRefresh()
```

Vault client features: token/AppRole/Kubernetes/UserPass/LDAP auth, token renewal loop,
auto-retry on 401/403, metrics callback, batch loading, background refresh.
See `references/vault.md`.

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
        Password string `vault:"true" env:"REDIS_PASSWORD" secret:"true" usage:"Redis password"`
    }
}
```

**Example 2: Slices and maps from env vars**

Input: User asks "I want to load `Items []ServerSpec` and `Tags map[string]string` from environment variables"

Output:

```go
type ServerSpec struct {
    Host string
    Port int
}

type Config struct {
    Items []ServerSpec
    Tags  map[string]string
}

// ITEMS_0_HOST=10.0.0.1, ITEMS_0_PORT=80, ITEMS_1_HOST=10.0.0.2
// TAGS_REGION=eu-west, TAGS_TIER=prod
//   → Items: [{Host:"10.0.0.1", Port:80}, {Host:"10.0.0.2"}]
//     Tags:  {REGION:"eu-west", TIER:"prod"}
```

For `map[string]ServerSpec` use `<PREFIX>_<KEY>_<FIELD>` (e.g.
`SERVERS_PRIMARY_HOST=...`). For pointer element types (`[]*T`, `map[string]*T`)
empty slots are allocated as `&T{}`. Pre-populated containers are kept; matching
env vars override individual entries. An `env:"..."` tag on a nested field
overrides only that field's segment — the surrounding index/key is preserved.

**Example 3: Custom plugin**

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

- **Plugin order matters.** Later plugins override earlier ones. Vault plugin runs last and has maximum priority. Understand the loading chain before modifying `load.go` to avoid subtle precedence bugs.
- **Visitor vs Walker.** Use Visitor when you need flat field access with metadata (env, flags, secrets). Use Walker when you need the raw struct (file loading, custom defaults). A plugin can implement both.
- **Refreshable is generic.** Any plugin can implement `plugins.Refreshable` for background config updates. The `Config.StartRefresh()` loop iterates all Refreshable plugins. This is not vault-specific — future sourcers (consul, etcd, SSM) should implement the same interface.
- **Vault tag is separate from secret tag.** `vault:"true"` marks a field to be sourced from Vault. `secret:"true"` marks a field as sensitive (for masking/docs). They are independent — a field can have both, one, or neither.
- **Vault key derivation.** VaultPlugin uses `f.Meta()["env"]` (set by the env plugin) as the vault key if available, otherwise `f.EnvName()`. This means vault keys should match env var names.
- **Zero-value semantics.** The defaults plugin only sets fields that are zero-valued. If a file loader sets a field, defaults won't overwrite it. This is intentional — don't change this behavior.
- **Tag registration prevents collisions.** Always call `plugins.RegisterTag()` in `init()` for new tags. This panics at startup if two plugins claim the same tag, catching errors early.
- **Map sync callback.** Map struct values are copied for addressability. After `field.Set()`, the `mapSync` callback writes the copy back to the map. Never remove this mechanism — it's required for map-based config to work.
- **Decoders are pure.** Decoders only unmarshal bytes into structs. They don't participate in the plugin lifecycle. Keep them stateless and simple.
