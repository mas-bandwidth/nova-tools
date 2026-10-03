# nova-cairn USE rating, nova-tools 1.1.0

Rater: Grok
Build: 2c02b2aa2042
Score: 8.5/10

## Reasons

`nova-cairn help` answers what it does, names the directory as the store, and prints four commands that run as written. `nova-cairn -h` and `nova-cairn help open` (and the same for append, index, receipt) exit 0 and say whether the verb writes or only reads. A bare `nova-cairn` exits 2 and names the verbs and `nova-cairn help`.

The first run was the example block, in a directory made for the trial. open printed `OPEN OK session=s1 store=./cairns source=- publish=manual` and created the directory. append printed `persisted=true published=false publish=manual duplicate=false`. index printed `INDEX OK sessions=1 entries=1` and one `INDEX ENTRY` line. receipt repeated the stamp, the byte count 17, and `published=false`. The words were in `entries/s1/e1.json`, which is the path the banner names. I did not have to invent a flag to get that far.

The first real job was a session with a source pointer, two entries, a reread, a retry, and a clash. An append with no `--source` carried the open pointer (`source=notes/day-1`). The retry of the same words printed `duplicate=true` and the original stamp, not the later `--now` I passed. Different words exited 1 with `APPEND FAILED` and `already holds different prose; pick a new id`. That names the problem. It does not give a command to run.

The second job was a different store: words from a file, words from stdin (`--file -`), then receipt and index as JSON, and index with `--max 1`. The file is 18 bytes. `receipt --text --json` returned those bytes, including the line break, in `facts.text`. `index --max 1` printed `INDEX OK sessions=1 entries=2`, one entry, and `INDEX MORE kind=entry shown=1 total=2` with the flag that shows the rest. I could act on that line.

The four refusals each name the problem and a next command. Missing `--publish` says what the flag wants (`never, manual, deferred or immediate`) and `run: nova-cairn help`. `--nope` lists the flags of index and `run: nova-cairn index -h`. `seal` lists the verbs and `run: nova-cairn help`. `--publish later` names the bad value and the allowed set. `nova-cairn open` with no flags prints three lines, one per missing flag. An append that was never opened prints a full open command; pasting it exited 0. Two gaps: an unknown flag hides every other problem (only `--nope` was reported), and `append --publish later` together with a bad `--now` and missing ids never mentions the bad policy. The next command on most lines is `nova-cairn help`, not `nova-cairn help <verb>`.

`--json` is on every verb help, and it matches the lines: a refusal is `status=refused` with `why` and `remedy` on stdout, exit 2. `--dry-run` is not on any help. `append --dry-run` is `unknown flag --dry-run`. I could not read a plan before a write.

Where I had to guess: the line from `receipt --text` is not the stored words. It printed `text=line\x20one\x0aline\x20two\x0a`. Nothing in the help says those escapes. I only knew they were the file's blanks and line breaks because I had just written the file, and because the JSON value was the file. A re-open also made me guess wrong. `open` of a session that already existed, with a new `--publish immediate` and a new `--now`, printed `OPEN OK` with that policy and that stamp, and with the old source. The session file and a later receipt still said `publish=never` and the original stamp. The OK line looks like a first open that applied the new flags. It did not. Index of a store whose only session has no entries prints `sessions=1` and does not print the id, so I cannot come back to a name I do not still hold.

A 10 would print a second open as unchanged, with the stored policy and stamp; list session ids; put the stored words in the line form or name the escapes in the help; report a bad `--publish` in the same run as the other problems; and offer a dry run that writes nothing. The example block, the duplicate stamp, the pasteable open remedy, and the JSON receipt would stay.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-cairn open --store ./notes --session day-1 --source other --publish immediate --now 2026-10-03T19:00:00Z` | A second open prints OPEN OK with the caller's policy and stamp. The file and a later receipt still say publish=never and the original stamp, so the OK line is not the record. | On an existing session, print the stored policy and stamp, and say that this call wrote nothing. | S |
| 2 | `nova-cairn index --store ./fresh` | The store holds one session and no entries, and the only line is INDEX OK sessions=1 entries=0. The id is not in the output, in lines or in JSON, so a return visit cannot list the sessions. | Print one row per session, including a session that has no entries yet. | S |
| 3 | `nova-cairn receipt --store ./inbox --session clip --entry from-file --text` | The line spells the stored words as text=line\x20one\x0aline\x20two\x0a. The help does not say so. The JSON value is the file, bytes and line breaks included. | Say the line escapes, or print the words in a form that matches the JSON value. | S |

## Good, keep

The example block runs as printed, creates the store, and the banner's entry path holds the words. Success says the note is stored here and was not sent.

Missing flags are one line each, each saying what the flag wants, and the refusal for a session that does not exist is an open command I pasted and ran.

A retry of the same words keeps the stored stamp and says duplicate=true. `--json` on receipt returns the file's own bytes. `--max` keeps the total and names the flag that shows the rest.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| index cannot list sessions | STILL THERE | nova-cairn index --store ./fresh printed INDEX OK sessions=1 entries=0 and no session id, and --json had the same two facts and no items |
| a re-open looks like a first open | STILL THERE | nova-cairn open --store ./notes --session day-1 --publish immediate --now 2026-10-03T19:00:00Z printed OPEN OK publish=immediate stamp=2026-10-03T19:00:00Z; receipt of the stored entry still said publish=never |
