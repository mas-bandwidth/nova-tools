# nova-fuse dogfood — codex, 2026-10-07

Read cold from `nova-fuse help`, `nova-fuse help <verb>` and the nova-fuse
section of `docs/CLI.md`. Built from `23864704a9765e11c6c57784ffefbfb0b72bf54f`
as `nova-fuse v0.0.0-20261008004609-23864704a976 linux/amd64 go1.26.6`.
Ran with the built binary on `PATH`. Used every verb with its real flags in a
scratch directory, including the first-run sequence, dry runs, `--`, and the documented refusals. The commands
below are the observed defects; no code was changed in this card.

## Findings

1. A write widens a private box to world-readable.

   **Command:**

   ```text
   $ chmod 600 mode.json
   $ nova-fuse quarantine --box mode.json private "sensitive reason"
   $ stat -c '%a %n' mode.json mode.json.lock
   ```

   **Output (first 3 lines):**

   ```text
   QUARANTINE OK private since=2026-10-08T00:59:46Z: sensitive reason (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)
   644 mode.json
   664 mode.json.lock
   ```

   **Expected:** a write should preserve the mode set by the owner. Reasons
   can contain sensitive context, so silently changing `0600` to `0644` exposes
   the record to every local user.

   **Grade:** URGENT

2. A write leaves an undocumented `.lock` sidecar beside the box.

   **Command:**

   ```text
   $ nova-fuse quarantine --box mode.json private "sensitive reason"
   $ stat -c '%a %n' mode.json mode.json.lock
   ```

   **Output (first 3 lines):**

   ```text
   QUARANTINE OK private since=2026-10-08T00:59:46Z: sensitive reason (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)
   644 mode.json
   664 mode.json.lock
   ```

   **Expected:** either avoid leaving the sidecar or document its name and
   ownership. The help and CLI page describe the JSON box as the written state
   and do not mention this extra file.

   **Grade:** NEXT

3. Dry runs on an already-set fuse omit the promised `dry_run=true` marker.

   **Command:**

   ```text
   $ nova-fuse quarantine --box mode.json --dry-run private "changed reason"
   $ nova-fuse lockdown --box mode.json --dry-run "another reason"
   ```

   **Output (first 3 lines):**

   ```text
   QUARANTINE OK private already=quarantined since=2026-10-08T00:59:46Z: sensitive reason (standing record kept; the new reason was not recorded: changed reason)
   LOCKDOWN OK already=blown since=2026-10-08T00:59:46Z: set lockdown (standing record kept; the new reason was not recorded: another reason)
   ```

   **Expected:** the banner says every dry run prints `dry_run=true`; these
   already-set branches print the same result shape without it, so a caller
   cannot identify them as dry runs from the response.

   **Grade:** NEXT

4. A symlink refusal incorrectly describes the target box as unreadable and
   blown.

   **Command:**

   ```text
   $ nova-fuse status --box link.json
   ```

   **Output (first 3 lines):**

   ```text
   nova-fuse status REFUSED: link.json is a symlink; use the real fuse box file path -- a box that cannot be read is treated as BLOWN, never as clear; repair the box by hand with the person you work with, live; run: nova-fuse help
   ```

   **Expected:** name the symlink policy refusal without claiming the target
   is unreadable or blown. The tested target was a readable, clear box.

   **Grade:** NEXT

5. A hand-edited quarantine for the empty surface is counted but cannot be
   checked or lifted.

   **Command:**

   ```text
   $ printf '{"lockdown":null,"quarantine":{"":{"at":"2026-01-01T00:00:00Z","reason":"empty surface"}}}\n' > blank.json
   $ nova-fuse status --box blank.json
   $ nova-fuse check --box blank.json ""
   $ nova-fuse lift quarantine --box blank.json ""
   ```

   **Output (first 3 lines):**

   ```text
   STATUS OK lockdown=clear quarantines=1
   STATUS OK quarantine= since=2026-01-01T00:00:00Z: empty surface
   nova-fuse check REFUSED: surface must not be blank; omit it to check lockdown only; run: nova-fuse help
   ```

   **Expected:** reject a box with an unreachable quarantine as invalid, or
   allow that counted quarantine to be checked and lifted. The status count
   says it exists, but neither check nor lift accepts its key.

   **Grade:** NEXT

READ 8/10 — the banner and per-verb help explain the state, flags, refusal paths
and exit codes; the false symlink explanation and the missing sidecar detail
are the main gaps.

USE 7/10 — the first-run sequence and fail-closed checks are clear, but the
private-mode widening and unmarked already-set dry runs make safe automation
harder to trust.

urgent=1 next=4
