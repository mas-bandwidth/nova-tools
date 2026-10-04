# nova-update USE rating, nova-tools 1.1.0

Rater: Grok
Build: 264f9c0135c7
Score: 7/10

## Reasons

The first run follows the example block. `nova-update example --out versions.tsv` exits 0, writes a one-row manifest, and prints the next command. `nova-update report --file versions.tsv` exits 0 and prints the installed identity. It prints no latest and no comparison. `nova-update status --file versions.tsv` exits 0 and prints EQUAL with installed and latest the same. `nova-update check --file versions.tsv` exits 0 and prints only the count line when every row is current.

The job the tool exists for, done twice, stays inside one scratch directory. The first is that local manifest: report, then status, then `nova-update apply --file versions.tsv go --dry-run`, which prints the plan `go install golang.org/dl/go1.27.1@latest` and says nothing was written. The second is a different manifest whose installed, latest and apply commands are scripts in that directory. `nova-update check --file toy.tsv` exits 1 with STALE, installed 1.0.0 against latest 1.2.0. The dry-run prints `./toy/apply.sh 1.2.0` and leaves the version file at 1.0.0. The real apply exits 0 with BEFORE, RUN and AFTER, the file becomes 1.2.0, and the next check exits 0.

Four refusals each name the problem and the next command, and one of them names every gap at once. `nova-update check` says missing --file. `nova-update report --file versions.tsv --nope` names the unknown flag and lists the flags. `nova-update frobnicate` names the unknown verb and lists the verbs. `nova-update check --file versions.tsv --max -1` says --max wants 0 or more. `nova-update report --send` lists --file, --as, --to, --bus, --remote and --branch in one line. A bad header and a short line are refused together. example refuses to overwrite a different file.

`--json` on report, example, version and apply --dry-run matches the lines: same verb, counts and plan. check rejects `--dry-run` and lists its own flags. adoption on a local ledger exits 0 and prints both an adopted row and a declined row. A local watch runs the checks and exits 1.

A 10 needs the help's own sample to survive contact. A pin whose latest is the help's `local:go version` exits 0 with installed=version and latest=version. The top of `nova-update help` says check and report compare the two sides, and the report does not. `nova-update release cut -h` says the version is such as 1.2.0, and that spelling is refused as not v-prefixed. Watch puts the refusal and ADOPT DONE on stderr and tells the caller a duty files an issue, while `nova-update watch -h` says this tool files nothing.

Not tried, because each needs a real service: `report --store`, `report --send`, watch with a bus, and release adopt, pull, cycle and install. One `release cut --dry-run` with a v-prefixed version still asks the forge and then refuses; it is not repeated. Network latest schemes are not asked. `release build` is judged from its help: it has no --dry-run, and a try is refused as an unknown flag.

The guess was which verb compares. The banner says report. The report does not. status does. The second guess was to leave the example apply command unrun: the pasted example includes --dry-run, and the file example writes names a network installer, while the manifest illustration in help says the apply column is none.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-update status --file pin.tsv` | A pin whose installed and latest are the help's own go version and local:go version exits 0 as EQUAL, with installed=version and latest=version. A green line names the second word of the version sentence, not the release. | Read the same version token a tool row reads, and stop offering that argv as a pin sample. | M |
| 2 | `nova-update watch --adopt checks.tsv` | ADOPT OK is the only stdout line. The refusal, the escalate line and ADOPT DONE are on stderr, and the escalate line says a duty files an issue. watch -h says this tool files nothing, and no issue id is printed. | Print the whole pass on one stream, and say who would file an issue without claiming it happened. | S |
| 3 | `nova-update release cut -h` | The flag text says the version is such as 1.2.0. That spelling is refused as not v-prefixed. A dry-run with a v-prefixed version still asks the forge before it refuses. | Show a v-prefixed sample, and make --dry-run decide from local inputs only. | M |
| 4 | `nova-update report --file versions.tsv` | The banner says check and report compare the two sides. This command prints the installed identity and no latest. status is the command that prints EQUAL. The manifest illustration says apply is none, and the file example writes names a network installer. | Say that report reads installed only, and make the illustration and the example file the same command. | S |

## Good, keep

`nova-update example --out versions.tsv` names the next command, and a second run leaves that file unchanged while a different file is refused.
`nova-update report --send` names every missing flag in one refusal, and an unknown flag lists the flags that exist.
`nova-update apply --file toy.tsv toy` prints the plan the dry-run printed, then BEFORE and AFTER, and the next check exits 0 with nothing stale.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| pin through the help's local:go version reports latest=version | STILL THERE | `nova-update status --file pin.tsv` prints installed=version latest=version and exits 0 |
| release verbs refuse the help's example version | STILL THERE | `nova-update release cut -h` says such as 1.2.0, and a dry-run with --version 1.2.0 refuses it as not v-prefixed |
| watch summary moves to stderr | STILL THERE | `nova-update watch --adopt checks.tsv` prints ADOPT OK on stdout and ADOPT DONE on stderr |
| local fake upgrade completes and rechecks | STILL THERE | `nova-update apply --file toy.tsv toy` prints APPLY OK from 1.0.0 to 1.2.0 and the next check exits 0 |
| cold use scores 6, 6.5 and 7 | STILL THERE | this pass is 7/10: the local upgrade completes, and the pin, the watch stream and the release sample version are still wrong |
