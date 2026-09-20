RESULT tools22-rule-secrets-6-L893 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SECRETS.md rule 6 says?
CONFORMS internal/secrets/sops.go:132
SPEC docs/SPEC-SECRETS.md:893 rule 6
PKG internal/secrets
ASK On any sops failure the tool must swallow sops' raw stderr (a plaintext-looking transcript reaches no stream) and, in the unrecognised case, print `sops failed: exit <n>` with the remedy `run 'sops -d <file>'` and never the transcript.
QUOTE internal/secrets/sops.go:131-132
  // Sanitize sops error: do NOT pass raw stderr through
  return nil, fmt.Errorf("sops failed: exit %d (transcript withheld: run 'sops -d %s' to inspect)", exitCode, filePath)
QUOTE internal/secrets/sops.go:121-123 (stderr captured, never forwarded)
  var stdoutBuf, stderrBuf bytes.Buffer
  cmd.Stdout = &stdoutBuf
  cmd.Stderr = &stderrBuf
QUOTE internal/secrets/seal.go:365 (the seal/seat decrypt path carries the same shape)
  return nil, fmt.Errorf("sops failed: exit %d (transcript withheld: run 'sops -d %s' to inspect)", exitCode, filePath)
GUARDED-BY cmd/nova-secrets/demanded_part2_test.go:12 TestSopsErrorsAreNeverPassedThroughRaw (passes; fake sops writes PLAINTEXT_SECRET_LEAK_IN_STDERR_12345 to stderr, asserts it reaches no stream and stderr carries "transcript withheld")
GREPS grep -rn "sops failed" --include='*.go' .
GREPS grep -rn "TestSopsErrorsAreNeverPassedThroughRaw" --include='*.go' .
GREPS grep -rn "transcript withheld\|DecryptFile\|sops failed" --include='*_test.go' internal/ cmd/
GREPS grep -rn '"-d"\|"-e"\|"--decrypt"\|sops' --include='*.go' internal/secrets/*.go (exec/command/run sites)
Left owed: none.