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
	"errors"
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
