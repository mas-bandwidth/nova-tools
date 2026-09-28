# Nova Tools 1.0.1

Nova Tools 1.0.1 keeps the fifteen command-line tools from 1.0.0. Each tool
has its own entry point; choose the tools that fit your work. The
[1.0.0 release notes](RELEASE-NOTES-1.0.0.md) describe their capabilities,
and the [README](../README.md) gives a first command for each tool.

## Checkpoints and receipts

`nova-cairn index` and `receipt` read dated entries in flat session files as
well as nested entry stores. A missing store or named session refuses;
an existing empty store produces an empty index. Flat receipts report
`publish=unknown` because those records do not store a publication policy.
Listings report the full count when the display limit hides entries.

Retrying an identical append returns the original stored timestamp and
`duplicate=true`. A malformed stored timestamp refuses without writing.

## Findings and commands you can act on

`nova-memory verify --exclude` applies to the whole verified corpus,
including coverage and frontmatter selectors. An excluded directory also
excludes its children. A retained note that links to an excluded target still
reports an unresolved link, and a selector left with no files refuses.
Summary counts report all findings, even when the display limit hides some.

`nova-self-talk` reports findings at their original source line numbers.
If every input is skipped, it prints an explicit `SKIP` result and exits 0.
Flags placed after a file operand refuse before the tool reads files, so
an option cannot silently become a filename.

The commands suggested by `nova-fuse lift` and `nova-cairn` refusals preserve
paths and reasons containing shell punctuation. Copying a remedy keeps the
arguments intact.

## Checks for contributors

The CI class check validates hosted package assignments at the shard
counts the workflow actually runs. Existing time budgets stay in force.

## Release publishing

Release uploads find existing drafts correctly and use the authored release
notes. Missing or empty notes refuse before an upload, and published releases
are refused rather than overwritten.

## Release history

The repository includes a [changelog](../CHANGELOG.md) with the source
commit and recorded change entries for 1.0.0. Use it to trace the release
back to its changes; use these release notes for the changes that affect
how you use the tools.

## Choosing a download

Choose the binary for your operating system and processor. The release
includes Linux and macOS builds for amd64 and arm64, and Windows builds
for amd64. `SHA256SUMS` lists the checksum of each binary so you can
verify the downloaded file before using it.

Each tool's `version` command identifies its build. Its help describes
the inputs and exit codes; the [command reference](CLI.md) collects the
usage examples.
