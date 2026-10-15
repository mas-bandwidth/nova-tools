# Dead-code evidence: pkg/testkit, pkg/testguard, pkg/onboarding, pkg/pathcase

Card tools-pkg-dead-code-testkit-onboarding-pathcase. Base: nova-tools ac5173d38
(branch sprint/tools-pkg-dead-code-testkit-onboarding-pathcase.w1.g1.e21).

## Measure

`go tool deadcode -test ./cmd/...` (golang.org/x/tools v0.50.0) over every main of
nova-tools, under GOOS linux, darwin and windows. Every function below is reported
unreachable from the mains on all three operating systems (condition "unreachable from
every main" holds; the deadcode output is in `evidence/deadcode-{linux,darwin,windows}.txt`).

A nova-sprint clone at v1.3/comms was not available (no network in the sandbox), and
`gh search code` could not be run for the same reason, so the importers below were
found by grepping the nova-tools tree itself. That is enough to decide every function:
each is imported by tests or CI inside the mas-bandwidth org, so the "imported nowhere
in either org" condition fails and each stays.

## Table

| function | deadcode result | live in a sprint copy | importers found (search) | verdict |
| --- | --- | --- | --- | --- |
| pkg/testkit.Git | unreachable over cmd mains on linux/darwin/windows | not checked (no clone) | pkg/friend/stage_test.go:54,241; pkg/friend/stage_worktree_test.go:47,226; pkg/testkit/git_test.go:16,26,38,47,55 (grep nova-tools) | STAY: imported by pkg/friend tests and testkit's own tests |
| pkg/testkit.gitEnv | unreachable over cmd mains | not checked | called by testkit.Git (pkg/testkit/git.go:42) (grep nova-tools) | STAY: required by a live function (Git) |
| pkg/testkit.GitRig.Commit | unreachable over cmd mains | not checked | pkg/friend/stage_test.go:55,59,242,246; pkg/friend/stage_worktree_test.go:48,95,227; pkg/testkit/git_test.go:24,40,51 (grep nova-tools) | STAY: imported by pkg/friend tests and testkit's own tests |
| pkg/testkit.GitRig.Push | unreachable over cmd mains | not checked | pkg/testkit/git_test.go:41 (TestGitPushFollowsTheCommitToTheRemote) (grep nova-tools) | STAY: tested by testkit's own tests |
| pkg/testkit.GitRig.Head | unreachable over cmd mains | not checked | pkg/testkit/git_test.go:44,56 (grep nova-tools) | STAY: tested by testkit's own tests |
| pkg/testkit.GitRig.run | unreachable over cmd mains | not checked | called by Git/Commit/Push/Head (pkg/testkit/git.go) (grep nova-tools) | STAY: required by live GitRig methods |
| pkg/testguard.AllowHosts | unreachable over cmd mains | not checked | pkg/secrets/model_invariants_test.go:241,513,567; pkg/secrets/place_test.go:24; pkg/secrets/seam_test.go:218; internal/ci/hostseam_class_test.go:31; pkg/testguard/testguard_test.go:63; pkg/testguard/testguard_cover_test.go:74,83 (grep nova-tools) | STAY: imported by pkg/secrets tests and internal/ci class test |
| pkg/onboarding.RepeatedSections | unreachable over cmd mains | not checked | internal/ci/onboarding_test.go:142; pkg/onboarding/onboarding_test.go:60,62 (grep nova-tools) | STAY: imported by internal/ci and package tests |
| pkg/onboarding.OpeningSentence | unreachable over cmd mains | not checked | internal/ci/secrets_in_errors_class_test.go:157; internal/ci/onboarding_functional_test.go:110; pkg/onboarding/rig_test.go:40; pkg/onboarding/opening_test.go:14 (grep nova-tools) | STAY: imported by internal/ci and package tests |
| pkg/onboarding.HowItWorksLine | unreachable over cmd mains | not checked | internal/ci/onboarding_functional_test.go:112; pkg/onboarding/rig_test.go:48; pkg/onboarding/opening_test.go:34; pkg/onboarding/opening.go:88 (internal) (grep nova-tools) | STAY: imported by internal/ci and package tests, and used internally |
| pkg/onboarding.HowItWorksLength | unreachable over cmd mains | not checked | internal/ci/onboarding_functional_test.go:113; pkg/onboarding/rig_test.go:56; pkg/onboarding/opening_test.go:68 (grep nova-tools) | STAY: imported by internal/ci and package tests |
| pkg/pathcase.measuredFold | unreachable over cmd mains | not checked | pkg/pathcase/boundary_test.go:18,31,45; called by CaseBoundaryDir (pkg/pathcase/boundary.go:125) (grep nova-tools) | STAY: tested and required by a live function |
| pkg/pathcase.nameFoldsInParent | unreachable over cmd mains | not checked | called by CaseBoundaryDir (pkg/pathcase/boundary.go:126) (grep nova-tools) | STAY: required by a live function |
| pkg/pathcase.CaseBoundaryDir | unreachable over cmd mains | not checked | pkg/swarm/slot_containment_test.go:181; pkg/sandbox/containment_test.go:153; pkg/pathcase/boundary_test.go:67 (grep nova-tools) | STAY: imported by pkg/swarm and pkg/sandbox tests |

## Conclusion

All fourteen candidate functions stay. Each is imported by tests or CI inside the
mas-bandwidth org (nova-tools' own tree), so the "imported nowhere in either org"
condition of the RULES fails, and deleting any of them would break the repository's
own tests. Nothing is deleted.

## Gate

- `go build ./...` — ok
- `go vet ./pkg/...` — ok
- `go test -count=1 -timeout 600s ./pkg/testkit/ ./pkg/testguard/ ./pkg/onboarding/ ./pkg/pathcase/ ./internal/ci/` — ok for testguard, onboarding, pathcase, internal/ci; one pre-existing failure in pkg/testkit/pipestore_test.go (TestPipeStoreReachesTheFakeOverPipes, line 14): miniredis cannot bind a TCP socket under the sandbox (`bind: operation not permitted`). That file is unchanged by this card and is red at BASE for the same reason.
