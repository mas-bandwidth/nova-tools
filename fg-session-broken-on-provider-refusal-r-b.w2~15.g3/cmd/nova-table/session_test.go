package main

import (
	"bytes"
	"errors"
	"io"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestShellWords(t *testing.T) {
	t.Parallel()
	cases := []struct {
		line string
		want []string
	}{
		{`row set jobs 'the tests' "note=two words"`, []string{"row", "set", "jobs", "the tests", "note=two words"}},
		{`row add t one\ row --label "a\"b"`, []string{"row", "add", "t", "one row", "--label", `a"b`}},
		{`row set t r note='' 'other=it'\''s here'`, []string{"row", "set", "t", "r", "note=", "other=it's here"}},
		{`create t --columns 'done,pct:pct(done)' # explanation`, []string{"create", "t", "--columns", "done,pct:pct(done)"}},
		{`row set t r 'note=$HOME $(touch file) ` + "`" + `whoami` + "`" + ` *'`, []string{"row", "set", "t", "r", "note=$HOME $(touch file) `whoami` *"}},
		{`row set t r "note=C:\path\file"`, []string{"row", "set", "t", "r", `note=C:\path\file`}},
		{`row add t '日本語' x#y "semi;colon"`, []string{"row", "add", "t", "日本語", "x#y", "semi;colon"}},
		{" \t# comment", nil},
	}
	for _, tc := range cases {
		got, err := shellWords(tc.line)
		assert.NoError(t, err, "%q: %q %v, want %q", tc.line, got, err, tc.want)
		assert.Equal(t, tc.want, got, "%q: %q %v, want %q", tc.line, got, err, tc.want)
	}
	for _, line := range []string{`row add t 'unfinished`, `row add t "unfinished`, `row add t trailing\`, `show t; drop t`, `show t | cat`, `show t > out`} {
		{
			_, err := shellWords(line)
			assert.Error(t, err, "accepted %q", line)
		}
	}
}

type shellReadError struct{}

func (shellReadError) Read([]byte) (int, error) { return 0, errors.New("input failed") }

func TestShellInputFailuresAreReported(t *testing.T) {
	t.Parallel()
	for _, r := range []io.Reader{shellReadError{}, strings.NewReader(strings.Repeat("x", maxShellLine+1))} {
		var out, errout bytes.Buffer
		code := (&application{}).readCommands(r, &out, &errout, false, false)
		require.EqualValues(t, 2, code, "code=%d out=%q err=%q", code, out.String(), errout.String())
		require.EqualValues(t, 0, out.Len(), "code=%d out=%q err=%q", code, out.String(), errout.String())
		require.Contains(t, errout.String(), "reading line 1", "code=%d out=%q err=%q", code, out.String(), errout.String())
	}
}

func TestShellHelpNeverDials(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"help", "shell"}, {"shell", "--help"}, {"shell", "-h"}} {
		code, out, errout := runTable(args...)
		require.EqualValues(t, 0, code, "%v: %d %s %s", args, code, out, errout)
		require.Empty(t, errout, "%v: %d %s %s", args, code, out, errout)
		require.Contains(t, out, "--keep-going", "%v: %d %s %s", args, code, out, errout)
		require.Contains(t, out, "--receipt", "%v: %d %s %s", args, code, out, errout)
	}
	// The Redis client is lazy: exploring help/version inside a shell needs
	// no network command, even though its store address is pinned.
	var out, errout bytes.Buffer
	app := &application{in: strings.NewReader("help row move\nversion\nquit\ndrop never\n")}
	{
		code := app.run([]string{"shell", "--redis", "127.0.0.1:1"}, &out, &errout)
		require.EqualValues(t, 0, code, "%d %s %s", code, &out, &errout)
		require.EqualValues(t, 0, errout.Len(), "%d %s %s", code, &out, &errout)
		require.Contains(t, out.String(), "row move", "%d %s %s", code, &out, &errout)
	}
}
