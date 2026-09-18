package release

// `release adopt --certify`: an adopt changes the build on every machine it touches, so the
// moment it lands every certificate those machines held stops being current. These tests
// hold the renewal that closes it, and the refusal that stops half of it being asked for.
//
// The fake ssh here is STRICT in the way the real one is: it decodes what was actually sent
// and answers per workload, and refuses an argv it does not recognise. A fake that answered
// the same string to every command would have let a certification that sent nothing pass.

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// certifySSH is fakeSSH plus the one thing certification needs: it reads the script back out
// of the base64 argv and answers per workload class.
type certifySSH struct {
	fakeSSH
	perClass map[string]string
	scripts  []string
}

func (s *certifySSH) Run(ctx context.Context, machine string, argv []string) (string, error) {
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "openssl base64 -d") {
		return s.fakeSSH.Run(ctx, machine, argv)
	}
	s.fakeSSH.runs = append(s.fakeSSH.runs, machine+": <certify script>")
	var script string
	for _, a := range argv {
		if raw, err := base64.StdEncoding.DecodeString(a); err == nil && len(raw) > 0 {
			script = string(raw)
		}
	}
	if script == "" {
		return "", errNoReceipt
	}
	s.scripts = append(s.scripts, script)
	class := ""
	for _, line := range strings.Split(script, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "# nova-certify workload "); ok {
			class = strings.TrimSpace(v)
			break
		}
	}
	answer, ok := s.perClass[class]
	if !ok {
		// What a machine says about a command it has no answer for; never a cheerful "".
		return "sh: " + class + ": command not found\n", errNoReceipt
	}
	return answer, nil
}

func certifyRegistry(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machines.tsv")
	body := "hulk\thulk\tlinux/x64\tbench\tswarm-hulk\t64\t-\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func certifyPaths(t *testing.T) (registry, certs, standard string) {
	t.Helper()
	dir := t.TempDir()
	certs = filepath.Join(dir, "certs.tsv")
	standard = filepath.Join(dir, "bench-standard.sh")
	if err := os.WriteFile(standard, []byte("echo STANDARD OK\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return certifyRegistry(t), certs, standard
}

// TestAdoptWithCertifyRunsTheWorkloadsOnEachAdoptedMachineUnderTheVersionJustInstalled.
func TestAdoptWithCertifyRunsTheWorkloadsOnEachAdoptedMachineUnderTheVersionJustInstalled(t *testing.T) {
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	registry, certs, standard := certifyPaths(t)
	s := &certifySSH{
		fakeSSH: fakeSSH{answer: map[string]string{
			"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n",
		}},
		perClass: map[string]string{
			"go-test":        "GO OK go version go1.26.5 linux/amd64 ok 0.4s\n",
			"c-build":        "C OK cc (GCC) 13.2.0\n",
			"cpp-build":      "CPP OK c++ (GCC) 13.2.0\n",
			"sbcl":           "SBCL OK SBCL 2.5.8\n",
			"git-push":       "GIT PUSH OK head=deadbeef git=git version 2.43.0\n",
			"path-resolves":  "PATH OK /home/nova/.local/bin/nova-merge v0.16.0\n",
			"go-on-path":     "GO PATH OK /home/gaffer/go/bin/go go version go1.26.5 linux/amd64\n",
			"git-identity":   "GIT IDENTITY OK Rowan Claude <rowan@mas-bandwidth.com>\n",
			"wall-toolchain": "WALL TOOLCHAIN OK go version go1.26.5 linux/amd64\n",
			"services-reach": "SERVICES OK name=space addr=100.115.99.19 redis=PONG loki=ready\n",
		},
	}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{
		"adopt", "--version", "v0.16.0", "--machines", machinesFile(t, "hulk\n"),
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/home/nova/.local/bin",
		"--dest", "/home/nova/nova-bench/build", "--platform", "linux-amd64",
		"--certify", registry, "--certs", certs, "--standard", standard,
	}, &o, &e, Deps{SSH: s})
	if code != 0 {
		t.Fatalf("code = %d\nstdout:%s\nstderr:%s", code, o.String(), e.String())
	}
	if !strings.Contains(o.String(), "CERTIFY hulk go-test OK") {
		t.Fatalf("the adopted machine was not certified:\n%s", o.String())
	}
	if !strings.Contains(o.String(), "CERTIFY OK machines=1 ok=10 fail=0 warn=0") {
		t.Fatalf("no closing certification line:\n%s\n%s", o.String(), e.String())
	}
	// An adopt has no runner list to read, so the forge classes are SKIPPED and left
	// uncertified rather than failed: "this tool could not ask" is not "this machine is
	// wrong", and the loop's own certify carries a forge.
	if !strings.Contains(e.String(), "CERTIFY NOTE machine=hulk class=registry-truth skipped=no-forge") {
		t.Errorf("the forge class was not skipped with its reason:\n%s", e.String())
	}
	rows, err := fleet.ReadCertificates(certs)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 10 {
		t.Fatalf("wrote %d certificate rows, want 10", len(rows))
	}
	for _, r := range rows {
		// THE POINT: the build on the row is the one this verb just put there, not the one
		// the machine was running when the run began.
		if r.Build != "v0.16.0" {
			t.Errorf("%s carries build %q, want the version just installed", r.Class, r.Build)
		}
	}
	// And the certification really did go through the wall for the classes that ask for it.
	wall := false
	for _, script := range s.scripts {
		if strings.Contains(script, "# nova-certify workload go-test") && strings.Contains(script, "nova-sandbox") {
			wall = true
		}
	}
	if !wall {
		t.Error("the go-test certification did not go through nova-sandbox")
	}
}

// TestAdoptWithCertifyFailsTheMachineWhoseWorkloadFailed: a machine that installed and then
// could not do the work is not an adopted machine anybody should launch a card onto.
func TestAdoptWithCertifyFailsTheMachineWhoseWorkloadFailed(t *testing.T) {
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	registry, certs, standard := certifyPaths(t)
	s := &certifySSH{
		fakeSSH:  fakeSSH{answer: map[string]string{"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n"}},
		perClass: map[string]string{"go-test": "go: go.mod requires go >= 1.26.5 (running go 1.22.2)\n"},
	}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{
		"adopt", "--version", "v0.16.0", "--machines", machinesFile(t, "hulk\n"),
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/home/nova/.local/bin",
		"--dest", "/home/nova/nova-bench/build", "--platform", "linux-amd64",
		"--certify", registry, "--certs", certs, "--standard", standard,
	}, &o, &e, Deps{SSH: s})
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(e.String(), "CERTIFY hulk go-test FAIL") {
		t.Fatalf("the failure is not named:\n%s", e.String())
	}
	if !strings.Contains(e.String(), "go.mod requires go >= 1.26.5") {
		t.Fatalf("the failure does not carry what the machine said:\n%s", e.String())
	}
	if fleet.Certified(mustCerts(t, certs), "hulk", "go-test", "v0.16.0", "") {
		t.Error("a FAIL was written as a certificate")
	}
}

// TestAdoptRefusesHalfOfTheCertifyFlags names every missing one at once, the same law the
// rest of this package's refusals keep.
func TestAdoptRefusesHalfOfTheCertifyFlags(t *testing.T) {
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	registry, _, _ := certifyPaths(t)
	var o, e bytes.Buffer
	code := Run("nova-update", []string{
		"adopt", "--version", "v0.16.0", "--machines", machinesFile(t, "hulk\n"),
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/home/nova/.local/bin",
		"--dest", "/home/nova/nova-bench/build", "--platform", "linux-amd64",
		"--certify", registry,
	}, &o, &e, Deps{SSH: &fakeSSH{}})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	for _, want := range []string{"--certs", "--standard"} { // named together or not at all
		if !strings.Contains(e.String(), want) {
			t.Errorf("the refusal does not name %s:\n%s", want, e.String())
		}
	}
}

// TestAdoptWaivedByNoCertifyCertifiesNothingAndSaysSo: the waiver is the whole switch, and
// an adopt that waived certification must not reach a machine for anything but the install --
// and must say `certified=waived` on its verdict, because a waived check that is silent is a
// check that was never there.
func TestAdoptWaivedByNoCertifyCertifiesNothingAndSaysSo(t *testing.T) {
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	s := &certifySSH{fakeSSH: fakeSSH{answer: map[string]string{"hulk": "RELEASE INSTALLED version=v0.16.0 tools=2 skipped=0\n"}}}
	var o, e bytes.Buffer
	if code := Run("nova-update", []string{
		"adopt", "--version", "v0.16.0", "--machines", machinesFile(t, "hulk\n"),
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/home/nova/.local/bin",
		"--dest", "/home/nova/nova-bench/build", "--platform", "linux-amd64", "--no-certify",
	}, &o, &e, Deps{SSH: s}); code != 0 {
		t.Fatalf("code = %d: %s", code, e.String())
	}
	if len(s.scripts) != 0 {
		t.Errorf("a waived adopt sent %d certification scripts", len(s.scripts))
	}
	if strings.Contains(o.String()+e.String(), "CERTIFY") {
		t.Errorf("a waived adopt spoke about certification:\n%s%s", o.String(), e.String())
	}
	if !strings.Contains(o.String(), "certified=waived") {
		t.Errorf("the waiver is not on the verdict line:\n%s", o.String())
	}
}

// TestAdoptRefusesWhenNeitherCertifiedNorWaived is what "on by default" means where paths may
// never be guessed: an adopt that says nothing about certification is refused, with both
// roads on the line.
func TestAdoptRefusesWhenNeitherCertifiedNorWaived(t *testing.T) {
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	var o, e bytes.Buffer
	code := Run("nova-update", []string{
		"adopt", "--version", "v0.16.0", "--machines", machinesFile(t, "hulk\n"),
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/home/nova/.local/bin",
		"--dest", "/home/nova/nova-bench/build", "--platform", "linux-amd64",
	}, &o, &e, Deps{SSH: &certifySSH{}})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	for _, want := range []string{"--certify", "--certs", "--standard", "--no-certify"} {
		if !strings.Contains(e.String(), want) {
			t.Errorf("the refusal does not name %s:\n%s", want, e.String())
		}
	}
}

// TestNoCertifyAndCertifyTogetherIsARefusal: one asks for it and the other waives it.
func TestNoCertifyAndCertifyTogetherIsARefusal(t *testing.T) {
	from := built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update")
	registry, certs, standard := certifyPaths(t)
	var o, e bytes.Buffer
	code := Run("nova-update", []string{
		"adopt", "--version", "v0.16.0", "--machines", machinesFile(t, "hulk\n"),
		"--ssh", "/usr/bin/ssh", "--from", from, "--bin", "/home/nova/.local/bin",
		"--dest", "/home/nova/nova-bench/build", "--platform", "linux-amd64",
		"--certify", registry, "--certs", certs, "--standard", standard, "--no-certify",
	}, &o, &e, Deps{SSH: &certifySSH{}})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(e.String(), "--no-certify waives certification") {
		t.Errorf("the refusal does not say the two disagree:\n%s", e.String())
	}
}

func mustCerts(t *testing.T, path string) []fleet.Certificate {
	t.Helper()
	certs, err := fleet.ReadCertificates(path)
	if err != nil {
		t.Fatal(err)
	}
	return certs
}
