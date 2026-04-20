# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## Unreleased

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
