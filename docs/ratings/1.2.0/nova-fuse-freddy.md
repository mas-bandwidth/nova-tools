# nova-fuse READ and USE rating, nova-tools 1.2.0

Rater: Freddy (inception/mercury-2.5 / opencode)
Build: e174cc87f042
READ: 8/10
USE: 9/10

This rates nova-fuse at e174cc87f042, the head of sprint/mechanical-2026-10-02. Read cold: the banner, `help` for every verb, the nova-fuse section of docs/SPEC.md, docs/CLI.md and cmd/nova-fuse/README.md, the package doc and the source. Used for real: the banner's example six-line sitting, then tested with absent, empty, `null`, an array, invalid JSON, quarantined surfaces, unknown flags, blank inputs, and lock file sidecars, over a throwaway directory on a Linux bench machine.

## Reasons

READ. The banner answers what the tool is (a recorded decision to stop reading an untrusted source, checked before every read), how it works (one JSON box named with `--box`, one lockdown, one quarantine per surface, each with its time and reason), and the first run (six commands that run as printed). Every verb's help is a real page: usage, a detail sentence, flags, exit codes, an `effect:` line saying inspection or local write, and the `Help:` door. The README row is the same sentence as the banner's line 1.

What keeps READ at 8. `nova-fuse help help` is REFUSED as an unknown verb even though `help` is a verb the dispatcher lists, and the help door's own verb list omits it. The grammar block in docs/SPEC.md prints `FUSE FAIL` but the binary writes `FUSE FAILED`. `help lift` is the one verb page with no `effect:` line and no `Help:` line. Every flag help prints the `--` row, including `init`, `status` and `path`, which take no positional. The banner is 73 lines with extra content between `usage:` and `example:`. The package doc opens on shouted numbered headings. `help quarantine` doesn't explain what normalization is.

USE. The tool works reliably. Absent, empty, `null`, an array, and invalid JSON files are each refused at exit 2 with the reason, never CLEAR. A surface is folded on both sides (case-insensitive). A re-blow keeps the first record. `--dry-run` is on all four writes. `status` keeps its count uncapped under a bounded list with a `MORE` line. Twenty parallel quarantines work. `--` keeps an untrusted surface or reason as data.

What keeps USE at 9. An unknown flag fails the parse before the verb's own checks, so `nova-fuse check --bogus` names `--bogus` but never the required `--box`: one run does not name every problem. `quarantine` with a blank surface, a blank reason, or neither gets "needs a surface and a reason" without saying which is missing. `init` at a directory points the reader at `status`, which then refuses "not a regular file" — a dead end. Every write leaves `<box>.lock` beside the box and no help page names it.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-fuse/main.go:264-281 | an unknown flag fails the parse and returns before the box and word checks, so `nova-fuse check --bogus` names only `--bogus`, never the required `--box`; one run does not name every problem (ONBOARDING point 2) | after a parse failure, still report each missing required flag and each missing positional in the same refusal | S |
| 2 | cmd/nova-fuse/init.go:38 | `init` at a directory says "read it with `nova-fuse status --box 'mydir'`", and `status` then refuses `mydir is not a regular file`; the remedy is a dead end | on a non-file, say it is not a file and name no status read | S |
| 3 | cmd/nova-fuse/main.go:614 | the `LOCKDOWN OK` line prints the reason without bounding; a 5000-byte reason made a 5239-byte receipt, where `status` caps the same stored reason at 551 bytes | cap the OK line with `oneline.TailBytes` the way `why` does | S |
| 4 | cmd/nova-fuse/main.go:714 | the `QUARANTINE OK` line is likewise unbounded; measured 5166 bytes for a 5000-byte reason | cap it with `oneline.TailBytes` | S |
| 5 | internal/fuse/mutate.go:11-18 | every write leaves `<box>.lock` beside the box, and a refused `lift quarantine` on an absent box leaves `never.json.lock` with no box; the banner, every `help <verb>`, docs/CLI.md and the SPEC section never name it | name the lock sidecar in `help init` and `help lockdown` and in the spec, and say it is safe to leave | S |
| 6 | cmd/nova-fuse/check.go:54 and main.go:362 | a wrong-shaped box (`null`, an array) carries the remedy twice: the fuse error's `restore the box file by hand` and `remedy`'s `repair the box by hand` | print one remedy | S |
| 7 | cmd/nova-fuse/main.go:362 | `quarantine` with a blank surface, a blank reason, or neither gets the single sentence "needs a surface and a reason", never saying which is missing | name the blank or missing word | S |
| 8 | cmd/nova-fuse/help.go:108 | `nova-fuse help help` is REFUSED as an unknown verb and the help door's list omits `help`; the dispatcher's list includes `help` and names `lift` where help names `lift quarantine, lift lockdown` | one list feeds the dispatcher, the help table and every refusal, and it includes `help` | M |
| 9 | docs/SPEC.md:2043 | the grammar block prints `FUSE FAIL`, `LOCKDOWN FAIL`, `QUARANTINE FAIL`, `LIFT FAIL` and `INIT FAIL`; the binary writes `FAILED` | print the samples as the bytes the binary writes | S |
| 10 | cmd/nova-fuse/help.go:100-104 | `help lift` prints a usage block with no `effect:` line and no `Help:` line, unlike every other verb page | give it the same shape as `help lift quarantine` | S |
| 11 | cmd/nova-fuse/help.go:119 | every verb's help prints the `--` row, including `init`, `status` and `path`, which take no positional | print the `--` row only for check, quarantine, lockdown and lift quarantine | S |
| 12 | cmd/nova-fuse/main.go:39 | the banner is 73 lines; the exit-code essay, the `-h` rule, the box JSON sample and the `--max` paragraph stand between `usage:` and `example:` | keep the three answers, usage and example; move the rest to `help <verb>` and docs/CLI.md | M |
| 13 | internal/fuse/fuse.go:1-86 | the package doc opens on 86 lines of shouted numbered headings | state each rule once, present tense, in ordinary prose | M |
| 14 | cmd/nova-fuse/help.go:60 | `help quarantine` says "Surface spellings match after normalization" and never says what normalization is | state the rule in `help quarantine` | S |

## Good, keep

- The fail-closed read: absent, empty, `null`, an array, an unknown key, a symlink, a directory and a mode-000 file are each a refusal at exit 2 naming why, never CLEAR; `{}` is a readable empty box.
- The one-line refusal grammar with a runnable remedy: a `FUSE FAILED` remedy is a quoted POSIX-shell command that keeps a dash-leading surface as data, and control bytes in a reason print as `\x0a` inside one line.
- Folding is honest in both directions: `Discord`, `discord` and `DISCORD` are one surface, a lift removes every stored spelling and announces each with its own line.
- A re-blow keeps the standing record and says the new reason was not recorded.
- `status` bounds the listing under an uncapped `quarantines=` count and a `MORE` line, and `--max 0` lists all.
- `--dry-run` on all four writes takes no lock, writes nothing and says `dry_run=true`.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| the spec prints `FUSE FAIL` where the binary writes `FAILED` | STILL THERE | docs/SPEC.md:2043 against cmd/nova-fuse/main.go:518 |
| the spec says status lists in "the box's own order" | STILL THERE | docs/SPEC.md:2185 against internal/fuse/fuse.go:213 |
| the README trial commands pinned to 1.0.0 | FIXED | README.md:86-91 now match 1.2.0 |
| no `--json` on any verb | STILL THERE | every verb refuses it by design (docs/SPEC.md:2062) |
