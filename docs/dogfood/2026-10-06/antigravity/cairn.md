# nova-cairn dogfood — Johnny Grok, 2026-10-06

Reviewer: Johnny Grok. Read as a stranger: `nova-cairn -h`, `nova-cairn help`, `nova-cairn <verb> -h`, and the page under `docs/` (`docs/SPEC-CAIRN.md` and the nova-cairn section of `docs/CLI.md`). Used the installed binary `nova-cairn v1.2.0-dev.d165b531 linux/amd64 go1.27.1` against scratch stores under the job directory. No server was started, and nothing was pointed at Redis, `127.0.0.1:6380`, `6381`, `6390`, or `100.76.29.55`.

## Findings

1. A flat append's success line reports a publish policy the record does not keep.
   Command: `nova-cairn append --store ./flat --session hand --publish manual --entry beat-3 --text "with a policy"`
   Printed:
   ```
   APPEND OK session=hand entry=beat-3 source=- persisted=true published=false publish=manual duplicate=false stamp=2026-10-06T20:33:26Z
   ```
   The hand file is one markdown file. The section it added is `## 2026-10-06T20:33:26Z — beat-3` and the words `with a policy`, with no policy stored. `nova-cairn receipt --store ./flat --session hand --entry beat-3 --text` then printed `publish=unknown`. `nova-cairn open --store ./flat2 --session solo --publish never --source pointer-ignored` left that file byte-identical and still printed `publish=never` (and, honestly, `source=-`). I expected the success line to say `publish=unknown`, which is what the page says a flat record prints because it stores no policy.
   Grade: URGENT.

2. The banner says every verb takes `--json`, and `help` does not.
   Command: `nova-cairn help --json`
   Printed:
   ```
   {"result":{"verb":"","status":"refused","exit":2,"remedy":"nova-cairn help","why":["unknown verb \"--json\"; the verbs are open, append, index, receipt, version"]},"facts":{}}
   ```
   I expected the help text as one JSON object, the way `nova-cairn version --json` is the version string. `help` is a verb in the usage list. The flag is read as a verb name.
   Grade: URGENT.

3. An append to a flat record leaves an empty lock file beside it.
   Command: `nova-cairn append --store ./prose --session note --entry beat-9 --text "added"`
   Printed:
   ```
   APPEND OK session=note entry=beat-9 source=- persisted=true published=false publish=unknown duplicate=false stamp=2026-10-06T20:36:39Z
   ```
   I expected nothing beside the markdown, as the page says: no `entries/`, no `log.jsonl`, no index. Those three stayed absent, and the prose above the new section was kept, but `prose/.note.md.lock` was left behind, empty, size 0. The same empty `.<session>.md.lock` remained after the hand-file appends and after the documented bench-file append. `open` alone did not create one. `receipt` and `index`, after I removed the lock, did not put it back.
   Grade: NEXT.

4. A matching re-open prints a new stamp and does not write it.
   Command: `nova-cairn open --store ./cairns --session s1 --publish manual`
   Printed (this was the second open; the first had stored `2026-10-06T20:33:24.850812149Z`):
   ```
   OPEN OK session=s1 store=./cairns source=- publish=manual stamp=2026-10-06T20:33:25.039303899Z
   ```
   I expected a no-op whose stamp was the one already on the record. `sessions/s1.md` and `log.jsonl` still have the original stamp; the new one is only on the OK line, so a matching re-open looks like a fresh open.
   Grade: NEXT.

5. The receipt line in the CLI shell block is not a command.
   Command: `nova-cairn receipt --store ./checkpoints --session session-1 --entry note-1 [--text]`
   Printed:
   ```
   RECEIPT REFUSED: takes no positional arguments, got "[--text]" (flags come before arguments); run: nova-cairn help
   ```
   I expected the fenced example in `docs/CLI.md` to run. The same command with `--text` instead of `[--text]` printed the stored words. The brackets are optional-flag notation inside a pasteable block.
   Grade: NEXT.

6. An append with no policy tells you to open the session as `manual`.
   Command: `nova-cairn append --store ./cairns --session nosuch --entry e1 --text hi`
   Printed:
   ```
   APPEND REFUSED: no such session "nosuch" under store "./cairns"; open first: nova-cairn open --store ./cairns --session nosuch --publish manual; run: nova-cairn help
   ```
   I expected the remedy to quote the store and the session and not choose a policy I never named. The same refusal with `--publish never` on the append did use `never`, and with `--publish immediate` did use `immediate`. Only the form that names no policy invents `manual`.
   Grade: NEXT.

7. The usage block offers `NOTE:` as a command.
   Command: `nova-cairn NOTE:`
   Printed:
   ```
   CAIRN REFUSED: unknown verb "NOTE:"; the verbs are open, append, index, receipt, version; run: nova-cairn help
   ```
   I expected that sentence to sit under the `open` usage, not as its own `nova-cairn NOTE: ...` line between `open` and `append`. The refusal itself names the real verbs.
   Grade: NEXT.

What did hold: missing flags are named together and nothing is guessed; a bad id is refused and not written; `open --dry-run` creates no directory; a dry-run append of a new entry is not there for `receipt`; a retry is `duplicate=true` and a different body is exit 1 naming `receipt --text`; a re-open that changes policy or source is exit 1 and names the matching `open`; `--source` is stored and not opened; on a nested session an entry's own `--publish` is what `receipt` reads back; `--file` and `--file -` keep the bytes, including newlines, spaces, and quotes; an empty store indexes as `sessions=0 entries=0`; `--max 0` lists all 21 and the default cap prints one `MORE` line whose total matches `INDEX OK`; `seal`, `consume`, `delete`, `grade`, `consolidate`, `wake`, `rollup`, and `retention` are unknown verbs; ordinary prose in a flat file is not turned into entries; when both shapes exist the nested record wins and the flat twin is not rewritten. The documented bench-file append exited 0 and added `## 2026-10-06T20:36:39Z — beat-1405`.

READ 7/10 — The verb help and the page agree with the binary on the four verbs, both store shapes, the exits, dry-run, and the id rules, but the banner's claim that every verb takes `--json` and the fenced receipt line with `[--text]` do not.

USE 6/10 — Nested open, append, retry, conflict, file, stdin, index, and receipt kept the bytes and named a next command on refusal, but a flat append reports a publish policy the receipt reads back as unknown and leaves an empty lock file beside the hand record.

urgent=2 next=5
