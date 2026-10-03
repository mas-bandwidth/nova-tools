package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const grammarRoster = `{
  "participants": [
    {"name": "Ada", "lane": "from-ada", "git_name": "Ada", "git_email": "ada@example.com"},
    {"name": "Bo", "lane": "from-bo", "git_name": "Bo", "git_email": "bo@example.com"}
  ]
}`

// grammarNote is the one note on the fixture bus: addressed to Ada, dated before the
// package clock, with an id the reply, receipt and close rows name.
const grammarNote = `From: Bo
To: Ada
Date: Mon Sep  7 00:01:00 UTC 2026
Id: bo-abcdef012345
Subject: A question about the gate

Should the gate run on the merge queue too?
`

const grammarValidDraft = "From: Ada\nTo: Bo\nSubject: gate\n\nhello\n"

const grammarBrokenDraft = "To: Nobody\nSubject: gate\n\nhello\n"

// TestStatusGrammar pins the first word after the verb token together with the exit code
// (docs/STANDARD.md section 2): OK at 0, REFUSED at 2 (and at 1 where the verb ran and a
// lock or the checkout said no), FAILED at 1 for every verb that can fail. draft and names
// have no FAILED print -- draft's exit 1 is a REFUSED and names has no exit-1 path -- and
// prepare's success is its artifact on stdout, not a `PREPARE OK` line, so its OK row pins
// only the exit code. version has its own shape and stays out of this grammar.
func TestStatusGrammar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		args   func(*testing.T) []string
		word   string
		token  string
		exit   int
		stream string
	}{
		{
			"draft ok",
			func(t *testing.T) []string {
				out := filepath.Join(t.TempDir(), "draft.md")
				return []string{"draft", "--bus", grammarRosterDir(t), "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", out}
			}, "OK", "DRAFT", 0, "stdout",
		},
		{
			"draft refused",
			func(t *testing.T) []string { return []string{"draft", "--zz-no-such"} },
			"REFUSED", "DRAFT", 2, "stderr",
		},
		{
			"draft refused at exit 1",
			func(t *testing.T) []string {
				exists := grammarFile(t, "x")
				return []string{"draft", "--bus", grammarRosterDir(t), "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", exists}
			}, "REFUSED", "DRAFT", 1, "stderr",
		},
		{
			"prepare ok",
			func(t *testing.T) []string {
				return []string{"prepare", "--bus", grammarBus(t), "--as", "Ada", "--file", grammarFile(t, grammarValidDraft)}
			}, "", "", 0, "stdout",
		},
		{
			"prepare refused",
			func(t *testing.T) []string { return []string{"prepare", "--zz-no-such"} },
			"REFUSED", "PREPARE", 2, "stderr",
		},
		{
			"prepare failed",
			func(t *testing.T) []string {
				return []string{"prepare", "--bus", grammarBus(t), "--as", "Ada", "--file", grammarFile(t, grammarBrokenDraft)}
			}, "FAILED", "PREPARE", 1, "stderr",
		},
		{
			"send ok",
			func(t *testing.T) []string {
				return []string{"send", "--bus", grammarNoteBus(t), "--file", grammarFile(t, grammarValidDraft), "--as", "Ada", "--remote", "origin", "--branch", "main", "--no-push"}
			}, "OK", "SEND", 0, "stdout",
		},
		{
			"send refused",
			func(t *testing.T) []string { return []string{"send", "--zz-no-such"} },
			"REFUSED", "SEND", 2, "stderr",
		},
		{
			"send failed",
			func(t *testing.T) []string {
				return []string{"send", "--bus", grammarBus(t), "--file", grammarFile(t, grammarBrokenDraft), "--as", "Ada", "--remote", "origin", "--branch", "main"}
			}, "FAILED", "SEND", 1, "stderr",
		},
		{
			"reply ok",
			func(t *testing.T) []string {
				return []string{"reply", "--bus", grammarBus(t), "--as", "Ada", "--re", "bo-abcdef012345", "--file", grammarFile(t, "Yes.\n"), "--remote", "origin", "--branch", "main", "--dry-run"}
			}, "OK", "REPLY", 0, "stdout",
		},
		{
			"reply refused",
			func(t *testing.T) []string { return []string{"reply", "--zz-no-such"} },
			"REFUSED", "REPLY", 2, "stderr",
		},
		{
			"reply failed",
			func(t *testing.T) []string {
				return []string{"reply", "--bus", grammarBus(t), "--as", "Ada", "--re", "x", "--file", grammarFile(t, "Yes.\n"), "--remote", "origin", "--branch", "wrongbranch"}
			}, "FAILED", "REPLY", 1, "stderr",
		},
		{
			"inbox ok",
			func(t *testing.T) []string {
				return []string{"inbox", "--bus", grammarBus(t), "--as", "Ada", "--receipt-max-words", "40", "--full"}
			}, "OK", "INBOX", 0, "stdout",
		},
		{
			"inbox refused",
			func(t *testing.T) []string { return []string{"inbox", "--zz-no-such"} },
			"REFUSED", "INBOX", 2, "stderr",
		},
		{
			"inbox failed",
			func(t *testing.T) []string {
				return []string{"inbox", "--bus", grammarNoteBus(t), "--as", "Ada", "--receipt-max-words", "40", "--advance", "--carry-history", "--no-push", "--remote", "origin", "--branch", "wrongbranch"}
			}, "FAILED", "INBOX", 1, "stderr",
		},
		{
			"receipt ok",
			func(t *testing.T) []string {
				return []string{"receipt", "--bus", grammarNoteBus(t), "--as", "Ada", "--note", "bo-abcdef012345", "--remote", "origin", "--branch", "main", "--no-push"}
			}, "OK", "RECEIPT", 0, "stdout",
		},
		{
			"receipt refused",
			func(t *testing.T) []string { return []string{"receipt", "--zz-no-such"} },
			"REFUSED", "RECEIPT", 2, "stderr",
		},
		{
			"receipt failed",
			func(t *testing.T) []string {
				return []string{"receipt", "--bus", grammarBus(t), "--as", "Ada", "--note", "bo-000000000000", "--remote", "origin", "--branch", "main"}
			}, "FAILED", "RECEIPT", 1, "stderr",
		},
		{
			"close ok",
			func(t *testing.T) []string {
				return []string{"close", "--bus", grammarBus(t), "--as", "Ada", "--before", "2026-09-08T00:00:00Z", "--dry-run"}
			}, "OK", "CLOSE", 0, "stdout",
		},
		{
			"close refused",
			func(t *testing.T) []string { return []string{"close", "--zz-no-such"} },
			"REFUSED", "CLOSE", 2, "stderr",
		},
		{
			"close failed",
			func(t *testing.T) []string {
				return []string{"close", "--bus", grammarBus(t), "--as", "Ada", "--before", "2026-09-08T00:00:00Z", "--remote", "origin", "--branch", "wrongbranch"}
			}, "FAILED", "CLOSE", 1, "stderr",
		},
		{
			"wait ok",
			func(t *testing.T) []string {
				return []string{"wait", "--bus", grammarWaitBus(t), "--as", "Ada", "--receipt-max-words", "40", "--timeout", "2s", "--remote", "origin", "--branch", "main"}
			}, "OK", "WAIT", 0, "stdout",
		},
		{
			"wait refused",
			func(t *testing.T) []string { return []string{"wait", "--zz-no-such"} },
			"REFUSED", "WAIT", 2, "stderr",
		},
		{
			"wait failed",
			func(t *testing.T) []string {
				bus := grammarWaitBus(t)
				// The checkout ends on a branch the wait did not ask for, so the fetch and
				// the listing succeed and the --advance cursor write refuses the branch:
				// INBOX FAILED, exit 1.
				grammarGit(t, bus, "checkout", "-q", "-b", "side")
				return []string{"wait", "--bus", bus, "--as", "Ada", "--receipt-max-words", "40", "--timeout", "2s", "--remote", "origin", "--branch", "main", "--advance", "--carry-history"}
			}, "FAILED", "INBOX", 1, "stderr",
		},
		{
			"check ok",
			func(t *testing.T) []string {
				return []string{"check", "--bus", grammarRosterDir(t), "--full"}
			}, "OK", "BUS", 0, "stdout",
		},
		{
			"check refused",
			func(t *testing.T) []string { return []string{"check", "--zz-no-such"} },
			"REFUSED", "CHECK", 2, "stderr",
		},
		{
			"check failed",
			func(t *testing.T) []string {
				return []string{"check", "--bus", grammarBadBus(t), "--full"}
			}, "FAILED", "BUS", 1, "stderr",
		},
		{
			"names ok",
			func(t *testing.T) []string {
				return []string{"names", "--bus", grammarRosterDir(t)}
			}, "OK", "NAMES", 0, "stdout",
		},
		{
			"names refused",
			func(t *testing.T) []string { return []string{"names", "--zz-no-such"} },
			"REFUSED", "NAMES", 2, "stderr",
		},
		{
			"fail-max alias",
			func(t *testing.T) []string {
				return []string{"check", "--bus", grammarRosterDir(t), "--full", "--fail-max", "5"}
			}, "NOTE", "", 0, "stderr",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := invoke(t, "", tc.args(t)...)
			assert.Equal(t, tc.exit, r.code, "stdout=%s stderr=%s", r.stdout, r.stderr)
			if tc.token == "" {
				return
			}
			stream := r.stdout
			if tc.stream == "stderr" {
				stream = r.stderr
			}
			got := statusWord(stream, tc.token)
			assert.Equal(t, tc.word, got, "stdout=%q stderr=%q", r.stdout, r.stderr)
		})
	}
}

// grammarRosterDir is a scratch directory holding only the roster. It is the smallest bus
// the read-only verbs will take: no git, no notes.
func grammarRosterDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "participants.json"), []byte(grammarRoster), 0o644))
	return dir
}

// grammarFile writes content to a fresh scratch file and returns its path.
func grammarFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

// grammarGit runs git in dir and asserts it succeeded.
func grammarGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), out)
}

// grammarBus is a git repository root with the roster and the one note on disk but not
// committed. It is the fixture for the verbs that run no commit themselves: the read
// verbs, and the FAILED rows that stop before a commit is needed.
func grammarBus(t *testing.T) string {
	t.Helper()
	dir := grammarRosterDir(t)
	grammarGit(t, dir, "init", "-q", "-b", "main")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "from-bo"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "from-bo", "note.md"), []byte(grammarNote), 0o644))
	return dir
}

// grammarNoteBus is a git repository with the roster and the one note committed, so the
// two write verbs that commit (send --no-push and receipt --no-push) run over a clean
// tree. commit.gpgsign is off in this fixture's own config, so the write verb's commit
// cannot hang on a signing key the runner's global git config set.
func grammarNoteBus(t *testing.T) string {
	t.Helper()
	dir := grammarBus(t)
	grammarGit(t, dir, "config", "commit.gpgsign", "false")
	grammarGit(t, dir, "add", "-A")
	grammarGit(t, dir, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "roster and one note")
	return dir
}

// grammarBadBus is a bus whose one lane note does not parse, so `check --full` fails it.
func grammarBadBus(t *testing.T) string {
	t.Helper()
	dir := grammarRosterDir(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "from-ada"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "from-ada", "bad.md"), []byte("this is not a note\n"), 0o644))
	return dir
}

// grammarWaitBus is a checkout whose origin is a local checkout holding the one committed
// note, so `wait` can fetch and read it in-process. The origin is populated with a clone
// rather than a push, because a card's git shim records every push for the sprint and
// nothing reaches a local remote that way.
func grammarWaitBus(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	checkout := filepath.Join(root, "checkout")
	require.NoError(t, os.MkdirAll(source, 0o755))
	grammarGit(t, source, "init", "-q", "-b", "main")
	grammarGit(t, source, "config", "commit.gpgsign", "false")
	require.NoError(t, os.WriteFile(filepath.Join(source, "participants.json"), []byte(grammarRoster), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(source, "from-bo"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "from-bo", "note.md"), []byte(grammarNote), 0o644))
	grammarGit(t, source, "add", "-A")
	grammarGit(t, source, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "roster and one note")
	grammarGit(t, root, "clone", "-q", source, checkout)
	return checkout
}

func statusWord(stream, token string) string {
	for _, line := range strings.Split(stream, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if token == "" {
			return fields[0]
		}
		if fields[0] == token && len(fields) >= 2 {
			word := strings.TrimRight(fields[1], ":")
			switch word {
			case "OK", "REFUSED", "FAILED":
				return word
			}
		}
	}
	return ""
}
