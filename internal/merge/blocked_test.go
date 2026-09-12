package merge

import (
	"context"
	"strings"
	"testing"
)

// The conflict remedy is a command a person runs by hand, and it prints the lane's
// CONFIGURED remote -- never an invented host, and never a credential. The old form
// hardcoded https://github.com/%s.git, which invented a host the lane may not use and, on
// a remote that carried a token, would have written the token into a log.

// fixedRemote is a Runner that answers every command with one URL (or one error) and
// records what it was asked to run, so a test can watch what the remedy would shell out.
type fixedRemote struct {
	url  string
	err  error
	seen [][]string
}

func (f *fixedRemote) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	f.seen = append(f.seen, append([]string{name}, args...))
	return f.url, f.err
}

func TestTheConflictRemedyReadsTheConfiguredRemote(t *testing.T) {
	cases := []struct {
		name       string
		url        string
		wantToken  string
		wantCmd    string
		wantAbsent []string
	}{
		{
			name:      "a plain URL is printed shell-quoted",
			url:       "https://github.com/o/n.git",
			wantToken: shellQuote("https://github.com/o/n.git"),
			wantCmd:   "git clone --branch feature-x " + shellQuote("https://github.com/o/n.git") + " ",
		},
		{
			name:       "a URL carrying a credential prints no token",
			url:        "https://x-access-token:TOKEN@host/o/n.git",
			wantToken:  "$(git -C " + shellQuote("lane/repo") + " remote get-url origin)",
			wantAbsent: []string{"TOKEN", "x-access-token", "host/o/n"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fixedRemote{url: tc.url}
			p := &Pass{
				Clone:  NewGit("lane/repo", 0, r),
				Remote: "origin",
				State:  &State{Repo: "o/n", Base: "main"},
			}
			token := cloneToken(p)
			if token != tc.wantToken {
				t.Errorf("cloneToken = %q, want %q", token, tc.wantToken)
			}
			cmd := HandCommand(token, "main", "feature-x", []string{"a.txt"})
			if tc.wantCmd != "" && !strings.Contains(cmd, tc.wantCmd) {
				t.Errorf("the hand command does not clone from the configured remote:\n%q\nwant it to contain %q", cmd, tc.wantCmd)
			}
			for _, a := range tc.wantAbsent {
				if strings.Contains(cmd, a) {
					t.Errorf("the hand command %q prints %q; a credential or an invented host must never reach the log", cmd, a)
				}
			}
		})
	}
}

func TestTheConflictRemedyFallsBackWhenTheRemoteCannotBeRead(t *testing.T) {
	r := &fixedRemote{err: context.DeadlineExceeded}
	p := &Pass{
		Clone:  NewGit("lane/repo", 0, r),
		Remote: "origin",
		State:  &State{Repo: "o/n", Base: "main"},
	}
	token := cloneToken(p)
	if !strings.Contains(token, "$(git -C") {
		t.Errorf("an unreadable remote falls back to the command substitution, got %q", token)
	}
	cmd := HandCommand(token, "main", "feature-x", []string{"a.txt"})
	if !strings.Contains(cmd, "$(git -C") {
		t.Errorf("the hand command must still run, via the substitution: %q", cmd)
	}
}
