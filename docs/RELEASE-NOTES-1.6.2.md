The stable tools release beside nova-sprint v1.6.3: card staging now checks out a card head's git submodules, so a card whose repository carries private submodules (the Virtual Go sim and its fixed3d and fixed submodules) builds inside the wall.

Install: download the assets for your platform from this release, then run `nova-sprint adopt v1.6.3 --source <nova-tools checkout at v1.6.2> --sprint-release <the nova-sprint v1.6.3 release downloaded> --inventory <file> --reason <text>` (see docs/SPEC-RELEASE.md).

## What changed in v1.6.2

- Staging checks out a card head's submodules outside the wall: `submodule update --init --recursive` after the head and any rework carry are staged, from the bench mirror when it holds the submodule commit, else from the submodule URL with the bench credential; the checkout keeps the .gitmodules URLs, and a submodule that could not be checked out rides on STAGE OK as a note. (#5980)
- CI: test-hosted (macos-latest, 5) runs under its 2-minute cap (shards rebalanced, one build in the playbook test). (#5979)
