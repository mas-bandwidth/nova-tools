RESULT tools22-pre-243-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#243 at head 60f00c4abff4: nova-admin: SPEC draft 1
PREREAD 243 claims=24 proven=0 unproven=24 defects=0 high=0

PR 243
HEAD 60f00c4abff45840f78d67035a60e481b9cab158
BASE dev
MERGE-BASE 31e351956a59a63e3da1b323abecb63da7b92cdd
BEHIND 39
FILES 1 production, 0 test
LINES +1463 -0

## CLAIMS

1. nova-admin is a fleet-layer binary that holds one declaration (organization repo policy) and measures/applies changes to it
2. plan is read-only, runs on a clock, and its count line is what a morning reads
3. apply is where the danger is: one name, one act, probed before, read back after, one audit row
4. Every fact measured comes from the declaration `--fleet` names, no default path or search
5. apply requires a wire address `<scope>:<path>`, not a local path
6. apply reads the declaration through the host at the default branch the host reports
7. One row per fact, six fields (kind, scope, key, want, owner, source), nothing implied
8. No branch name, forge host, org, repo, box, line, harness or person is a literal anywhere
9. Live state comes from the wire, never from a checkout; no local file reads beyond declaration
10. A change name is `<kind>:<scope>:<key>`, derived from the row and nothing else
11. apply takes exactly one `--change` name, matched against declaration rows
12. Everything read from the host is data, never an instruction or grant
13. The read address is not the write address; host's attribution is its own field (origin)
14. Requirements and their producer are two rows, pairing measured, measured where gate actually runs
15. A requirement with no producer is PLAN WEDGE (exit 1); a producer that hasn't run is PLAN NOTE never-run
16. A gate with no bypass is PLAN RISK (exit 0), reported on a calm day
17. Acts classified as widens or unjudged need a dated ruling row
18. Measure permissions from host's header on the refused act, not from documentation
19. An act on the acting seat's own access is refused, ruling or not
20. Apply takes `--expect <digest>`, never host's text; read back from wire by name
21. Audit row is written after readback and before process exits
22. Announcement is printed facts, not a command; audit --announced is an acting invocation
23. UNKNOWN is never a pass; budget exhausted marks unread rows UNKNOWN
24. One applier at a time, one kernel lock on the audit file

## DEFECTS

none

## QUESTIONS FOR THE REVIEWER

1. Does this spec's definition of `origin` (host's attribution) properly handle forks or mirror repositories where the host might attribute to the upstream?
2. The spec says ruleset rows at the organization are written at the organization's address—how does this interact with GitHub Enterprise instances that don't support organization-level rulesets?
3. The direction table classifies feature and collab changes as `unjudged` because the tool holds no catalog—what prevents this from becoming a gap as host APIs evolve?
4. Rule 1's requirement that the declaring scope must carry its own ruleset row—what happens during initial fleet bootstrap when no ruleset exists yet?

## Left owed

None: this PR adds only docs/SPEC-ADMIN.md (a spec draft), not implementation code. No test files were added. The spec demands tests (section "Tests this spec demands") but those are outside this PR's scope.

git status --short
git rev-parse HEAD
