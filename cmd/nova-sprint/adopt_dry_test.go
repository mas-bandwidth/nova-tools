package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runAdoptDry(t *testing.T, play *fakePlay) (int, string, string) {
	t.Helper()
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "fleet"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "fleet", "tools.yml"), []byte("[]\n"), 0o644))
	a := newApp(func(k string) string {
		if k == "HOME" {
			return "/home/test"
		}
		return ""
	})
	adoptPlayOf.Store(a, play)
	defer adoptPlayOf.Delete(a)
	var out, errs bytes.Buffer
	code := a.run([]string{"adopt", "v1.2.0-dev.abc1234", "--source", src, "--inventory", "/fixture/inventory", "--reason", "test rehearsal", "--dry-run"}, &out, &errs)
	return code, out.String(), errs.String()
}

func TestAdoptDryRunPrintsEachWouldStepAndSummary(t *testing.T) {
	t.Parallel()
	out := strings.ReplaceAll(playOK, "CHANGED", "WOULD-CHANGE")
	code, stdout, stderr := runAdoptDry(t, &fakePlay{out: out})
	require.Equal(t, 0, code, stderr)
	for _, step := range adoptPlaySteps {
		assert.Contains(t, stdout, "ADOPT WOULD step="+step+" host=seat-a")
	}
	assert.Contains(t, stdout, "ADOPT DRY-RUN OK steps=4")
	assert.NotContains(t, stdout, "WOULD-ADOPT")
}

func TestAdoptDryRunNamesFatalStepAndOneLine(t *testing.T) {
	t.Parallel()
	play := &fakePlay{
		out: "TASK [friends: the candidate takes every friend daemon's flags] ***\nfatal: [seat-a]: FAILED! => {\"msg\": \"missing stderr\"}\n",
		err: errors.New("exit status 2"),
	}
	code, stdout, stderr := runAdoptDry(t, play)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "ADOPT REFUSED step=friends dry-run=yes: fatal: [seat-a]: FAILED! =>")
	assert.Equal(t, 1, strings.Count(stderr, "\n"), "a check-mode failure is one line")
}

func TestFleetPlayFailMessagesDefaultRegisteredAttributes(t *testing.T) {
	t.Parallel()
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	source, err := filepath.Abs(source)
	require.NoError(t, err)
	fleet := filepath.Join(filepath.Dir(source), "..", "..", "fleet")
	files, err := filepath.Glob(filepath.Join(fleet, "*.yml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	attribute := regexp.MustCompile(`\b([A-Za-z_]\w*)((?:\.[A-Za-z_]\w*)+)`)
	for _, file := range files {
		body, err := os.ReadFile(file)
		require.NoError(t, err)
		lines := strings.Split(string(body), "\n")
		registered := map[string]bool{}
		for _, line := range lines {
			if m := regexp.MustCompile(`\bregister:\s*([A-Za-z_]\w*)`).FindStringSubmatch(line); m != nil {
				registered[m[1]] = true
			}
		}
		for i := 0; i < len(lines); i++ {
			m := regexp.MustCompile(`^(\s*)fail_msg:\s*(.*)$`).FindStringSubmatch(lines[i])
			if m == nil {
				continue
			}
			indent := len(m[1])
			var msg strings.Builder
			msg.WriteString(m[2])
			for j := i + 1; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) != "" && len(lines[j])-len(strings.TrimLeft(lines[j], " \t")) <= indent {
					break
				}
				msg.WriteByte('\n')
				msg.WriteString(lines[j])
			}
			text := msg.String()
			for _, loc := range attribute.FindAllStringIndex(text, -1) {
				ref := attribute.FindStringSubmatch(text[loc[0]:loc[1]])
				if !registered[ref[1]] {
					continue
				}
				tail := strings.TrimLeft(text[loc[1]:], " \t")
				assert.True(t, strings.HasPrefix(tail, "| default("), "%s fail_msg reads %s without a default", file, text[loc[0]:loc[1]])
			}
		}
	}
}
