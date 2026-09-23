package main

// nova-tools#2222: `nova-pulse accept --job` is the real gate the selftest's seeds are
// run through (SPEC-TOOLWORK §1 rules 1-5). Each case builds the shipped fixture
// repository, makes one change to it, and reads the one verdict line back through
// run(), the verb's public entry point. Before this card the verb did not exist.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// acceptTestJob builds the fixture into a fresh directory and returns the job clone and
// a cert file for it.
func acceptTestJob(t *testing.T) (job, cert string) {
	t.Helper()
	dir := t.TempDir()
	job = filepath.Join(dir, "job")
	if _, _, err := acceptBuildFixture(context.Background(), job); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	cert = acceptTestCert(t, "test", time.Now().Add(24*time.Hour))
	return job, cert
}

// acceptTestCert writes a certify record (SPEC-TOOLWORK §2 rule 4's CERTIFY OK line)
// for bench whose until= is until and at= 24 hours before it, carrying this build and
// the host's go version (rule 6), and returns its path.
func acceptTestCert(t *testing.T, bench string, until time.Time) string {
	t.Helper()
	return acceptTestCertAt(t, bench, until.Add(-24*time.Hour), until)
}

// acceptTestCertAt is acceptTestCert with at= given.
func acceptTestCertAt(t *testing.T, bench string, at, until time.Time) string {
	t.Helper()
	goVersion := acceptHostGo(context.Background())
	if goVersion == "" {
		t.Fatal("go env GOVERSION answered nothing")
	}
	cert := filepath.Join(t.TempDir(), "cert")
	line := "CERTIFY OK   bench=" + bench + " cert=0123456789ab legs=go failed=none absent=none wall=seatbelt build=" + buildVersion() +
		" go=" + goVersion + " at=" + at.UTC().Format(time.RFC3339) + " until=" + until.UTC().Format(time.RFC3339) + "\n"
	if err := os.WriteFile(cert, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return cert
}

// acceptTestWeakenPositive changes the fixture's Sign for a positive n and rewrites the
// pre-existing TestSignPositive to expect the new value: no Skip is added and no base
// Test disappears, so only the base copy of sign_test.go run over the head can see it.
func acceptTestWeakenPositive(t *testing.T, job string) {
	t.Helper()
	acceptTestEdit(t, job, "sign.go", func(s string) string { return strings.Replace(s, "\t\treturn 1\n", "\t\treturn 2\n", 1) })
	acceptTestEdit(t, job, "sign_test.go", func(s string) string { return strings.Replace(s, "Sign(5), 1)", "Sign(5), 2)", 1) })
}

// acceptTestRun runs the gate on job with the given card text and returns the exit
// code and the verdict line.
func acceptTestRun(t *testing.T, job, cert, card string) (int, string) {
	t.Helper()
	cardPath := filepath.Join(t.TempDir(), "card.txt")
	if err := os.WriteFile(cardPath, []byte(card), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"accept", "--job", job, "--card", cardPath, "--base", "base",
		"--bench", "test", "--cert", cert, "--identity", "Nova Fixture <fixture@example.invalid>"},
		&out, &errb, time.Now().UTC())
	return code, strings.TrimSpace(out.String() + errb.String())
}

// acceptTestEdit rewrites one file of the job with edit and commits it as the fixture.
func acceptTestEdit(t *testing.T, job, file string, edit func(string) string) {
	t.Helper()
	p := filepath.Join(job, file)
	raw, _ := os.ReadFile(p)
	after := edit(string(raw))
	if after == string(raw) {
		t.Fatalf("edit of %s changed nothing", file)
	}
	if err := os.WriteFile(p, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptFixtureCommit(context.Background(), job, "edit "+file); err != nil {
		t.Fatal(err)
	}
}

func acceptTestCard(t *testing.T) string {
	t.Helper()
	card, err := acceptFixtureCard()
	if err != nil {
		t.Fatal(err)
	}
	return card
}

// TestAcceptKnownGoodFixIsOK is the fixture's known-good fix: ACCEPT OK, exit 0. It is
// also rule 4(d)'s pin (TestAcceptDoesNotCallAPreExistingGreenVacuous, PR #1721): the
// fixture's untouched TestSignPositive stays green under the revert and is not charged.
func TestAcceptKnownGoodFixIsOK(t *testing.T) {
	t.Parallel()
	job, cert := acceptTestJob(t)
	code, line := acceptTestRun(t, job, cert, acceptTestCard(t))
	if code != 0 || !strings.HasPrefix(line, "ACCEPT OK ") {
		t.Fatalf("known-good fix: exit=%d line=%q, want ACCEPT OK exit 0", code, line)
	}
	for _, want := range []string{"label=accept-fixture ", "kind=fix-red ", "red_without=1 ", "bench=test ", "control="} {
		if !strings.Contains(line, want) {
			t.Fatalf("ACCEPT OK line missing %q: %q", want, line)
		}
	}
}

// TestAcceptRejectsWithTheFirstFailingToken drives one defect per case through the gate
// and asserts the token, and nothing else, comes back on an ACCEPT REJECT line, exit 1.
func TestAcceptRejectsWithTheFirstFailingToken(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, want string
		edit       func(t *testing.T, job string)
		card       func(string) string
	}{
		{name: "fix reverted", want: "reason=red-at-head at=TestSignZero", edit: func(t *testing.T, job string) {
			acceptTestEdit(t, job, "sign.go", func(s string) string { return strings.Replace(s, "\treturn 0\n", "\treturn -1\n", 1) })
		}},
		{name: "vacuous", want: "reason=vacuous-test at=TestSignZero", edit: func(t *testing.T, job string) {
			acceptTestEdit(t, job, "sign_test.go", func(s string) string { return strings.Replace(s, "Sign(0), 0)", "Sign(0), Sign(0))", 1) })
		}},
		{name: "skip on a base test", want: "reason=test-weakened at=TestSignPositive", edit: func(t *testing.T, job string) {
			acceptTestEdit(t, job, "sign_test.go", func(s string) string {
				return strings.Replace(s, "func TestSignPositive(t *testing.T) {\n", "func TestSignPositive(t *testing.T) {\n\tt.Skip(\"seeded\")\n", 1)
			})
		}},
		{name: "base test weakened without a skip", want: "reason=test-weakened at=TestSignPositive", edit: acceptTestWeakenPositive},
		{name: "renamed", want: "reason=named-test-missing at=TestSignZero", edit: func(t *testing.T, job string) {
			acceptTestEdit(t, job, "sign_test.go", func(s string) string { return strings.Replace(s, "TestSignZero", "TestSignNil", 1) })
		}},
		{name: "out of path", want: "reason=out-of-path at=README", edit: func(t *testing.T, job string) {
			acceptTestEdit(t, job, "README", func(s string) string { return s + "one more line\n" })
		}},
		{name: "vet", want: "reason=vet at=sign.go:18", edit: func(t *testing.T, job string) {
			acceptTestEdit(t, job, "sign.go", func(s string) string { return strings.Replace(s, "=%d\"", "=%s\"", 1) })
		}},
		{name: "wrong name", want: "reason=named-test-not-red at=TestSignPositive",
			card: func(c string) string { return strings.Replace(c, "TEST: TestSignZero", "TEST: TestSignPositive", 1) }},
		{name: "wrong author", want: "reason=identity", edit: func(t *testing.T, job string) {
			cmd := exec.Command("git", "-c", "user.name=Someone Else", "-c", "user.email=else@example.invalid",
				"commit", "-q", "--amend", "--no-edit", "--reset-author", "--no-verify")
			cmd.Dir = job
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("re-author: %v %s", err, out)
			}
		}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			job, cert := acceptTestJob(t)
			if c.edit != nil {
				c.edit(t, job)
			}
			card := acceptTestCard(t)
			if c.card != nil {
				card = c.card(card)
			}
			code, line := acceptTestRun(t, job, cert, card)
			if code != 1 || !strings.HasPrefix(line, "ACCEPT REJECT ") || !strings.Contains(line, " "+c.want+" ") {
				t.Fatalf("%s: exit=%d line=%q, want ACCEPT REJECT with %q exit 1", c.name, code, line, c.want)
			}
		})
	}
}

// TestAcceptAbstainsOnAnUncertifiedBench: a certification record that is missing,
// stale, for another bench, failing the go leg or not a CERTIFY OK record at all is the
// bench's fault, not the card's: ABSTAIN, exit 2, and no verdict about the card
// (SPEC-TOOLWORK §1 rule 3, §2 rules 4-6). Rule 6's voids are named cases: an until= more
// than 24 hours after at= (a-cert-past-until-is-void's lifetime half),
// cert-voids-on-build-change, a-changed-go-version-voids-the-cert; and rule 7's
// wall-none-is-uncertified-for-a-code-card. Each names the check that fired on stderr.
func TestAcceptAbstainsOnAnUncertifiedBench(t *testing.T) {
	t.Parallel()
	later := time.Now().Add(24 * time.Hour)
	rewrite := func(t *testing.T, from, to string) string {
		p := acceptTestCert(t, "test", later)
		raw, _ := os.ReadFile(p)
		if !strings.Contains(string(raw), from) {
			t.Fatalf("cert has no %q", from)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(raw), from, to, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name, why string
		cert      func(t *testing.T) string
	}{
		{"missing", "unreadable", func(t *testing.T) string { return filepath.Join(t.TempDir(), "no-such-cert") }},
		{"not a record", "not-a-record", func(t *testing.T) string {
			p := filepath.Join(t.TempDir(), "cert")
			if err := os.WriteFile(p, []byte("bench=test certified\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return p
		}},
		{"stale", "stale", func(t *testing.T) string { return acceptTestCert(t, "test", time.Now().Add(-time.Hour)) }},
		{"another bench", "bench", func(t *testing.T) string { return acceptTestCert(t, "other", later) }},
		{"go leg failed", "go-leg", func(t *testing.T) string { return rewrite(t, "legs=go failed=none", "legs=lisp failed=go") }},
		{"no go leg", "go-leg", func(t *testing.T) string { return rewrite(t, "legs=go ", "legs=lisp ") }},
		{"a-cert-past-until-is-void: until more than 24h after at", "over-24h", func(t *testing.T) string {
			return acceptTestCertAt(t, "test", time.Now().Add(-time.Hour), time.Now().Add(10*365*24*time.Hour))
		}},
		{"cert-voids-on-build-change", "build", func(t *testing.T) string {
			return rewrite(t, " build="+buildVersion()+" ", " build=000000000000-another-build ")
		}},
		{"cert with no build", "build", func(t *testing.T) string { return rewrite(t, " build="+buildVersion()+" ", " ") }},
		{"a-changed-go-version-voids-the-cert", "go-version", func(t *testing.T) string {
			return rewrite(t, " go="+acceptHostGo(context.Background())+" ", " go=go1.0 ")
		}},
		{"cert with no go version", "go-version", func(t *testing.T) string {
			return rewrite(t, " go="+acceptHostGo(context.Background())+" ", " ")
		}},
		{"wall-none-is-uncertified-for-a-code-card", "wall-none", func(t *testing.T) string { return rewrite(t, " wall=seatbelt ", " wall=none ") }},
		{"cert with no wall", "wall-none", func(t *testing.T) string { return rewrite(t, " wall=seatbelt ", " ") }},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			job, _ := acceptTestJob(t)
			code, line := acceptTestRun(t, job, c.cert(t), acceptTestCard(t))
			if code != 2 || !strings.HasPrefix(line, "ACCEPT ABSTAIN ") || !strings.Contains(line, "reason=bench-uncertified ") ||
				!strings.Contains(line, "ACCEPT CERT void="+c.why+" ") {
				t.Fatalf("%s: exit=%d line=%q, want ACCEPT ABSTAIN reason=bench-uncertified exit 2 and void=%s", c.name, code, line, c.why)
			}
		})
	}
}

// TestAcceptAToolchainAbstainVoidsTheCert is §2 rule 6's a-toolchain-abstain-voids-the-cert:
// a card whose gate cannot fetch a module (GOPROXY=off, rule 10) is ABSTAIN
// reason=toolchain, and from then on the same record is void for every card on that
// bench, the known-good fix included, while the record file itself is left as it was.
func TestAcceptAToolchainAbstainVoidsTheCert(t *testing.T) {
	t.Parallel()
	job, cert := acceptTestJob(t)
	before, _ := os.ReadFile(cert)
	for file, text := range map[string]string{
		"go.mod": "module example.invalid/sign\n\ngo 1.21\n\nrequire example.invalid/absent v1.0.0\n",
		"go.sum": "example.invalid/absent v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n" +
			"example.invalid/absent v1.0.0/go.mod h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n",
		"absent.go": "package sign\n\nimport _ \"example.invalid/absent\"\n",
	} {
		if err := os.WriteFile(filepath.Join(job, file), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := acceptFixtureCommit(context.Background(), job, "need a module the bench does not hold"); err != nil {
		t.Fatal(err)
	}
	card := strings.Replace(acceptTestCard(t), "PATHS: sign.go, sign_test.go", "PATHS: sign.go, sign_test.go, go.mod, go.sum, absent.go", 1)
	code, line := acceptTestRun(t, job, cert, card)
	if code != 2 || !strings.Contains(line, "reason=toolchain ") {
		t.Fatalf("module the bench does not hold: exit=%d line=%q, want ACCEPT ABSTAIN reason=toolchain exit 2", code, line)
	}
	good, _ := acceptTestJob(t)
	code, line = acceptTestRun(t, good, cert, acceptTestCard(t))
	if code != 2 || !strings.Contains(line, "reason=bench-uncertified ") || !strings.Contains(line, "ACCEPT CERT void=void ") {
		t.Fatalf("known-good fix after a toolchain abstain: exit=%d line=%q, want ABSTAIN bench-uncertified void=void", code, line)
	}
	if after, _ := os.ReadFile(cert); string(after) != string(before) {
		t.Fatalf("the record was edited: before=%q after=%q (a void record is re-run, never edited)", before, after)
	}
	fresh := acceptTestCert(t, "test", time.Now().Add(23*time.Hour))
	if code, line = acceptTestRun(t, good, fresh, acceptTestCard(t)); code != 0 {
		t.Fatalf("a re-run record: exit=%d line=%q, want ACCEPT OK", code, line)
	}
}

// TestAcceptTestEditExcusesOnlyTheNamedFile: the weakened-without-a-skip edit is
// test-weakened (above) unless the card's header names the file under TEST-EDIT:
// (SPEC-TOOLWORK eligibility rule 11), and naming another file excuses nothing.
func TestAcceptTestEditExcusesOnlyTheNamedFile(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ edit, wantPrefix, want string }{
		{"sign_test.go", "ACCEPT OK ", "kind=fix-red "},
		{"other_test.go", "ACCEPT REJECT ", "reason=test-weakened at=TestSignPositive "},
	} {
		c := c
		t.Run(c.edit, func(t *testing.T) {
			t.Parallel()
			job, cert := acceptTestJob(t)
			acceptTestWeakenPositive(t, job)
			card := strings.Replace(acceptTestCard(t), "TEST: TestSignZero", "TEST: TestSignZero\nTEST-EDIT: "+c.edit, 1)
			_, line := acceptTestRun(t, job, cert, card)
			if !strings.HasPrefix(line, c.wantPrefix) || !strings.Contains(line, c.want) {
				t.Fatalf("TEST-EDIT: %s: line=%q, want %q with %q", c.edit, line, c.wantPrefix, c.want)
			}
		})
	}
}
