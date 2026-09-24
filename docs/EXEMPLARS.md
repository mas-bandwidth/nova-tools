# Card exemplars

These pull requests are the worked examples for the six kinds the v2 card renderer currently
implements in `internal/pulse.CardV2Kinds`. A card writer links the matching row in the task body
as `Example to follow:`. The link is guidance about method and evidence; the card's pinned source,
PATHS, test, and DONE-WHEN remain the authority for the new job.

“10” below is never an editorial claim made by this page. Each row links an exact-head review
that recorded score 10, and each selected head is present on `dev` as of 2026-09-24. The reasons
state what is worth copying from the PR rather than treating a score as a substitute for reading
the change.

| kind | exemplar | why this is the example | score-10 evidence |
| --- | --- | --- | --- |
| `recut` | [#2918](https://github.com/mas-bandwidth/nova-tools/pull/2918) | The recut is narrow—one behavior and its biting regression—and its final head has a clean base, full green CI, and no carried scope. Copy the way it turns the prior failed attempt into a smaller two-file repair with an observable control. | [exact-head review at `7e17aff6`](https://github.com/mas-bandwidth/nova-tools/pull/2918#issuecomment-5788890831) |
| `fix` | [#3061](https://github.com/mas-bandwidth/nova-tools/pull/3061) | The repair follows failures through several concrete HOLDs, adds post-command disconnect controls that failed before the fix, and keeps the reserved batch safe for every ambiguous SSH diagnostic. Copy the red-first boundary cases and the exact receipt, not the patch size. | [exact-head review at `978db16a`](https://github.com/mas-bandwidth/nova-tools/pull/3061#issuecomment-5796287827) |
| `port` | [#2751](https://github.com/mas-bandwidth/nova-tools/pull/2751) | The final test drives the Go client through a real Unix-socket session, checks one request, compares the server reply byte for byte, and proves the dry-run control omits the write. This is the target-versus-reference comparison a port card needs. The PR was closed into a stream; its reviewed final head is an ancestor of `dev`. | [exact-head review at `c5afd3c8`](https://github.com/mas-bandwidth/nova-tools/pull/2751#issuecomment-5805627713) |
| `docs-guard` | [#3140](https://github.com/mas-bandwidth/nova-tools/pull/3140) | The change adds executable comparators for four `docs/CLI.md` examples without editing the documentation to make the checks pass. Copy its direct document-to-command comparison and its failure-sensitive fixtures. | [exact-head review at `8dad57b9`](https://github.com/mas-bandwidth/nova-tools/pull/3140#issuecomment-5796825968) |
| `report` | [#3337](https://github.com/mas-bandwidth/nova-tools/pull/3337) | The table reads the live Redis snapshot and renders it to stdout, with tests rejecting the retired file-output flags and proving repeated reads leave the working directory empty. Copy the measured-input, bounded-output, no-artifact discipline. | [exact-head review at `7f861ca9`](https://github.com/mas-bandwidth/nova-tools/pull/3337#issuecomment-5801935102) |
| `read` | [#3483](https://github.com/mas-bandwidth/nova-tools/pull/3483) | The review trail first names two source defects, then re-reads only their repairs at a new pinned head and records CLEAR after focused controls and exact-head CI pass. Copy the head pin, numbered findings, repair verification, and separation of source judgment from gates. | [exact-head CLEAR at `a4197c01`](https://github.com/mas-bandwidth/nova-tools/pull/3483#issuecomment-5815787228) |

The legacy-only cutter names `replay`, `spec`, `rebase`, and `guard` are outside this catalog for
now: `cut --kind` accepts them, but `RenderCardV2` does not yet render those kinds. Add a row when
the v2 renderer adds the kind and a landed exact-head score-10 example exists; do not borrow an
adjacent kind's PR to make the table look complete.
