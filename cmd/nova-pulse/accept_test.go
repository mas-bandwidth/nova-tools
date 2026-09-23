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
	cert = filepath.Join(dir, "cert")
	if err := os.WriteFile(cert, []byte("bench=test certified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return job, cert
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

// TestAcceptAbstainsOnAnUncertifiedBench: a cert that cannot be read is the bench's
// fault, not the card's: ABSTAIN, exit 2, and no verdict about the card.
func TestAcceptAbstainsOnAnUncertifiedBench(t *testing.T) {
	t.Parallel()
	job, _ := acceptTestJob(t)
	code, line := acceptTestRun(t, job, filepath.Join(t.TempDir(), "no-such-cert"), acceptTestCard(t))
	if code != 2 || !strings.HasPrefix(line, "ACCEPT ABSTAIN ") || !strings.Contains(line, "reason=bench-uncertified ") {
		t.Fatalf("uncertified bench: exit=%d line=%q, want ACCEPT ABSTAIN reason=bench-uncertified exit 2", code, line)
	}
}
