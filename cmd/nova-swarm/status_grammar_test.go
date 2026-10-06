package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// stdoutMu synchronizes native subtests that read or swap os.Stdout.
var stdoutMu sync.Mutex

// verbTokens lists the verb and line tokens that lead status lines in nova-swarm.
var verbTokens = []string{
	"nova-swarm template",
	"nova-swarm lint",
	"nova-swarm member",
	"nova-swarm native",
	"nova-swarm worker",
	"nova-swarm verify",
	"nova-swarm doctor",
	"nova-swarm profile",
	"nova-swarm slots",
	"nova-swarm version",
	"nova-swarm",
	"LINT",
	"MEMBER",
	"NATIVE",
	"STAGE",
	"CARD",
	"WORKER",
	"RESULT",
	"DOCTOR",
	"SLOTS",
}

// statusWords lists the status grammar words that lead a typed outcome.
var statusWords = map[string]bool{
	"OK":      true,
	"REFUSED": true,
	"FAILED":  true,
	"FAIL":    true,
	"DRIFT":   true,
}

// statusAfter returns the first status word after token in out, and whether the
// token was found on any line: the status word that leads a typed line (STANDARD §2).
func statusAfter(out, token string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, token); ok && strings.HasPrefix(rest, " ") {
			rest = strings.TrimLeft(rest, " ")
			word, _, _ := strings.Cut(rest, " ")
			word = strings.TrimRight(word, ":")
			if word == strings.ToUpper(word) && word != "" {
				return word, true
			}
		}
	}
	return "", false
}

// captureStageOutput captures the first STAGE line printed to os.Stdout during fn.
func captureStageOutput(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err, "os.Pipe")
	orig := os.Stdout
	os.Stdout = w
	lineCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintln(orig, line)
			if strings.HasPrefix(line, "STAGE FAIL") && strings.Contains(line, "missing-mirror.git") {
				lineCh <- line
			}
		}
		close(lineCh)
	}()

	fn()

	os.Stdout = orig
	w.Close()
	defer r.Close()
	line, ok := <-lineCh
	require.True(t, ok, "printed no STAGE line")
	return line
}

// aResult writes a RESULT.md a verify row reads and returns its path.
func aResult(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "RESULT.md")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}
