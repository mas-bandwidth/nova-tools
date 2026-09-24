# Card exemplars

These pull requests are the worked examples for the ten kinds accepted by `nova-pulse cut
--kind`. Six use the v2 renderer and four still use their legacy renderer. A card writer links the
matching row in the task body as `Example to follow:`. The link is guidance about method and
evidence; the card's pinned source, PATHS, test, and DONE-WHEN remain the authority for the new
job.

The v2-rendered kinds are `recut`, `fix`, `port`, `docs-guard`, `report`, and `read`;
`replay`, `spec`, `rebase`, and `guard` retain their legacy renderer.

“10” below is never an editorial claim made by this page. Each row links an exact-head review
that recorded score 10, and each selected head is present on `dev` as of 2026-09-24. The reasons
state what is worth copying from the PR rather than treating a score as a substitute for reading
the change.

| kind | exemplar | why this is the example | score-10 evidence |
| --- | --- | --- | --- |
| `read` | [#3483](https://github.com/mas-bandwidth/nova-tools/pull/3483) | The review trail first names two source defects, then re-reads only their repairs at a new pinned head and records CLEAR after focused controls and exact-head CI pass. Copy the head pin, numbered findings, repair verification, and separation of source judgment from gates. | [exact-head CLEAR at `a4197c01`](https://github.com/mas-bandwidth/nova-tools/pull/3483#issuecomment-5815787228) |
| `fix` | [#3061](https://github.com/mas-bandwidth/nova-tools/pull/3061) | The repair follows failures through several concrete HOLDs, adds post-command disconnect controls that failed before the fix, and keeps the reserved batch safe for every ambiguous SSH diagnostic. Copy the red-first boundary cases and the exact receipt, not the patch size. | [exact-head review at `978db16a`](https://github.com/mas-bandwidth/nova-tools/pull/3061#issuecomment-5796287827) |
| `replay` | [#2792](https://github.com/mas-bandwidth/nova-tools/pull/2792) | The criterion replay is registered in the real suite and exercises the default plus missing and invalid modes, repository scope, authors, and authority. Copy the named behavior replay and the cases that distinguish the accepted path from nearby refusals. | [exact-head review at `d3c6e373`](https://github.com/mas-bandwidth/nova-tools/pull/2792#issuecomment-5799590439) |
| `spec` | [#3162](https://github.com/mas-bandwidth/nova-tools/pull/3162) | The spec turns projection identity into explicit file and digest invariants, then names a falsifiable replay for a C-only change, full rebuild parity, and manifest mismatch refusal. Copy the decision boundary and controls that make later code review mechanical. | [exact-head review at `f360ec8a`](https://github.com/mas-bandwidth/nova-tools/pull/3162#issuecomment-5797711903) |
| `rebase` | [#2794](https://github.com/mas-bandwidth/nova-tools/pull/2794) | The stacked-rebase verb has focused source and CLI tests, a clean dev base, and a fully green exact-head matrix. Copy its explicit source/base handling and its proof that replaying the stack preserves the intended changes. | [exact-head review at `863350d7`](https://github.com/mas-bandwidth/nova-tools/pull/2794#issuecomment-5788618986) |
| `guard` | [#2543](https://github.com/mas-bandwidth/nova-tools/pull/2543) | This test-only guard checks the production harness writer rather than a fixture that merely agrees with itself; its negative controls prove literal secrets and malformed environment references bite. Copy the production seam and the mutation evidence. | [exact-head review at `2dbb36cb`](https://github.com/mas-bandwidth/nova-tools/pull/2543#issuecomment-5785511036) |
| `recut` | [#2918](https://github.com/mas-bandwidth/nova-tools/pull/2918) | The recut is narrow—one behavior and its biting regression—and its final head has a clean base, full green CI, and no carried scope. Copy the way it turns the prior failed attempt into a smaller two-file repair with an observable control. | [exact-head review at `7e17aff6`](https://github.com/mas-bandwidth/nova-tools/pull/2918#issuecomment-5788890831) |
| `port` | [#2751](https://github.com/mas-bandwidth/nova-tools/pull/2751) | The final test drives the Go client through a real Unix-socket session, checks one request, compares the server reply byte for byte, and proves the dry-run control omits the write. This is the target-versus-reference comparison a port card needs. The PR was closed into a stream; its reviewed final head is an ancestor of `dev`. | [exact-head review at `c5afd3c8`](https://github.com/mas-bandwidth/nova-tools/pull/2751#issuecomment-5805627713) |
| `docs-guard` | [#3140](https://github.com/mas-bandwidth/nova-tools/pull/3140) | The change adds executable comparators for four `docs/CLI.md` examples without editing the documentation to make the checks pass. Copy its direct document-to-command comparison and its failure-sensitive fixtures. | [exact-head review at `8dad57b9`](https://github.com/mas-bandwidth/nova-tools/pull/3140#issuecomment-5796825968) |
| `report` | [#3337](https://github.com/mas-bandwidth/nova-tools/pull/3337) | The table reads the live Redis snapshot and renders it to stdout, with tests rejecting the retired file-output flags and proving repeated reads leave the working directory empty. Copy the measured-input, bounded-output, no-artifact discipline. | [exact-head review at `7f861ca9`](https://github.com/mas-bandwidth/nova-tools/pull/3337#issuecomment-5801935102) |
