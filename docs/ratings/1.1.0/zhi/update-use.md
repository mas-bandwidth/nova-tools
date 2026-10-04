# nova-update USE rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 9/10

## Reasons

The first successful run was `nova-update example --out ./versions.tsv`, which writes a one-tool manifest and prints the exact next command. The real job the tool exists for ran end to end: `report --file ./versions.tsv` exits 0 with `REPORT OK checked=1 known=1` and one row per tool; `status` shows the same entry as EQUAL; `check` exits 0 with the count line; and `apply go --dry-run` prints the plan and installs nothing. The `--json` report returns one object with the counts and the rows as items.

The four refusals all exit 2 and name the next command: a missing --file says `CHECK REFUSED: missing --file; refusing to guess; run: nova-update check -h`; an unknown flag lists the verb's flags; an unknown verb lists the nine verbs; `apply` with no name says it wants exactly one entry name. `nova-update version --bogus` refuses, so version no longer accepts unknown flags.

What costs the score: `apply go --dry-run` on an already-equal entry still prints an APPLY PLAN line with the install command, which reads as if work were needed; `report` prints the raw field as `raw=go\x20version\x20go1.27.1\x20darwin/arm64`, so a reader must decode the escapes; and the dry-run accepts a version the source may not publish and plans it anyway.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-update apply --file ./versions.tsv go --dry-run` | an already-equal entry still prints APPLY PLAN with the install command, reading as if an update were needed | skip the plan when installed equals latest and print APPLY EQUAL only | S |
| 2 | `nova-update report --file ./versions.tsv` | the raw field prints with \x20 escapes, so a reader must decode it | print the raw field as one escaped token only in --json, and plain text in the line | S |
| 3 | `nova-update apply --file ./versions.tsv go --version 1.2.3.4 --dry-run` | the dry-run plans an install for a version the source may not publish | check the requested version against the source before planning | S |

## Good, keep

The example manifest that installs nothing and prints the next command. The refusal grammar with the flag list. The JSON report with typed counts and rows.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a pin through the help's own local:go version reports latest=version | FIXED | `nova-update status --file ./versions.tsv` prints `installed=1.27.1 latest=1.27.1` |
