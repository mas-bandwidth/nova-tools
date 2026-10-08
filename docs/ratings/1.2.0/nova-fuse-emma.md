# nova-fuse READ and USE rating, nova-tools 1.2.0

Rater: deepseek-v4 in dsh (the DeepSeek Harness headless runner), a sprint worker on a friend's re-rate card; the rating is the worker's, not the friend's
Build: 9c9b2ebc49fd
READ: 7/10
USE: 8/10

This rates nova-fuse at 9c9b2ebc49fd, the head of sprint/mechanical-2026-10-02. `nova-fuse version` prints `nova-fuse v1.0.1-0.20261007202756-9c9b2ebc49fd linux/amd64 go1.26.6`. Read cold: the banner, `help` for every verb (`lift quarantine` and `lift lockdown` included), the nova-fuse section of docs/SPEC.md, docs/CLI.md and cmd/nova-fuse/README.md, the package doc and the source. Used for real: the banner's six-line sitting in order, then every verb with and without `--dry-run`, over boxes that are absent, empty, `null`, an array, carry an unknown key, hold `{}`, are a symlink, a directory, mode 000, are a valid empty box, and hold one and three fold-equivalent keys, plus twenty parallel quarantines. Built and run on a Linux bench machine in a throwaway directory; no live box, no server, no network.

## Reasons

READ. The banner answers what the tool is (a recorded decision to stop reading an untrusted source, checked before every read), how it works (one JSON box named with `--box`, one lockdown, one quarantine per surface, each with its time and reason), and the first run (six commands that run as printed). Every verb's help is a real page: usage, a detail sentence, flags, exit codes, an `effect:` line saying inspection or local write, and the `Help:` door. The README row is the same sentence as the banner's line 1.

What keeps READ at 7. `nova-fuse help help` is REFUSED as an unknown verb even though `help` is a verb the dispatcher lists, and the help door's own verb list omits it (help.go:108), while the dispatcher's list (main.go:141, main.go:205) includes it and spells the group differently: two lists that must agree and do not. The grammar block in docs/SPEC.md:2043 prints `FUSE FAIL`, `LOCKDOWN FAIL`, `QUARANTINE FAIL`, `LIFT FAIL` and `INIT FAIL`; the binary writes `FAILED` everywhere (check.go:59, main.go:458, main.go:598, main.go:693, init.go:38), so a caller matching the spec's token misses every refusal. The spec says status lists "in the box's own order" (docs/SPEC.md:2185) while its own line 2074 and the code sort. README.md:86 still says "These are the Nova Tools 1.0.0 commands" and README.md:87/91 install `@v1.0.0` in a 1.2.0 tree. `help lift` (help.go:100) is the one verb page with no `effect:` line and no `Help:` line. Every flag help prints the `--` row, including `init`, `status` and `path`, which take no positional (help.go:119); the flag column is one column off (`--box <path>` against `--max <n>`); `help version` and `help lift lockdown` print a double blank line before the exit line; `help quarantine` says "Surface spellings match after normalization" without ever saying what the normalization is; and the banner is 73 lines with the exit essay, the `-h` rule, the box sample and the `--max` paragraph between `usage:` and `example:`. The package doc (internal/fuse/fuse.go:1-86) opens on shouted numbered headings. `help lockdown` promises the unreadable bytes are backed up but never says where. A 10 would put the shipped version in the README, print the spec's tokens as the binary's bytes, keep one verb list, cut the banner, gate the `--` row to verbs with a positional, and name the lock sidecar and the backup suffix.

USE. The tool works, and it is the fail-closed reads that earn the score. Absent, empty, `null`, an array, an unknown top-level key, a symlink, a directory and a mode-000 file are each refused at exit 2 with the reason, never CLEAR; `{}` is a readable empty box. A surface is folded on both sides: `Discord`, `discord` and `DISCORD` are one surface, `check` refuses on any spelling and `lift quarantine` removes all three, announcing each under its stored spelling. A re-blow keeps the first record and says the new reason was not recorded, so a second quarantine no longer silently rewrites the time and reason. `--dry-run` is on all four writes, takes no lock and writes nothing. `status` keeps its count uncapped under a bounded list with a `MORE` line, and `--max 0` lists all. Twenty parallel quarantines produced `quarantines=20`, so the box lock holds. `--` keeps an untrusted surface or reason as data (`check --box b.json -- -h` answers rather than reading `-h` as a flag), and a `FUSE FAILED` line's remedy is a quoted shell command.

What keeps USE at 8. An unknown flag fails the parse and returns before the verb's own checks, so `nova-fuse check --bogus` names `--bogus` and never the required `--box`: one run does not name every problem, which is the standard's second onboarding point. `quarantine` with a blank surface, a blank reason, or neither gets the one sentence "needs a surface and a reason" and never says which is missing. A wrong-shaped box (`null`, an array) carries the remedy twice in one line. `init` at a directory points the reader at `status`, which then refuses `not a regular file`. The `LOCKDOWN OK` and `QUARANTINE OK` lines print the reason uncapped (a 5000-byte reason made 5239-byte and 5166-byte receipts) while `status` caps the same reason at 551 bytes. Every write leaves `<box>.lock` beside the box and a refused `lift quarantine` on an absent box leaves `never.json.lock` with no box at all, and no help page, the CLI section or the spec section names the file. A 10 would name every problem in one run, cap every receipt, fix the directory remedy, and name the sidecars.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-fuse/main.go:264-281 | an unknown flag fails the parse and returns before the box and word checks, so `nova-fuse check --bogus` names only `--bogus`, never the required `--box`, and `quarantine --bogus` names neither the box nor the surface and reason; one run does not name every problem (ONBOARDING point 2) | after a parse failure, still report each missing required flag and each missing positional in the same refusal | S |
| 2 | cmd/nova-fuse/main.go:614 | the `LOCKDOWN OK` line prints the reason through `oneline.Escape` with no `oneline.Cap`; a 5000-byte reason made a 5239-byte receipt, where `status` caps the same stored reason at 551 bytes (`why`, main.go:758) | cap the OK line with `oneline.TailBytes` the way `why` does | S |
| 3 | cmd/nova-fuse/main.go:714 | the `QUARANTINE OK` line is likewise unbounded; measured 5166 bytes for a 5000-byte reason | cap it with `oneline.TailBytes` | S |
| 4 | cmd/nova-fuse/init.go:38 | `init` at a directory says "read it with `nova-fuse status --box 'adir'`", and `status` then refuses `adir is not a regular file` (internal/fuse/fuse.go:243); the remedy is a dead end | on a non-file, say it is not a file and name no status read | S |
| 5 | internal/fuse/mutate.go:11-18 | every write leaves `<box>.lock` beside the box, and a refused `lift quarantine` on an absent box leaves `never.json.lock` with no box; the banner, every `help <verb>`, docs/CLI.md and the SPEC section never name it | name the lock sidecar in `help init` and `help lockdown` and in the spec, and say it is safe to leave | S |
| 6 | cmd/nova-fuse/check.go:54 and main.go:362 | a wrong-shaped box (`null`, an array) carries the remedy twice: the fuse error's `restore the box file by hand with the person you work with` and `remedy`'s `repair the box by hand with the person you work with, live` | print one remedy | S |
| 7 | cmd/nova-fuse/main.go:630-634 | `quarantine` with a blank surface, a blank reason, or neither gets the single sentence "needs a surface and a reason", never saying which is missing | name the blank or missing word | S |
| 8 | cmd/nova-fuse/version.go:24 | `version -h` and `version --help` take the argument-count path and refuse `got 1`, not the documented `-h` refusal, though `help version`'s `Help:` line promises that rule | refuse `-h` with the documented line, or quote the first unexpected word | S |
| 9 | cmd/nova-fuse/help.go:108 | `nova-fuse help help` is REFUSED as an unknown verb and the help door's list omits `help`; the dispatcher's list (main.go:141, main.go:205) includes `help` and names `lift` where help names `lift quarantine, lift lockdown` | one list feeds the dispatcher, the help table and every refusal, and it includes `help` | M |
| 10 | docs/SPEC.md:2043 | the grammar block prints `FUSE FAIL`, `LOCKDOWN FAIL`, `QUARANTINE FAIL`, `LIFT FAIL` and `INIT FAIL`; the binary writes `FAILED` (check.go:59, main.go:458, main.go:598, main.go:693, init.go:38) | print the samples as the bytes the binary writes | S |
| 11 | docs/SPEC.md:2185 | "in the box's own order"; `status` sorts (internal/fuse/fuse.go:213, status.go:60) and docs/SPEC.md:2074 says quarantines sort | say the list is sorted by stored name | S |
| 12 | README.md:86 | "These are the Nova Tools 1.0.0 commands" and README.md:87/91 install `@v1.0.0` while the tree ships 1.2.0 | move the sentence and the install line to the shipped version | S |
| 13 | cmd/nova-fuse/help.go:100-104 | `help lift` prints a usage block with no `effect:` line and no `Help:` line, unlike every other verb page, and its exit line omits the absent and unreadable box case | give it the same shape as `help lift quarantine` | S |
| 14 | cmd/nova-fuse/help.go:112-114 | the flag column is misaligned: the `--box <path>` description starts one column right of `--max <n>` and `--dry-run` | pad every flag name to one width | S |
| 15 | cmd/nova-fuse/help.go:119 | every verb's help prints the `--` row, including `init`, `status` and `path`, which take no positional | print the `--` row only for check, quarantine, lockdown and lift quarantine | S |
| 16 | cmd/nova-fuse/help.go:110-122 | `help version` and `help lift lockdown` print a double blank line before the exit line where verbs with a flags block print one | print one blank line when there is no flags block | S |
| 17 | cmd/nova-fuse/main.go:39 | the banner is 73 lines; the exit-code essay, the `-h` rule, the box JSON sample and the `--max` paragraph stand between `usage:` and `example:` | keep the three answers, usage and example; move the rest to `help <verb>` and docs/CLI.md | M |
| 18 | internal/fuse/fuse.go:1-86 | the package doc opens on 86 lines of shouted numbered headings ("WHAT IS ACTUALLY DECIDED HERE", "THE READ HAS ONE YES AND TWO NOES") | state each rule once, present tense, in ordinary prose | M |
| 19 | cmd/nova-fuse/main.go:42 | the banner says a missing or broken box "reads as blown"; `check` exits 2 there, not blown's 1 (check.go:51-55) | say it refuses at exit 2, which a gate treats as blown | S |
| 20 | cmd/nova-fuse/help.go:60 | `help quarantine` says "Surface spellings match after normalization" and never says what normalization is: case folds, control characters and runs of blanks become one blank, ends trim, `-` and `_` stay distinct (internal/fuse/fuse.go:133-150) | state the rule in `help quarantine` | S |
| 21 | cmd/nova-fuse/main.go:36 | no verb accepts `--json` (docs/SPEC.md:2062), while the standard asks one value rendered as lines or JSON; a caller must parse typed lines and read a grammar that finding 10 shows has drifted | render the same value as JSON under `--json`, keeping the exit codes, or cite the exception beside the standard's rule | M |

## Good, keep

- The fail-closed read: absent, empty, `null`, an array, an unknown key, a symlink, a directory and a mode-000 file are each a refusal at exit 2 naming why, never CLEAR; `{}` is a readable empty box.
- The one-line refusal grammar with a runnable remedy: a `FUSE FAILED` remedy is a quoted POSIX-shell command that keeps a dash-leading surface as data, and control bytes in a reason print as `\x0a` inside one line.
- Folding is honest in both directions: `Discord`, `discord` and `DISCORD` are one surface, a lift removes every stored spelling and announces each with its own line.
- A re-blow keeps the standing record and says the new reason was not recorded, and a lockdown re-blow keeps the first time and reason.
- `status` bounds the listing under an uncapped `quarantines=` count and a `MORE` line, and `--max 0` lists all.
- `--dry-run` on all four writes takes no lock, writes nothing and says `dry_run=true`; twenty parallel quarantines all landed.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| the spec prints `FUSE FAIL` where the binary writes `FAILED` | STILL THERE | docs/SPEC.md:2043 against check.go:59 |
| the spec says status lists in "the box's own order" | STILL THERE | docs/SPEC.md:2185 against status.go:60 |
| the README trial commands pinned to 1.0.0 | STILL THERE | README.md:86-91 |
| the verb list stands in four places | CHANGED | now two lists that disagree, help.go:108 and main.go:141/205, and `help help` is refused |
| a re-blow silently rewrites the time and reason | FIXED | a second `quarantine` prints `already=quarantined since=... (standing record kept; the new reason was not recorded: ...)` |
| lost concurrent updates | FIXED | twenty parallel quarantines gave `quarantines=20` |
| the unknown-option refusal omits the offending flag | FIXED | `check --bogus` prints `unknown flag --bogus; the flags of check are --box` |
| the package doc's shouted capitals | STILL THERE | internal/fuse/fuse.go:1-86 |
| no `--json` on any verb | STILL THERE | every verb refuses it by design (docs/SPEC.md:2062) |
| README 6.5 to 8.4 | CHANGED | the nova-fuse row now matches the banner, but README.md:86 still pins 1.0.0 |
