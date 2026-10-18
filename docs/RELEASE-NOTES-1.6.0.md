The stable tools release beside nova-sprint v1.6.3: the v1.5.3 sandbox caps hotfix, the idle-seat churn cuts, and a hook push the moment a friend comes up.

Install: download the assets for your platform from this release, then run `nova-sprint adopt v1.6.3 --source <nova-tools checkout at v1.6.0> --sprint-release <the nova-sprint v1.6.3 release downloaded> --inventory <file> --reason <text>` (see docs/SPEC-RELEASE.md).

## What changed in v1.6.0

- The hook server pushes a friend coming up at the pass that finds the friend armed: `FRIEND UP <name> <live|proven> <held|unheld> width=<n> ready=<n> working=<n> at=<time>`, the sprint row from `friend cards --json` (nova-sprint v1.6.3; an older server gives `row=unread`). FRIEND BACK still waits for the proof; a change between two down states and the coordinator's own state are never pushed. (#5971, #5954)
- The sandbox wall's caps follow the machine: 64 processes per logical CPU, an eighth of the memory. (#5944, #5950)
- Idle members read their queue every 15 s; the idle seat's background churn stops; push.git is tidied hourly; the fleet never reinstalls a friend daemon. (#5951, #5952, #5953)
- CI: test-hosted (macos-latest, 5) never started at the cut (hosted runners over the spending limit); its steps ran green by hand on a self-hosted darwin-arm64 bench, and the waiver is in the CHANGELOG section. Security read of the 12 sensitive paths: https://github.com/mas-bandwidth/nova-tools/pull/5958#issuecomment-6252920239
