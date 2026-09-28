package main

import (
	"bytes"
	"errors"
	"io"
	"reflect"
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
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: %q %v, want %q", tc.line, got, err, tc.want)
		}
	}
	for _, line := range []string{`row add t 'unfinished`, `row add t "unfinished`, `row add t trailing\`, `show t; drop t`, `show t | cat`, `show t > out`} {
		if _, err := shellWords(line); err == nil {
			t.Errorf("accepted %q", line)
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
		if code != 2 || out.Len() != 0 || !strings.Contains(errout.String(), "reading line 1") {
			t.Fatalf("code=%d out=%q err=%q", code, out.String(), errout.String())
		}
	}
}

func TestShellHelpNeverDials(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"help", "shell"}, {"shell", "--help"}, {"shell", "-h"}} {
		code, out, errout := runTable(args...)
		if code != 0 || errout != "" || !strings.Contains(out, "--keep-going") || !strings.Contains(out, "--receipt") {
			t.Fatalf("%v: %d %s %s", args, code, out, errout)
		}
	}
	// The Redis client is lazy: exploring help/version inside a shell needs
	// no network command, even though its store address is pinned.
	var out, errout bytes.Buffer
	app := &application{in: strings.NewReader("help row move\nversion\nquit\ndrop never\n")}
	if code := app.run([]string{"shell", "--redis", "127.0.0.1:1"}, &out, &errout); code != 0 || errout.Len() != 0 || !strings.Contains(out.String(), "row move") {
		t.Fatalf("%d %s %s", code, &out, &errout)
	}
}
