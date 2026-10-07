# nova-cairn READ and USE rating, nova-tools 1.2.0

Rater: Inception/mercury-2.5 in opencode, a sprint worker on a friend's re-rate card
Build: c76fcb249cc1
READ: 8/10
USE: 8/10

This rates nova-cairn at c76fcb249cc1, the head of sprint/mechanical-2026-10-02. `nova-cairn version` prints `nova-cairn v1.0.1-0.20261007015429-c76fcb249cc1 linux/amd64 go1.26.6`. Built and run on a Linux machine in a throwaway directory; no live store, no server.

## Reasons

READ. The banner answers what it does, how it works (store is a named directory, synced before OK; same id+words = duplicate, other words = conflict; --publish is recorded, nothing sent), and how to use it (four examples in one sitting). Every verb answers `-h` with usage, flags, and an `effect:` line. The spec is explicit about refusals and the flat record shape. Refusals and conflicts name the next command whole and shell-ready.

What keeps READ at 8: The usage block has a line `nova-cairn NOTE: --publish is a recorded word, ...` that reads as a verb. Every `-h` prints all four verbs' exit codes, not just that verb's. Flags print Go type placeholders like `<string>` instead of `dir` or `id`. The CLI.md section still lacks a First run block. README.md still names 1.0.0 commands. The spec has a 544-character line.

USE. The tool works: nested store is solid (open, append, index, receipt all work), flat record works, conflicts and duplicates are handled, --dry-run and --json work on all verbs, --file and --text keep bytes exactly, --now allows replay, twenty concurrent appends under one id gave one OK and 19 conflicts (no race), one run names every problem.

What keeps USE at 8: The flat record drops --source and --publish values it doesn't store. A flat append leaves a .md.lock file beside the record. Index and receipt disagree on what a session is when entries survive a deleted session file. One corrupt entry breaks the whole index. Re-opens print a fresh stamp with no marker. --now accepts non-Z offsets despite saying UTC.

A 10 would fix the flat append to refuse or store --source/--publish, move the lock or document it, make index/receipt agree, handle corrupt entries gracefully, mark re-opens, and clean up the usage block and exit codes display.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/cairn/cairn.go:638 | Append to a flat record with --source drops the value; receipt prints source=- | Refuse --source on a flat record (it stores none), or record it in the section | S |
| 2 | internal/cairn/cairn.go:369 | Append to a flat record with --publish prints publish=never but receipt says publish=unknown | Report publish=unknown on a flat append always, or refuse --publish there | S |
| 3 | internal/cairn/cairn.go:289 | A flat append leaves .day1.md.lock beside the record; spec says nothing appears | Lock the record file itself (flock), or document the lock file | S |
| 4 | internal/cairn/cairn.go:863 | With sessions/s.md removed and entries/s/e.json kept, index lists it but receipt refuses "no such session" | Count session from entries or log as well as file, or have index refuse the orphan | S |
| 5 | internal/cairn/cairn.go:738 | One corrupt entries/s/bad.json makes index refuse whole, naming neither session nor path | Name the file path and session, give receipt or the path as remedy | S |
| 6 | nova-cairn open --store ./cairns --session s1 --publish manual (twice) | Second open prints fresh stamp with no marker; carried from 1.1.0 | Print duplicate=true with stored open stamp on a re-open | S |
| 7 | cmd/nova-cairn/main.go:104 | Banner usage block has `nova-cairn NOTE: --publish is a recorded word, ...` reading as a verb | Move the note under the usage block as prose, or into open -h | S |
| 8 | nova-cairn version -h (every verb's -h) | Each -h prints exit codes of all four verbs; version lists none of its own | Print the common line and the verb's own row only | S |
| 9 | nova-cairn open -h | --store <string>, --session <string>, --now <string>: Go type placeholders instead of dir, id, rfc3339-utc | Backtick the placeholder word in each flag's usage text | S |
| 10 | cmd/nova-cairn/main.go:212 | --now 2026-01-01T00:00:00+02:00 is accepted; help says RFC 3339 UTC | Refuse non-Z offset, or say "any RFC 3339 offset, stored as UTC" | S |
| 11 | nova-cairn append --store ./cairns --session s2 --entry e4 | Required-input refusals end run: nova-cairn help; unknown flag ends run: nova-cairn open -h | End every per-verb refusal with run: nova-cairn <verb> -h | S |

## Good, keep

The nested store's duplicate and conflict rules hold under contention: 20 concurrent appends under one new id gave exactly one OK and 19 conflicts. --dry-run on both write verbs checks everything and writes nothing. One run names every problem at once. Every conflict and missing-record refusal names the next command whole and shell-ready. Every line splits persisted= from published=

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| same-id concurrent appends can overwrite what the banner promises never is | FIXED | 20 concurrent append --entry same with different words: 1 exit 0, 19 exit 1 |
| a re-open looks like a first open | STILL THERE | two opens printed different stamps, no marker |
| no --dry-run on the write verbs | FIXED | open --dry-run and append --dry-run print dry_run=true |
| receipt text renders spaces as \x20 | FIXED | receipt --text printed text="the words to keep" |
| a conflict names no command | FIXED | conflict ends run: nova-cairn receipt --store ./cairns --session s1 --entry e1 --text |
| a flat record gains no sidecars | REGRESSED | flat append leaves .day1.md.lock in the store |
| the CLI section has no First run block | STILL THERE | docs/CLI.md section lacks First run |
| README trial commands pinned to 1.0.0 | STILL THERE | README.md:54-59 |
