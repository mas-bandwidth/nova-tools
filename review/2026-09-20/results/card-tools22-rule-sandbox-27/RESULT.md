RESULT tools22-rule-sandbox-27 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SANDBOX.md rule 27 says?
GAP cmd/nova-sandbox/main_test.go:320
SPEC docs/SPEC-SANDBOX.md:2516 rule 27
PKG internal/sandbox
ASK An implementation must make `git push` from inside the wall fail against a local bare repository and prove each of four mechanisms in its own assertion — (a) a push at the reference checkout under `--read` leaving `refs/` byte-identical, (b) an ssh `git@example.invalid` origin failing before any connection with the planted `.ssh/id_test` unreadable inside and readable outside, (c) the child holding none of rule 9's exact set with a unix socket denied and its control succeeding, (d) an HTTPS origin with a planted gh config unread — and run the same four pushes outside the wall to succeed.
QUOTED
The one push test the tree has, `TestGitPushOutOfTheJobFails` at cmd/nova-sandbox/main_test.go:320-341, does a SINGLE push to a local bare remote outside every named path and asserts only that it fails and that refs is empty:
  cmd/nova-sandbox/main_test.go:334  code, _, _ := j.wrapped(t, "cd '"+repo+"' && '"+git+"' push -q origin HEAD:refs/heads/main")
  cmd/nova-sandbox/main_test.go:336  t.Fatal("a push out of the job succeeded; the remote is outside every named path")
  cmd/nova-sandbox/main_test.go:338  out := mustOutput(t, git, "-C", remote, "for-each-ref", "--format=%(refname)")
  cmd/nova-sandbox/main_test.go:340  t.Fatalf("the push landed: %q", out)
The rule demands four assertions, one per mechanism, "so that a change that removes one of them turns exactly one line red", plus "The same four pushes run outside the wall against the local bare repository and succeed" (docs/SPEC-SANDBOX.md:2516-2538). Missing: (a) has no push at a `--read` reference with a byte-identical `refs/` (the remote here is in neither list); (b) has no `git@example.invalid` origin at all — no test names that string and the ssh-before-any-connection shape does not exist; (d) has no HTTPS origin and no planted `gh` config (nothing in cmd/nova-sandbox, profiles/ or scripts/ greps to `hosts.yml`/`gh config`); and there is no outside-the-wall control push. Mechanism (c)'s halves do exist as separate tests — the env scrub in `TestScrubSetIsExactlyTheSpecs` (internal/sandbox/policy_test.go:615) and `TestTheNoteNamesExactlyWhatWasDropped` (cmd/nova-sandbox/main_test.go:1233), the unix socket with its control in `TestTheAgentSocketIsUnreachable` (cmd/nova-sandbox/main_test.go:251) and `profiles/darwin-check.sh:199-205` (`unix_socket_outside`, `unix_socket_outside_control`) — but none is a push assertion, and the rule binds them into the push test. GAP: the code does less than the rule describes.
GUARDED-BY n/a (verdict is GAP)
GREPS
  grep -rn "func Test" --include='*_test.go' internal/sandbox/
  grep -rn "git push\|push origin\|push -q" --include='*.sh' --include='*.go' cmd/ internal/sandbox/ profiles/ scripts/
  grep -rn "git@example.invalid\|example.invalid:x\|hosts.yml\|gh config\|\.ssh/id_test\|GH_TOKEN" --include='*.go' --include='*.sh' cmd/nova-sandbox/ internal/sandbox/ profiles/
  grep -rn "unix_socket_outside\|unix_socket_outside_control\|env_no_ssh_auth_sock" profiles/darwin-check.sh
  grep -rn "refs/heads/main\|for-each-ref\|remote.git" --include='*.go' --include='*.sh' cmd/nova-sandbox/ internal/sandbox/ profiles/
  git log -S "TestGitPushOutOfTheJobFails" --all; git blame -L 2516,2538 docs/SPEC-SANDBOX.md
CONFIRMED go test ./cmd/nova-sandbox/ -run TestGitPushOutOfTheJobFails passes (0.490s), so the one existing push assertion is real.
Left owed