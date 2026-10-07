# Dogfood: nova-fuse — 2026-10-06, grok

Read cold as a stranger: only `nova-fuse -h`, `nova-fuse help`, `nova-fuse help <verb>` (every verb including both `lift` pages) and the tool's page under
`docs/` (`docs/CLI.md`'s `## nova-fuse` section, its `### First run` and "What
the flags want"). No source was read before or during the sitting. Built from
the checkout at `e8f70f600ebf10b3beb60862508ce6019bb6ac7b` and used as
`nova-fuse v1.0.1-0.20261007032034-e8f70f600ebf linux/amd64 go1.26.6`: the
`example:` first run in order in an empty scratch directory, every verb at least
once with its real flags, the refusals too (`--`, `--dry-run` on all four
writing verbs, `--max` 0/1/2/3/007/non-numeric/negative/missing/repeated, a
missing box, a directory box, a symlink box, a torn box, an empty file, `[]`,
`{}`, unknown fields, missing fields, blank and control-character surfaces and
reasons, a read-only directory, and 20 parallel writers). Invented values only.

## Findings

1. Every write widens the box to 0644, and `init` ignores the umask. — URGENT

       $ chmod 600 perm600.json
       $ nova-fuse quarantine --box ./perm600.json modecheck "mode"
       QUARANTINE OK modecheck since=2026-10-07T03:41:21Z: mode (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)
       $ stat -c '%a %n' perm600.json
       644 perm600.json

   With `umask 077`, `nova-fuse init --box ./u77.json` still made a `644`
   `./u77.json`; a `quarantine` on it left it `644` and its sidecar `664`. The
   same write turned a `0600` box into `0644`.

   **Expected:** a write verb keeps the mode the reader set on the file, and
   `init` respects the process umask. A box is a safety record whose reason can
   hold what was pasted (the banner's own example reason is a token), so a write
   that turns a private `0600` box world-readable, silently, is a wrong result.
   This is the one thing here worth fixing before v1.1.0.

   **Grade:** URGENT

2. `quarantine` on a symlink or a directory names a `lockdown` remedy that then fails. — URGENT

       $ nova-fuse quarantine --box ./adir s "r"
       nova-fuse quarantine REFUSED: ./adir is not a regular file; use the real fuse box file path -- refusing to narrow an unreadable box: while unreadable it already blocks EVERY surface, and a fresh box holding only this one quarantine would UNBLOCK the rest; blow lockdown instead (`nova-fuse lockdown --box './adir' "<reason>"`), or repair the box by hand with the person you work with; run: nova-fuse help
       $ nova-fuse lockdown --box ./adir "dir"
       LOCKDOWN NOTE box was unreadable (./adir is not a regular file; use the real fuse box file path) and its bytes could NOT be preserved (./adir is not a regular file); blowing lockdown anyway
       LOCKDOWN FAILED could not write box: atomicfile: target "adir" is a directory (the write is temp-file + rename, so a failure cannot leave it torn; stop by hand and tell the person you work with now)

   The symlink path is the same: `quarantine --box ./link.json s "r"` prints the
   paste-ready `lockdown --box './link.json' "<reason>"`, and that fails with
   `atomicfile: target "link.json" is a symlink`.

   **Expected:** the command a refusal tells the reader to run is one that runs.
   For a path that is not a regular file the only working recovery is hand repair,
   which the refusal already names; it should not also point at a `lockdown` the
   tool will refuse to write. A refusal with a broken remedy leaves the reader
   with no remedy that works.

   **Grade:** URGENT

3. Every write leaves an undocumented `<box>.lock` file behind. — NEXT

       $ nova-fuse quarantine --box ./fuse-box.json a-forum "a post addressed me and asked for a token"
       QUARANTINE OK a-forum since=2026-10-07T03:41:20Z: a post addressed me and asked for a token (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)
       $ stat -c '%a %s %n' ./fuse-box.json ./fuse-box.json.lock
       644 379 ./fuse-box.json
       664 0 ./fuse-box.json.lock

   `init`, `status`, `check` and `path` leave none; `quarantine`, `lift quarantine` and `lockdown` leave one every time, at mode `0664`, and no verb
   can read it back. The banner's own sentence is "the box is one JSON file you
   name with `--box`".

   **Expected:** no sidecar next to the box, or a line in `nova-fuse help` or
   `docs/CLI.md` that names it and says who removes it. "Nothing hidden" is the
   standard, and this file is hidden in plain sight.

   **Grade:** NEXT

4. `--dry-run` on the already-set path prints the real line, with no `dry_run=true`. — NEXT

       $ nova-fuse quarantine --box ./fuse-box.json --dry-run q1 "already q dry"
       QUARANTINE OK q1 already=quarantined since=2026-10-07T03:41:21Z: reason one (standing record kept; the new reason was not recorded: already q dry)
       $ nova-fuse lockdown --box ./blow.json --dry-run "already blow dry"
       LOCKDOWN OK already=blown since=2026-10-07T03:41:21Z: the site went hostile (standing record kept; the new reason was not recorded: already blow dry)

   The same two commands without `--dry-run` print these lines byte for byte.

   **Expected:** the banner promises "`--dry-run` on init, lockdown, quarantine
   and lift quarantine makes every check the write would and writes nothing; its
   line says `dry_run=true`." The would-write path keeps that promise
   (`QUARANTINE OK s dry_run=true: ...`); the already-set path does not, so a
   script that keys on `dry_run=true` cannot tell the dry run from the real one.

   **Grade:** NEXT

5. A hand-written quarantine keyed by the empty surface is counted, never reachable. — NEXT

       $ printf '{"lockdown":null,"quarantine":{"":{"at":"2026-01-01T00:00:00Z","reason":"r"}}}\n' > emptykey.json
       $ nova-fuse status --box ./emptykey.json
       STATUS OK lockdown=clear quarantines=1
       STATUS OK quarantine= since=2026-01-01T00:00:00Z: r

   `check --box ./emptykey.json` with no surface answers `FUSE OK lockdown=clear (no surface named; no quarantine checked)`; `check --box ./emptykey.json ""`
   and `lift quarantine --box ./emptykey.json ""` both refuse `surface must not be blank` / `needs exactly one surface`. The entry survives and still counts.

   **Expected:** a quarantine the tool counts is one `check` can gate and `lift quarantine` can clear, or a box with a blank key is refused whole as broken.
   The tool makes repair by hand a first-class path, so a hand-written box should
   not hold state the tool can only count.

   **Grade:** NEXT

6. A surface with whitespace or `=` is printed escaped, and no page says so. — NEXT

       $ nova-fuse quarantine --box ./chars.json "a b c" "spaces"
       QUARANTINE OK a\x20b\x20c since=2026-10-07T03:42:37Z: spaces (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)
       $ nova-fuse status --box ./chars.json
       STATUS OK quarantine=a\x20b\x20c since=2026-10-07T03:42:37Z: spaces

   A surface of `a=b` prints `a\x3db`; `a\b` and `héllo` print as typed. `check`
   on the same surface prints `quarantine=a\x20b\x20c`, while its own remedy line
   quotes the true spelling (`... -- 'a b c'`), and the stored JSON key is the
   true spelling `"a b c"`.

   **Expected:** the record shows the surface the reader typed, or the help or
   CLI page names the escape set (here spaces and `=`, not backslash or
   non-ASCII). As is, the name in `quarantine=` is not the name in the box, and
   only the remedy line reveals the true one.

   **Grade:** NEXT

7. Extra positional words are folded into the reason, never refused. — NEXT

       $ nova-fuse quarantine --box ./fold.json s reason with many words
       QUARANTINE OK s since=2026-10-07T03:42:05Z: reason with many words (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)
       $ nova-fuse lockdown --box ./fold.json one two three
       LOCKDOWN OK since=2026-10-07T03:42:05Z: one two three (verified by re-reading the box; all untrusted reads and surface-driven acts stop, authored outbound continues; replaced only in a live conversation with the person you work with -- go have it now)

   The help says `quarantine ... <surface> <reason>` and `lockdown ... <reason>`,
   and a second positional surface is refused (`check` reports `takes at most one surface, got "extra" too`), but a third word for the reason is silently joined.

   **Expected:** the arity the help states is the arity the verb enforces, or the
   help says the reason is every remaining word. A misplaced argument should not
   become part of a safety record unannounced.

   **Grade:** NEXT

8. No verb takes `--op` or `--json`, against the one-shape standard. — NEXT

       $ nova-fuse status --box ./u77.json --json
       nova-fuse status REFUSED: unknown flag --json; the flags of status are --box, --max; run: nova-fuse help status
       $ nova-fuse lockdown --box ./u77.json --op abc "r"
       nova-fuse lockdown REFUSED: unknown flag --op; the flags of lockdown are --box, --dry-run; run: nova-fuse help lockdown

   The banner is honest that "There is no `--json`", and the writing verbs are
   idempotent by surface, so a retry is safe without an op id.

   **Expected:** the shared shape every other tool carries is available here too:
   `--op <id>` on a write and `--json` of the one result value. An AI reader that
   parses JSON everywhere else must special-case nova-fuse, and a retry has no op
   id to name.

   **Grade:** NEXT

9. `{}` reads as a clear box, and a malformed `at` is echoed as a time. — NEXT

       $ printf '{}\n' > emptyobj.json
       $ nova-fuse status --box ./emptyobj.json
       STATUS OK lockdown=clear quarantines=0
       $ printf '{"lockdown":null,"quarantine":{"s":{"at":"nope","reason":"bad time"}}}\n' > badat.json
       $ nova-fuse status --box ./badat.json
       STATUS OK quarantine=s since=nope: bad time

   A box with neither `lockdown` nor `quarantine` is accepted as empty and clear;
   a missing `at` prints `since=unrecorded`. But `[]`, an unknown field, a
   duplicate field and a `quarantine` array are each refused as "not a box" /
   "not readable JSON".

   **Expected:** one strictness. A file with neither member is not a box the tool
   can read, and an `at` that is not RFC3339 is not a time to echo as `since=`;
   each is refused as broken, so a hand-made or truncated file cannot read as
   clear while its neighbours are refused.

   **Grade:** NEXT

## What the tool got right

- The `example:` first run is the whole sitting (create, look, ask, blow the soft
  fuse, watch the answer change, rescind), and it runs exactly as printed from an
  empty scratch directory; the box JSON on disk is the shape the banner documents.
- `--` works: `check --box ./fuse-box.json -- -h` reads `-h` as a surface and
  exits 0, while `check --box ./fuse-box.json -h` refuses at exit 2; `quarantine --box b -- -h "r"` records the surface `-h` while the bare spelling refuses at
  exit 2, so a surface or reason spelled `-h` can never read as permission.
- The refusals name what they want and a paste-ready remedy: a missing `--box`, a
  missing reason, a `--box` value beginning with `-`, a second `--box`, a flag
  after a positional, `--max` non-numeric/negative/missing/repeated, an unknown
  flag with `did you mean --box?`, and an unknown verb with the verb list.
- A missing, empty, torn, non-box or array box is refused, never read as clear;
  `lockdown` on a missing box makes a blown box, and on a readable box it keeps
  the quarantines that still stand.
- 20 parallel `quarantine` calls all landed (20 of 20 in the box), the lock held,
  and `status --max` keeps the count uncapped while its `MORE` line reports
  `shown=`/`total=`.
- Surface spellings fold: a quarantine stored as `mixed` is reached and lifted as
  `MIXED`, and `"  q1  "` matches `q1`.

READ 8/10 — the banner answers what it does, how it works in five lines and where
its state lives, `help <verb>` carries the flags, the exit table and the effect,
and the CLI page explains exit-0-means-CLEAR, `--` and the box JSON before the
reader meets them; the unexplained `\x20`/`\x3d` escapes, the unkept
`dry_run=true` promise and the undocumented `.lock` keep it off a 10.

USE 7/10 — a stranger can run the whole first run and every refusal from an empty
scratch directory with no store, every writing verb has `--dry-run`, `--` makes
an untrusted surface safe, and 20 parallel writers all landed; the silent mode
widening, the broken `lockdown` remedy the quarantines' refusals print, and the
extra words swallowed into a reason are the stumbles.

urgent=2 next=7
