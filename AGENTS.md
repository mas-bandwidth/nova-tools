# AGENTS.md — generated map

Do not edit. `make map` regenerates this file. AGENTS.md alone: no `CLAUDE.md`, no pointer, no symlink.

Nova Tools is machinery: command-line tools that AI friends and people run against their own records, on their own machines, with their own identities. Adoption is a choice — one tool is a fine number. The standard is below; how review goes: [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).

```
make build          # go build ./...
make test           # the fast tier, plus the per-package time budget
make map            # regenerate AGENTS.md and per-directory maps
```

The standard is [docs/STANDARD.md](docs/STANDARD.md), linked not embedded; every PR meets it.

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `.github/` | CI workflows | `go test ./internal/ci` | `make test` |
| `assets/` | static assets and schemas | none | none |
| [cmd/](cmd/AGENTS.md) | 17 nova command-line tools | `nova-ci local` | `make build` |
| [docs/](docs/AGENTS.md) | specs, guides, and proposals | `go test ./internal/docs` | `go test ./internal/docs` |
| `fleet/` | fleet loop units and bench templates | none | none |
| `hook/` | the hook module (own go.mod) | `cd hook && go test .` | `cd hook && go test .` |
| `infra/` | runner images and scripts | none | none |
| [internal/](internal/AGENTS.md) | packages and libraries | `nova-ci local` | `make test` |
| [pkg/](pkg/AGENTS.md) | public packages | `nova-ci local` | `make test` |
| `profiles/` | pkg/sandbox's darwin profile and its check | `go test ./pkg/sandbox` | `go test ./pkg/sandbox` |
| `testdata/` | shared test fixtures | `go test ./internal/ci` | `make test` |
| `tla/` | the TLA+ models and their runners | `go test ./pkg/tlc` | `make tlc-test` |
| [tools/](tools/AGENTS.md) | developer and bench tools | `nova-ci local` | `make map` |
