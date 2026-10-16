# nova-self-talk AI-usability review, 2026-10-15

Reviewer: Mercury-3-preview-1002
Build: d7e47f037ea8
Score: 9.5/10 (52/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 5 | `nova-self-talk example ./pages` -> `EXAMPLE OK dir=./pages wrote=RULES.md,journal.md kept=-; run: nova-self-talk pages/journal.md`; first run done from the banner alone |
| 2 | refusals | 4 | `nova-self-talk --bogus --max abc ./t1.md` -> `REFUSED: unknown flag -bogus; the flags are --skip, --rule-doc, --max, --json; run: nova-self-talk help` (names the cause and the next command, but only the first of the two problems) |
| 3 | output | 5 | `nova-self-talk --json ./pages/journal.md` -> `{"result":{"verb":"scan","status":"failed","exit":1},"facts":{"files":1,"claims":2,...}}`; stable `SELFTALK` line grammar, `--json` offered for scan, shapes and example |
| 4 | dry-run | 5 | `nova-self-talk example --dry-run ./p2` -> `EXAMPLE OK dir=./p2 would-write=RULES.md,journal.md kept=-`; `ls p2` -> `No such file or directory` |
| 5 | idempotent | 5 | `nova-self-talk example ./p3` twice -> md5 of the two pages identical after the second run (`IDENTICAL_AFTER_RERUN`) |
| 6 | no prompts | 5 | n/a: no interactive path exists; every verb returns without reading a control line |
| 7 | exit codes | 5 | `nova-self-talk ./pages/journal.md` -> exit 1; `nova-self-talk` (bare) -> exit 2; `nova-self-talk version` -> exit 0; match help's stated 0/1/2 |
| 8 | one word | 4 | default file-scan and the `scan` verb reach the same action, a duplicate verb (`nova-self-talk scan ./t1.md` prints the same lines as `nova-self-talk ./t1.md`) |
| 9 | push | 5 | n/a: keeps no state and waits on nothing |
| 10 | no leaks | 5 | scanned every output line of scan/shapes/example/version/help; no path, env or error line carries a secret |
| 11 | true help | 4 | `nova-self-talk version -h` -> `usage: nova-self-talk version [flags]` (takes none); `nova-self-talk help frobnicate` -> prints the 94-line banner at exit 0 |
## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-self-talk version -h` | usage line claims `[flags]` though version takes none; `scan -h` and `example -h` drop the required `<file>...` and `<dir>`; all three end with the scan's `SELFTALK SKIP files=0` all-skipped exit clause, false of them | print each verb's real usage line and its own exit table | S |
| 2 | `nova-self-talk help frobnicate` | prints the whole 94-line banner and exits 0; a caller cannot tell a fallback from an answer | print `frobnicate is not a verb; the verbs are scan, shapes, example, version, help` and exit 2 | S |
| 3 | `nova-self-talk --bogus --max abc ./t1.md` | refuses only the unknown flag, never the bad `--max`; and the bad-value voice is stdlib's `invalid value "abc" for flag -max: parse error` where `--max -1` gets the tool's own sentence | collect every flag problem into one refusal, in the tool's own voice | S |
| 4 | `nova-self-talk example --dry-run /proc/nope` | reports `EXAMPLE OK dir=/proc/nope would-write=RULES.md,journal.md kept=-` at exit 0 where the real run refuses, and offers a next command that would do nothing | make the dry-run check the target dir is creatable/writable as the real run does, and offer the scan when nothing would be written | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-self-talk version extra` | says `got 1` and points at `version -h` where every other refusal points at `help <verb>` | quote the offending word and point at `nova-self-talk help version` | S |
| 2 | default scan vs `scan` verb | two words for the same action (the `scan` verb duplicates the default file-scan) | pick one canonical spelling and keep the other as a deprecated alias | S |

## Top 3 fixes
1. Give each verb's `-h` its real usage line and its own exit table (moves criteria 11 and 1).
2. Make `help <unknown>` refuse at exit 2 instead of printing the whole banner.
3. Refuse once, naming every flag problem, in the tool's own voice.

## Good, keep
- `shapes` prints the live detector table the scan walks, each row with a sentence it finds (`finds=`) and a near miss it passes (`passes=`), run through the scan by the tests.
- All-or-nothing reads: every unreadable file is named before anything is scanned, `NOTHING was scanned, which is not a green`, invalid UTF-8 is refused, and an all-skipped run is `SKIP files=0`, never OK.
- The every-run NOTE limits a green to the known shapes; DATED claims are counted on one line, never quoted; `example` re-runs to identical bytes and `--json` mirrors the line output.

## What could not be tried
None: every verb (scan, shapes, example, version, help) was run, and no verb needs a service or a live store.
