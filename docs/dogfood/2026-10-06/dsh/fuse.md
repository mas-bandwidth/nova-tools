# nova-fuse dogfood — dsh (zhi), 2026-10-06

Read as a stranger: only `nova-fuse -h`, `nova-fuse help`, `nova-fuse help <verb>` (each verb's page, `lift quarantine` and `lift lockdown` included) and
the tool's pages under `docs/` (CLI.md's nova-fuse section, USAGE.md's, and
SPEC.md's nova-fuse section). No source was read before or during the sitting.
Built from the staged checkout at
6ec8bb02edc83283630f2e10e21bd035b3336f65 and used as
`nova-fuse v0.0.0-20261006195723-6ec8bb02edc8 linux/amd64 go1.26.6` (built and
run on the Linux bench; no go runs on the machine the author sat at). The
sitting ran from about
4:23 PM ET to about 4:38 PM ET, one scratch directory, every verb used for
real with its real flags: version, init (real, `--dry-run`, on an existing
box, into new dirs, on a directory), status (`--max` 1/2/0/20/22/23, `--max`
negative, non-numeric, missing, `--max=n`), check (no surface, bare `--`, a
surface, under lockdown, folded spellings, missing box, torn box, directory
box, blank surface, two surfaces, dash surface with and without `--`),
lockdown (`--dry-run`, real, re-blow, on a missing box, on a torn box, into a
read-only directory), quarantine (all of the above plus control-character
reasons and surfaces), lift quarantine (real, `--dry-run`, nothing to lift,
under lockdown, two stored spellings removed at once), lift lockdown (the
forever refusal, with and without flags), path (echo, grammar-bait value,
extra positional), help (banner, every verb, `help help`), plus the refusals:
missing `--box` on every verb, no box at the path, unreadable and wrong-shaped
boxes, blank surface, blank and missing reason, duplicate `--box` and
`--max`, `--box` value starting with `-`, unknown flag, flags after
positionals, unknown verb, `-h` after a verb, stray arguments.

## Findings

1. `nova-fuse check --box ./fuse-box.json hot-surface` (a blown lockdown)
   ```
   FUSE FAILED lockdown since=2026-10-06T20:23:35Z: everything untrusted turned hostile at once (hard: all untrusted reads and surface-driven acts stop, authored outbound continues; replaced only in a live conversation with the person you work with)
   EXIT=1
   ```
   I expected the line SPEC.md's normative grammar block promises: `FUSE FAIL lockdown since=<t>: <reason> (…)` — and likewise `INIT FAIL`, `LIFT FAIL`,
   `QUARANTINE FAIL`, `LOCKDOWN FAIL` — but the tool prints `FUSE FAILED`,
   `INIT FAILED`, `LIFT FAILED`, `QUARANTINE FAILED`, `LOCKDOWN FAILED`
   everywhere I could reach, which is what docs/CLI.md's transcripts show. The
   section's own rule says that when the code and the document disagree one of
   them has a bug; here the tool, CLI.md and the exit tables all agree, and
   SPEC.md's grammar block is the one out of step.
   Grade: NEXT.

2. `nova-fuse quarantine` (no flags at all)
   ```
   nova-fuse quarantine REFUSED: --box is required; refusing to guess; run: nova-fuse help
     --box <path> is the JSON file your fuses live in, named on every verb: there is no default path and no environment variable, because a fuse box the tool went looking for is one an attacker can put somewhere. A path with no box at it is refused, never read as CLEAR: nova-fuse init --box <path> makes an empty one, once.
   nova-fuse quarantine REFUSED: needs a surface and a reason: `quarantine --box <path> <surface> "<reason>"`; run: nova-fuse help
   ```
   I expected one line: docs/CLI.md says "A refusal is one line, `nova-fuse[ <verb>] REFUSED: <why>; run: nova-fuse help[ <verb>]`". The indented
   `--box` explainer is genuinely helpful, but it is a second, untyped line,
   and on the verbs with more required arguments (lockdown, quarantine, lift
   quarantine) the run prints three lines with two REFUSED tokens; a caller
   parsing one-line refusals sees shapes the docs do not admit.
   Grade: NEXT.

3. `nova-fuse quarantine --box ./d9/b.json some-surface "why"` (in a scratch
   dir where `./d9` did not exist and no box was there)
   ```
   nova-fuse quarantine REFUSED: no box at ./d9/b.json -- refusing to make a box holding only this quarantine: with no box every surface is refused, and that box would clear the rest; make the box first (nova-fuse init --box './d9/b.json'), or blow lockdown; run: nova-fuse help
   EXIT=2
   ```
   then `ls -la d9` shows the directory `d9/` now exists and holds
   `b.json.lock` (0 bytes) — created by a run that refused and wrote no box. I
   expected a refused run to touch nothing: the fuse semantics are right (the
   box is not made, `check` still refuses on that path, treating it as BLOWN),
   but an unwitnessed mkdir and a lock file from a run that said no is a
   side effect no page names.
   Grade: NEXT.

4. `nova-fuse quarantine --box ./f4box.json lf "why"` (after `init` made the
   box)
   ```
   QUARANTINE OK lf since=2026-10-06T20:30:41Z: why (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)
   EXIT=0
   ```
   then `ls f4box.json*` shows `f4box.json` and `f4box.json.lock` (mode 664,
   zero bytes) — and the `.lock` is still there after the run, as it is beside
   every box this sitting wrote to. I expected one state file: the docs say
   "one binary, one state file", name `<box>.unreadable` as the one
   companion, and never mention `<box>.lock`. It is harmless in use — a
   stale `.lock` does not block later writes — but it is an undocumented
   sidecar that rides along in backups and syncs of the box's directory.
   Grade: NEXT.

5. `nova-fuse help help`, then `nova-fuse frobnicate --box ./it1.json`
   ```
   nova-fuse help REFUSED: unknown verb "help"; the verbs are init, status, check, lockdown, quarantine, lift quarantine, lift lockdown, path, version; run: nova-fuse help
   nova-fuse REFUSED: unknown verb "frobnicate"; the verbs are init, status, check, lockdown, quarantine, lift, path, version, help; run: nova-fuse help
   ```
   I expected one answer to "what can I type". The first list omits `help`
   (and `help help` is refused as an unknown verb); the second includes `help`
   but spells `lift` as one word where the first spells out `lift quarantine`
   and `lift lockdown`. Two refusals one typo apart give two different
   vocabularies; a stranger cannot tell which one is the truth.
   Grade: NEXT.

6. `nova-fuse status --box ./big.json` (a box holding 22 quarantines, default
   `--max`)
   ```
   STATUS OK lockdown=clear quarantines=22
   STATUS OK quarantine=surface-01 since=2026-10-06T20:28:10Z: reason 01
   STATUS OK quarantine=surface-02 since=2026-10-06T20:28:10Z: reason 02
   ```
   I expected the rows under the count line to open with a continuation token
   (the `STATUS MORE` line at the end shows the tool has one); as printed,
   every row re-uses the `STATUS OK` verdict token, so each quarantine line is
   grammatically its own all-clear verdict, and a reader skimming or a grep
   for `^STATUS OK` cannot tell a summary from a row. CLI.md's transcript
   documents exactly this shape, so I record it as friction, not surprise.
   Grade: NEXT.

7. `nova-fuse quarantine --box ./verify.json ctrl-feed "the second reason"`
   (`ctrl-feed` already quarantined with "the first reason")
   ```
   QUARANTINE OK ctrl-feed already=quarantined since=2026-10-06T20:30:24Z: the first reason (standing record kept; the new reason was not recorded: the second reason)
   EXIT=0
   ```
   I expected the exit table in `help quarantine` to cover this outcome: it
   says exit 0 is "quarantined and verified", but this run quarantined
   nothing — the standing record kept the old reason and the new one is only
   echoed inside the parenthetical. `lockdown` behaves the same way with
   `already=blown` at exit 0. A scripter keying on the exit table counts a
   no-op as a write. (init's table, by contrast, does name its
   already-there outcome, at exit 1.)
   Grade: NEXT.

8. `nova-fuse init --box .` (a directory where the box would go)
   ```
   INIT FAILED box=.: something is already there, and init never replaces a box (a blown lockdown is replaced only in a live conversation with the person you work with); read it with nova-fuse status --box '.'
   EXIT=1
   ```
   but the remedy it hands me cannot run: `nova-fuse status --box .` refuses
   with "`. is not a regular file`". I expected a next step that works for the
   case at hand; the refusal itself is right and the box is safe, but the
   remedy line assumes "something is already there" means "a box", and for this
   input it points at a dead end.
   Grade: NEXT.

## What the tool got right

- The first-run example in the banner ran exactly as printed, in order, and
  each of its six lines behaved as documented, including the exit 1 on the
  gate.
- The safety asymmetries all held: `lockdown` proceeds on a torn box and
  preserves the bytes at `<box>.unreadable`; `quarantine` refuses rather than
  narrow an unreadable box; no box and unreadable box both read as BLOWN,
  never CLEAR; `lift lockdown` refuses forever, before flags and files.
- The escape guarantees held under attack: a surface named
  `LOCKDOWN=clear quarantines=0` prints as
  `lockdown\x3dclear\x20quarantines\x3d0`, a grammar-bait box path prints
  escaped in fields and POSIX-quoted in remedies, and `path` echoes its value
  bare exactly as documented.
- Widened matching worked in both directions: `check DISCORD` failed quoting
  the stored `Discord`, and `lift quarantine discord` removed both stored
  spellings, announcing each.
- Every refusal named a door back in (`run: nova-fuse help ...`), every write
  said `verified by re-reading the box`, and dry-runs said
  `dry_run=true: nothing written`.
- The remedies run verbatim: the parenthetical `nova-fuse lift quarantine --box './remedy.json' -- 'hostile-forum'` lifted the surface when pasted
  back as printed, the `--max 0` advice listed every quarantine, and the
  no-box refusal's `init` command made the box.
- The write is as atomic as claimed: 100 writes against a box read
  continuously (78 status reads, 12 raw JSON parses) produced zero torn
  reads, and 100 quarantines at once against one box all landed.
- The exit codes matched the documented table everywhere I could reach,
  including the boundaries: init 0/1, check 0/1/2, lift quarantine 0/1,
  lift lockdown 2, path 0, unknown verb 2, `-h` after a verb 2.

READ 8/10 — the banner is a model (first-run example, exit codes up front, the
`-h` refusal explained before I could hit it, the box JSON shape), but the
per-verb exit tables miss the `already=` outcomes, two refusals give two
different verb lists, and SPEC.md's grammar tokens do not match the printed
lines.
USE 9/10 — every verb did what its help promised across the sitting, refusals
included remedies, and every safety behavior matched the docs; only the
undocumented `.lock` sidecar and the mkdir on a refused write surprised me.

urgent=0 next=8
