RESULT: s17mr-1059 sha=UNKNOWNABLE

**CANNOT READ**

The card asserts a `repo/` directory exists in the working tree with `git rev-parse HEAD` printing `7c29513b...`. My reading finds no such directory: the working tree (`/Users/glenn/rowan-working/tmp/c9ca9930-2422-3649-05b4-bf5aa935543d-card-s17mr-1059/jobs/card-s17mr-1059`) contains only `.git/` (an empty repository with zero commits), `.lease`, `opencode.json`, and `.nova-sandbox-tmp`. The bundle `/tmp/schema14-ftf.bundle` is inaccessible (permission denied). There is no `repo/`, no `docs/`, no `test/` — nothing to diff against. Every step of the review (read the base tree, quote the law, check each hunk against the base, check `newWireRoot` signatures, check make targets) is blocked by the absence of the tree the card says is present.