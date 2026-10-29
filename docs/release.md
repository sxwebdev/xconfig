# Development and releases

The repository holds several Go modules, released together under one version:

| Directory                  | Module                                                |
| -------------------------- | ----------------------------------------------------- |
| `.`                        | `github.com/sxwebdev/xconfig`                          |
| `decoders/xconfigdotenv`   | `github.com/sxwebdev/xconfig/decoders/xconfigdotenv`   |
| `decoders/xconfigjson`     | `github.com/sxwebdev/xconfig/decoders/xconfigjson`     |
| `decoders/xconfigyaml`     | `github.com/sxwebdev/xconfig/decoders/xconfigyaml`     |
| `sourcers/xconfigvault`    | `github.com/sxwebdev/xconfig/sourcers/xconfigvault`    |
| `watchers/xconfigfsnotify` | `github.com/sxwebdev/xconfig/watchers/xconfigfsnotify` |

`tests/integration` is a test module and is never published.

## Development

Published `go.mod` files contain no `replace` directives, and modules that depend on the
core require the version being prepared. They build against the checkout through a
generated, git-ignored workspace:

```bash
make workspace   # writes go.work
make test        # go vet and go test -race in every module
make lint        # golangci-lint in every module
```

## Preparing a release

1. Rename `## Unreleased` in [CHANGELOG.md](../CHANGELOG.md) to `## vX.Y.Z — YYYY-MM-DD`
   and add a new empty `## Unreleased` above it.
2. Set `github.com/sxwebdev/xconfig vX.Y.Z` in every module that requires the core
   (`sourcers/xconfigvault`, `watchers/xconfigfsnotify`).
3. Run the preflight:

   ```bash
   make release-check     # needs goreleaser v2.19.0
   make release-snapshot  # optional; writes .artifacts/release/
   ```

   `go run tools/release.go` checks the latest changelog entry, its notes, module paths,
   matching core versions, the absence of `replace` directives, and a `LICENSE` in every
   module. Only stable `v0.x.y` and `v1.x.y` versions are supported.

## Publishing

Merge the release preparation into `master`, then open **Actions → release → Run
workflow**, select **master**, and enter the version. The workflow:

1. Validates the input against `CHANGELOG.md` and the module manifests.
2. Runs the complete CI workflow.
3. Checks that the previous root tag is older and an ancestor of the release commit, and
   validates the GoReleaser configuration with a snapshot.
4. Pushes `vX.Y.Z` and `<module>/vX.Y.Z` for every published module in one atomic push.
5. Publishes the GitHub Release with notes grouped by commit type.

Tags are never force-pushed; a tag pointing to another commit stops the workflow. A
failed run can be rerun on the same commit and reuses the tags it already pushed. Once
the tags are pushed the modules are installable, so if the GitHub Release step fails,
rerun the workflow instead of moving tags.
