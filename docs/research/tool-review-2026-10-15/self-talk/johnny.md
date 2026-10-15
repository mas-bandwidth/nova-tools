# nova-self-talk AI-usability review, 2026-10-15

Reviewer: mercury-3-preview-1002
Build: cc2f2f8c8148
Score: 8.5/10 (47/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-self-talk example ./pages` -> `EXAMPLE OK dir=./pages wrote=RULES.md,journal.md kept=-; run: nova-self-talk pages/journal.md`; usable from help alone, but the 94-line banner and per-verb help that drop required args add friction |
| 2 | refusals | 3 | `nova-self-talk --bogus t.md` -> `nova-self-talk REFUSED: unknown flag -bogus; the flags are --skip, --rule-doc, --max, --json; run: nova-self-talk help`; but `nova-self-talk help frobnicate` prints the whole banner at exit 0 and names nothing |
| 3 | output | 5 | `nova-self-talk --json ./t.md` -> `{"result":{"verb":"scan","status":"failed","exit":1},"facts":{"files":1,"claims":1,"standing":1,...}}`; one typed value, line and JSON agree, `SELFTALK OK or FAIL` lines are stable |
| 4 | dry-run | 4 | `nova-self-talk example --dry-run ./ep` -> `EXAMPLE OK dir=./ep would-write=RULES.md,journal.md kept=-` and wrote nothing; but `nova-self-talk example --dry-run /proc/nope` reports `EXAMPLE OK` exit 0 where the real run cannot write |
| 5 | idempotent | 3 | `nova-self-talk example ./pages` twice -> second run `EXAMPLE OK dir=./pages wrote=- kept=RULES.md,journal.md`, so nothing changes, but no write verb takes `--op` |
| 6 | no prompts | 5 | `n/a: every verb ran to completion with no interactive wait` |
| 7 | exit codes | 5 | `nova-self-talk ./t.md` -> `exit=1`, clean file -> `exit=0`, no files -> `exit=2`; 0/1/2 match the help table |
| 8 | one word | 5 | verbs scan, shapes, example, version, help are distinct with one word each; the bare form is documented as the same scan |
| 9 | push | 5 | `n/a: read-only local scanner; no state is waited on` |
| 10 | no leaks | 5 | `nova-self-talk --json ./t.md` -> output carries only file text, shapes and counts; no secret or environment value appeared |
| 11 | true help | 3 | `nova-self-talk version -h` -> ends `1 findings ... An all-skipped run exits 0 with SELFTALK SKIP files=0`, false for version; `nova-self-talk help frobnicate` prints the banner and exits 0, contradicting `help [<verb>] this text, or one verb's help` |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-self-talk help frobnicate` | prints the whole banner at exit 0; an unknown verb is not refused and no next command is named | refuse with `frobnicate is not a verb; the verbs are scan, shapes, example, version, help` and exit 2 | S |
| 2 | `nova-self-talk version -h`, `shapes -h`, `example -h` | each ends with the scan's exit table `1 findings ... An all-skipped run exits 0 with SELFTALK SKIP files=0`, which is not true of those verbs | give each verb its own exit line: 0 done; 2 could not run | S |
| 3 | `nova-self-talk example --dry-run /proc/nope` | `EXAMPLE OK dir=/proc/nope would-write=RULES.md,journal.md`, exit 0, but the real run cannot make the directory | have the dry run check the directory exists, is writable, or can be made, and refuse as the real run would | S |
| 4 | `nova-self-talk scan -h`, `example -h` | `usage: nova-self-talk scan [flags]` (and `example [flags]`) drop the required `<file>...` and `<dir>` | print each verb's real usage line, as the banner's usage block has it | S |
| 5 | `nova-self-talk version extra` | `REFUSED: takes no flags and no arguments, got 1; run: nova-self-talk version -h` names a count, not the word, and the remedy points at `version -h` instead of `help version` | quote what it got and point at `nova-self-talk help version` | S |
| 6 | `nova-self-talk --max abc ./t.md` | `REFUSED: invalid value "abc" for flag -max: parse error`, in the flag library's voice, not the tool's own | say `--max must be a whole number of zero or more (got "abc")`, as the `-1` check does | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-self-talk help` | the banner is 94 lines with a print-line inventory and a flags essay between usage and examples | keep the three questions, usage and examples; move the inventory to `scan -h` and docs/CLI.md | M |
| 2 | `nova-self-talk scan -h` | `--rule-doc <value>` and `--skip <value>`; the banner says `<basename>` | name the value `basename` in the flag usage text | S |

## Top 3 fixes
1. Make `help <unknown verb>` refuse with the verb list and exit 2 (moves refusals and true help).
2. Give each verb's `-h` its own accurate usage line and exit table.
3. Make `example --dry-run` validate the target directory so it never reports OK when the real run would fail.

## Good, keep
- `shapes` prints the live detector table, each row with a sentence it finds and a near miss it passes, and a test runs both through the scan.
- All-or-nothing reads: a missing or unreadable file refuses the whole run with `NOTHING was scanned, which is not a green`; a binary or NUL file is refused as not valid UTF-8, so the 2026-10-02 ratings' binary-green defect is gone.
- The every-run NOTE (a green clears the known shapes, never the file) and the DATED count on one line keep the tool honest without spending the reader's context.

## What could not be tried
- Nothing: every verb (scan, shapes, example, version, help) was run cold, and the tool has no service or network dependency to probe.
