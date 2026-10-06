# nova-cairn READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker on a friend's re-rate card
Build: 7e117276e198
READ: 7/10
USE: 7/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of sprint/mechanical-2026-10-02. `nova-cairn version` prints `nova-cairn v1.0.1-0.20261006142739-7e117276e198 linux/amd64 go1.26.6`. Built and run on a Linux bench machine, in a scratch directory made for the trial; no live store, no server.

## Reasons

READ. The banner's first line is the README row's sentence, and the next four lines say the whole contract: a store is a directory you name, synced before OK; same id and same words is a duplicate, other words a conflict; `--publish` is recorded and nothing is sent. The four examples are one sitting and run as printed. Every verb answers `-h` and `help <verb>` with its usage line, its example, a flag list with the required ones marked, and an `effect:` line. The spec opens with what the tool refuses to be, and every refusal and conflict I hit names the next command, quoted for the shell.

What keeps READ at 7. The usage block carries a line that is not a usage line: `nova-cairn NOTE: --publish is a recorded word, ...` (cmd/nova-cairn/main.go:104) reads as a verb called `NOTE:`. Every verb's `-h`, `version -h` included, prints the exit codes of all four verbs instead of its own. Flags print `<string>` for a store, an id and a stamp. The spec opens its second paragraph with "not a ratification ... before any approval of that draft" (docs/SPEC-CAIRN.md:13-14), where no draft has been named, names Redis twice in a tool that has none, and keeps one 544-character line (docs/SPEC-CAIRN.md:88) in an otherwise wrapped section. The docs/CLI.md section still has no First run block and uses `./checkpoints` where the help uses `./cairns`, and README.md:54 still calls the commands 1.0.0. The required-flag refusals point at `nova-cairn help`, while the unknown-flag refusal points at `open -h`, the better remedy.

USE. The banner's example ran clean, and the nested store is solid: open, append, index and receipt answered with typed lines, the store held three plain files, a duplicate retry returned `duplicate=true` with the stored stamp, and a conflict exited 1 naming `receipt --text`. `--file` and `--file -` kept bytes exactly, control bytes included. `--dry-run` on open and append writes nothing and says `persisted=false dry_run=true`. A bad session, entry, policy, stamp and a missing `--store` were named together in one run. Twenty concurrent appends under one new entry id gave one OK and nineteen exit-1 conflicts, with the stored file holding the OK's words: the 1.1.0 overwrite race is closed. `--json` is the same result as one object on every verb, refusals included.

What keeps USE at 7. The flat record, the shape the spec is proudest of, now misreports. An append to `<store>/<session>.md` with `--source x.md --publish never` printed `source=-` and `publish=never`: the source was dropped without a word, and the policy on the OK line was stored nowhere, so the receipt for the same entry says `publish=unknown`. The flat append also leaves `.day1.md.lock` beside the file, where the spec and the 1.1.0 ratings both say nothing appears. With `sessions/s.md` gone and the entry file still there, `index` counts `sessions=0 entries=1` and lists the entry, while `receipt` for it refuses "no such session". One corrupt entry file makes the whole index refuse, naming the entry id but not its path, with `run: nova-cairn help` as the remedy. A re-open still prints a fresh clock stamp and no marker. `--now 2026-01-01T00:00:00+02:00` is accepted and converted, though the help and the refusal say UTC. A store that is a plain file is refused as "cannot read the session's open record from afile/log.jsonl: not a directory".

A 10 would make the flat append refuse `--source` and `--publish` (or store them) instead of reporting what it did not keep, take the flat lock somewhere other than beside the record or say in the spec that it does, make index and receipt agree on what a session is, mark a re-open as one, move the NOTE out of the usage block, print each verb's own exit codes in its `-h`, and give the CLI section its First run.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/cairn/cairn.go:638 | `append --store ./flat --session day1 --entry n2 --text two --source x.md --publish never` on a flat record printed `source=-`: the `--source` was dropped with no word, and appendFlat is never handed it. | Refuse `--source` on a flat record (the format stores none), naming why, or record it in the section. | S |
| 2 | internal/cairn/cairn.go:369 | The same append printed `publish=never`, but the flat format stores no policy; `receipt` for n2 says `publish=unknown`. The OK line reports a policy nothing kept. | Report `publish=unknown` on a flat append always, or refuse `--publish` there as for `--source`. | S |
| 3 | internal/cairn/cairn.go:289 | A flat append leaves `.day1.md.lock` in the store beside the record; docs/SPEC-CAIRN.md:53-54 says nothing appears beside the file, and both 1.1.0 ratings kept "gains no sidecars". Added after 1.1.0 (3a33afaed). | Lock the record file itself (flock on the open .md), or say in the spec that one hidden lock file appears. | S |
| 4 | internal/cairn/cairn.go:863 | With `sessions/s.md` removed and `entries/s/e.json` kept, `index` answers `sessions=0 entries=1` and lists the entry; `receipt --session s --entry e` refuses "no such session". The two read verbs disagree. | Count a session from its entries or its log open record as well as its file, or have index refuse the orphan entry naming it. | S |
| 5 | internal/cairn/cairn.go:738 | One corrupt `entries/s/bad.json` makes `index --store` refuse whole: `stored entry "bad" is corrupt ...; run: nova-cairn help`, naming neither the session nor the path. | Name the file path and session, and give `receipt` or the path as the remedy; consider listing the rest with a CORRUPT row. | S |
| 6 | `nova-cairn open --store ./cairns --session s1 --publish manual` (twice) | The second open prints a fresh `stamp=` and no marker, the same line as a first open. Carried from 1.1.0. | Print `duplicate=true` with the stored open stamp on a re-open. | S |
| 7 | cmd/nova-cairn/main.go:104 | The banner's usage block has a line `nova-cairn NOTE: --publish is a recorded word, ...`, which reads as a verb named `NOTE:`. | Move the note under the usage block as prose, or into `open -h`. | S |
| 8 | `nova-cairn version -h` (every verb's `-h`) | Each `-h` prints the exit codes of open, append, index and receipt; version's lists four verbs' codes and none of its own. | Print the common line and the verb's own row only. | S |
| 9 | `nova-cairn open -h` | `--store <string>`, `--session <string>`, `--now <string>`: the placeholder says the Go type, not `dir`, `id` or `rfc3339-utc` as the usage line does. | Backtick the placeholder word in each flag's usage text. | S |
| 10 | cmd/nova-cairn/main.go:212 | `--now 2026-01-01T00:00:00+02:00` is accepted and stored as `2025-12-31T22:00:00Z`; the flag help and the refusal both say RFC 3339 UTC. | Refuse a non-Z offset, or say "any RFC 3339 offset, stored as UTC". | S |
| 11 | `nova-cairn append --store ./cairns --session s2 --entry e4` | Required-input refusals end `run: nova-cairn help`; an unknown flag ends `run: nova-cairn open -h`. The verb's `-h` is the better remedy and only one path uses it. | End every per-verb refusal with `run: nova-cairn <verb> -h`. | S |
| 12 | internal/cairn/cairn.go:453 | `open --store ./afile` where afile is a plain file refuses as "cannot read the session's open record from afile/log.jsonl: not a directory". | Check the store first: `--store "./afile" is not a directory`. | S |
| 13 | `nova-cairn open` / `nova-cairn append` conflicts | Two failure grammars: `OPEN FAILED: session ...` and `APPEND FAILED session=s1 entry=e1: ...`; refusals are `<VERB> REFUSED: ...`. | One shape: `<VERB> FAILED session=<s> [entry=<e>]: <why>; run: <remedy>` for both. | S |
| 14 | docs/SPEC-CAIRN.md:13-14 | "This slice is a bounded contribution, not a ratification. The differences are stated here first, before any approval of that draft": no draft is named, so a cold reader cannot tell what is being differed from. | Name the draft with a link, or cut the two sentences and keep the list of what the tool refuses. | S |
| 15 | docs/SPEC-CAIRN.md:90 | "fsync-durable ... independently of Redis" and line 129 "no ... Redis": the tool has no Redis, so the reader looks for one. | Say "before success is acknowledged, with no network involved" and drop Redis from both lines. | S |
| 16 | docs/SPEC-CAIRN.md:88 | One 544-character line in a section otherwise wrapped at 80; line 63 is 148. | Wrap both. | S |
| 17 | docs/CLI.md:2451 | The nova-cairn section has no First run block (the next `### First run` at docs/CLI.md:2524 is nova-decide's), and its examples use `./checkpoints` where the help uses `./cairns`. Carried from 1.1.0. | Add the block from the banner's four examples, with the same store name. | S |
| 18 | README.md:54 | "These are the Nova Tools 1.0.0 commands", pinned to the 1.0.0 release in a 1.2.0 candidate. Carried from 1.1.0. | Pin to the current release, or say in one line why it stays at 1.0.0. | S |

## Good, keep

The nested store's duplicate and conflict rules now hold under contention: twenty concurrent appends under one new id gave exactly one OK and nineteen conflicts, and the stored words are the OK's. `--dry-run` on both write verbs checks everything and writes nothing, saying `persisted=false dry_run=true`. One run names every problem at once, missing flag, bad ids, bad policy and bad stamp together. Every conflict and missing-record refusal names the next command whole and shell-ready: `receipt --text` for a conflict, `index --session` for a missing entry, the matching `open` for a re-open conflict. Every line splits `persisted=` from `published=false`.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| same-id concurrent appends can overwrite what the banner promises never is | FIXED | 20 concurrent `append --entry same` with different words: 1 exit 0, 19 exit 1; entries/s/same.json holds the OK's words |
| a re-open looks like a first open | STILL THERE | two `open --session s1 --publish manual` printed `stamp=...14.144248435Z` then `stamp=...14.414790627Z`, no marker |
| no --dry-run on the write verbs | FIXED | `open --dry-run` and `append --dry-run` print `dry_run=true`, no session file written |
| receipt text renders spaces as \x20 | FIXED | `receipt --text` printed `text="the words to keep"` |
| a conflict names no command | FIXED | the conflict ends `run: nova-cairn receipt --store ./cairns --session s1 --entry e1 --text` |
| the bench heading grammar is parsed twice | FIXED | internal/cairn/read_existing.go:62 reads through flatSections (internal/cairn/cairn.go:244) |
| a flat record gains no sidecars | REGRESSED | a flat append leaves `.day1.md.lock` in the store (internal/cairn/cairn.go:289, added in 3a33afaed) |
| the CLI section has no First run block | STILL THERE | docs/CLI.md:2451-2515 has none |
| README trial commands pinned to 1.0.0 | STILL THERE | README.md:54-59 |
