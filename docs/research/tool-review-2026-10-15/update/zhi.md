# nova-update AI-usability review, 2026-10-15

Reviewer: Claude Sonnet 5.5
Build: cc2f2f8c8148
Score: 8/10 (43/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-update example --out versions.tsv` -> `EXAMPLE NOTE next: nova-update report --file versions.tsv` (the first run works from help alone; the manifest rules are long and repeat in every verb's -h, and the example apply column is misleading, see blocker 5) |
| 2 | refusals | 4 | `nova-update check --file nosuch.tsv` -> `CHECK REFUSED: cannot open nosuch.tsv (supply a readable --file: ... nova-update example --out nosuch.tsv writes one to start from); run: nova-update check -h`; every bad-manifest problem is listed at once with its line; the UNKNOWN reason `shape` names no next command |
| 3 | output | 3 | `nova-update check --file m2.tsv --json` -> `{"result":{"verb":"check","status":"failed","exit":1},"facts":{...},"items":[...]}`; but `CHECK UNKNOWN name=gone ... source=-: not_found (install nosuchbinary ...; searched PATH=...)` breaks the key=value grammar, and `watch --adopt chk.tsv --json` is refused though watch is the one verb that takes lines only |
| 4 | dry-run | 4 | `nova-update apply --file versions.tsv go --dry-run` -> `APPLY NOTE dry run: nothing installed, nothing written`; `example --out` and `report --snapshot` write a file with no dry-run |
| 5 | idempotent | 3 | `nova-update apply --file versions.tsv go` on an entry already current -> `APPLY OK name=go from=1.26.6 to=1.26.6 took=2.323s` (the installer ran again; there is no --op); `nova-update example --out versions.tsv` twice -> `EXAMPLE OK wrote=versions.tsv entries=1 unchanged=true` and a repeated `report --snapshot` -> `changed=no` are idempotent |
| 6 | no prompts | 5 | `nova-update watch --adopt chk.tsv` -> `ADOPT DONE sha=8a4c2c1a11c0 ok=1 refused=1`; no verb waited on input |
| 7 | exit codes | 5 | `nova-update check --file m2.tsv` -> `CHECK FAILED checked=4 current=1 stale=2 ...` with exit 1; refusals exit 2; a current entry exits 0, all as the help states |
| 8 | one word | 3 | `nova-update status --file m2.tsv` -> `STATUS STALE name=mytool ...`; check, status and report read one manifest three ways, and a second tool, nova-version, shares it, so an AI must learn which of two binaries and three verbs answers a question |
| 9 | push | 5 | `n/a: no verb waits on state; every verb reads once and returns` |
| 10 | no leaks | 4 | `nova-update check --file m2.tsv` -> `CHECK UNKNOWN name=gone ... searched PATH=<dir>/bin:...` (no secret, but the whole PATH is echoed into a result line, three times over in report, check and status, and PATH can carry one); a failed send names NOVA_BUS_REDIS unset without a value |
| 11 | true help | 3 | `nova-update help` -> `release <cut, build, install, adopt or pull> ...` while `nova-update release bogus` -> `the release verbs are cut, build, install, adopt, pull, cycle`; help says `local:<argv> (a pin takes this only)` yet the example is a tool with `local:go version` and works |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-update apply --file versions.tsv go` (entry already current) | The installer runs again on an entry that is already on the target: `APPLY OK name=go from=1.26.6 to=1.26.6 took=2.323s` followed by `APPLY RUN name=go argv=3 version=1.26.6: go install ...`; a retry after a timeout re-installs and nothing says it need not | When installed already equals the target, print `APPLY OK name=<n> unchanged=true` and start no process (or take --op and refuse a repeated op), and pin it with a test that counts process starts | S |
| 2 | `nova-update check --file m2.tsv` (an entry whose binary is missing) | `CHECK UNKNOWN name=gone kind=tool installed=- path=- source=-: not_found (install nosuchbinary or supply its executable path; searched PATH=<dir>/bin:<dir>/go/bin:...)`: a colon glued to a key=value, a 300-character PATH inside the result line, and the same text in `report` and `status` | Emit `reason=not_found remedy="install nosuchbinary or give its absolute path"` as fields as the --json already does, and name only the first PATH directory count (`searched=N dirs`) | S |
| 3 | `nova-update check --file net.tsv` with `github:golang/go` | `CHECK UNKNOWN name=gh ... source=github:golang/go@https://api.github.com/repos/golang/go/tags?per_page\x3d1: shape (check the declared latest source or ask again when it answers)`: the plainest github source in the help is UNKNOWN with the word `shape`, and `\x3d` encodes the `=` inside the URL | Say what shape was wrong (`the newest tag go1.26.6 is not v1.2.3 form`) and the next command, or read the version from the tag's trailing digits; stop escaping `=` in the URL | M |
| 4 | `nova-update help` (usage and manifest section 4) | Usage lists `release <cut, build, install, adopt or pull>` but the verbs are also `cycle`; section 4 says `local:<argv> (a pin takes this only)` yet the example is kind tool with `local:go version` and passes; `<verb> -h` prints, for example, `apply that left the box on the target` under `version -h` | List cycle in the usage line; reword section 4 to `local:<argv>` for any kind; print on each verb's -h only the exit codes that verb can return | S |
| 5 | `nova-update example` | The one manifest line an AI copies has `go install golang.org/dl/go{version}@latest` as its apply: it installs a downloader stub named go1.26.6 into the Go bin directory and leaves the real go unchanged, so `apply ... go` reports success and updates nothing | Make the example's apply `none` (a read-only first run), or an apply that really updates the tool | S |
| 6 | `nova-update apply --file m2.tsv gone --dry-run` (installed missing, apply none) | `APPLY REFUSED: gone is installed by hand: its apply column is none (its owner team installs it ...)`: the entry is not installed at all, and the refusal says it is installed | Branch the message: when installed is unknown say `gone is not found on PATH and its apply column is none; run: nova-update status --file m2.tsv` | S |
| 7 | `nova-update report --store localhost:1` | Four raw lines `redis: 2026/10/15 17:28:16 pool.go:762: redis: connection pool: failed to dial after 5 attempts ...` precede the one `REPORT REFUSED:` line, a second grammar on stderr, and the wait is seconds | Silence the pool logger in this verb and let `REPORT REFUSED: fleet beats at localhost:1: connection refused` stand alone | S |
| 8 | `nova-update apply --file m2.tsv withver --dry-run --version` | `APPLY REFUSED: invalid target "--"`: the flag with no value swallowed `--dry-run` and the cause shown is a made-up target | Refuse `--version wants a value (1.2.3), got the flag --dry-run` | S |
| 9 | `nova-update apply --file m2.tsv withver --dry-run --version 1.1.0` | `APPLY STALE name=withver ... installed=1.0.0 latest=1.1.0` while the source's latest is 1.2.0 (the first run printed `latest=1.2.0`); the requested version overwrites the field named latest | Keep `latest=1.2.0` and print `target=1.1.0` on the plan line | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-update report --file m2.tsv --draft` | The report refuses `missing --as, --to` before showing anything, though a draft sends nothing | Let --draft print with a placeholder sender and recipients | S |
| 2 | `nova-update help` | The manifest rules (about 3 KB) repeat in check, status, apply and report -h, so each -h is long | Print the manifest block once under `help manifest` and point to it | M |
| 3 | `nova-update watch --adopt chk.tsv --json` | The only verb besides release with no --json, though its lines are a grammar already | Add --json for watch | M |
| 4 | `nova-update report --file m2.tsv --send --as me --to you` | The bus message is cut at `g...` in the note | Print the whole remedy line | S |
| 5 | check, status and report | Three verbs read one manifest, and nova-version a fourth surface | Name in `help` one question per verb in a table; any merge is a larger change | L |

## Top 3 fixes
1. Make apply on an already-current entry start no process (blocker 1), and the example's apply read-only (blocker 5): the only two ways a first-run AI changes the box by accident.
2. Give UNKNOWN lines fields and drop the PATH from the line (blocker 2), and make the github source answer for a plain repo (blocker 3).
3. Make help true: list cycle, fix the local: rule, per-verb exit codes (blocker 4).

## Good, keep
Every refusal is one line naming the cause and `run: nova-update <verb> -h`, and a bad manifest lists every problem with its line at once. `example --out` never overwrites and a second run says `unchanged=true`. `apply --dry-run` prints the exact argv with the version filled in and starts no process. The one JSON envelope (`result`, `facts`, `items`) is the same for every verb and exit codes 0, 1, 2 hold everywhere.

## What could not be tried
`report --send` delivery (needs the bus; the draft and the unset-bus refusal were run). `report --store` against a live fleet store (only the refusal on a closed port). `release cut`, `build`, `install`, `adopt`, `pull` and `cycle`: they publish, install or fan out to machines, so only their usage and their missing-flag refusal were run. `check` against brew:, ollama: and an npm package that exists (the github and the missing-npm answers were run). `apply` was run once for real by mistake on the example entry, which installed the downloader stub go1.26.6 into the scratch machine's Go bin directory; it was removed at once and nothing else changed.
