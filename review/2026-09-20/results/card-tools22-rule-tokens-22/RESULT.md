"RESULT tools22-rule-tokens-22 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOKENS.md rule 22 says?
ABSENT
SPEC docs/SPEC-TOKENS.md:2286 rule 22
PKG internal/tokens
ASK The nova-tokens binary must implement a `publish` verb with a `--v1-day` flag that lands one commit in a bare remote from a side clone, enforces mutual exclusivity between `--batch` and `--v1-day` (one required), rejects `--all`, refuses a clock-chosen day, and moves no local ref.
The publish verb is not implemented. The spec itself says so at docs/SPEC-TOKENS.md:1208-1212: "The shipped binary has not implemented this verb: the publication boundary (cmd/nova-tokens/boundary_test.go) still holds rule 16 for the shipped build, so a git publisher cannot land until that tripwire falls. This section is the verb's spec; the verb itself is an explicit spec gate, not shipped behaviour."
cmd/nova-tokens/main.go:162-184 has no `"publish"` case in the verb switch.
cmd/nova-tokens/boundary_test.go:571-589 (TestTheRecordsNamespaceIsNotAVerbUntilItsGateIsDecided) confirms `records publish` is an unknown subcommand (exit 2).
cmd/nova-tokens/demanded_test.go has no rule-22 test (grep for "publish", "v1.day", "v1-day", "rule.22" found nothing).
internal/tokens/ has no publish or --v1-day code (grep for "publish", "v1.day", "v1-day" found only publish_frictions.go which is a different future verb).
Left owed: implement the publish verb, --v1-day, --batch, contribution-kind mutual-exclusivity check, and the demanded test at SPEC-TOKENS.md:1286-1293.
git status --short