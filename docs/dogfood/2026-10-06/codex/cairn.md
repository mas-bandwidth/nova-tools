# nova-cairn dogfood — Codex, 2026-10-07

Built `cmd/nova-cairn` from the staged branch on the Linux bench:
`nova-cairn v0.0.0-20261007161243-728877d133ab linux/amd64 go1.26.6`.
I read the binary's `-h`, `help`, each `help <verb>`, and `docs/SPEC-CAIRN.md` as a stranger. Every verb ran against scratch stores under the job directory; all stored words and source pointers were synthetic. No private records or live stores were used.

1. Command: `nova-cairn append --store ./store --session s1 --entry e2 --file ./input/words.txt --source scratch://file --publish deferred --now 2026-10-07T16:03:00Z`

   First three printed lines:

   ```text
   APPEND FAILED session=s1 entry=e2: session "s1" holds publish=manual; --publish deferred names another; run: nova-cairn append --store ./store --session s1 --entry e2 --publish manual
   ```

   Exit 1.

   Expected: `docs/SPEC-CAIRN.md` says `append --publish` records the entry's own policy and a value different from the session policy is recorded as given, not refused. The staged binary's help and result instead describe a conflict and tell me to reuse the session policy, so the page and executable disagree about the flag's effect. Grade: URGENT.

2. Command: `nova-cairn append --store ./store --session s1 --entry e6 --file ./input/missing.txt`

   First three printed lines:

   ```text
   APPEND REFUSED: open ./input/missing.txt: no such file or directory; run: nova-cairn help
   ```

   Exit 2.

   Expected: the refusal should say that `--file` needs a readable file and give the next step for correcting the path. It exposes the raw open error and sends the reader back to general help. Grade: NEXT.

3. Command: `nova-cairn open --store ./regular-file --session s1 --publish manual`

   First three printed lines:

   ```text
   OPEN REFUSED: cannot read the session's open record from regular-file/log.jsonl: not a directory; run: nova-cairn help
   ```

   Exit 2. `./regular-file` is a scratch file, not a directory.

   Expected: `--store <dir>` should be named as the invalid input and the refusal should say it needs a directory. `index --store ./regular-file` does name that condition directly, while `open` exposes the internal `log.jsonl` path and gives only general help. Grade: NEXT.

4. Command: `nova-cairn append --store ./flat --session flat --entry later --text "added scratch section" --now 2026-10-07T15:30:00Z`

   First three printed lines:

   ```text
   APPEND OK session=flat entry=later source=- persisted=true published=false publish=unknown duplicate=false stamp=2026-10-07T15:30:00Z
   ```

   Exit 0. `./flat/flat.md` was a scratch flat record. After the append,
   `find ./flat -maxdepth 2 -type f -print | sort` printed:

   ```text
   ./flat/.flat.md.lock
   ./flat/flat.md
   ```

   Expected: the spec says a flat record remains the whole store and that no sidecar appears beside it. The successful append leaves a persistent hidden lock file in the user's record directory. Grade: URGENT.

READ 8/10 — The help lists the verbs, flags, effects and exits; the spec gives a clear store model, but its `--publish` rule disagrees with the staged executable.

USE 7/10 — The scratch flows for open, append, index and receipt, including duplicate, conflict, dry-run, JSON and both file and stdin input, worked; the policy mismatch and two generic store/file refusals cost time.

Coverage: `open`, `append`, `index`, `receipt`, `version`, and help for every verb; matching and conflicting opens; `--text`, `--file`, `--file -`, `--source`, `--publish`, `--now`, `--dry-run`, `--json`, `--max 1` and `--max 0`; nested and flat records; unknown verb and flag, missing store and entry, invalid id, invalid clock and policy, empty note, both text inputs, and missing file. The documented four-command example ran in a scratch directory. Hands-on use included the staged Linux binary and scratch-only records.

urgent=2 next=2
