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

// TestStatusGrammar pins the status word after the verb token together with the
// exit code (docs/STANDARD.md section 2): OK at 0, REFUSED at 2, FAILED at 1.
// version has no FAILED print and its OK line is the pinned four-token shape, so
// that verb's rows name the words it actually prints.
func TestStatusGrammar(t *testing.T) {
	t.Parallel()
	bus := grammarBus(t)
	gitBus := grammarGitBus(t)
	broken := filepath.Join(t.TempDir(), "draft.md")
	require.NoError(t, os.WriteFile(broken, []byte("To: Nobody\nSubject: x\n\nbody\n"), 0o644))
	out := filepath.Join(t.TempDir(), "draft-out.md")
	exists := filepath.Join(t.TempDir(), "exists.md")
	require.NoError(t, os.WriteFile(exists, []byte("x"), 0o644))
	badNote := filepath.Join(bus, "from-ada", "bad.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(badNote), 0o755))
	require.NoError(t, os.WriteFile(badNote, []byte("this is not a note\n"), 0o644))

	cases := []struct {
		name   string
		args   []string
		stdin  string
		word   string
		token  string
		exit   int
		stream string
	}{
		{"draft ok", []string{"draft", "--bus", bus, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", out}, "", "OK", "DRAFT", 0, "stdout"},
		{"draft refused", []string{"draft", "--zz-no-such"}, "", "REFUSED", "DRAFT", 2, "stderr"},
		{"draft failed", []string{"draft", "--bus", bus, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", exists}, "", "REFUSED", "DRAFT", 1, "stderr"},
		{"prepare refused", []string{"prepare", "--zz-no-such"}, "", "REFUSED", "PREPARE", 2, "stderr"},
		{"prepare failed", []string{"prepare", "--bus", gitBus, "--as", "Ada", "--file", broken}, "", "FAILED", "PREPARE", 1, "stderr"},
		{"send refused", []string{"send", "--zz-no-such"}, "", "REFUSED", "SEND", 2, "stderr"},
		{"send failed", []string{"send", "--bus", gitBus, "--file", broken, "--remote", "origin", "--branch", "main", "--as", "Ada"}, "", "FAILED", "SEND", 1, "stderr"},
		{"reply refused", []string{"reply", "--zz-no-such"}, "", "REFUSED", "REPLY", 2, "stderr"},
		{"inbox ok", []string{"inbox", "--bus", gitBus, "--as", "Ada", "--receipt-max-words", "40", "--full"}, "", "OK", "INBOX", 0, "stdout"},
		{"inbox refused", []string{"inbox", "--zz-no-such"}, "", "REFUSED", "INBOX", 2, "stderr"},
		{"receipt refused", []string{"receipt", "--zz-no-such"}, "", "REFUSED", "RECEIPT", 2, "stderr"},
		{"close refused", []string{"close", "--zz-no-such"}, "", "REFUSED", "CLOSE", 2, "stderr"},
		{"wait refused", []string{"wait", "--zz-no-such"}, "", "REFUSED", "WAIT", 2, "stderr"},
		{"check ok", []string{"check", "--bus", gitBus, "--full"}, "", "OK", "BUS", 0, "stdout"},
		{"check refused", []string{"check", "--zz-no-such"}, "", "REFUSED", "CHECK", 2, "stderr"},
		{"check failed", []string{"check", "--bus", bus, "--full"}, "", "FAILED", "BUS", 1, "stderr"},
		{"fail-max alias", []string{"check", "--bus", gitBus, "--full", "--fail-max", "5"}, "", "NOTE", "", 0, "stderr"},
		{"names ok", []string{"names", "--bus", bus}, "", "OK", "NAMES", 0, "stdout"},
		{"names refused", []string{"names", "--zz-no-such"}, "", "REFUSED", "NAMES", 2, "stderr"},
		{"version ok", []string{"version"}, "", "nova-bus", "", 0, "stdout"},
		{"version refused", []string{"version", "--short"}, "", "REFUSED", "VERSION", 2, "stderr"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := invoke(t, tc.stdin, tc.args...)
			assert.Equal(t, tc.exit, r.code, "stdout=%s stderr=%s", r.stdout, r.stderr)
			stream := r.stdout
			if tc.stream == "stderr" {
				stream = r.stderr
			}
			got := statusWord(stream, tc.token)
			assert.Equal(t, tc.word, got, "stdout=%q stderr=%q", r.stdout, r.stderr)
		})
	}
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
			case "OK", "REFUSED", "FAILED", "NOTE":
				return word
			}
		}
	}
	return ""
}
func grammarGitBus(t *testing.T) string {
	t.Helper()
	dir := grammarBus(t)
	cmd := exec.Command("git", "-C", dir, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "init", "-q", "-b", "main")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git init: %s", out)
	return dir
}

func grammarBus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	const roster = `{
  "participants": [
    {"name": "Ada", "lane": "from-ada", "git_name": "Ada", "git_email": "ada@example.com"},
    {"name": "Bo", "lane": "from-bo", "git_name": "Bo", "git_email": "bo@example.com"}
  ]
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "participants.json"), []byte(roster), 0o644))
	return dir
}
