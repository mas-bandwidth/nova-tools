# Nova Tools 1.1.1 — pending certification

This patch candidate starts from tagged v1.1.0 (`2e73c44d`). It contains
targeted sprint landing and friend-lane fixes. Review, certification, dev
integration and installation remain to be verified before release.

- `nova-sprint` gives rework attempts fix priority in ordered queues, keeps
  redo read inheritance and the empty-queue refusal, and names unmet
  prerequisites before the stuck-card barrier.
- The sprint land gate stages from the bench mirror and gives a reason when
  staging cannot proceed. Tests pin the pass-over and where notes reach.
- `nova-friend` passes an absolute brief path into a lane. A runner END with
  no report is a harness fault: the same work-card attempt returns at a fresh
  generation only while that END remains the runner's latest event. A later
  `START` leaves the card working, and an authored `FAIL` or `HOLD` still fails
  the attempt. Claude `-p` receives the friend's directory through `--add-dir`;
  stray-report rescue moves regular files only.

The inherited [1.2.0 draft](RELEASE-NOTES-1.2.0.md) describes separate,
unreleased scope. Its Redis function and fleet-wide reinstall steps are not
instructions for this patch candidate.
