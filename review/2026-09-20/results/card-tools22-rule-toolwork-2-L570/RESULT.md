RESULT tools22-rule-toolwork-2-L570 sha=5298f6be12ea
ABSENT
SPEC docs/SPEC-TOOLWORK.md:570 rule 2
PKG internal/hygiene, internal/swarm, cmd/nova-pulse, cmd/nova-check
ASK An implementation must walk every entry in a staged job directory before workers start and refuse any absolute symlink or symlink resolving outside the job root.

WHAT RULE 2 SAYS (verbatim, docs/SPEC-TOOLWORK.md:570):
> 2. **Staging leaves no way out of the job root.** No absolute symlink and no symlink
>    resolving outside the job root exists in a staged tree (#1557's `repo/dist ->
>    /Users/…` cost four legs a toolchain); toolchains are real directories or
>    copy-on-write clones. The launcher checks this before the first worker starts and
>    refuses by path.

CONTEXT (docs/SPEC-TOOLWORK.md:550-575):
Section 3 header: "Identity and hygiene, at staging and at harvest"
- Rules 1-2 are STAGING obligations ("what the launcher puts in a job directory before the worker starts")
- Rules 3-6 are HARVEST obligations (checked by `hygiene.Check`)
- "Both halves are mechanical, and the harvest half never trusts that the staging half ran."

GREPS RAN:
  LC_ALL=C grep -rni "symlink" --include="*.go" .          → found matches in internal/hygiene/, internal/bus/, internal/check/, internal/swarm/planted_gather_test.go, cmd/nova-pulse/hygiene_test.go
  LC_ALL=C grep -rni "job.root\|job_root\|jobRoot" --include="*.go" . → no matches
  LC_ALL=C grep -rni "staging\|Staging" --include="*.go" .  → no matches
  LC_ALL=C grep -rni "#1557" --include="*.go" .             → no matches
  LC_ALL=C grep -rni "refuses.*path\|outside.*root\|absolute.*link" --include="*.go" . → no matches
  LC_ALL=C grep -rn "func Test" --include="*_test.go" ./internal/hygiene/ → 29 test functions including TestHygieneRejectsASymlink

WHERE SYMLINK HANDLING EXISTS:
  internal/hygiene/hygiene.go:547-561 — modeFinding(e entry):
    case "120000": returns Finding{Token: "stray-file", Why: "a symlink: a staged tree holds no way out of itself"}
  This catches symlinks (git mode 120000) as part of the STRAY-FILE harvest check.

WHY IT IS ABSENT:
Rule 2's primary obligation is the STAGING check: "The launcher checks this before the first worker starts."
No code was found that walks the staged job directory before workers begin. Specifically:
  1. `hygiene.Check` (internal/hygiene/hygiene.go:85) runs at HARVEST time over git diff entries between base..head — it cannot see symlinks already present in the base commit.
  2. The identity/git-config setup (rule 1) is handled separately; the launcher's pre-worker symlink scan (rule 2) has no corresponding Go function.
  3. Searched all relevant packages: internal/hygiene, internal/swarm, internal/dispatch, cmd/nova-pulse, cmd/nova-check, internal/safepath, internal/bounded, internal/sandbox. None implements a staging-time symlink scan.

LEFT OWED: A pre-worker launcher check that walks the staged tree (using os.Lstat + filepath.EvalSymlinks on each entry) and refuses the job if any file is an absolute symlink or resolves outside the job root. This is distinct from the existing harvest-side stray-file/symlink rejection (which only covers diff-added entries).

git status --short
(nothing)
