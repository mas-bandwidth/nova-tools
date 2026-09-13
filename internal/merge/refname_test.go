package merge

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// security#30 finding 5: A VALUE THAT BECOMES A COMMAND-LINE ARGUMENT IS CHECKED WHERE IT
// ARRIVES. ValidRefName (lesson 48) was applied to the branches a person TYPES -- init's
// --base, --lane-branch, --default-branch -- and to the discovered default branch, and to
// nothing the forge says. A pull request whose head branch is named `--upload-pack=<cmd>`
// puts that argument on `git fetch`'s argv, where git runs it as the remote helper over a
// local or ssh remote. The value is never executed by these tests: the assertion is that
// the refusal happens BEFORE any argv is built, so the recording runner sees nothing.

// argvRecorder answers every git command with one empty success and keeps the argv, so a
// test can assert what would have reached git.
type argvRecorder struct{ seen [][]string }

func (a *argvRecorder) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	a.seen = append(a.seen, append([]string{name}, args...))
	return "", nil
}

func (a *argvRecorder) sawAnywhere(needle string) []string {
	for _, cmd := range a.seen {
		for _, arg := range cmd {
			if strings.Contains(arg, needle) {
				return cmd
			}
		}
	}
	return nil
}

const hostileRef = "--upload-pack=touch /tmp/nova-merge-finding-5"

// TestAForgeHeadRefIsCheckedBeforeItReachesGitArgv drives one entry through classify with
// a host that names its head branch a flag. Both of the unvalidated fetches of the audit
// are reached from here: the CONFLICTING arm's re-merge fetch, and the build's fetch.
func TestAForgeHeadRefIsCheckedBeforeItReachesGitArgv(t *testing.T) {
	for _, mergeable := range []string{"CONFLICTING", "MERGEABLE"} {
		t.Run(mergeable, func(t *testing.T) {
			oid := strings.Repeat("a", 40)
			host := NewFakeHost()
			host.PRs[7] = PR{
				Number: 7, Author: "someone", Base: "main",
				HeadRef: hostileRef, HeadOID: oid, Mergeable: mergeable,
				URL: "https://example.invalid/pr/7", Subject: "s",
			}
			host.ChecksBy[oid] = Checks{Green: 3}
			rec := &argvRecorder{}
			var out, errOut bytes.Buffer
			p := &Pass{
				Lane:   "lane",
				State:  &State{Repo: "o/n", Base: "main"},
				Host:   host,
				Clone:  NewGit("lane/repo", 0, rec),
				Remote: "origin",
				Now:    time.Unix(0, 0),
				Stdout: &out,
				Stderr: &errOut,
			}
			e := &Entry{PR: 7, NeedsRead: "no"}
			res := &Result{}

			c := p.classify(e, strings.Repeat("b", 40), res)

			if cmd := rec.sawAnywhere("--upload-pack"); cmd != nil {
				t.Fatalf("a forge-supplied head branch reached git argv: %q", cmd)
			}
			if !res.Stopped {
				t.Errorf("the pass did not stop on a head branch this tool cannot hand to git; state=%q detail=%q", c.State, c.Detail)
			}
			line := errOut.String()
			if !strings.Contains(line, "RUN STOPPED entry=7:") {
				t.Errorf("the refusal does not ride the existing grammar, got %q", line)
			}
			if strings.Count(line, "\n") != 1 {
				t.Errorf("a refusal is one line, got %q", line)
			}
		})
	}
}

// TestTheForgeDecodeRefusesAFlagShapedHeadRef checks the arrival point itself: the JSON
// gh answers. A head branch that is a flag never becomes a PR this tool hands on.
func TestTheForgeDecodeRefusesAFlagShapedHeadRef(t *testing.T) {
	good := `{"number":7,"author":{"login":"a"},"baseRefName":"main","headRefName":"feature-x",` +
		`"headRepositoryOwner":{"login":"o"},"headRefOid":"` + strings.Repeat("a", 40) + `",` +
		`"mergeable":"MERGEABLE","isDraft":false,"url":"u","title":"t"}`
	if pr, err := decodePR(good, 7, "o/n"); err != nil || pr.HeadRef != "feature-x" {
		t.Fatalf("an ordinary pull request must decode: pr=%+v err=%v", pr, err)
	}
	hostile := strings.Replace(good, `"headRefName":"feature-x"`, `"headRefName":"`+hostileRef+`"`, 1)
	pr, err := decodePR(hostile, 7, "o/n")
	if err == nil {
		t.Fatalf("the decode accepted a head branch that is a flag to git: %+v", pr)
	}
	if !strings.Contains(err.Error(), "head branch") {
		t.Errorf("the refusal must name what was wrong, got %q", err)
	}
}
