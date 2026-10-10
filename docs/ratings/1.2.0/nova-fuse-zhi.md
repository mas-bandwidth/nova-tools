# nova-fuse READ and USE rating, nova-tools 1.2.0

Rater: deepseek/deepseek-v4 on dsh (the DeepSeek Harness headless runner), a sprint worker on Zhi's re-rate card
Build: 24e5b7120dda
READ: 8/10
USE: 8.5/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the
head of sprint/mechanical-2026-10-02. `nova-fuse version` prints
`nova-fuse v1.0.1-0.20261007204559-24e5b7120dda linux/amd64 go1.26.6`. Built and run
on a Linux bench, in a scratch directory made for the trial: throwaway boxes,
hand-edited boxes, absent, unreadable, malformed and symlinked boxes, and no live
store and no server.

## Reasons

READ. The banner answers the three questions in order: line 1 is the same sentence
as the tool's README row, the `how it works:` paragraph names the box and the
surface and says the read has one yes and two noes, and the `example:` block is six
lines a stranger can paste in one sitting. `nova-fuse help <verb>` answers for every
verb with its usage, its flags and their defaults, its own exit rows and an
`effect:` line, and the effect lines are true of the binary: status and path only
read, init, lockdown, quarantine and lift write and re-read. The `-h`-is-refused
exception, which the house standard would otherwise call a defect, is stated in the
banner, in the CLI section and in the per-verb help, and argued from the one fact
that makes it necessary: exit 0 here is CLEAR, so an argument spelled `-h` must
never reach it. docs/SPEC.md's per-verb contract (Asserts, Says NO, Refuses,
Deliberately does not check) remains the clearest shape in the repository, and the
new docs/CLI.md section carries a First run whose transcript the tests execute.

What keeps READ at 8. The normative grammar block still prints `FUSE FAIL`,
`LOCKDOWN FAIL`, `QUARANTINE FAIL`, `LIFT FAIL` and `INIT FAIL` where the binary,
the CLI transcript and the section's own Conventions write `FAILED`
(docs/SPEC.md:2043-2054). The spec still says status lists quarantines in the box's
own order where the binary sorts them (docs/SPEC.md:2185, internal/fuse/fuse.go:214).
The verb list stands in four places and two of them disagree: `nova-fuse help bogus`
names `lift quarantine, lift lockdown` and omits `help`, `nova-fuse bogus` names
`lift` and `help`, and `nova-fuse help help` is refused although `help` dispatches
(cmd/nova-fuse/help.go:108, cmd/nova-fuse/main.go:141, cmd/nova-fuse/main.go:173).
The missing-`--box` hint is a second, indented line that is not the `NOTE` line the
grammar names (cmd/nova-fuse/main.go:300). README.md:86 still says the commands are
1.0.0 and installs the 1.0.0 tag. The internal/fuse package doc opens on 86 lines of
shouted numbered headings. And the tool has no `--json` and never calls
pkg/tool, so the one house shape cannot see the exception the spec argues in a
sentence (docs/SPEC.md:2062).

A 10 would print the grammar the binary emits, say the status listing is sorted,
keep one verb list, put the hint inside the grammar, put README on the shipped
release, cut the package doc to present-tense prose, and either render the one value
as JSON or cite the exception where the standard can see it.

USE. The first sitting runs as printed, in order, and every line came out as stated:
create, look, ask, blow the soft fuse, watch the answer change, rescind. The tool is
a gate that says so: only `check` exits 0 as permission, its bare form says out loud
that it checked no quarantine, and every other verb's exit tells the truth (0 done
and verified, 1 ran and said no, 2 could not run). Every write is verified by
re-reading the box, and a re-blow or a re-quarantine now keeps the first record and
says so (`already=blown`, `already=quarantined`), so the 1.1.0 silent overwrite is
fixed. Surface folding works in both directions: `quarantine Discord` then
`check discord` refuses; a hand-written `sp\x20ace` key is matched by the typed name
and by the displayed escape, and lift announces each stored spelling. A reason of
control bytes is kept as its visible escapes, a whitespace-only reason is refused,
and every reason, name and stamp prints through one-line escaping, so a hand-edited
box cannot forge a second event. Absent, unreadable, malformed, duplicate-member and
symlinked boxes all fail closed with the right remedy. `--max` bounds the listing
while the count on the first line stays uncapped. `--` keeps a leading dash a
surface or a reason. A dry run writes nothing and creates no lock file.

What keeps USE at 8.5. A dry run over a state that already stands does not say it is
a dry run: `lockdown --dry-run` on a blown box prints `LOCKDOWN OK already=blown …`
and `quarantine --dry-run` on a quarantined surface prints
`QUARANTINE OK … already=quarantined …`, with no `dry_run=true`, though the spec and
the CLI section both promise the marker on a dry run's OK line. An unknown flag
returns before `--box` is judged, so `nova-fuse check --bogus` names only `--bogus`
and not the missing required flag, and two unknown flags name only the first. Every
mutation leaves a 0-byte `<box>.lock` beside the box, while the spec says all state
lives in one JSON file and neither the spec nor the CLI names the sibling. A blown
`FUSE FAILED` line goes only to stderr, so a caller capturing stdout alone sees an
empty result. `--dry-run` must precede the positional words, so the first dry run a
reader tries after the example is refused. And there is no `--json`, so an AI parses
typed lines by hand.

A 10 keeps this loop and these refusals, carries `dry_run=true` on every dry-run OK
line including the standing ones, names the missing required flag beside the unknown
one, documents or removes the sibling lock file, prints the blown result on stdout
as well, accepts the flags after the words, and renders the same value as JSON.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC.md:2043 | The normative grammar prints `FUSE FAIL`, `LOCKDOWN FAIL`, `QUARANTINE FAIL`, `LIFT FAIL` and `INIT FAIL`; the binary writes `FAILED` (cmd/nova-fuse/check.go:59, cmd/nova-fuse/main.go:458, cmd/nova-fuse/main.go:598, cmd/nova-fuse/init.go:38) and docs/CLI.md:316 prints `FUSE FAILED`, so a caller matching the spec's token misses the byte the tool writes. | Print the block with the byte the binary writes, or make the binary write the block's token; one token across docs/SPEC.md, docs/CLI.md and the binary. | S |
| 2 | docs/SPEC.md:2185 | Says status lists quarantines "in the box's own order"; the binary sorts them (internal/fuse/fuse.go:214), and the CLI transcript shows the sorted order. | Say the listing is sorted, which is what makes two runs print the same bytes. | S |
| 3 | cmd/nova-fuse/help.go:108 | The unknown-verb list drifts from cmd/nova-fuse/main.go:141: `nova-fuse help bogus` names `lift quarantine, lift lockdown` and omits `help` and bare `lift`, while `nova-fuse bogus` names `lift` and `help`; `nova-fuse help help` is refused though `help` dispatches at cmd/nova-fuse/main.go:173. | One verb list feeds the dispatcher, the banner, every refusal and `help <verb>`. | S |
| 4 | cmd/nova-fuse/main.go:575 | `lockdown --dry-run` on a blown box prints `LOCKDOWN OK already=blown …` with no `dry_run=true`, so a dry run and a write print the same line (the same at cmd/nova-fuse/main.go:687 for an already-quarantined surface). docs/SPEC.md:2059 and docs/CLI.md:331 both say a dry run's OK line carries the marker. | Carry `dry_run=true` and "nothing written" on the standing lines when `dry` is set. | S |
| 5 | cmd/nova-fuse/main.go:264 | A flag parse failure returns before `--box` is judged, so `nova-fuse check --bogus` names only `--bogus` and not the missing required `--box`, and `--bogus1 --bogus2` names only the first; this contradicts the "every problem at once" rule the function's own comment states at cmd/nova-fuse/main.go:214. | After a parse failure, report every missing required flag and every unknown flag in one run. | S |
| 6 | internal/fuse/mutate.go:15 | Every mutation leaves a 0-byte `<box>.lock` beside the box (internal/fuse/mutate.go:94), while docs/SPEC.md:1954 says "All state lives in one JSON file — the box" and neither the spec nor docs/CLI.md names the sibling. | Name `<box>.lock` in the spec and the CLI box paragraph, or unlink it after unlock where the lock design allows. | S |
| 7 | cmd/nova-fuse/main.go:282 | `--dry-run` must precede the positional words, so `nova-fuse quarantine --box b s1 "reason" --dry-run` is refused `flags come before positional arguments`, and the banner's `example:` block never shows a dry run, so the first dry run a reader tries fails. | Accept the flags after the positional words, or put a `--dry-run` line in the example. | S |
| 8 | docs/SPEC.md:2062 | The tool has no `--json` and never calls pkg/tool, while the house standard tells every tool to render one value as lines or JSON; the spec argues the exception in one sentence, so the standard cannot see the argument. | Build the one value and encode it on request, or cite the exception beside the standard's rule. | M |
| 9 | internal/fuse/fuse.go:1 | The package doc opens on 86 lines of shouted numbered headings ("WHAT IS ACTUALLY DECIDED HERE", "THE READ HAS ONE YES AND TWO NOES"), so a reader meets capitals before a sentence. | State each rule once, present tense, in ordinary prose, and move the design essay to a note. | M |
| 10 | README.md:86 | The trial section says "These are the Nova Tools 1.0.0 commands" and README.md:91 installs `@v1.0.0`, while the tree carries the 1.2.0 rating tree and the 1.2.0 candidate. | Move the sentence and the install example to the shipped release. | S |
| 11 | cmd/nova-fuse/main.go:300 | The missing-`--box` refusal prints the one-line refusal plus a second indented `--box <path> is the JSON file …` paragraph; the paragraph carries no `NOTE` token, so it is outside the grammar docs/SPEC.md:2055 defines. | Prefix the hint with the tool's `NOTE` token, or fold it into the refusal line. | S |
| 12 | cmd/nova-fuse/check.go:59 | A blown `FUSE FAILED` line and its remedy go to stderr while `FUSE OK` goes to stdout, so a caller that captures stdout alone sees an empty result on a blow; docs/SPEC.md:2065 states the split but `help check` does not. | Print the blown result on stdout as the result too, keeping exit 1, and state the stream contract on `help check`. | S |
| 13 | cmd/nova-fuse/status.go:17 | The `STATUS MORE` remedy is the hint `--max <n> raises the ceiling, --max 0 lists every quarantine`, not a command that runs, where the house standard asks a result to name the next command. | Print `nova-fuse status --box <path> --max <n>` as the remedy. | S |
| 14 | cmd/nova-fuse/help.go:44 | `help check` prints `check --box <path> [--] [surface]` and the banner and docs/CLI.md print `check --box <path> [surface]`, so the same verb's usage line differs between the three help surfaces; `lockdown`, `quarantine` and `lift quarantine` differ the same way. | One usage line per verb, reused by the banner, the CLI section and `help <verb>`. | S |
| 15 | docs/SPEC.md:2297 | The double-blown blind spot is named as a limit: a quarantine behind a blown lockdown is invisible through `check`'s boolean seam, so it re-emerges only after the lockdown is replaced in conversation. | Add a per-fuse answer to `check`, an output-grammar addition, as the spec itself proposes. | M |

## Good, keep

The read discipline: a readable box says what it says, an absent, unreadable or
malformed box is CANNOT TELL treated as BLOWN, and the refusal says which fact it
is instead of claiming a fuse is blown. The gate is one exit code, `check` with no
surface says out loud that it checked no quarantine, and `lift lockdown` refuses
forever before a flag or a file is read, naming only the conversation.

Every write is verified by re-reading the box, and a repeat write keeps the first
record and says so (`already=blown`, `already=quarantined`), which fixes the 1.1.0
silent overwrite; a re-quarantine under another spelling of one surface is one
surface in both directions.

The one-line guarantee holds under a hostile box: every reason, name, stamp, path
and error prints escaped through pkg/oneline, so a hand-edited newline or an
ESC arrives as text and cannot forge a `FUSE OK` beneath a `FUSE FAILED`.

The refusal grammar and its remedies: one line naming the problem, a runnable
`init` or `lockdown` remedy, the box path quoted so a blank or a quote stays one
shell argument, and `--` so a surface beginning with a dash stays data.

`--dry-run` writes nothing and creates no lock file, `--max` bounds the listing
while the count line stays uncapped, and `status` sorts its listing so two runs
print the same bytes.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| spec prints `FUSE FAIL` where the binary writes `FUSE FAILED` (1.1.0 READ 1) | STILL THERE | docs/SPEC.md:2043 vs `check --box ./b.json s1` printing `FUSE FAILED quarantine=s1 …` |
| spec says status lists in the box's own order (1.1.0 READ 2) | STILL THERE | docs/SPEC.md:2185 vs the sorted `STATUS OK quarantine=surf1, surf10, surf11 …` listing |
| README pins the trial at 1.0.0 (1.1.0 READ 3) | STILL THERE | README.md:86, README.md:91 |
| the banner is about seventy lines (1.1.0 READ 4) | CHANGED | the banner still runs long, but its first paragraph now names the box and the surface, and the example is six pasteable lines |
| the package doc shouts in capitals (1.1.0 READ 5) | STILL THERE | internal/fuse/fuse.go:1 |
| the verb list stands in four places (1.1.0 READ 6) | WORSE | the two refusal lists now disagree and `nova-fuse help help` is refused (cmd/nova-fuse/help.go:108) |
| no `--json`, no pkg/tool (1.1.0 READ 7) | STILL THERE | docs/SPEC.md:2062 |
| a quarantine behind a blown lockdown is invisible (1.1.0 READ 8) | STILL THERE | docs/SPEC.md:2297 |
| a re-blow silently rewrites the record (1.1.0 USE 2) | FIXED | `lockdown` again prints `LOCKDOWN OK already=blown … (standing record kept; the new reason was not recorded)`; the same for quarantine |
| an unknown flag stops the parse before a missing `--box` is named (1.1.0 USE 3) | STILL THERE | `nova-fuse check --bogus` names only `--bogus` |
| the blown line is on stderr, the green on stdout (1.1.0 USE 4) | STILL THERE | `check --box ./b.json s2 2>/dev/null` prints nothing at exit 1 |
| no machine-readable rendering (1.1.0 USE 1) | STILL THERE | docs/SPEC.md:2062 |
| two writers of one box can lose a write (1.1.0 READ, state half) | CHANGED | `MutateBox` now serialises under `<box>.lock`; the lock file is left beside the box, which is the new docs gap |
