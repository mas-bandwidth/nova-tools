# nova-update

nova-update installs nova-tools releases on one machine. It compares the `nova-*` binaries in a bin
directory with a GitHub release by checksum, and installs one tool or all of them. Every byte is
checked against the release's `SHA256SUMS` before the first binary is replaced.

## The verbs

```
nova-update [report] [--bin <dir>] [--version <v>] [--repo <owner/name>]
nova-update install [<tool> | all] [--bin <dir>] [--version <v>] [--repo <owner/name>] [--dry-run]
nova-update release install --from <dir> --version <v> --bin <dir> [--retire <dir>] [--platform <goos-goarch>]
nova-update version
```

**report** (also `status`, and plain `nova-update`) prints one line per tool and a summary:

```
TOOL name=nova-bus state=current installed=8685c80e2e72
REPORT version=v1.7.0 platform=darwin-arm64 bin=/home/me/.local/bin current=1 stale=0 missing=0 unreleased=0
```

`current` means the installed bytes are the release's, `stale` means they differ, `missing` means the
release ships the tool and the bin directory does not have it, and `unreleased` means the bin directory
has a `nova-*` binary the release does not ship. Exit 1 when any tool is stale.

**install** (also `apply`) downloads the named tool, or every tool the release ships for this platform,
into temporary files in the bin directory and checks each against `SHA256SUMS`. Only then is each target
moved aside and the new file renamed into place. A failed rename puts back every target already
replaced. A tool already holding the release's bytes is skipped. `--dry-run` prints `WOULD INSTALL` or
`WOULD SKIP` per tool.

**release install** installs a release built on disk (`<from>/<v>/<goos-goarch>/` with its own
`SHA256SUMS`) the same way. `--retire <dir>` removes the release's tools from a second directory.
Afterwards it keeps the four newest release directories under `--from` besides the one installed and
removes the older ones. It prints
`RELEASE INSTALLED version=<v> tools=<n> skipped=<n> retired=<n> bin=<dir> platform=<p> pruned=<n> prune-failed=<n>`.

The bin directory defaults to the one the running nova-update lives in. The repository defaults to
`mas-bandwidth/nova-tools`. A release's assets are named `<tool>_<version>_<goos>_<goarch>[.exe]`, plus
one `SHA256SUMS`. When `GITHUB_TOKEN` or `GH_TOKEN` is set, it is sent to the GitHub API and never
printed. Downloads send no token. Exit 2 is a refusal, and nothing was changed.

Cutting and building a release is not this tool's job.

## Build and test

```
go build -o nova-update .
go test .                           # unit tests: a release in memory, temporary directories
go test -tags functional .          # report against the real latest release (network)
```
