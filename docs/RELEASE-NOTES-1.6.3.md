The stable tools release beside nova-sprint v1.6.3: the macOS card wall no longer hangs a harness's cold start on macOS 27.0.1, and a flaky nova-config signal test is made deterministic.

Install: download the assets for your platform from this release, then run `nova-sprint adopt v1.6.3 --source <nova-tools checkout at v1.6.3> --sprint-release <the nova-sprint v1.6.3 release downloaded> --inventory <file> --reason <text>` (see docs/SPEC-RELEASE.md).

## What changed in v1.6.3

- The macOS wall's default rule is `(deny default (with no-report))`: the same denials, without the violation report that hung most walled cold starts of opencode (Bun) on macOS 27.0.1. `profiles/embed_test.go` refuses any other default rule. (#5987)
- nova-config's signal helper waits for its own SIGTERM to land, so `TestLoopCommandsOutliveTheSkeletonsInterruptCancellation` and `TestLoopRunsRealRunnerReportsASignalDeathAsItsStatus` no longer read exit 0 about one run in twenty. (#5988)
