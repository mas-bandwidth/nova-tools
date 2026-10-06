# nova-fuse dogfood — codex, 2026-10-06

Read as a stranger: only `nova-fuse -h`, `nova-fuse help`, `nova-fuse help <verb>`
and the tool's page in `docs/CLI.md` (its verbs refuse `-h` by design, and the
banner says so). Built from the staged checkout at
`cb5fb8d4c3290f710d22a21a86aa8d229e4db905` and used as
`nova-fuse v1.0.1-0.20261006204015-cb5fb8d4c329 linux/amd64 go1.26.6`: the
`example:` first run in order in a scratch dir, every verb once with its real
flags, and the boundaries too (`--`, `--dry-run` on all four writing verbs,
`--max`, an absent box, a broken box, a symlink, a read-only dir, a 25-quarantine
box, and 20 parallel writes).

## Findings

1. Every writing verb silently widens the box file's mode to 0644. — URGENT

   ```
   $ chmod 600 perm600.json
   $ nova-fuse quarantine --box ./perm600.json s "r"
   QUARANTINE OK s since=2026-10-06T20:53:00Z: r (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)
   $ stat -c '%a %n' perm600.json
   644 perm600.json
   ```

   The same write turned a 0400 box into 0644 and a 0660 box into 0644, and
   `lockdown` did the same to a 0600 box. `init` creates 0644 and the left
   `<box>.lock` file is 0664.

   **Expected:** a write verb replaces the box's contents and keeps the mode the
   reader set on the file. A box is a safety record whose reasons can hold what
   was pasted (a token in a reason is the case the docs themselves use), so a
   write that quietly turns a private 0600 box world-readable is a wrong result
   with no line saying so. This is the one thing here worth fixing before
   v1.1.0.

   **Grade:** URGENT

2. Every writing verb leaves an undocumented `<box>.lock` file behind. — NEXT

   ```
   $ nova-fuse quarantine --box ./a.json s1 "r1"
   QUARANTINE OK s1 since=2026-10-06T20:52:32Z: r1 (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)
   $ stat -c '%a %s %n' a.json a.json.lock
   644 121 a.json
   664 0 a.json.lock
   ```

   `init` leaves none, `status`/`check`/`path` leave none, and `quarantine`,
   `lift quarantine` and `lockdown` leave one every time; it is not removed when
   the command ends. When the box's directory is not writable the failure names
   the internal thing and its path: `QUARANTINE FAILED s1: could not write box:
   filelock "rodir/b.json.lock": open rodir/b.json.lock: permission denied ...`.

   **Expected:** either no sidecar next to the box, or a line in `nova-fuse help`
   or `docs/CLI.md` that names it and says who removes it. "Nothing hidden" is
   the standard, and no verb can read this file back.

   **Grade:** NEXT

3. `--dry-run`'s "already set" line omits the promised `dry_run=true`. — NEXT

   ```
   $ nova-fuse lockdown --box ./box.json --dry-run "another"
   LOCKDOWN OK already=blown since=2026-10-06T20:53:18Z: for real (standing record kept; the new reason was not recorded: another)
   $ nova-fuse quarantine --box ./box.json --dry-run s1 "another"
   QUARANTINE OK s1 already=quarantined since=2026-10-06T20:53:18Z: for real quarantine (standing record kept; the new reason was not recorded: another)
   ```

   The same two commands without `--dry-run` print the identical lines.

   **Expected:** the banner says "`--dry-run` on init, lockdown, quarantine and
   lift quarantine makes every check the write would and writes nothing; its
   line says `dry_run=true`." The `would write` path does say it (`QUARANTINE
   OK fresh1 dry_run=true: ...`), but the already-set path does not, so a script
   that keys on the marker cannot tell a dry run from the real one.

   **Grade:** NEXT

4. The symlink refusal appends a false "unreadable box" reason. — NEXT

   ```
   $ ln -s real.json link.json
   $ nova-fuse status --box ./link.json
   nova-fuse status REFUSED: ./link.json is a symlink; use the real fuse box file path -- a box that cannot be read is treated as BLOWN, never as clear; repair the box by hand with the person you work with, live; run: nova-fuse help
   $ nova-fuse quarantine --box ./link.json s "r"
   nova-fuse quarantine REFUSED: ./link.json is a symlink; use the real fuse box file path -- refusing to narrow an unreadable box: while unreadable it already blocks EVERY surface, and a fresh box holding only this one quarantine would UNBLOCK the rest; blow lockdown instead (`lockdown --box ./link.json "<reason>"`), or repair the box by hand with the person you work with; run: nova-fuse help
   ```

   `real.json` there is a readable, clear box; nothing about it is unreadable or
   blown. `lockdown` on the symlink is worse: it prints `LOCKDOWN NOTE box was
   unreadable (./link.json is a symlink; use the real fuse box file path) ...;
   blowing lockdown anyway` and then `LOCKDOWN FAILED could not write box:
   atomicfile: target "link.json" is a symlink`.

   **Expected:** the refusal names its real cause — the tool refuses symlinks by
   policy — without asserting the box is unreadable or that every surface is
   already blocked. The first clause is right; the reason appended to it is not,
   and a reader who believes it thinks a clear box is blown.

   **Grade:** NEXT

5. A hand-edited quarantine keyed by the empty surface is unreachable state. — NEXT

   ```
   $ printf '{"lockdown":null,"quarantine":{"":{"at":"2026-01-01T00:00:00Z","reason":"r"}}}\n' > emptykey.json
   $ nova-fuse status --box ./emptykey.json
   STATUS OK lockdown=clear quarantines=1
   STATUS OK quarantine= since=2026-01-01T00:00:00Z: r
   $ nova-fuse check --box ./emptykey.json
   FUSE OK lockdown=clear (no surface named; no quarantine checked)
   ```

   `check --box ./emptykey.json ""` and `lift quarantine --box ./emptykey.json
   ""` both refuse with `surface must not be blank` / `needs exactly one
   surface`, and the entry survives a later `quarantine` of a real surface
   (`quarantines=2`, one of them blank). So the count and the blank row are the
   only traces, and no verb can check or clear it.

   **Expected:** a quarantine the tool counts is one `check` can gate on and
   `lift quarantine` can clear, or a box whose key is blank is refused whole as
   broken (treated as BLOWN). The docs make repair by hand a first-class path,
   so a hand-written box should not hold state the tool can only count.

   **Grade:** NEXT

## What the tool got right

- The `example:` first run is the whole sitting (create, look, ask, blow,
  watch, rescind), and it runs exactly as printed from an empty scratch dir; the
  box JSON on disk is the shape the banner documents.
- `--` works: `check --box ./box.json -- -h` reads `-h` as a surface and exits
  0, while `check --box ./box.json -h` refuses at exit 2, so exit 0 can never
  mean CLEAR to a hostile surface.
- The refusals name what they want and a paste-ready remedy: a missing `--box`,
  a missing reason, a second `--box`, a flag after a positional, an unknown
  flag with `did you mean --box?`, `--max` non-numeric, negative or missing.
- Hostile text is quoted correctly: a surface of `it's a surface` yields the
  remedy `... -- 'it'\''s a surface'`, which runs and lifts it; a `--box` path
  with a quote is quoted the same way.
- A broken, empty, non-box or unreadable box is refused, never read as CLEAR,
  and `lockdown` replaces it while keeping the old bytes at
  `<box>.unreadable`; when it cannot keep them it says so on the NOTE line.
- 20 parallel `quarantine` calls all landed (20 of 20 in the box), and
  `status --max` keeps the count uncapped (`quarantines=25`) while the `MORE`
  line reports `shown=20 total=25`.

READ 9/10 — the banner answers what it does, how it works in five lines and
where its state lives, `help <verb>` carries the flags, exit table and effect,
and the `docs/CLI.md` page explains the exit-0-means-CLEAR exception before the
reader meets it; the unkept `dry_run=true` promise and the hidden `.lock` file
keep it off a 10.

USE 8/10 — a stranger can run the whole first run and every refusal from an
empty scratch dir with no store, every writing verb has `--dry-run`, `--` makes
an untrusted surface safe, and the atomic write with a lock held 20 parallel
writers; the silent mode widening and the false symlink reason are the two
stumbles.

urgent=1 next=4
