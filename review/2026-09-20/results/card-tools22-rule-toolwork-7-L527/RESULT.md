RESULT tools22-rule-toolwork-7-L527 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 7 says?
ABSENT
SPEC docs/SPEC-TOOLWORK.md:527 rule 7
PKG internal/docs
ASK For rule 7 to be true the code must carry a `certify` verb that certifies a Linux bench with `wall=none` legs only, whose record says so and which prints the UDP note, plus an `accept` gate that treats `wall=none` as uncertified for any card that changes code.
DECIDING LINES
  cmd/nova-pulse/main.go:258-308 — the whole verb switch of nova-pulse (the binary SPEC-TOOLWORK §1/§2 names for `nova-pulse accept` and `nova-pulse certify` at SPEC-TOOLWORK.md:282 and :508) has no `accept` and no `certify` case: pool, launch, fill, cut, harvest, beat, watch, manager, status, progress, gate, run, triage, sweep, reap, hygiene, fleet, wake, sleep, width.
  internal/pulse/harvestguard.go:27-29 — "WHAT THIS STILL DOES NOT DO: #1650's `accept` gate between verify and push. That is #1650's lane (toolwork T05), and Stella holds the adoption word on it."
  internal/swarm/lintheader.go:66-68 — "internal/pulse still does not carry `cardheader.go` on `dev` -- T03/#1721 is open"; internal/pulse/kinds.go is also absent (ls: no such file), the table SPEC-TOOLWORK.md:731 says `nova-pulse accept --kinds` reads.
GREPS
  grep -rn "wall=none" --include='*.go' .   -> no match (only docs/SPEC-TOOLWORK.md:533-534 and SPEC-SWARM.md carry the token)
  grep -rn "certify\|Certify" cmd/nova-pulse/*.go internal/pulse/*.go | grep -v _test  -> only the machines-registry `certified=<date>` note (internal/fleet/registry.go:77, internal/pulse/fill.go:29) and hygiene's "certify tree", never a certification verb
  grep -rn "func cmdAccept\|func cmdCertify" cmd/  -> no match
  grep -rn "CERTIFY LEG\|CERTIFY OK\|ACCEPT OK" --include='*.go' .  -> no match
  grep -rn "wall-none-is-uncertified-for-a-code-card" --include='*.go' .  -> no match (the rule's red test does not exist)
  grep -rn "uncertified" --include='*.go' .  -> internal/fleet/registry.go:75 only, a comment on the machines registry pool
  grep -rn "cert=" internal/ cmd/ --include='*.go' | grep -v _test  -> no match (no cert record field)
  grep -rn "toolchain" cmd/nova-sandbox/*.go internal/sandbox/*.go | grep -v _test  -> only --go (run.go:208-209); rule 2's --toolchain flag, which rule 7's Landlock clause builds on, is absent too
  grep -rn "func Test" internal/docs/*.go  -> 35 tests; none concerns SPEC-TOOLWORK rule 7 or any accept/certify/leg behaviour
VERDICT NOTE The rule's noun verbs (`certify`, `accept` with its `wall=none`-is-uncertified rule) belong to the SPEC-TOOLWORK §1/§2 gate system, which the tree itself marks as not landed: the accept gate is explicitly deferred to #1650, and the gate's card header and kinds table are still open PRs. Nothing in internal/docs or anywhere else implements rule 7's behaviour, so it is ABSENT, not a GAP: there is no partial `wall=none` certification logic to conform or diverge.
Left owed