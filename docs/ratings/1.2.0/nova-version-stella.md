# nova-version READ and USE rating, nova-tools 1.2.0

Rater: assigned friend, gpt-5.6-terra in the Codex external Terra-child harness
Build: 7a61cf1d1ff9
READ: 7/10
USE: 6/10

This rates the prescribed v1.2.0 release candidate, not an installed older
release. An authorized serialized Linux bench built the detached `7a61cf1d1ff94294fcacdd5e1f5f144a93dfe733`
source in an isolated bench directory. `go version -m` recorded that VCS revision
and `v1.0.1-0.20261006144045-7a61cf1d1ff9` module version. The trial used only a
fresh local scratch directory: no store, server, or delivery was used.

## Reasons

READ. `nova-version help` explains the inventory, comparison, report, send and
moved jobs, and each verb's `-h` gives flags and an effect. The example created a
one-entry manifest, which `snapshot --file` and `report` then read successfully.
The missing-argument `snapshot` refusal named both missing flags, its alternative
manifest shape, and the next help command.

USE. The isolated trial ran `example --out`, `snapshot --file`, `report`,
`snapshot --bin`, and `diff` against a freshly built binary. The normal commands
were small and direct. But the core identity tool reports the just-built exact
candidate as `devel`, and its binary snapshot records `revision=-`; that weakens a
tool whose purpose is to preserve build identity. The report help also understates
local writes, and compound invalid input reports only one independent bad value.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-version version` on the Linux-built candidate | The binary prints the stamp `devel` although `go version -m` records VCS revision `7a61cf1d1ff94294fcacdd5e1f5f144a93dfe733` and the candidate module version. | Populate the version-line stamp and revision from the build VCS metadata, or refuse a production identity build that lacks them. | M |
| 2 | `nova-version snapshot --bin <scratch/bin> --out <scratch/snapshot.tsv>` | The snapshot of that freshly built candidate records `stamp=devel revision=-`, losing the identity the tool was asked to inventory. | Preserve the embedded VCS revision in the version line and snapshot row. | M |
| 3 | `nova-version report -h`; `report --file <scratch/versions.tsv> --snapshot <scratch/state.json>` | Help says that without `--send` report reads and writes nothing, but the real run wrote `state.json` and `state.json.lock`. The implementation writes whenever `o.snapshot` is set. | State that `--snapshot` is a local write even without `--send`. | S |
| 4 | `nova-version report --file <scratch/versions.tsv> --kind bogus --max -2` | The command exits 2 after naming only invalid `--kind`; the separately invalid `--max` is omitted despite the manifest help promising every problem at once. | Validate and report all independent flag-value failures before returning the refusal. | S |
| 5 | every `nova-version <verb> -h` in the Linux trial | Each verb repeats the full five-verb exit table, so the relevant exit meaning is buried in unrelated verbs. | Print the called verb's exit contract, or a compact shared table once from top-level help. | S |
| 6 | `internal/update/snapverb.go:299-300` | The implementation text says "the adopted sixteen" and "the thirty-two nova-* executables", fleet-sized counts that drift as the set changes. | Describe the manifest and binary set without fixed counts. | S |
| 7 | `nova-version snapshot` and `nova-version report --snapshot` | One word, `snapshot`, means both the four-column binary inventory and the report delivery-state JSON file. | Rename the report state flag to `--state` while retaining a documented compatibility alias. | M |

## Good, keep

The help survey was complete and every verb accepted `-h`. `example --out` made a
usable manifest, `snapshot --file` and `report` produced concise rows, and the
bare snapshot refusal was specific: it named `--bin`, `--out`, the `--file`
alternative, and `nova-version help`. `diff` gave a clean `changed=0` answer for
two identical scratch snapshots. The report-state write is atomic and protected
by the adjacent lock; the defect is the misleading effect text, not the mechanism.

## Compared with earlier ratings

The earlier attempt is not attributed to this card. This fresh rating confirms the
same release-candidate identity by a new serialized Linux build and narrows its
findings to defects observed in this Terra trial or directly in the prescribed
candidate source. The old attempt's missing-artifact
hold no longer applies because this task used the authorized staged Linux build.
