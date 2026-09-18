package fleet

// Red tests for what a NON-AUTHOR found by running this branch for real on hulk
// (rowan-child-dogfood-6, 2026-09-18). Every one of these is a fault the fakes could not
// see, because every fake answered as though the machine had been reached:
//
//  1. certify shelled `ssh hulk` FROM hulk, and died on its own host key. A machine
//     certifying itself must not go near ssh.
//  2. `CERTIFY hulk go-test FAIL evidence="Host key verification failed."` -- a TRANSPORT
//     failure written as a verdict about the machine's work, and `build=Host\x20key...` in
//     the build column of a certificate row. A tool that cannot reach a machine knows
//     nothing about that machine.
//  3. `--fix` then ran `standard --apply` against the machine it had just failed to reach.
//  4. `gh` is not on hulk, so the forge workloads asked the wrong host: they are questions
//     for the COORDINATOR about the machine.
//  5. A dry run ended `CERTIFY OK ... ok=14`, which is a fleet reporting fourteen passes it
//     never ran.
//  6. `--status` refused without `--machines` and without a provisioning standard, though it
//     touches no machine and reads one file.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 1. no ssh to the local machine
// ---------------------------------------------------------------------------

// TestTheLocalMachineIsNeverReachedByAnSSH: hulk certifying hulk runs the workload here,
// through the same seam, with no ssh and so no host key, no BatchMode and no agent.
func TestTheLocalMachineIsNeverReachedByAnSSH(t *testing.T) {
	remote := &fakeRemote{answers: map[string]remoteAnswer{}}
	local := &fakeRemote{answers: localSpaceAnswers()}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: writeFile(t, "certs.tsv", ""),
		Remote: remote, Local: local, LocalHost: "space.tail1234.ts.net",
		Hash: "h", Now: fixedNow,
	})
	all := out + errs
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, all)
	}
	if len(remote.calls) != 0 {
		t.Errorf("the local machine was reached by ssh %d times; hulk certifying hulk died on its own host key", len(remote.calls))
	}
	if len(local.calls) == 0 {
		t.Error("the local runner was never used")
	}
	if !strings.Contains(all, "CERTIFY NOTE machine=space transport=local") {
		t.Errorf("nothing says the machine was run locally:\n%s", all)
	}
}

// TestALocalMachineIsTheNameOrTheSSHTargetOrTheShortHost: the registry says `space`, the
// machine calls itself `space.tail1234.ts.net`, and ssh may carry a user. All three are the
// same machine.
func TestALocalMachineIsTheNameOrTheSSHTargetOrTheShortHost(t *testing.T) {
	m := Machine{Name: "space", SSH: "nova@space"}
	for _, local := range []string{"space", "space.tail1234.ts.net", "SPACE", "nova@space"} {
		if !IsLocalMachine(m, local) {
			t.Errorf("%q is not seen as this machine", local)
		}
	}
	for _, other := range []string{"", "studio", "spacex", "hulk.local"} {
		if IsLocalMachine(m, other) {
			t.Errorf("%q is taken for this machine", other)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. a transport failure is not a verdict
// ---------------------------------------------------------------------------

// TestATransportFailureIsUnreachableAndNeverAVerdict. "Host key verification failed" says
// nothing whatever about whether that bench can build Go inside the wall, and a FAIL row for
// it is a lie that `fill` then acts on.
func TestATransportFailureIsUnreachableAndNeverAVerdict(t *testing.T) {
	certs := writeFile(t, "certs.tsv", "")
	remote := &fakeRemote{answers: map[string]remoteAnswer{
		"space": {out: "Host key verification failed.\n", err: errors.New("exit status 255")},
	}}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs,
		Remote: remote, Hash: "h", Now: fixedNow,
	})
	all := out + errs
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (unreachable, not failed)\n%s", code, all)
	}
	if !strings.Contains(all, "CERTIFY space go-test UNREACHABLE reason=") {
		t.Errorf("a transport failure was not reported as UNREACHABLE:\n%s", all)
	}
	if strings.Contains(all, "go-test FAIL") {
		t.Errorf("a transport failure was written as a verdict about the machine's work:\n%s", all)
	}
	if !strings.Contains(all, "unreachable=") {
		t.Errorf("the closing line does not count the unreachable classes:\n%s", all)
	}
	rows, err := ReadCertificates(certs)
	if err != nil {
		t.Fatal(err)
	}
	// Every row a machine nobody reached may hold is one the COORDINATOR answered about it
	// (the forge knows what it knows); nothing the machine itself was asked may be recorded.
	for _, r := range rows {
		if r.Class != "registry-truth" {
			t.Errorf("an unreachable machine wrote a %s row (%s); it proved nothing", r.Class, r.Verdict)
		}
	}
	// And it must not have run eleven doomed workloads to learn it once.
	if len(remote.calls) > 2 {
		t.Errorf("the machine was reached %d times after the first transport failure", len(remote.calls))
	}
}

// TestTheBuildColumnOnlyEverHoldsAParsedVersion: `build=Host\x20key\x20verification...`
// reached a certificate row on hulk. The build is half of what makes a certificate current,
// so anything that is not a version is no build at all.
func TestTheBuildColumnOnlyEverHoldsAParsedVersion(t *testing.T) {
	for _, tc := range []struct{ out, want string }{
		{"nova-merge v0.17.0 linux/amd64\n", "v0.17.0"},
		{"v0.17.0\n", "v0.17.0"},
		{"Host key verification failed.\n", ""},
		{"bash: line 1: nova-merge: command not found\n", ""},
		{"", ""},
	} {
		if got := BuildVersion(tc.out); got != tc.want {
			t.Errorf("BuildVersion(%q) = %q, want %q", tc.out, got, tc.want)
		}
	}
}

// TestTheTransportMarkersAreSSHsOwnWords, and a machine's own failure is never one of them.
func TestTheTransportMarkersAreSSHsOwnWords(t *testing.T) {
	for _, said := range []string{
		"Host key verification failed.",
		"ssh: connect to host space port 22: Connection refused",
		"Permission denied (publickey).",
		"ssh: Could not resolve hostname potato: Name or service not known",
		"Connection closed by remote host",
		"kex_exchange_identification: read: Connection reset by peer",
	} {
		if _, ok := TransportFailure(said+"\n", errors.New("exit status 255")); !ok {
			t.Errorf("%q is not seen as a transport failure", said)
		}
	}
	for _, said := range []string{
		"go: go.mod requires go >= 1.26.5 (running go 1.22.2)",
		"/home/nova/.local/bin/sbcl: Permission denied",
		"GO OK go version go1.26.5 linux/amd64",
	} {
		if reason, ok := TransportFailure(said+"\n", errors.New("exit status 1")); ok {
			t.Errorf("%q was taken for a transport failure (%s); it is what the machine said about the work", said, reason)
		}
	}
}

// ---------------------------------------------------------------------------
// 3. no repair on a machine nobody reached
// ---------------------------------------------------------------------------

// TestTheFixNeverRunsOnAnUnreachableMachine: applying a remedy to a machine you could not
// reach is a second ssh to the same closed door, and a `changed=` line about a machine
// nobody has seen.
func TestTheFixNeverRunsOnAnUnreachableMachine(t *testing.T) {
	fixer := &fakeFixer{}
	bus := &fakeBus{}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: writeFile(t, "certs.tsv", ""),
		Remote: &fakeRemote{answers: map[string]remoteAnswer{
			"space": {out: "Host key verification failed.\n", err: errors.New("exit status 255")},
		}},
		Hash: "h", Now: fixedNow, Fix: true, Fixer: fixer, Bus: bus, Lane: "fleet",
	})
	all := out + errs
	if code != 3 {
		t.Fatalf("exit = %d, want 3\n%s", code, all)
	}
	if len(fixer.calls) != 0 {
		t.Errorf("the repair ran against an unreachable machine: %v", fixer.calls)
	}
	if len(bus.notes) != 0 {
		t.Errorf("an unreachable machine escalated as though its work had failed: %d notes", len(bus.notes))
	}
}

// ---------------------------------------------------------------------------
// 4. where the workload runs
// ---------------------------------------------------------------------------

// TestTheForgeWorkloadsRunFromTheCoordinator: `gh` is not on hulk, and the question "is the
// registry telling the truth about this machine" was never a question for the machine.
func TestTheForgeWorkloadsRunFromTheCoordinator(t *testing.T) {
	loads, err := StandardWorkloads()
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range loads {
		switch {
		case w.Forge != "" && w.Where != WhereCoordinator:
			t.Errorf("%s asks the forge and is marked where=%s; gh runs where the coordinator is", w.Class, w.Where)
		case w.Forge == "" && w.Where != WhereMachine:
			t.Errorf("%s runs on the machine and is marked where=%s", w.Class, w.Where)
		}
	}
}

// TestAnUnreachableMachineStillAnswersItsCoordinatorClasses: the forge knows whether a
// machine's runners are online whether or not anyone can ssh to it, and that answer is worth
// having on the day the machine is unreachable.
func TestAnUnreachableMachineStillAnswersItsCoordinatorClasses(t *testing.T) {
	certs := writeFile(t, "certs.tsv", "")
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs,
		Remote: &fakeRemote{answers: map[string]remoteAnswer{
			"space": {out: "ssh: connect to host space port 22: Connection refused\n", err: errors.New("exit status 255")},
		}},
		Forge: fakeForge{}, Repo: "mas-bandwidth/nova-tools",
		Hash: "h", Now: fixedNow,
	})
	all := out + errs
	if code != 3 {
		t.Fatalf("exit = %d, want 3\n%s", code, all)
	}
	if !strings.Contains(all, "CERTIFY space registry-truth OK") {
		t.Errorf("the coordinator's own class was not answered for an unreachable machine:\n%s", all)
	}
	rows, err := ReadCertificates(certs)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Class != "registry-truth" {
		t.Fatalf("rows = %v, want the one class the coordinator could answer", rows)
	}
	if rows[0].Build != "-" {
		t.Errorf("the row carries build %q; an unreachable machine's build is unknown, and `-` is how a fixed table says so", rows[0].Build)
	}
}

// ---------------------------------------------------------------------------
// 5. a dry run never says OK
// ---------------------------------------------------------------------------

// TestADryRunSaysWouldAndNeverOK: `CERTIFY OK machines=1 ok=14` for a run that reached
// nothing is the tool reporting fourteen passes it never made.
func TestADryRunSaysWouldAndNeverOK(t *testing.T) {
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: writeFile(t, "certs.tsv", ""),
		Remote: &fakeRemote{answers: benchOK()}, DryRun: true, Hash: "h", Now: fixedNow,
	})
	all := out + errs
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, all)
	}
	if !strings.Contains(all, "CERTIFY DRY-RUN machines=1 would=") {
		t.Errorf("the dry run's closing line is not a dry run's:\n%s", all)
	}
	if strings.Contains(all, "CERTIFY OK machines=") {
		t.Errorf("a dry run reported passes it never ran:\n%s", all)
	}
}

// ---------------------------------------------------------------------------
// 6. --status reads one file
// ---------------------------------------------------------------------------

// TestStatusNeedsOnlyTheCertificatesFile: it touches no machine, so it must not need the
// registry, the workloads or the provisioning standard to print what was recorded.
func TestStatusNeedsOnlyTheCertificatesFile(t *testing.T) {
	certs := writeFile(t, "certs.tsv", strings.Join([]string{
		"space\tv0.17.0\th\tgo-test\tOK\tGO OK\t2026-09-18T11:00:00Z",
		"hulk\tv0.17.0\th\tpath-resolves\tFAIL\tPATH -\t2026-09-18T11:00:00Z",
	}, "\n")+"\n")
	var out, errs strings.Builder
	code := Status(StatusInput{Certs: certs, Now: fixedNow, Stdout: &out, Stderr: &errs})
	all := out.String() + errs.String()
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (hulk's row is a FAIL)\n%s", code, all)
	}
	if !strings.Contains(all, "CERTIFY STATUS space go-test OK") {
		t.Errorf("the recorded pass is not printed:\n%s", all)
	}
	if !strings.Contains(all, "CERTIFY STATUS hulk path-resolves FAIL") {
		t.Errorf("the recorded failure is not printed:\n%s", all)
	}
	if strings.Contains(all, "REFUSED") {
		t.Errorf("--status refused although it reads one file:\n%s", all)
	}
}

// localSpaceAnswers is benchOK keyed on nothing but the class: the local runner is handed
// the same scripts with a target the fake answers for whatever it is.
func localSpaceAnswers() map[string]remoteAnswer {
	out := map[string]remoteAnswer{}
	for key, answer := range benchOK() {
		out[key] = answer
	}
	return out
}

// ---------------------------------------------------------------------------
// what Stella's read of 7e6309e found (bus note stella-b59d59aaef49)
// ---------------------------------------------------------------------------

// TestACoordinatorWorkloadWithABodyRunsHereAndNeverOpensAnSSH. The forge classes never
// touched ssh because they never ran a script at all; a CUSTOM workload that says
// `where: coordinator` and carries a body went straight down the ssh pipe, which is the
// whole of what `where` was added to prevent. The test uses a real card file, because the
// embedded set has no such workload and so could not see this.
func TestACoordinatorWorkloadWithABodyRunsHereAndNeverOpensAnSSH(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "gh-ready.card"), strings.Join([]string{
		"roles: bench",
		"expect: ^GH OK",
		"where: coordinator",
		"",
		"gh auth status >/dev/null 2>&1 && echo GH OK",
	}, "\n")+"\n")
	loads, err := ReadWorkloads(dir)
	if err != nil {
		t.Fatal(err)
	}
	ssh := &fakeRemote{answers: map[string]remoteAnswer{
		"space|build": {out: "nova-merge v0.17.0\n"},
	}}
	here := &fakeRemote{answers: map[string]remoteAnswer{
		"space|gh-ready": {out: "GH OK gh version 2.62.0\n"},
		"|gh-ready":      {out: "GH OK gh version 2.62.0\n"},
	}}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: writeFile(t, "certs.tsv", ""),
		Workloads: loads, Remote: ssh, Local: here, Hash: "h", Now: fixedNow,
	})
	all := out + errs
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, all)
	}
	for _, script := range ssh.scripts() {
		if scriptKey(script) == "gh-ready" {
			t.Errorf("a coordinator workload went down the ssh pipe:\n%s", script)
		}
	}
	if len(here.calls) == 0 {
		t.Error("the coordinator workload did not run here")
	}
	if !strings.Contains(all, "CERTIFY space gh-ready OK") {
		t.Errorf("the coordinator workload was not certified:\n%s", all)
	}
}

// TestAWorkloadThatRunsOutOfTimeIsITSOWNTokenAndNeverAFailure: `exec sleep 5` under a
// one-millisecond bound was called FAIL -- a verdict about the machine's work, from a run
// that never let the work finish. A timeout writes no certificate, is never repaired, and is
// counted apart, exactly like UNREACHABLE.
func TestAWorkloadThatRunsOutOfTimeIsItsOwnTokenAndNeverAFailure(t *testing.T) {
	certs := writeFile(t, "certs.tsv", "")
	fixer := &fakeFixer{changed: map[string][]string{ItemGitIdentity: nil}}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs,
		Remote: &fakeRemote{answers: timesOut(benchOK(), "space", "git-identity")},
		Hash:   "h", Now: fixedNow, Fix: true, Fixer: fixer, Bus: &fakeBus{}, Lane: "fleet",
	})
	all := out + errs
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (no answer is not a failure)\n%s", code, all)
	}
	if !strings.Contains(all, "CERTIFY space git-identity TIMEOUT reason=") {
		t.Errorf("a workload that ran out of time was not TIMEOUT:\n%s", all)
	}
	if strings.Contains(all, "git-identity FAIL") {
		t.Errorf("a timeout was written as a verdict about the machine's work:\n%s", all)
	}
	if !strings.Contains(all, "timeout=1") {
		t.Errorf("the closing line does not count the timeout:\n%s", all)
	}
	if len(fixer.calls) != 0 {
		t.Errorf("a timeout was repaired: %v", fixer.calls)
	}
	for _, r := range mustRead(t, certs) {
		if r.Class == "git-identity" {
			t.Errorf("a timeout wrote a certificate row: %+v", r)
		}
	}
}

// TestOneRunWritesOneRowPerMachineAndClass: the repair round certified a class a second
// time and APPENDED, so one run left two rows for one (machine, class) -- two records of one
// pass, and the earlier one a failure that never happened by the time the run ended. The
// record keeps the run's FINAL verdict, once.
func TestOneRunWritesOneRowPerMachineAndClass(t *testing.T) {
	answers := benchOK()
	failing(answers, "space", "git-identity", "GIT IDENTITY name=- email=-")
	certs := writeFile(t, "certs.tsv", "")
	fixer := &fakeFixer{
		changed: map[string][]string{ItemGitIdentity: nil},
		after:   func() { answers["space|git-identity"] = benchOK()["space|git-identity"] },
	}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs,
		Remote: &fakeRemote{answers: answers}, Hash: "h", Now: fixedNow,
		Fix: true, Fixer: fixer, Bus: &fakeBus{}, Lane: "fleet",
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s%s", code, out, errs)
	}
	seen := map[string]int{}
	for _, r := range mustRead(t, certs) {
		seen[r.Machine+"|"+r.Class]++
		if r.Class == "git-identity" && r.Verdict != VerdictOK {
			t.Errorf("the record holds the verdict before the repair: %+v", r)
		}
	}
	for key, n := range seen {
		if n != 1 {
			t.Errorf("one run wrote %d rows for %s; a run records one verdict per class", n, key)
		}
	}
}

// timesOut is one class of one machine that never answers inside the bound.
func timesOut(answers map[string]remoteAnswer, machine, class string) map[string]remoteAnswer {
	answers[machine+"|"+class] = remoteAnswer{err: context.DeadlineExceeded}
	return answers
}

// TestAWorkloadThatPrintedItsLineAndThenRanOutOfTimeIsTimeout is Stella's second read
// (stella-ee86e7fc3435), and it is the sharper half of the timeout: `echo PROOF OK` followed
// by `exec sleep 3` under a 100ms bound printed the expected line, ran out of time, and was
// recorded OK with a certificate behind it. The matched line was being accepted BEFORE the
// timeout was looked at, on the reasoning that a machine which printed the marker had been
// reached -- true, and beside the point. The work did not finish.
func TestAWorkloadThatPrintedItsLineAndThenRanOutOfTimeIsTimeout(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "proof.card"), strings.Join([]string{
		"roles: bench",
		"expect: ^PROOF OK",
		"",
		"echo PROOF OK",
		"exec sleep 3",
	}, "\n")+"\n")
	loads, err := ReadWorkloads(dir)
	if err != nil {
		t.Fatal(err)
	}
	certs := writeFile(t, "certs.tsv", "")
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs, Workloads: loads,
		Remote: &fakeRemote{answers: map[string]remoteAnswer{
			"space|build": {out: "nova-merge v0.17.0\n"},
			// The machine printed the line and then never came back.
			"space|proof": {out: "PROOF OK\n", err: context.DeadlineExceeded},
		}},
		Hash: "h", Now: fixedNow,
	})
	all := out + errs
	if code != 3 {
		t.Fatalf("exit = %d, want 3\n%s", code, all)
	}
	if !strings.Contains(all, "CERTIFY space proof TIMEOUT reason=") {
		t.Errorf("a workload that printed its line and then ran out of time was not TIMEOUT:\n%s", all)
	}
	if strings.Contains(all, "CERTIFY space proof OK") {
		t.Errorf("a half-finished workload was certified:\n%s", all)
	}
	if !strings.Contains(all, "timeout=1") {
		t.Errorf("the closing line counts no timeout:\n%s", all)
	}
	if rows := mustRead(t, certs); len(rows) != 0 {
		t.Errorf("a timed-out workload wrote %d certificate rows: %+v", len(rows), rows)
	}
}

// TestEveryAttemptLeavesItsEvidenceAsItHappens: holding the rows to one per (machine, class)
// is right for the RECORD and wrong for the TRAIL -- a run killed halfway through a machine
// used to leave nothing of what it had already learned. Every attempt appends its evidence
// as it happens, beside the certificates, so an interrupted pass is still readable.
func TestEveryAttemptLeavesItsEvidenceAsItHappens(t *testing.T) {
	answers := benchOK()
	failing(answers, "space", "git-identity", "GIT IDENTITY name=- email=-")
	certs := writeFile(t, "certs.tsv", "")
	attempts := filepath.Join(filepath.Dir(certs), "attempts.log")
	fixer := &fakeFixer{
		changed: map[string][]string{ItemGitIdentity: nil},
		after:   func() { answers["space|git-identity"] = benchOK()["space|git-identity"] },
	}
	_, _, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs,
		Remote: &fakeRemote{answers: answers}, Hash: "h", Now: fixedNow,
		Fix: true, Fixer: fixer, Bus: &fakeBus{}, Lane: "fleet",
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	trail := readFile(t, attempts)
	if trail == "" {
		t.Fatalf("no attempts trail at %s; a run killed mid-machine leaves nothing", attempts)
	}
	var identity []string
	for _, line := range strings.Split(strings.TrimSpace(trail), "\n") {
		if strings.Contains(line, "\tgit-identity\t") {
			identity = append(identity, line)
		}
	}
	if len(identity) != 2 {
		t.Fatalf("the trail holds %d git-identity attempts, want 2 (the failure and the proof after the repair):\n%s", len(identity), trail)
	}
	if !strings.Contains(identity[0], VerdictFail) || !strings.Contains(identity[0], "email=-") {
		t.Errorf("the first attempt's evidence is not on the trail: %q", identity[0])
	}
	if !strings.Contains(identity[1], VerdictOK) {
		t.Errorf("the second attempt is not on the trail: %q", identity[1])
	}
	// And the record itself still holds ONE row for that class.
	rows := 0
	for _, r := range mustRead(t, certs) {
		if r.Class == "git-identity" {
			rows++
		}
	}
	if rows != 1 {
		t.Errorf("the record holds %d git-identity rows, want 1", rows)
	}
}

// TestAForgeThatCannotBeReadIsUnreachableAndNeverAFailure: the same rule as ssh. "gh could
// not answer" says nothing about whether a machine's runners are online, and a FAIL for it
// withholds a certificate on the strength of this tool's own trouble.
func TestAForgeThatCannotBeReadIsUnreachableAndNeverAFailure(t *testing.T) {
	certs := writeFile(t, "certs.tsv", "")
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "hulk", Certs: certs,
		Remote: &fakeRemote{answers: map[string]remoteAnswer{
			"hulk": {out: "nova-merge v0.17.0\nEVERYTHING OK PONG ready online\n"},
		}},
		Forge: fakeForge{err: errors.New("gh: HTTP 401: Bad credentials")},
		Repo:  "mas-bandwidth/nova-tools", Hash: "h", Now: fixedNow,
	})
	all := out + errs
	if !strings.Contains(all, "CERTIFY hulk registry-truth UNREACHABLE reason=") {
		t.Errorf("a forge that could not be read was not UNREACHABLE:\n%s", all)
	}
	if strings.Contains(all, "registry-truth FAIL") || strings.Contains(all, "runner-online FAIL") {
		t.Errorf("a forge failure was written as a verdict about the machine:\n%s", all)
	}
	for _, r := range mustRead(t, certs) {
		if r.Class == "registry-truth" || r.Class == "runner-online" {
			t.Errorf("a forge failure wrote a row: %+v", r)
		}
	}
	if code == 0 {
		t.Errorf("exit = 0 although two classes could not be answered:\n%s", all)
	}
}

// TestTheStandardHashIsTheSameFromTheFileAndFromTheEmbeddedCopy: the launchd job runs outside
// any clone, so `--standard` must have a default -- and a default that hashed to something
// else would expire the whole fleet every time the loop ran.
func TestTheStandardHashIsTheSameFromTheFileAndFromTheEmbeddedCopy(t *testing.T) {
	body := "echo standard v1\n"
	path := writeFile(t, "bench-standard.sh", body)
	loads, err := StandardWorkloads()
	if err != nil {
		t.Fatal(err)
	}
	fromFile, err := StandardHash(path, loads)
	if err != nil {
		t.Fatal(err)
	}
	fromBytes, err := StandardHashFrom([]byte(body), loads)
	if err != nil {
		t.Fatal(err)
	}
	if fromFile != fromBytes {
		t.Errorf("the hash of the file is %s and of the same bytes %s; a certificate written by the loop would expire the fleet", fromFile, fromBytes)
	}
}
