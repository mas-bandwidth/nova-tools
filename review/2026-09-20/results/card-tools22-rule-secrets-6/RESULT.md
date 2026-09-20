RESULT tools22-rule-secrets-6 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SECRETS.md rule 6 says?
CONFORMS internal/secrets/gate.go:29
SPEC docs/SPEC-SECRETS.md:1116 rule 6
PKG internal/secrets
ASK The code must implement a gate that inspects a PR's diff (base..head) and APPROVEs only when the shape matches exactly a seat PR — two age recipients (including the recovery key), ciphertext-only seat files, no files beyond .sops.yaml/README.md/seat .yaml — and REFUSEs everything else.
internal/secrets/gate.go:58-62: only .sops.yaml, README.md and seat .yaml files may change
internal/secrets/gate.go:102-106: each rule has exactly two recipients and includes the recovery key
internal/secrets/gate.go:119-120: path_regex names exactly one seat file
internal/secrets/gate.go:172-177: seat files are encrypted (sops: metadata present, no plaintext values)
internal/secrets/gate.go:148-156: no seat file removed that existed at base
GUARDED-BY internal/secrets/gate_test.go:77 TestGateApprovesAGoodSeatPR
GUARDED-BY internal/secrets/gate_test.go:93 TestGateRefusesARuleWithThreeRecipients
GUARDED-BY internal/secrets/gate_test.go:109 TestGateRefusesAPlaintextValue
GUARDED-BY internal/secrets/gate_test.go:125 TestGateRefusesAChangeToAnotherFile
GUARDED-BY internal/secrets/gate_recipients_test.go:167 TestGateRefusesARemovedSeatFile
Grep runs: grep -rn "seat.rule.gate\|seat-rule\|seat_rule\|seatRule" --include='*.go' .; ls internal/secrets/; grep -rn "recovery.pub" --include='*.go' internal/secrets/
Left owed: none
git status --short