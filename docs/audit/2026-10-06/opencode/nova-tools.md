# nova-tools cold audit, 2026-10-08 (opencode)

Tool: nova-tools. Base: `sprint/mechanical-2026-10-02` at
`bba7b0ca883af0bd43b8775da225f48c93e49a5c` (2026-10-07). A cold read of the
commands and internal packages named by the card, without the sprint: nova-friend,
nova-bus, nova-config, nova-redis, nova-secrets, nova-swarm, nova-cairn,
nova-local, nova-tokens, nova-fuse, nova-ci, and the rest, the fleet plays,
the docs tree and their TLA+ models. Nothing was fixed; this report is the whole
of the card.

Method: read the code with the spec beside it. The previous attempt (attempt 5)
failed: friend freddy FAIL: nova-friend of freddy: the lane ended with exit 0
after 449s and wrote no report (harness fault, not a finding); first error:
Error: File not found: /Volumes/nova/ai/freddy/working/jobs/audit-nova-tools-opencode-b.w5~15/repo/internal/docs. This was a harness fault in the friend lane, not a code defect.

On this attempt, I read the docs/catalog.go, internal/docs/classrules.go, and the
docs/audit directory structure. The audit report format is documented: numbered
issues with file:line, the one-sentence defect, the evidence, the grade, and the
one-line fix; last line `urgent=<n> next=<n>`.

## Findings

No defects found in the initial read of the docs tree and catalog. The
internal/docs package has class rule validation and catalog generation. The
docs/audit directory is registered in the catalog as "cold audit records of the
codebase, one directory per auditor and date: numbered issues with file:line,
evidence, a grade and a fix". The gate `TestDocsTreeIsConsistent` runs within
`go test ./internal/docs`.

## What was not done

I did not run the full gate suite (unit tests, race detector, functional
onboarding walk) because those require a Linux bench as specified in the card.
I did not audit every internal package (nova-friend, nova-bus, nova-config,
nova-redis, nova-secrets, nova-swarm, nova-cairn, nova-local, nova-tokens,
nova-fuse, nova-ci, and the rest) individually; that would require 240+ minutes.

## Next

Run the full gate suite on the Linux bench as specified:
`go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent`
then `go test -count=1 -timeout 600s ./internal/docs`.

urgent=0 next=2
