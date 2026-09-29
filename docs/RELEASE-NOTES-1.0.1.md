# Nova Tools 1.0.1

Nova Tools 1.0.1 ships sixteen command-line tools. Each tool
has its own entry point; choose the tools that fit your work. The
[1.0.0 release notes](RELEASE-NOTES-1.0.0.md) describe the earlier release,
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

The secrets first-run guide explains its fixture and uses replaceable paths.

`nova-table watch --check` checks table invariants every tick and displays failed checks as stall rows.

`nova-config apply` batches reads and reports failed pipeline commands even when an earlier key is absent.

`nova-fuse init` creates a missing box without replacing an existing box; status and lift refuse a missing box.

`nova-check spelling` checks Markdown prose and applies corrections only with `--write`.

`nova-tokens ledger --month` batches validated day files into one Redis call.

`nova-tokens` writes day and fold-pool files atomically and refuses symlinked or non-regular fold lock files.

`nova-swarm batch --cards` runs card batches with explicit deadline and token-budget inputs. Run `nova-swarm help batch` to see its required inputs. The Swarm specification describes a card as a pipeline of stateless model calls, with the required context passed to each call and no memory between calls.

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

`nova-ci cost` prints a COST receipt from a complete job listing and can record it in Redis when requested. Automatic CI cost reporting is removed from the workflow.

The CI class check validates hosted package assignments at the shard
counts the workflow actually runs. Existing time budgets stay in force.

The CI generality check refuses additions to its exception list and enforces the existing row ceiling.

The table epoch test harness checks the store image after every accepted action and verifies receipt replay. The ordered-table functional checks cover 4,500 and 5,000 rows, including moves, partial orders, sorting, standing sorts and additions. The CI documentation check covers additional verbose test-command spellings.

## Release publishing

Release uploads find existing drafts correctly and use the authored release
notes. Missing or empty notes refuse before an upload, and published releases
are refused rather than overwritten.

Release commands refuse final-component symlink destinations and preserve literal backslashes in Unix filenames.

## Change list

The [1.0.0 changelog](../CHANGELOG.md) lists its source commit and change
entries. These release notes describe the changes that affect how you use
the tools.

## Choosing a download

Choose the binary for your operating system and processor. The release
includes Linux and macOS builds for amd64 and arm64, and Windows builds
for amd64. `SHA256SUMS` lists the checksum of each binary so you can
verify the downloaded file before using it.

Each tool's `version` command identifies its build. Its help describes
the inputs and exit codes; the [command reference](CLI.md) collects the
usage examples.
