package fleet

// WHAT THE MACHINE SAID IS A VERDICT, EVEN WHEN IT CONTAINS SSH'S OWN WORDS.
//
// Measured on the M2 Air, 2026-09-18, the run after the darwin workloads were made portable:
//
//	CERTIFY air services-reach UNREACHABLE reason="SERVICES FAIL redis name=space
//	addr=69.67.149.151 by=dscacheutil:6379 refused: nothing is listening on that address
//	(check the bind line, not the name) -- Could not connect to Redis at
//	69.67.149.151:6379: Connection refused"
//
// The machine answered. It answered in its own class's words, with the address it tried and
// the remedy, and the answer was thrown away and no row was written -- because `connection
// refused` is one of ssh's own markers and the marker scan reads the whole output. The
// machinery that exists so a transport failure is never a verdict had started turning a
// verdict into a transport failure, which is the same lie in the other direction: a genuine
// fault of the fleet (redis on space is not reachable from the Air) disappeared into a
// count of machines nobody could reach.
//
// THE RULE: every workload's expect names the token its class speaks in -- `^SERVICES OK`,
// `^GO OK`, `^RUNNER PATH OK`. A line that begins with that token came from the body, so the
// machine was reached and it answered, and nothing after that is a transport question.
//
// And the second half: the FORGE not answering is not a verdict either. `gh` on the Air is
// not authenticated for this repository, and the two forge classes were written down as
// FAIL -- a judgement on a machine, from a question nobody managed to ask.

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

// TestTheClassTokenIsReadOffTheExpect.
func TestTheClassTokenIsReadOffTheExpect(t *testing.T) {
	for expect, want := range map[string]string{
		"^SERVICES OK":       "SERVICES",
		"^GO OK":             "GO",
		"^RUNNER PATH OK":    "RUNNER",
		"^WALL TOOLCHAIN OK": "WALL",
		"^C OK":              "C",
		"OK":                 "", // no anchor is no promise about the start of a line
		"^[A-Z]+ OK":         "", // and neither is a pattern
	} {
		if got := AnswerToken(regexp.MustCompile("(?m)" + expect)); got != want {
			t.Errorf("AnswerToken(%q) = %q, want %q", expect, got, want)
		}
	}
}

// TestAnAnswerInTheClassTokenIsNeverATransportFailure is the Air's line, as a test.
func TestAnAnswerInTheClassTokenIsNeverATransportFailure(t *testing.T) {
	answers := benchOK()
	answers["space|services-reach"] = remoteAnswer{
		out: "SERVICES FAIL redis name=space addr=69.67.149.151 by=dscacheutil:6379 refused: " +
			"nothing is listening on that address (check the bind line, not the name) -- " +
			"Could not connect to Redis at 69.67.149.151:6379: Connection refused\n",
		err: errors.New("exit status 1"),
	}
	certs := writeFile(t, "certs.tsv", "")
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: certs,
		Remote: &fakeRemote{answers: answers}, Hash: "h", Now: fixedNow,
	})
	all := out + errs
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (the machine answered, and its answer is a FAIL)\n%s", code, all)
	}
	if strings.Contains(all, "services-reach UNREACHABLE") {
		t.Errorf("the machine's own answer was read as ssh failing:\n%s", all)
	}
	if !strings.Contains(all, "CERTIFY space services-reach FAIL evidence=") {
		t.Errorf("no FAIL line carrying what the machine said:\n%s", all)
	}
	// And the row must exist: a verdict thrown away is a fault of the fleet nobody records.
	rows := mustRead(t, certs)
	found := false
	for _, r := range rows {
		if r.Class == "services-reach" {
			found = true
			if !strings.Contains(r.Evidence, "69.67.149.151") {
				t.Errorf("the row does not carry the address that was tried: %q", r.Evidence)
			}
		}
	}
	if !found {
		t.Error("no services-reach row was written; an answer nobody records is an answer nobody acts on")
	}
	// The other direction still holds, and it is the more important one: ssh's own words,
	// with NOTHING from the class, are still never a verdict.
	answers["space|services-reach"] = remoteAnswer{
		out: "ssh: connect to host space port 22: Connection refused\n",
		err: errors.New("exit status 255"),
	}
	out, errs, code = runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "space", Certs: writeFile(t, "certs2.tsv", ""),
		Remote: &fakeRemote{answers: answers}, Hash: "h", Now: fixedNow,
	})
	if all = out + errs; !strings.Contains(all, "services-reach UNREACHABLE") {
		t.Errorf("a real transport failure stopped being UNREACHABLE (exit %d):\n%s", code, all)
	}
}

// TestTheForgeNotAnsweringIsNotAVerdictAboutAMachine: `gh` unauthenticated on the machine
// running the verb says nothing whatever about whether the Air's two runners are online.
func TestTheForgeNotAnsweringIsNotAVerdictAboutAMachine(t *testing.T) {
	certs := writeFile(t, "certs.tsv", "")
	out, errs, code := runCertify(t, CertifyInput{
		Machines: testRegistry(t), Only: "batman", Certs: certs,
		Remote: &fakeRemote{answers: map[string]remoteAnswer{
			"batman":               {out: "nova-merge v0.17.0\n"},
			"batman|runner-path":   {out: "RUNNER PATH OK runners=6 units=6 agents=6 listeners=6 go>=go1.26.5\n"},
			"batman|diag-size":     {out: "DIAG OK 3 MB across 6 _diag directories, oldest 2d, about 1 MB/day\n"},
			"batman|path-resolves": {out: "PATH OK /Users/u/.local/bin/nova-merge v0.17.0\n"},
			"batman|go-on-path":    {out: "GO PATH OK /opt/homebrew/bin/go go version go1.27.1 darwin/amd64\n"},
		}},
		Forge: failingForge{}, Repo: "mas-bandwidth/nova-tools",
		Hash: "h", Now: fixedNow,
	})
	all := out + errs
	if strings.Contains(all, "runner-online FAIL") || strings.Contains(all, "registry-truth FAIL") {
		t.Errorf("a forge nobody could ask was written down as a verdict about the machine:\n%s", all)
	}
	if !strings.Contains(all, "runner-online UNREACHABLE") {
		t.Errorf("the forge failure is not reported as this tool being unable to ask:\n%s", all)
	}
	if code != 3 {
		t.Errorf("exit = %d, want 3 (nothing failed; something could not be asked)\n%s", code, all)
	}
	for _, r := range mustRead(t, certs) {
		if r.Class == "runner-online" || r.Class == "registry-truth" {
			t.Errorf("a row was written for %s although the forge never answered: %+v", r.Class, r)
		}
	}
}

// failingForge is `gh` on a machine that is not the coordinator.
type failingForge struct{}

func (failingForge) Runners(string) ([]RunnerStatus, error) {
	return nil, errors.New("gh api repos/mas-bandwidth/nova-tools/actions/runners --jq .runners: exit status 1")
}
