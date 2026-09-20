RESULT tools22-rule-secrets-6-L282 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SECRETS.md rule 6 says?
CONFORMS internal/secrets/invariants.go:309
SPEC docs/SPEC-SECRETS.md:282 rule 6
PKG internal/secrets
ASK Before reading, decrypting or otherwise using the private key, an implementation must verify that the key file's mode is exactly 0600 and its parent directory's is exactly 0700, and refuse otherwise.
CONFORMS internal/secrets/invariants.go:309
  `if fi.Mode().Perm() != 0600 {` (internal/secrets/invariants.go:314)
  `return fmt.Errorf("key file %s mode is %04o; expected 0600; run: chmod 600 %s", ...)` (internal/secrets/invariants.go:315)
  `if dirFi.Mode().Perm() != 0700 {` (internal/secrets/invariants.go:323)
  `return fmt.Errorf("key directory %s mode is %04o; expected 0700; run: chmod 700 %s", ...)` (internal/secrets/invariants.go:324)
GUARDED-BY cmd/nova-secrets/demanded_part2_test.go:63 TestTheKeyFileModeIsARefusalOnEveryVerbThatTakesOne
  (asserts exec 0644 -> 125 with "chmod 600", check 0644/0640 -> 2 with "chmod 600", check 0600-in-0755-dir -> 2 with "chmod 700"; passes: `GOMAXPROCS=8 go test ./cmd/nova-secrets/ -count=1 -run '^TestTheKeyFileModeIsARefusalOnEveryVerbThatTakesOne$'` -> ok, 17.593s)
GREPS
  `grep -rn "0600|0700|0o600|0o700|Perm" --include='*.go' internal/secrets/`
  `grep -rn "CheckInvariant6" --include='*.go' .`
  `grep -rn "func Test" --include='*_test.go' internal/secrets/`
  `grep -rn "Chmod|mode is|expected 0600|expected 0700" --include='*_test.go' internal/secrets/`
  `grep -rn "CheckInvariant6|key file .* mode|expected 0600|expected 0700|chmod 600|chmod 700" --include='*_test.go' .`
Left owed
  none
`git status --short` prints nothing.