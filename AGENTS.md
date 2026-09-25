# AGENTS.md — generated map

Do not edit. `make map` regenerates this file. Rules: [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md). Glenn, 2026-09-18: AGENTS.md alone — no `CLAUDE.md`, no pointer, no symlink.

Nova Tools is machinery: command-line tools that AI friends and people run against their own records, on their own machines, with their own identities. Adoption is a choice — one tool is a fine number. Rules: [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).

```
make build          # go build ./...
make test           # the fast tier, plus the per-package time budget
make map            # regenerate AGENTS.md and per-directory maps
```

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `.github/` | CI workflows and automation | `go test ./internal/ci` | `make test` |
| `assets/` | static assets and schemas | none | none |
| [cmd/](cmd/AGENTS.md) | 26 nova command-line tools | `go test ./cmd/...` | `make build` |
| [docs/](docs/AGENTS.md) | specs, guides, and proposals | `go test ./internal/docs` | `go test ./internal/docs` |
| `fleet/` | fleet loop units and bench templates | none | none |
| `infra/` | runner images and scripts | none | none |
| [internal/](internal/AGENTS.md) | packages and libraries | `go test ./internal/...` | `make test` |
| `lisp/` | nova-work lisp kernel | `go test ./internal/ci` | `make test-lisp` |
| `profiles/` | swarm worker profiles | `go test ./internal/swarm` | `nova-swarm lint` |
| `scripts/` | maintenance and operational scripts | none | none |
| `testdata/` | shared test fixtures and data | `go test ./internal/ci` | `make test` |
| `tests/` | bash test suites | `bats tests/` | `bats tests/` |
| [tools/](tools/AGENTS.md) | developer and bench tools | `go test ./tools/...` | `make map` |
