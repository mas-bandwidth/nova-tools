# nova-fuse READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker on a friend's re-rate card; the rating is the worker's, not the friend's
Build: 361d4451bcd8
READ: 7.5/10
USE: 8.5/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of sprint/mechanical-2026-10-02. `nova-fuse version` prints `nova-fuse v1.0.1-0.20261006144651-361d4451bcd8 linux/amd64 go1.26.6`. Built and run on a Linux bench machine in a scratch directory made for the trial; no live box, no server, no network. Read cold: the banner, `help` for every verb (lift quarantine and lift lockdown included), the nova-fuse section of docs/SPEC.md and the package doc. Used for real: the banner's six-line sitting in order, then every verb with and without `--dry-run`, lockdown over a clear box, a blown box, a missing box and an unreadable one, quarantine and lift over case, whitespace, control-character, dash-leading and hand-edited spellings, and `check` and `status` over boxes that are absent, empty, null, an array, carry an unknown key, hold `{}` fuses, are a symlink, a directory, or unreadable by mode.

## Reasons

READ. The banner's first eight lines answer what the tool is (a recorded decision, checked before every read), how it works (one box, one lockdown, one quarantine per surface, and the tool enforces nothing) and the first run. The exit contract is stated once, and per verb in `help <verb>` with an `effect:` line saying whether the verb writes; `-h` after a verb is refused at exit 2 and the banner says why (exit 0 is CLEAR, so nothing spelled `-h` may reach it). `help lift lockdown` says plainly that the tool supplies no reset. Those are the right answers for a gate.

What keeps READ at 7.5. The banner is still 73 lines (cmd/nova-fuse/main.go:39-111), with the exit essay, the `-h` rule, a box JSON sample and the `--max` paragraph between usage and example; and its exit paragraph gives init's exit 1 only as "re-reading did not show it" where `help init` and the binary also give 1 for a box already there. The banner says a missing or broken box "reads as blown", and the reader then finds it exits 2, not blown's 1. "Surface spellings match after normalization" is the only word on matching in the help; nowhere short of the package doc does it say what the normalization is (case folds, control characters and whitespace runs become one blank, `-` and `_` stay distinct), which a caller choosing surface names needs. "(soft, both directions)" in the usage block is unglossed. `help check` gives only the no-surface case and never says what check does with one. Every verb's help prints a `--` row saying "following words are literal surfaces or reasons", including init, status and path, which take none; the flag column is misaligned; `help version` and `help lift lockdown` carry a double blank line; `help help` is refused as an unknown verb; and the verb list stands in two forms (cmd/nova-fuse/help.go:108 and the dispatcher's list at cmd/nova-fuse/main.go:211). The spec still prints `FUSE FAIL`, `INIT FAIL` and the rest where the binary writes `FAILED` (docs/SPEC.md:2043), still says status lists in "the box's own order" though its own line 2074 and the binary sort, and README.md:54 still calls the trial commands 1.0.0. The `<box>.lock` file every write leaves beside the box is named in neither the help nor the spec section.

USE. The sitting runs as printed, and every answer is one line naming what was done and how it was verified. The fail-closed reads are thorough: absent, empty, `null`, an array, an unknown key, a symlink, a directory and a mode-000 file are each refused at exit 2 with the reason, never CLEAR; a fuse written as `{}` is still blown, shown as `since=unrecorded: NO REASON RECORDED`. A hand-edited reason holding a newline and `FUSE OK lockdown=clear` printed as `\x0a` inside its own line, and an ESC in a surface printed as text. Lockdown over an unreadable box kept the old bytes at `<box>.unreadable` and said so in a NOTE. A second quarantine or lockdown now keeps the standing record and says the new reason was not recorded, which fixes the 1.1.0 re-blow defect. `lift quarantine` prints one line per removal, verifies, and adds a NOTE when a lockdown still blocks. `--` lets a surface be `-h`, and the FAILED line's lift remedy quotes it as a runnable shell command.

What keeps USE at 8.5. Refusals mostly end `run: nova-fuse help`, not the verb's help, though the unknown-flag refusals name it. One refusal still names one problem: `check --bogus --max x` names the unknown flag only, not the missing `--box`. `quarantine` with a blank surface, a blank reason or neither gets the same sentence, never saying which is missing. Unreadable-box refusals carry the remedy twice in one line. `init` on a directory points at `status`, which then refuses the directory. `quarantine` under a blown lockdown says nothing of the lockdown, where `lift` does. A hand-edited `at` of `x` prints as `since=x` with no word that it is not a time. `version extra` says `got 1` without the word. There is still no `--json`, by design.

A 10 would cut the banner to its answers, usage and example; say what normalization does in `help quarantine`; give `help check` its surface sentence and drop the `--` row where no positional exists; keep one verb list; bring the spec's tokens, order and README version into line with the binary; name the lock and backup files; and make every refusal name every problem and point at `help <verb>`.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC.md:2043 | The grammar block prints `FUSE FAIL`, `LOCKDOWN FAIL`, `QUARANTINE FAIL`, `LIFT FAIL` and `INIT FAIL`; the binary writes `FAILED`. Carried from 1.1.0. | Print the samples as the binary's bytes, `FAILED` throughout. | S |
| 2 | docs/SPEC.md:2185 | "in the box's own order"; the binary sorts, and docs/SPEC.md:2074 says quarantines sort. Carried from 1.1.0. | Say the list is sorted by stored name. | S |
| 3 | README.md:54 | "These are the Nova Tools 1.0.0 commands" and the install pins v1.0.0. Carried from 1.1.0. | Move the sentence and the install line to the shipped version. | S |
| 4 | cmd/nova-fuse/main.go:65 | The banner gives init's exit 1 only as "the write was attempted and re-reading the box did not show it"; `help init` and the binary also exit 1 when something is already at the path. | Add "init: 1 also when something is already there". | S |
| 5 | cmd/nova-fuse/main.go:45 | "no box, or a broken one, reads as blown"; check then exits 2 there, not blown's 1. | Say it "exits 2, which a gate treats as blown". | S |
| 6 | cmd/nova-fuse/main.go:58 | "(soft, both directions)" is unglossed in the usage block. | Say "your own to lift; lifts every equivalent spelling". | S |
| 7 | cmd/nova-fuse/help.go:60 | "Surface spellings match after normalization" never says what normalization is; a caller learns `Foo_Bar` matches `FOO_BAR` but not `foo-bar` only by trying or reading internal/fuse/fuse.go:120. | Say it: case folds, control characters and whitespace runs become one blank, ends trimmed; `-` and `_` stay distinct. | S |
| 8 | cmd/nova-fuse/help.go:46 | `help check` says only what check does without a surface. | Lead with "Exit 0 only when no lockdown is blown and the named surface is not quarantined." | S |
| 9 | cmd/nova-fuse/help.go:119 | Every verb's help prints `--  end flags; following words are literal surfaces or reasons`, including init, status and path, which take no positional. | Print the `--` row only for check, quarantine, lockdown and lift quarantine. | S |
| 10 | cmd/nova-fuse/help.go:112 | The flag column is misaligned: the `--box <path>` description starts at column 16, the `--dry-run` and `--` descriptions at column 13. | Pad every flag name to one width. | S |
| 11 | `nova-fuse help version`, `nova-fuse help lift lockdown` | Two blank lines between the description and the exit line where other verbs have a flags block. | Print one blank line when there is no flags block. | S |
| 12 | `nova-fuse help help` | `REFUSED: unknown verb "help"`, exit 2, though `help` is in the dispatcher's verb list. | Print the banner, or a one-line help of help, at exit 0. | S |
| 13 | cmd/nova-fuse/help.go:108 | Two verb lists: help's refusal says `lift quarantine, lift lockdown, ... version`; the dispatcher's (cmd/nova-fuse/main.go:211) says `lift, ... version, help`. Carried from 1.1.0. | One list feeds the dispatcher, the help table and every refusal. | M |
| 14 | `nova-fuse help lift` | Its exit line omits 2 for an absent or unreadable box, and it has no `effect:` or `Help:` line, unlike every other verb. | Give it the same shape, or point straight at `help lift quarantine`. | S |
| 15 | cmd/nova-fuse/version.go:24 | `version extra` refuses `got 1`, naming a count, not the word. | Quote the first unexpected word. | S |
| 16 | `nova-fuse check --bogus --max x` | Names `--bogus` only; the missing `--box` takes a second run. Carried from 1.1.0. | After a parse failure, also name each missing required flag. | S |
| 17 | `nova-fuse status --box a --box b` | This, `check --box b a b`, `quarantine` with missing words, `--max -1` and every unexpected argument end `run: nova-fuse help`, while unknown flags end `help <verb>`. | Always name the verb in the help door. | S |
| 18 | `nova-fuse quarantine --box b "" why` | A blank surface, a blank reason and both missing get one sentence, `needs a surface and a reason`, never saying which. | Name the blank or missing word. | S |
| 19 | internal/fuse/fuse.go:337 | Unreadable-box refusals carry the remedy twice: `restore the box file by hand with the person you work with (...) -- ...; repair the box by hand with the person you work with, live` (cmd/nova-fuse/main.go:372 appends the second). | Print one remedy. | S |
| 20 | cmd/nova-fuse/main.go:888 | `init` on an existing box always adds "(a blown lockdown is replaced only in a live conversation ...)" even over a clear box, and on a directory points at `status`, which refuses `not a regular file`. | Mention lockdown only when the box holds one; on a non-file, say it is not a file. | S |
| 21 | cmd/nova-fuse/main.go:534 | `STATUS OK lockdown=blown since=<t> quarantines=5: <reason>` puts the lockdown's reason after the quarantine count, so it reads as the quarantines' reason. | Put `quarantines=<n>` before `lockdown=`, keeping the reason after the lockdown's stamp. | S |
| 22 | cmd/nova-fuse/main.go:860 | `quarantine` under a blown lockdown says nothing of it; `lift quarantine` adds `LIFT NOTE lockdown is still blown`. | Add `QUARANTINE NOTE lockdown is still blown (since=<t>)`. | S |
| 23 | internal/fuse/mutate.go:15 | Every write leaves `<box>.lock` beside the box; neither the help nor the nova-fuse section of docs/SPEC.md names it, so a reader finds an unexplained file. | Name the lock file in `help init`, `help lockdown` and the spec, and say it is safe to leave. | S |
| 24 | cmd/nova-fuse/help.go:53 | `help lockdown` says "unreadable bytes are backed up when possible" without saying where. | Say `kept at <box>.unreadable`. | S |
| 25 | `nova-fuse status --box hand.json` | A hand-edited `"at": "x"` prints as `since=x` with no note, though the banner says `at` is RFC3339 UTC. | Add a NOTE naming a stamp that is not RFC3339; keep the fuse blown. | S |
| 26 | cmd/nova-fuse/main.go:39 | The banner is 73 lines; the exit essay, `-h` rule, JSON sample and `--max` paragraph stand before `example:`. Carried from 1.1.0. | Keep the three answers, usage and example; move the rest to `help <verb>` and docs/CLI.md. | M |
| 27 | internal/fuse/fuse.go:1 | The package doc still opens on numbered headings in capitals. Carried from 1.1.0. | State each rule once in ordinary prose. | M |
| 28 | `nova-fuse status --box b --json` | No `--json` on any verb, by design (docs/SPEC.md:2062); a caller parses typed lines by the spec's grammar, which finding 1 shows has drifted. Carried from 1.1.0. | Render the same value as JSON under `--json`, keeping the exit codes, or fix the grammar so the lines are a contract. | M |

## Good, keep

- The fail-closed read: absent, empty, `null`, an array, an unknown key, a symlink, a directory and an unreadable mode are each a refusal at exit 2 naming why, never CLEAR; a fuse written as `{}` is still blown.
- One event per line whatever the box holds: a hand-edited newline arrives as `\x0a`, field slots escape blanks and `=`, and the lift remedy is a quoted shell command that survives a dash-leading surface.
- A re-blow keeps the standing record and says the new reason was not recorded; lockdown over an unreadable box keeps the old bytes at `<box>.unreadable` and says so.
- `-h` after a verb is refused so nothing spelled `-h` can reach CLEAR, and `lift lockdown` is refused before any flag or file is read.
- Every write is verified by re-reading the box and says so; `--dry-run` writes nothing and its line says `dry_run=true`.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| spec prints `FAIL`, binary `FAILED` (1.1.0 read, finding 1) | STILL THERE | docs/SPEC.md:2043 |
| spec says "the box's own order" (1.1.0 read, finding 2) | STILL THERE | docs/SPEC.md:2185 |
| README trial section on 1.0.0 (1.1.0 read, finding 3) | STILL THERE | README.md:54 |
| long banner (1.1.0 read, finding 4) | STILL THERE | 73 lines, cmd/nova-fuse/main.go:39-111 |
| shouted package doc (1.1.0 read, finding 5) | STILL THERE | internal/fuse/fuse.go:1-80 |
| verb list in several places (1.1.0 read, finding 6) | STILL THERE | cmd/nova-fuse/help.go:108 and cmd/nova-fuse/main.go:211 disagree |
| no `--json` (1.1.0 read finding 7, use finding 1) | STILL THERE | refused by design, docs/SPEC.md:2062 |
| a quarantine hidden behind a blown lockdown (1.1.0 read, finding 8) | STILL THERE | `check` under lockdown prints the lockdown line only; `status` lists the quarantines |
| a re-blow silently replaces the reason (1.1.0 use, finding 2) | FIXED | `QUARANTINE OK a-forum already=quarantined since=...: <first reason> (standing record kept; the new reason was not recorded: second reason)` |
| an unknown flag hides the missing `--box` (1.1.0 use, finding 3) | STILL THERE | `check --bogus --max x` names `--bogus` only |
| the blown line on stderr (1.1.0 use, finding 4) | CHANGED | still on stderr, now stated as the contract (docs/SPEC.md:2065); the exit code is the answer |
| scores | CHANGED | READ 7.5 and USE 9 at 1.1.0; READ 7.5 and USE 8.5 here: the re-blow fix is real, the carried read defects are unchanged, and this pass found new ones in the per-verb help and the refusal remedies |
