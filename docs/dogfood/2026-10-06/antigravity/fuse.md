# nova-fuse dogfood — antigravity (zhi), 2026-10-06

Read as a stranger: only `nova-fuse -h`, `nova-fuse help`, `nova-fuse help
<verb>` and the page under `docs/` (its verbs refuse `-h` by design, and the
banner says so). Built from the staged checkout at
c02c00769c229a6e012b0dff03ef8929016f3b23 and used as
`nova-fuse v1.0.1-0.20261006191814-c02c00769c22 darwin/arm64 go1.27.1`: the
`example:`/first-run lines run in order in a scratch dir, every verb once, the
refusals and the boundaries (`-h`, no box, an existing box, a re-blow, a
re-quarantine, `--dry-run`) too.

## Findings

1. `nova-fuse status --box ./box.json` prints its rows as fresh result lines
   rather than continuations:
   ```
   STATUS OK lockdown=blown since=2026-10-06T19:38:13Z quarantines=1: all untrusted reads stop
   STATUS OK quarantine=q1 since=2026-10-06T19:38:26Z: one
   ```
   I expected the second and later rows to open with `MORE` or `NOTE` (the one
   line grammar in docs/STANDARD.md section 2), so a reader can tell a summary
   from a row; as printed, each row reads as its own verdict.
   Grade: NEXT.

2. The per-verb exit table in `nova-fuse -h` is incomplete for a refusal the
   tool itself makes: `nova-fuse init --box ./box.json` on an existing box
   prints
   ```
   INIT FAILED box=./box.json: something is already there, and init never replaces a box ...; read it with nova-fuse status --box './box.json'
   ```
   at exit 1, but the table says init's exit 1 is "the write was attempted and
   re-reading the box did not show it" (no write is attempted here), and the
   `already=blown`/`already=quarantined` successes at exit 0 (a repeat
   `lockdown` or `quarantine`) are not named either. A reader scripting on the
   table gets a meaning the tool does not keep.
   Grade: NEXT.

## What the tool got right

- The `-h` refusal is documented in the banner and behaves exactly so
  (`nova-fuse check -h` -> exit 2, `run: nova-fuse help check`), with the reason
  (exit 0 is CLEAR).
- Its first run needs nothing: `init`, `status`, `check`, `quarantine`,
  `lift quarantine`, `lockdown` all ran from an empty scratch dir.
- A missing or unreadable box is refused at exit 2 with a remedy naming `init`;
  it is never read as clear.
- `init` never replaces a box, `lockdown` is never lifted by the tool, and both
  say so with the next step a person takes.
- Every write has `--dry-run` (`dry_run=true`, nothing written), and
  `lockdown`/`quarantine` on an already-set fuse keep the standing record and
  say `already=`.

READ 9/10 — the banner answers what it does, how it works in five lines and
where its state lives, and it states the two exceptions (no `-h`, no `--json`)
where the reader will meet them; the continuation grammar is what keeps it off a
10.

USE 9/10 — a stranger can run the whole first run from an empty scratch dir
with no store and see every fuse act, and the refusals name the exact next
command; only the exit table's silence on the already-set and existing-box cases
is a stumble.

urgent=0 next=2
