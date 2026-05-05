# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## Unreleased

## v0.4.1

### Added

- `flat.ExpandContainersFromKeys(conf, globalPrefix, keys)` — public helper
  that grows slice-of-struct and map fields in conf based on a set of
  UPPER_SNAKE_CASE keys (env var names or Vault secret keys), using the same
  conventions as the env plugin. Returns the flat-path → env-name mapping
  for callers to apply values after re-flattening.
- `flat.MakeEnvName(prefix, name)` — exported helper for building env-style
  names with an optional global prefix.
- Vault plugin (`sourcers/xconfigvault`) now populates slice-of-struct and
  map fields directly from Vault secret keys, mirroring the env plugin.
  Secret keys like `ITEMS_0_PASSWORD`, `SERVERS_PRIMARY_PASSWORD` grow the
  containers and create entries automatically. Pointer element types
  (`[]*T`, `map[string]*T`) and `env:"..."` per-segment overrides on inner
  fields work the same way they do for the env plugin.
- `xconfigvault.WithEnvPrefix(prefix)` plugin option for `client.Plugin(ctx, ...)`
  — pins the env-style prefix used when expanding slice/map containers from
  Vault secret keys (e.g. `WithEnvPrefix("MYAPP")` makes the plugin
  expand on `MYAPP_ITEMS_0_*`). When not set the plugin auto-detects
  the prefix from the env plugin's already-stamped metadata at Visit time;
  pass this option only when the conf has no top-level scalar fields visible
  at Visit time and auto-detection cannot recover the prefix.

### Changed

- Vault plugin (`sourcers/xconfigvault.VaultPlugin`) now also implements
  `plugins.Walker`. The conf reference is captured during `Walk`, secrets
  are fetched in `Parse`/`Refresh`, and containers are expanded based on the
  fetched secret keys before fields are applied. Existing leaf-tag
  (`vault:"true"`) usage is unchanged.
- Env plugin's `Visit` now uses the same context-aware naming logic as
  `Parse` — fields with an explicit `env:"..."` tag inside a slice element
  or map value get the surrounding index/key in their env name (e.g.
  `MYAPP_NODES_0_USE_TLS` instead of `MYAPP_USE_TLS`). This affects
  metadata stamped on `flat.Field.Meta()["env"]` that callers like
  `GenerateMarkdown` read to render env-var names.
- The env plugin's expansion logic moved to the `flat` package so the Vault
  plugin (and future sourcers) can reuse it. Env plugin behaviour is
  unchanged.

### Fixed

- `xconfig.GenerateMarkdown` no longer fails with `unable to find the key`
  when a Config has slice-of-struct fields. The lookup-by-path used the
  bracket-syntax helper (`Nodes[0].GRPCAddr`) but flat paths contain dotted
  indices (`Nodes.0.GRPCAddr`). Markdown now reads the value via
  `flat.Field.FieldValue()` directly.
- An `env:"..."` tag on a field inside a slice/map element is no longer
  collapsed to the absolute name in metadata stamped at `Visit` time —
  previously `Nodes[i].UseTLS env:"USE_TLS"` produced
  `MYAPP_USE_TLS` (same name for every element); now it produces
  `MYAPP_NODES_<i>_USE_TLS` so each element has a distinct env var.

## v0.4.0

### Added

- `env` plugin can now populate slices of struct and maps directly from
  environment variables, including nil/empty containers. The plugin walks the
  conf at parse time, scans `os.Environ()`, grows slices to fit the largest
  index found, and creates map entries by key. Pointer element types
  (`[]*T`, `map[string]*T`) are supported — empty slots are allocated as
  `&T{}`. Examples:
  - `ITEMS_0_KEY_1=a, ITEMS_1_KEY_2=b` → `Items []struct{Key1, Key2 string}`
  - `TAGS_FOO=v1, TAGS_BAR=v2` → `Tags map[string]string` (key as-is from env, case preserved)
  - `SERVERS_PRIMARY_HOST=..., SERVERS_PRIMARY_PORT=...` → `Servers map[string]Server`
    (map keys discovered by longest-suffix match against the inner struct's leaf field names)
- `flat.View` now traverses primitive-valued maps (`map[string]string`,
  `map[string]int`, `map[string]time.Duration`, etc.) and pointer-to-struct
  maps (`map[string]*T`). Each entry becomes a `Field` with a `mapSync`
  callback so `field.Set` writes back through Go's map-copy semantics.

### Changed

- An `env:"..."` tag on a field nested inside a slice element or map value
  now acts as a per-segment override — the surrounding slice index or map key
  is preserved so different elements no longer collide on the same env var.
  At the top level the tag continues to anchor the full env name (only the
  global `WithEnvPrefix` is prepended), so existing `Database.Host env:"DB_HOST"`
  patterns are unchanged.
- `env` plugin's `buildEnvName` now keeps middle path segments (slice index,
  map key) when the parent struct has an `env:` tag. Previously
  `Items []T env:"ITEMS"` with `T{Key string}` produced `ITEMS_KEY` for every
  element; it now produces `ITEMS_0_KEY`, `ITEMS_1_KEY`, etc.
- `internal/utils.SplitNameByWords` no longer merges across dot boundaries.
  Paths like `Map.primary` are now split as `["Map", "primary"]` (env name
  `MAP_PRIMARY`) instead of `["Ma", "pprimary"]` (`MA_PPRIMARY`). This fixes
  env-name generation for map entries with lowercase keys.

## v0.3.4

### Added

- `xconfig.ApplyDefaults(v any) error` — apply `default:` struct tags to a
  pointer to a struct, `*[]T`, or `*[]*T` programmatically. Useful when a
  value was populated outside `xconfig.Load` (e.g. by `yaml.Unmarshal` of an
  external file). Non-zero fields are preserved.

### Fixed

- `default:` struct tags on fields nested inside slices of structs (`[]T`,
  `[]*T`) are now applied during `xconfig.Load` / `xconfig.Custom`. Previously
  the flat-view walker only descended into structs and maps, so slice
  elements were silently skipped and their default tags had no effect.
- `loader.PresentFields()` now includes slice indices (`groups.0.is_enabled`).
  This lets the rescan defaults pass distinguish "element didn't set
  `is_enabled`" from "element explicitly set it to `false`" on a per-element
  basis.

### Changed

- `customdefaults` now walks the whole config graph and invokes
  `SetDefaults()` on every reachable struct whose pointer receiver implements
  the interface — including elements of slices/arrays and values of maps.
  Children are visited before their parent, so a parent's `SetDefaults`
  observes values populated by its children. Previously only the root
  object's `SetDefaults` was called.
