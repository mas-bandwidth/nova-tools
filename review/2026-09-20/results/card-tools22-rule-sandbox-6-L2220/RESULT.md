RESULT tools22-rule-sandbox-6 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SANDBOX.md rule 6 says?
CONFORMS cmd/nova-sandbox/main.go:904
SPEC docs/SPEC-SANDBOX.md:2729 rule 6
PKG internal/sandbox
ASK The `policy` verb must accept an optional `-- <command>`, resolve it exactly as a wrapped run would so that the directory of the resolved command shows as a root in the printed policy, and never execute it.
CONFORMS — the deciding lines:
  cmd/nova-sandbox/main.go:904 `argv := f.argv`
  cmd/nova-sandbox/main.go:905-911 `if len(argv) == 0 { shell, err := exec.LookPath("sh") ... argv = []string{shell, "-c", "true"} }` — the command after `--` is OPTIONAL; with none, /bin/sh is the floor.
  cmd/nova-sandbox/main.go:898-903 (comment) `"policy prints exactly what a wrapped run would apply". One root is computed from the COMMAND ... The command is therefore optional and comes after --`
  cmd/nova-sandbox/main.go:913-917 the argv is passed to `sandbox.Build(sandbox.Input{... Argv: argv ...})`, so the command's directory becomes an OptRoot the same way a wrap computes it.
  cmd/nova-sandbox/main.go:886 (comment) `policyVerb prints the generated policy ... and runs NOTHING.` The function has no exec; it prints the profile and a POLICY OK line and returns 0 (main.go:924-932).
  internal/sandbox/policy.go:628 `p.OptRoots = OptionalRoots(p.Command)` and internal/sandbox/policy.go:179-180 `if command != "" { candidates = append(candidates, filepath.Dir(command)) }` — the one root computed from the command.
  internal/sandbox/profile.go:40-44 the OptRoots render in the printed text as `(allow file-read* (subpath "<command dir>"))`, so the root is visible to a reader.
GUARDED-BY internal/sandbox/policy_test.go:338 TestACommandInTheCallersHomeIsRefused (asserts at :398-411 that the directory of a command in a directory of its own IS in p.OptRoots; passes on this bench) and cmd/nova-sandbox/main_test.go:1302 TestPolicyVerbPrintsAndRunsNothing (asserts at :1316 that the policy verb ran nothing — the marker file the command would create is not there).
NOTE: no test drives `policy -- <command>` end to end; the two halves (command-dir root via Build, runs-nothing via the verb) are each guarded, but the verb's acceptance of the optional command is not itself asserted.
Greps run: `grep -rn policy --include='*.go' .`; `grep -rn '"policy"' --include='*_test.go' .`; `grep -rn 'subpath' --include='*_test.go' internal/sandbox/ cmd/nova-sandbox/`; `grep -rn 'func Test' --include='*_test.go' cmd/nova-sandbox/`; `grep -rn 'resolved command\|did not run\|as a root' --include='*_test.go' .`; `grep -rn 'policy --' docs/SPEC-SANDBOX.md`. Read in full: cmd/nova-sandbox/main.go (parse :229-297, policyVerb :886-933, usage :41-169), internal/sandbox/policy.go (Build, OptionalRoots), internal/sandbox/profile.go, internal/sandbox/policy_test.go, internal/sandbox/optroots_test.go.
Left owed===FILE=== card-tools22-rule-sandbox-6/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-rule-sandbox-6	1	2026-09-20T20:07:20Z	2026-09-20T20:11:02Z	0	opencode	deepseek-v4-flash	54400	13401	0	1234176	0	0.0459
