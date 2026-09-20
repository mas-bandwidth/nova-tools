RESULT tools22-pre-814-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#814 at head 89db45f95696: nova-tools nova-swarm bench probe: a card-shaped probe per bench (clone, go test one package, w
PREREAD 814 claims=3 proven=3 unproven=0 defects=2 high=0

PR 814
HEAD 89db45f9569623569d43e2a3ed30fac03a43a5df
BASE dev
MERGE-BASE 7e7f185203ccc9ed20674a3c021348a9718a8f21
BEHIND 99
FILES 2 production, 2 test
LINES +528 -5

CLAIMS
1. Run a card-shaped probe on each bench before the batch's first card: clone the repo, run go version, run go test ./internal/oneline/, write scratch/probe.txt, and print a RESULT
2. A bench whose probe fails carries no card: every card on that bench is ABSTAIN reason=bench-probe
3. The probe result is cached per (bench, binary sha256) in bench-probe.tsv; a passing probe is not re-run for the same build identity within one hour

PROVEN-BY internal/swarm/bench_probe_test.go:12 TestBenchProbeCachePassingNotRerun — verifies cache lookup and lookup by bench+sha
PROVEN-BY internal/swarm/bench_probe_test.go:123 TestBenchProbeLocal — verifies local probe runs go version, go test, writes probe.txt
PROVEN-BY internal/swarm/bench_probe_test.go:45 TestBenchProbeCacheFailingNotCached — verifies failing probes not cached as passing

DEFECTS
DEFECT medium internal/swarm/bench_probe.go:119 — ssh error is ignored and output is parsed regardless — could mask ssh failures that produce partial output
DEFECT medium internal/swarm/bench_probe.go:83-91 — if MkdirAll fails, FileOK is set to false but the function returns nil anyway; if WriteFile fails, FileOK is false but nil is returned

LEFT OWED
No production files outside the PR changes were read.

git status --short
git rev-parse HEAD
