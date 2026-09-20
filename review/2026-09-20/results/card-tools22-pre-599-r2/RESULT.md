RESULT tools22-pre-599-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#599 at head d0421f10f044: nova-swarm native opens the loopback a keyless provider names for the walled harness, and a har
PREREAD 599 claims=6 proven=2 unproven=4 defects=3 high=0

PR 599
HEAD d0421f10f044ab4c2dd8edfabf58dd9a257a79f1
BASE dev
MERGE-BASE 4a93fd75a3b030ee36c4a16fcd55f71aa4379549
BEHIND 15
FILES 6 production, 2 test
LINES +176 -13

CLAIMS

1. nova-swarm native reads the keyless provider's loopback baseURL host:port out of the
   carried --config and carries it into the wall argv as --net-allow <host:port>.
   PROVEN-BY cmd/nova-swarm/native_test.go:838 TestNativeAllowsProviderLoopback — runs a
   nativeRun with a config whose ollama baseURL is http://127.0.0.1:11434/v1 and asserts the
   fake wall's recorded argv contains "--net-allow 127.0.0.1:11434".

2. The darwin wall opens exactly that loopback host:port back up, so the local model stays
   reachable inside the wall.
   UNPROVEN — the only witness records argv through a fake sandbox that applies no policy,
   and the emission at internal/sandbox/profile.go:108-112 has no test in the diff or the tree.

3. A --net-allow entry that will not split into host and port is refused reason=bad_net at
   exit 125 rather than a profile sandbox-exec rejects at exit 65.
   UNPROVEN — the Build loop at internal/sandbox/policy.go:488-500 is new and untested; the
   sole existing bad_net test (internal/sandbox/policy_test.go:595
   TestNetDenyWithNetListenIsRefusedOnEveryPlatform) covers --net-deny with --net-listen only.

4. nova-sandbox accepts a repeatable --net-allow <host:port> flag on the bare form and the
   policy verb.
   UNPROVEN — the parse at cmd/nova-sandbox/main.go:288-291 and the Input wiring are
   exercised by no test; the native test witnesses an argv string that nativeSandboxArgv
   builds without going through the nova-sandbox parser.

5. A harness that exits clean without writing its report is carried on the NATIVE OK line as
   harness=silent and scored harness-silent, never OK.
   PROVEN-BY-EXISTING — harnessState (cmd/nova-swarm/native.go:682-697) sets the token and
   internal/swarm/batch.go:1400 cardHarnessSilent reads "harness=silent" off the line; both
   predate this PR (base native.go:556, base batch.go:1400).

6. The run records a harness-silent note when the child exited 0 but wrote no RESULT.md.
   UNPROVEN — res.reason at cmd/nova-swarm/native.go:631 is written and never read; the NATIVE
   OK line renders harness=%s from res.harness, and no code reads res.reason, so nothing in
   this PR makes the note observable.

DEFECTS

DEFECT medium cmd/nova-swarm/native.go:631 — the new res.reason = "harness-silent" is a dead
write: the NATIVE OK line (cmd/nova-swarm/main.go:1817) ends "harness=%s" plus the fence,
usage and term suffixes and never renders res.reason, so the PR's headline "a harness that
exits silently is never scored OK" adds no observable behaviour and the block's own comment
says the note is "carried on the OK line" when it never is — what would fix it: delete the
field and the block, or render it as a " reason=harness-silent" suffix on the OK line and
update the fixed-shape readers (cardHarnessSilent, the OK-line tests) that a new token would
need to keep parsing.

DEFECT low cmd/nova-sandbox/main.go:491 — --net-allow is accepted by the shared parse on the
probe verb and silently dropped (probeVerb's Build Input omits NetAllow while it passes
NetDeny and NetListen), exactly the accepted-and-ignored class the tool's own notForThisVerb
refusal exists to eliminate — what would fix it: pass NetAllow into the probe policy build or
refuse the flag there.

DEFECT low internal/sandbox/policy.go:493 — the flag text and SPEC-SANDBOX say --net-allow
opens "the loopback host:port named" and "the one address, nothing wider", but Build never
checks that the host is a loopback address, so any host:port a caller names is carried into
the profile, wider than the documented promise — what would fix it: refuse a non-loopback
host in Build, or re-word the docs.

QUESTIONS

Q1. The profile emits (allow network-outbound (local ip (host "127.0.0.1") (port "11434")))
    for an OUTBOUND connect whose LOCAL endpoint is 127.0.0.1:ephemeral, not port 11434. Was
    this SBPL form actually exercised against sandbox-exec on the Mac bench, and if (remote
    ip) really does not match a connect to 127.0.0.1, why does the (local ip ...) filter match
    an outgoing client socket? The diff's only test records argv through a fake sandbox.

Q2. res.reason ("harness-silent") is never rendered on the NATIVE OK line, whose fixed shape
    ends harness=%s plus the fence, usage and term suffixes. Was the intent to add a
    " reason=harness-silent" suffix (mirroring " reason=terminated") and the main.go wiring
    was missed, or is the existing harness=silent token considered the complete note?

Q3. --net-deny together with --net-allow is accepted and the allow line is emitted "even
    under --net-deny", while --net-deny with --net-listen is refused as contradictory. Is the
    asymmetry deliberate, and should the SANDBOX OK line keep net=denied when one outbound
    grant is present?

Q4. The head is 15 commits behind dev and the merge base (4a93fd75) is older than the card
    base 5298f6be12ea; the branch tip is two dev-sync merges (2f469325, d0421f10) on top of
    one feature commit (0a12a097). Is landing the merged branch as-is intended, or should the
    feature be re-cut onto current dev before landing?

Left owed: I read the full diff (all 8 hunks, 176 insertions) and the surrounding production
code: native.go's admission/argv/OK-line regions, sandbox Build and the net-allow validation,
DarwinProfile in full, and nova-sandbox's parse and the bare/probe/policy verbs. I did not
read the unchanged bodies of docs/SPEC-SANDBOX.md and docs/SPEC-SWARM.md beyond the diff
hunks, nor internal/swarm/batch.go beyond scoreCard and cardHarnessSilent, nor the internals
of the fake sandbox/harness test binaries. No test run was performed (none required).

---
$ git status --short
$ git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-599-r4/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-599-r4	1	2026-09-20T19:57:00Z	2026-09-20T20:05:52Z	0	opencode	deepseek-v4-flash	79462	42843	0	3826688	0	0.1303
