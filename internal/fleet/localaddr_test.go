package fleet

// THE AIR CERTIFYING THE AIR. Measured on 2026-09-18, the first run of this verb ON the M2
// Air, for the machine the M2 Air IS:
//
//	CERTIFY air go-test UNREACHABLE reason="Host key verification failed."
//	... twelve of them ...
//
// The local transport exists exactly so a machine never proves itself over a transport it
// does not need -- and it did not fire, because the Air's registry row names it by its
// TAILNET ADDRESS (`air  glenn@100.117.59.68`) while the machine calls itself `macbook`.
// Neither the registry name nor the ssh target is any spelling of the host name, so a fleet
// machine whose row carries an address could never certify itself.
//
// Two faults, and this is the test for both: the addresses this machine answers on are part
// of "is this me", and an IPv4 literal is not a dotted host name to be cut at the first dot
// (`100.117.59.68` shortened to `100`, which would have matched a machine called `100`).

import (
	"strings"
	"testing"
)

// TestAMachineNamedByItsAddressIsStillThisMachine is the Air's own row, as a test.
func TestAMachineNamedByItsAddressIsStillThisMachine(t *testing.T) {
	air := Machine{Name: "air", SSH: "glenn@100.117.59.68"}
	addrs := []string{"127.0.0.1", "100.117.59.68", "fe80::1"}
	if !IsLocalMachineAt(air, "macbook.local", addrs) {
		t.Error("the Air is not seen as itself; twelve classes came back UNREACHABLE over an ssh to its own address")
	}
	// And it is still not every other machine.
	hulk := Machine{Name: "hulk", SSH: "hulk"}
	if IsLocalMachineAt(hulk, "macbook.local", addrs) {
		t.Error("another machine was taken for this one")
	}
	// No addresses is the old behaviour, exactly: the host name comparison and nothing else.
	if IsLocalMachineAt(air, "macbook.local", nil) {
		t.Error("with no addresses, an address row must not match a host name")
	}
	if !IsLocalMachineAt(Machine{Name: "space", SSH: "nova@space"}, "space.tail1234.ts.net", nil) {
		t.Error("the host-name comparison stopped working")
	}
}

// TestAnIPv4LiteralIsNotCutAtTheFirstDot: `100.117.59.68` is not a host called `100`.
func TestAnIPv4LiteralIsNotCutAtTheFirstDot(t *testing.T) {
	if IsLocalMachineAt(Machine{Name: "air", SSH: "glenn@100.117.59.68"}, "100", nil) {
		t.Error("an address was cut at the first dot and matched a machine called 100")
	}
	if !IsLocalMachineAt(Machine{Name: "air", SSH: "glenn@100.117.59.68"}, "100.117.59.68", nil) {
		t.Error("a machine whose host name IS the address is not seen as itself")
	}
}

// TestTheAirRunsItsOwnWorkloadsWithNoSSH is the whole run, through the fakes: with the Air's
// own address given, every class goes through the local runner and no ssh is opened.
func TestTheAirRunsItsOwnWorkloadsWithNoSSH(t *testing.T) {
	reg := writeFile(t, "machines.tsv", strings.Join([]string{
		"# name\tssh\tos/arch\troles\tseat\tcores\tnotes",
		"air\tglenn@100.117.59.68\tdarwin/arm64\tbench\tswarm-air\t8\tthe M2 Air, a bench while it is up",
	}, "\n")+"\n")
	remote := &fakeRemote{answers: map[string]remoteAnswer{}}
	local := &fakeRemote{answers: airBenchAnswers()}
	out, errs, code := runCertify(t, CertifyInput{
		Machines: reg, Only: "air", Certs: writeFile(t, "certs.tsv", ""),
		Remote: remote, Local: local, LocalHost: "macbook.local",
		LocalAddrs: []string{"100.117.59.68"},
		Hash:       "h", Now: fixedNow,
	})
	all := out + errs
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, all)
	}
	if len(remote.calls) != 0 {
		t.Errorf("the Air was reached by ssh %d times; it is the machine running this", len(remote.calls))
	}
	if !strings.Contains(all, "CERTIFY NOTE machine=air transport=local") {
		t.Errorf("nothing says the machine was run locally:\n%s", all)
	}
	if strings.Contains(all, "UNREACHABLE") {
		t.Errorf("a machine certifying itself reported itself unreachable:\n%s", all)
	}
}

// airBenchAnswers is what the Air actually said on 2026-09-18, keyed on its ssh target so
// the fake answers whichever transport was chosen.
func airBenchAnswers() map[string]remoteAnswer {
	const target = "glenn@100.117.59.68|"
	return map[string]remoteAnswer{
		target + "build":          {out: "nova-merge v0.16.0-dev.2b6d6ba5 darwin/arm64\n"},
		target + "go-test":        {out: "GO OK go version go1.27.1 darwin/arm64 ok novacertify 0.257s\n"},
		target + "c-build":        {out: "C OK Apple clang version 21.0.0\n"},
		target + "cpp-build":      {out: "CPP OK Apple clang version 21.0.0\n"},
		target + "sbcl":           {out: "SBCL OK SBCL 2.6.8\n"},
		target + "git-push":       {out: "GIT PUSH OK head=26fa6ed1 git=git version 2.50.1\n"},
		target + "path-resolves":  {out: "PATH OK /Users/glenn/.local/bin/nova-merge v0.16.0-dev.2b6d6ba5\n"},
		target + "go-on-path":     {out: "GO PATH OK /opt/homebrew/bin/go go version go1.27.1 darwin/arm64\n"},
		target + "git-identity":   {out: "GIT IDENTITY OK Glenn Fiedler <glenn@mas-bandwidth.com>\n"},
		target + "wall-toolchain": {out: "WALL TOOLCHAIN OK go version go1.27.1 darwin/arm64\n"},
		target + "services-reach": {out: "SERVICES OK name=space addr=69.67.149.151 redis=PONG loki=ready\n"},
	}
}
