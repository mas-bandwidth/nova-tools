# nova-cairn READ and USE rating, nova-tools 1.2.0

Rater: deepseek-v4.1-flash (opencode), a sprint worker on a friend's re-rate card
Build: 96b7601e9689
READ: 7.5/10
USE: 7.5/10

## Reasons

READ. `nova-cairn help`, every verb's `-h`, `help <verb>` and docs/SPEC-CAIRN.md
were read cold, then the banner's four examples were run in a throwaway directory.
The banner's first line is the README row's sentence, and the four lines after it
carry the whole contract: a store is a directory you name, plain files synced
before OK; the same id with the same words is `duplicate=true` at exit 0, other
words a conflict at exit 1; `--publish` is recorded and nothing is sent. Every
verb answers `-h` with its usage, an example, flags marked `(required)` or given a
default, and an `effect:` line that is true: open and append write, index and
receipt only read. The spec is short, says plainly what the tool refuses to be,
and pins each promise to a named case.

The first confusion is the usage block itself: a line beginning `nova-cairn NOTE:`
sits among the usage lines and reads as a verb called `NOTE`, though `nova-cairn NOTE` is refused as unknown. The first doubt is the flat store, the shape the spec
is proudest of: its entry grammar `## <rfc3339> — <entry>` is written in the spec
and not on `append -h`, and docs/SPEC-CAIRN.md:53 promises that nothing appears
beside the file while an append leaves `.<session>.md.lock` there. The first
boredom is the flag list: `--store <string>`, `--session <string>` and `--now <string>` where the usage line says `<dir>`, `<id>` and `<rfc3339-utc>`, and every
verb's `-h` repeats all four verbs' exit table, so `version -h` explains append's
conflict. Smaller costs: docs/CLI.md:2567 still has no `### First run`;
README.md:55 still calls these the 1.0.0 commands; the unknown-verb refusal lists
five verbs and omits `help`; `index -h` gives `--max` no default where the banner
and spec say 20.

USE. The help's first run ran as printed, exit 0 on all four lines, and a second
real job ran end to end in the same scratch directory: open with `--source`,
append from `--file` and from stdin, multi-line non-ASCII words returned byte for
byte by `receipt --text`, a repeat append `duplicate=true` with the original
stamp, other words under the same id refused at exit 1 naming the `receipt --text`
that reads the holder, and a re-open naming another policy refused at exit 1
naming the matching `open`. A bad id, a bad policy, a bad `--now` and missing
flags were named together in one run; `--dry-run` wrote nothing; `--max 2` cut
with `INDEX MORE ... total=5` and kept the total; `--json` gave one object on ok,
failed and refused alike. The nested store is solid, and the 1.1.0 concurrency
race is gone: the append publishes with `atomicfile.NoReplace()`, so two racing
appends of one id leave one winner and one conflict.

What holds USE at 7.5 is the flat path and one false fact. A matching re-open
prints `OPEN OK` with the current clock as `stamp=` (or the `--now` given), not
the stamp the session was opened with, so a retry cannot tell a new record from
one that already stood. A flat append with `--source x.md --publish never` prints
`source=-` and `publish=never` although the format stores neither, and the receipt
for the same entry says `publish=unknown`: the OK line reports what was not kept.
A flat append leaves `.<session>.md.lock` beside the record. With
`sessions/s.md` removed and `entries/s/e.json` kept, `index` says `sessions=0 entries=1` and lists the entry while `receipt` refuses "no such session". One
corrupt entry file makes `index` refuse whole, naming the entry id and not its
path, with `run: nova-cairn help`. `--json` after an unknown flag or a stray
positional is lost, so that refusal prints as prose. The missing-session remedy
picks `--publish manual` for a caller who gave none, and under `--json` its
`remedy` is `nova-cairn help` while the runnable `open` sits inside `why`.

A 10 needs: the NOTE line out of the usage block and `help` in the verb list; one
placeholder vocabulary from the usage to the flag list, and each verb's own exit
row on its `-h`; the flat heading on `append -h` and the lock file named in the
spec and the command reference; a flat append to report `publish=unknown source=-`
or to refuse the flags it cannot store; a re-open marked `existing=true` with the
recorded stamp; `--json` honoured whatever comes before it; the `open` command in
the `remedy` field with no policy chosen for the caller; index and receipt to
agree on an orphan entry, and a corrupt file to name its path; `--now` to refuse a
non-Z offset or say it converts; and `### First run` plus the current release in
the README.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-cairn/main.go:271 | a re-open that changes nothing prints `OPEN OK` with the call's clock (or the `--now` given) as `stamp=`, not the `Opened:` stamp in the record, and no field says the record already stood: `open --now 2020-01-01T00:00:00Z` on an open session printed `stamp=2020-01-01T00:00:00Z` while sessions/s1.md says `Opened: 2026-10-07T02:00:11.18421952Z` | read the open event's stamp from log.jsonl and print it with `existing=true` on a matching re-open, as append prints `duplicate=true` | S |
| 2 | internal/cairn/cairn.go:213 | `append` to a session nothing holds names `open first: nova-cairn open --store ./cairns --session nosuch --publish manual`: the tool chose a policy the caller never gave, in a tool that says it refuses to guess | leave `--publish <never\|manual\|deferred\|immediate>` as the placeholder, or carry the append's own `--publish` when it was given | S |
| 3 | internal/cairn/cairn.go:289 | a flat append leaves `.<session>.md.lock` beside the record; docs/SPEC-CAIRN.md:53 and docs/CLI.md:2619 say nothing appears beside the file (no `entries/`, no `log.jsonl`, no index) | say in both docs that one hidden `.<session>.md.lock` is left beside a flat record, or lock the record file itself | S |
| 4 | internal/cairn/cairn.go:638 | `append --store ./flatsrc --session day1 --entry n1 --text one --source x.md --publish never` on a flat record prints `source=-` and `publish=never`, though the flat format stores neither and `receipt` for the same entry says `publish=unknown`: the `--source` was dropped without a word and the OK line reports a policy nothing kept | refuse `--source` and `--publish` on a flat record, naming that the format stores neither, or report `source=- publish=unknown` on the OK line | S |
| 5 | cmd/nova-cairn/main.go:104 | the usage block carries a line `nova-cairn NOTE: --publish is a recorded word ...`, which reads as a verb; `nova-cairn NOTE` is refused as an unknown verb | move the note under the usage lines without the `nova-cairn ` prefix, or into `open -h` | S |
| 6 | `nova-cairn open --store ./cairns --session s1 --publish manual --bogus 1 --json` | the unknown flag stops parsing, so the `--json` after it is never seen and the refusal prints as a prose line on stderr, not as the one JSON object every verb promises; a stray positional has the same effect | scan the whole argument list for `--json` before parsing and render every refusal in the mode asked for | S |
| 7 | internal/cairn/read_existing.go:19 | `index --store ./nosuch` refuses with Go's `cannot read store "./nosuch": stat ./nosuch: no such file or directory` and `run: nova-cairn help`, which does not tell the reader the `open` that makes a store | say `no store at ./nosuch` and name `nova-cairn open --store ./nosuch --session <id> --publish <policy>` as the remedy | S |
| 8 | internal/cairn/cairn.go:863 | with `sessions/s.md` removed and `entries/s/e.json` kept, `index` answers `INDEX OK sessions=0 entries=1` and lists the entry, while `receipt --session s --entry e` refuses "no such session": the two read verbs disagree on what a session is | count a session from its entries or its log open record as well as its file, or have index refuse the orphan entry naming it | S |
| 9 | internal/cairn/cairn.go:770 | one corrupt `entries/s/bad.json` makes `index` refuse whole: `stored entry "bad" is corrupt ...; run: nova-cairn help`, naming neither the session nor the path, and the remedy is the whole help | name the file path and session, give the path or `receipt` as the remedy, and list the rest with a CORRUPT row | S |
| 10 | cmd/nova-cairn/main.go:212 | `--now 2026-01-01T00:00:00+02:00` is accepted and stored as `2025-12-31T22:00:00Z`, while the flag text and the refusal both say "RFC 3339 UTC" | refuse a non-`Z` offset, or say "any RFC 3339 offset, converted to UTC" in the flag text | S |
| 11 | `nova-cairn open -h` | the flag list prints `--store <string>`, `--session <string>`, `--now <string>` where the usage line directly above says `<dir>`, `<id>` and `<rfc3339-utc>` | use the usage line's placeholders in the flag list | S |
| 12 | cmd/nova-cairn/main.go:92 | every verb's `-h` quotes the exit table of all four verbs; `version -h` lists append's conflict code and none of version's own | print the shared `0 done, 2 usage` line plus the verb's own row | S |
| 13 | `nova-cairn index -h` | `--max <int>` carries no default, while the banner and docs/SPEC-CAIRN.md:107 say default 20 | print `(default 20)` on `--max` | S |
| 14 | `nova-cairn append -h` | the heading a flat record must carry to be read as an entry (`## <rfc3339> — <entry>`) is not on the verb's help; a hand-written `## e1` section is silently not an entry, and `receipt --entry e1` says "no such entry" with no word about the heading it did not read | name the flat heading form on `append -h`, and have index count the `##` lines it did not read as entries | S |
| 15 | cmd/nova-cairn/main.go:250 | under `--json`, the no-such-session refusal carries `remedy: "nova-cairn help"` while the runnable `open` command is inside the `why` string, so a program cannot act on it without parsing prose | set the `NotFoundError`'s Remedy to the `open` command `noRecord` already builds | S |
| 16 | `nova-cairn open --store ./afile --session s --publish never` | where `./afile` is a plain file, the refusal is `cannot read the session's open record from afile/log.jsonl: not a directory`, naming an internal path and not the wrong input | check the store first and refuse `--store "./afile" is not a directory` | S |
| 17 | `nova-cairn bogus` | the unknown-verb refusal lists `open, append, index, receipt, version` and omits `help`, which the usage block lists as a verb | include `help` in the verb list | S |
| 18 | docs/CLI.md:2567 | the nova-cairn section opens with prose and a bare command block and has no `### First run`, which onboarding point 3 asks for; the runnable transcript already stands in docs/TESTS.md | add the `### First run` subsection with the banner's four examples, using the same store name the help uses | S |
| 19 | README.md:55 | the trial section says "These are the Nova Tools 1.0.0 commands" and installs `@v1.0.0`, at a 1.2.0 tree | name the version the tree ships | S |

## Good, keep

- Refusals name every problem in one run and end with a command that runs: the
  whole `open` for an unopened session, and the `receipt --text` that reads the
  holder for a conflict.
- Exact-text durability: `duplicate=true` at exit 0 with the original stamp, a
  conflict at exit 1, multi-line non-ASCII words returned byte for byte; one id
  under concurrent appends now always leaves one winner and one conflict.
- One result value, two renderings: `--json` carries `result`, `why` and `remedy`
  on ok, failed and refused alike; `--dry-run` writes nothing and says
  `persisted=false dry_run=true`.
- The id rule refuses `..`, `CON` and whitespace before anything is written, with
  the whole rule quoted in the line.
- The nested store is plain files with no hidden state, and `index` finds an
  opened session that has no entries as `entries=0`.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| same-id concurrent appends can overwrite what the banner promises never is (READ 1.1.0 #1) | FIXED | `internal/cairn/cairn.go:694` publishes with `atomicfile.NoReplace()`; two racing appends of one id leave one winner and one conflict |
| no `### First run` in the nova-cairn command reference (READ 1.1.0 #2) | STILL THERE | docs/CLI.md:2567 opens with prose and a bare command block |
| the two store shapes share one file (READ 1.1.0 #3) | STILL THERE | internal/cairn/cairn.go is 915 lines and holds both writers |
| the flat writer trims trailing newlines unstated (READ 1.1.0 #4) | CHANGED | docs/SPEC-CAIRN.md:56 now says the flat body is whitespace-trimmed; `append -h` still does not |
| `index` walks and parses the store twice (READ 1.1.0 #6) | FIXED | cmd/nova-cairn/main.go:341 makes one `cairn.Index` call |
| the README names 1.0.0 at a later head (READ 1.1.0 #8) | STILL THERE | README.md:55 |
| `index` cannot list sessions (USE 1.1.0) | FIXED | `index --store ./cairns` prints an `INDEX SESSION` row per session, an empty one as `entries=0` |
| a re-open looks like a first open (USE 1.1.0) | STILL THERE | cmd/nova-cairn/main.go:271; a matching re-open prints a fresh `stamp=` and no marker |
| `--json` after a stray positional is lost (USE 1.1.0 #2) | STILL THERE | `open ... --bogus 1 --json` prints the refusal as prose |
| a NOTE line in the usage reads like a verb (USE 1.1.0 #3) | STILL THERE | cmd/nova-cairn/main.go:104 |
| the flat entry grammar is shown nowhere (USE 1.1.0 #1) | STILL THERE | `append -h` names the flat record and not its heading; a hand `## e1` section is not an entry |
| a flat record gains no sidecars (USE 1.1.0) | REGRESSED | internal/cairn/cairn.go:289 leaves `.<session>.md.lock` beside the record |
