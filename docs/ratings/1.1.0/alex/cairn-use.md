# nova-cairn USE rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 17b4f6ee35bb
Score: 8.5/10

## Reasons

Used cold from `nova-cairn help`, `help <verb>` and `-h`, every command in a
scratch directory, nothing real touched. The first run was the help's example
block as printed, one sitting: open, append, index, receipt all answered OK
with persisted=true published=false, and the store held three plain files
(sessions/, entries/, a log) exactly as the banner says.

Two real jobs, end to end. A hand-kept record file (a daily note written by
hand, no machine lines) was appended to in its own shape with a dated section
heading, gained no sidecars, and the duplicate retry answered duplicate=true
with the stored stamp while receipt honestly said publish=unknown for a record
that never stored a policy. A nested store carried the open's --source onto
every append that named none, kept words from --file and from stdin
byte-for-byte (the receipt's trailing \x0a is the file's own newline), and
refused the same entry id under different prose at exit 1.

The refusals are the best I have used cold: one run names every missing input
at once (four lines, one exit), an unknown flag lists the flags there are, an
unknown verb lists the verbs, a bad value names the valid set, and the
no-record refusal prints the whole open command shell-quoted, ready to paste.
--json agrees with the typed lines on every verb, listings are bounded with a
MORE line and its remedy, and --max 0 lists all. No verb needs a real
service, so all four ran cold; the publish policies are recorded, nothing sent.

What costs it: the write verbs offer no dry run, so an AI cannot preview which
file and which bytes land before letting the tool write; a re-open prints a
fresh stamp and the call's publish, so it reads like a first open; the typed
line renders the kept words with control escapes, so the prose is only plain in
--json; and the conflict line's remedy names the problem but no runnable
command. Where I had to guess: whether the escaped text was the literal words
(resolved with --json), and whether the second open created or found the record
(resolved by reading the file). A 10 needs a dry run on open and append, a
re-open marked in the output, plain words on the line, and a command in the
conflict remedy.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn help append` | open and append are local writes and no verb offers a dry run (a --dry-run probe is refused as unknown with the full flag list), so an AI cannot see which file, which heading and how many bytes will land before it lets the tool write | add --dry-run to the write verbs printing the target file, the planned heading and the byte count, writing nothing | S |
| 2 | `nova-cairn open --store ./re --session r1 --publish never` | run twice, the second open prints the same OPEN OK shape with a fresh clock stamp and echoes the call's publish rather than the record's, so a re-open reads as a first open and the line does not say what the record holds | print duplicate=true with the stored open stamp and the stored publish on a re-open | S |
| 3 | `nova-cairn receipt --store ./bench --session daily --entry beat-1 --text` | the typed line renders the kept words with control escapes (text=found\x20the\x20stale...), so the line an AI reads back is not the prose stored; only --json gives the plain words | render the text fact in a plain quoted form on the line, or say the escaping in the --text flag help | S |
| 4 | `nova-cairn append --store ./work --session s2 --entry e2 --text "different words under the same id" --publish deferred` | the conflict line ends with pick a new id, which names the problem and the entry but no command, unlike every refusal which prints the next command to run | print the receipt command for the stored entry as the remedy | S |

## Good, keep

The no-record refusal prints the whole remedy verb shell-quoted
(`nova-cairn open --store './cairns' --session 'sX' --publish never`): a cold
reader pastes it and it opens exactly the named record.

The store is read, never imposed: the hand-kept record was appended to in its
own shape, kept its prose untouched, and gained no sidecars beside it.

Retry honesty: a duplicate append answers duplicate=true with the stored stamp,
and a flat record's receipt says publish=unknown rather than inventing a policy.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| index cannot list sessions | FIXED | `nova-cairn index --store ./work --max 0` answered INDEX OK sessions=2 entries=4 with one INDEX ENTRY line per entry, each carrying session=s2; the first line counts every record |
| a re-open looks like a first open | STILL THERE | `nova-cairn open --store ./re --session r1 --publish never` run twice answered two identical OPEN OK lines with stamps 2026-10-03T17:38:01.095885Z then 2026-10-03T17:38:01.113214Z and no marker that the second was a no-op |
