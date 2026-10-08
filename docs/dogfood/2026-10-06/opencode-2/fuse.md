# nova-fuse dogfood — opencode-2, 2026-10-06

Built cold on a Linux bench (`<bench>`) from the staged checkout at `7acb90e18a764f0e728cd5ed701196a34405a824`, with `go build -o $JOB/bin/nova-fuse ./cmd/nova-fuse` in that checkout and never the installed binary; the binary printed `nova-fuse v1.0.1-0.20261007211151-7acb90e18a76 linux/amd64 go1.26.6`. Read as a stranger: only `nova-fuse -h`, `nova-fuse help`, `nova-fuse help <verb>` (every verb, both `lift` pages), the `-h` refusals, and the tool's page under `docs/` (`docs/CLI.md`, its `## nova-fuse` section with "First run" and "What the flags want"). Every verb ran for real with its real flags against scratch temp directories: `version`, `init`, `status`, `check`, `lockdown`, `quarantine`, `lift quarantine`, `lift lockdown`, `path` and `help`, plus `--dry-run` on all four writing verbs, `--max` 0/1/2/negative/non-numeric/missing, `--`, a missing and a doubled `--box`, a missing box, an unreadable box, a directory, a FIFO, a symlink, torn and hand-edited boxes, and the refusals; the surfaces and reasons were invented.

## Findings

1. `nova-fuse quarantine --box ./p600.json modecheck "mode"`
   Printed:
   ```
   QUARANTINE OK modecheck since=2026-10-07T21:19:02Z: mode (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)
   (one line printed)
   ```
   I expected the write to keep the mode the reader set on the box (it was `0600` before this run) and `init` to honor `umask`; instead `stat -c '%a %s %n' ./p600.json ./p600.json.lock` printed `644 130 ./p600.json` and `664 0 ./p600.json.lock`, and under `umask 077` the `init` above still made a `644` box, so a private safety record whose reason can hold a pasted token becomes world-readable on the first write.
   Grade: URGENT (a wrong result: a private box is silently widened on every write)

2. `nova-fuse lockdown --box ./adir "dir"`
   Printed:
   ```
   LOCKDOWN NOTE box was unreadable (./adir is not a regular file; use the real fuse box file path) and its bytes could NOT be preserved (./adir is not a regular file); blowing lockdown anyway
   LOCKDOWN FAILED could not write box: atomicfile: target "adir" is a directory (the write is temp-file + rename, so a failure cannot leave it torn; stop by hand and tell the person you work with now)
   ```
   I expected the paste-ready remedy that `nova-fuse quarantine --box ./adir s "r"` prints (blow lockdown instead: nova-fuse lockdown --box './adir' "<reason>") to run; it fails, and the symlink case is the same failure with `./link.json` and `atomicfile: target "link.json" is a symlink`, so a refusal for a path that is not a regular file sends the reader to a command that cannot work.
   Grade: URGENT (a refusal whose only tool remedy cannot run; only hand repair is left)

3. `nova-fuse quarantine --box ./dry.json --dry-run q1 "already q dry"`
   Printed:
   ```
   QUARANTINE OK q1 already=quarantined since=2026-10-07T21:19:02Z: reason one (standing record kept; the new reason was not recorded: already q dry)
   (one line printed)
   ```
   I expected the banner's promise that `--dry-run` "makes every check the write would and writes nothing; its line says `dry_run=true`"; the fresh-surface path keeps it (`nova-fuse quarantine --box ./dry.json --dry-run q2 "fresh q dry"` printed `QUARANTINE OK q2 dry_run=true: nothing written, the surface is not quarantined; a real run would record: fresh q dry`), but the already-set path prints the same line the real run prints, so a caller keying on `dry_run=true` cannot tell a preflight from a write.
   Grade: NEXT (friction: one dry-run path omits the token the banner names)

4. `nova-fuse quarantine --box ./box.json race1 "one"`
   Printed:
   ```
   QUARANTINE OK race1 since=2026-10-07T21:18:31Z: one (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)
   (one line printed)
   ```
   I expected one state file, because the banner says "the box is one JSON file you name with `--box`"; `ls -la` in that directory then showed `box.json` and a zero-byte `box.json.lock` at mode `0664`, and the lock stayed there after `lift quarantine`, though no verb reads, clears or documents it.
   Grade: NEXT (an undocumented sidecar rides along with every write)

5. `nova-fuse help status --max`
   Printed:
   ```
   nova-fuse help REFUSED: unknown verb "status --max"; the verbs are init, status, check, lockdown, quarantine, lift quarantine, lift lockdown, path, version; run: nova-fuse help
   (one line printed)
   ```
   I expected `help` to take at most one verb and to refuse a trailing flag or word as an extra argument (the tool has that refusal, `unexpected argument`); instead it folds the extra words into a verb name that never existed, sending a reader who mistyped a flag to look for `status --max`.
   Grade: NEXT (unclear help: a verb name is invented from the reader's typo)

6. `nova-fuse quarantine --box ./fresh2.json -s5 "leading dash"`
   Printed:
   ```
   nova-fuse quarantine REFUSED: unknown flag --s5; the flags of quarantine are --box, --dry-run; run: nova-fuse help quarantine
   (one line printed)
   ```
   I expected the refusal to quote the token as typed (`-s5`); it names `--s5`, a spelling I never used, so a reader who greps the message for their own input does not find it.
   Grade: NEXT (friction: the refusal requotes a single-dash token as a double-dash flag)

7. `nova-fuse init --box ./no-such-dir/box.json`
   Printed:
   ```
   INIT OK box=./no-such-dir/box.json: an empty box, no fuse blown (verified by re-reading the box)
   (one line printed)
   ```
   I expected a refusal, or a line saying `init` makes parents: `help init` says "Create an empty box only where nothing exists" and the tool refuses to guess elsewhere; this run made `./no-such-dir/` on the way, so a path typo becomes a clear box that reads as safety.
   Grade: NEXT (friction: an undocumented directory is created)

8. `nova-fuse status --box ./badat.json`
   Printed:
   ```
   STATUS OK lockdown=clear quarantines=1
   STATUS OK quarantine=s since=nope: bad time
   ```
   I expected a value that cannot be a time to be reported as unrecorded or called invalid, not echoed in the `since=` slot as the time it happened: the banner says `at` is RFC3339 UTC and `status` answers "what is blown, and since when", and a missing `at` prints `since=unrecorded`.
   Grade: NEXT (a report repeats a value it has not checked)

## What held

Every verb ran at least once against a scratch temp directory and none could not be run. The verbs with no finding against them are `version`, `check`, `path`, `lift quarantine` and `lift lockdown`; `quarantine` (findings 1, 3, 4 and 6), `lockdown` (finding 2), `help` (finding 5), `init` (finding 7) and `status` (finding 8) each carry a finding above, so this section claims for them only what their findings record and no blanket claim that every verb matched its help is made. The six-line first run printed in the banner ran exactly as shown, in order, including the exit 1 on the gate; the `-h`-after-a-verb refusal is documented and fires at exit 2; `--` makes an untrusted surface safe (`check --box ./box.json -- -h` reads a surface and exits 0); a missing, unreadable, torn, directory, FIFO or symlink box is refused at exit 2 and never read as clear; `lockdown` on a missing box makes a blown box in one write and preserves quarantines that still stand; `lift lockdown` refuses forever before flags or files; every write says `verified by re-reading the box`; twenty concurrent `quarantine` runs all landed; and the count line stayed uncapped while `--max` only trimmed the rows under it.

READ 8/10 — the banner answers what the tool is, how it works, where its state lives and where its two exceptions (`-h`, no `--json`) are met, and every `help <verb>` gives usage, flags, exit codes and effect; it is held off a 10 because the banner's `dry_run=true` promise is not kept on the already-set path and its one-line refusal shape is not kept for a missing `--box`.

USE 7/10 — the whole first run and every refusal ran from an empty scratch directory with no store, the gate and `--` behaved, and all safety asymmetries held; it is held off a 10 by the mode widening on every write and the quarantine refusal that names a `lockdown` remedy the tool then refuses to run.

urgent=2 next=6
