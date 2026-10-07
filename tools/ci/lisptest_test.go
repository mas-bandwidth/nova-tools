package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lispEnv(dir, tmpRoot string) (env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return env{stdout: &out, stderr: &errb, dir: dir, getenv: func(k string) string {
		if k == "LISP_TEST_TMPROOT" {
			return tmpRoot
		}
		return ""
	}}, &out, &errb
}

func TestLispTestWithNoLispTreeHasNothingToTest(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	e, out, _ := lispEnv(root, t.TempDir())
	r := &fakeCmdRunner{}
	if code := lispTest(e, r, 1, nil); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if want := "lisp-test: nothing to test (lisp/ holds no system; the old nova-work kernel lives in the nova-work-old repository)\n"; out.String() != want {
		t.Fatalf("stdout %q, want %q", out.String(), want)
	}
	if len(r.calls) != 0 {
		t.Fatalf("ran %v with nothing to test", r.lines())
	}
}

func TestLispTestRefusesALispTreeItDoesNotKnow(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lisp", "other-system"), 0o755); err != nil {
		t.Fatal(err)
	}
	e, _, errb := lispEnv(root, t.TempDir())
	r := &fakeCmdRunner{}
	if code := lispTest(e, r, 1, nil); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "lisp/ exists and holds no nova-work; this verb does not know how to test it") {
		t.Fatalf("stderr %q does not refuse the unknown tree", errb.String())
	}
	if len(r.calls) != 0 {
		t.Fatalf("ran %v for a tree it refuses", r.lines())
	}
}

func TestLispTestRunsSbclUnderItsOwnShortTmpdirAndRemovesIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lisp", "nova-work"), 0o755); err != nil {
		t.Fatal(err)
	}
	tmpRoot := t.TempDir()
	e, _, errb := lispEnv(root, tmpRoot)
	var during string
	r := &fakeCmdRunner{answer: func(c cmdSpec) (string, int, error) {
		for _, kv := range c.Env {
			if v, ok := strings.CutPrefix(kv, "TMPDIR="); ok {
				if fi, err := os.Stat(v); err != nil || !fi.IsDir() {
					t.Errorf("TMPDIR %s does not exist while sbcl runs", v)
				}
				during = v
			}
		}
		return "", 3, nil
	}}
	if code := lispTest(e, r, 4242, nil); code != 3 {
		t.Fatalf("exit %d, want sbcl's 3", code)
	}
	if want := filepath.Join(tmpRoot, "nw-4242"); during != want {
		t.Fatalf("TMPDIR during the run %q, want %q", during, want)
	}
	if _, err := os.Stat(during); !os.IsNotExist(err) {
		t.Fatalf("the run's TMPDIR %s is still there after the verdict (%v)", during, err)
	}
	if !strings.Contains(errb.String(), "lisp-test: TMPDIR="+during) {
		t.Fatalf("stderr %q does not name the TMPDIR", errb.String())
	}
	lines := r.lines()
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "sbcl --non-interactive --eval (require :asdf) --eval (push #p\"") {
		t.Fatalf("commands %q", lines)
	}
	abs, _ := filepath.Abs(filepath.Join(root, "lisp", "nova-work"))
	for _, want := range []string{
		`(push #p"` + abs + `/" asdf:*central-registry*)`,
		`(handler-bind ((warning #'muffle-warning)) (asdf:load-system :nova-work/tests))`,
		`(nova-work/tests:main)`,
	} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("sbcl was not given %s:\n%s", want, lines[0])
		}
	}
}

func TestLispTestWithNoSbclExits127(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lisp", "nova-work"), 0o755); err != nil {
		t.Fatal(err)
	}
	e, _, errb := lispEnv(root, t.TempDir())
	r := &fakeCmdRunner{answer: func(cmdSpec) (string, int, error) { return "", 0, os.ErrNotExist }}
	if code := lispTest(e, r, 1, nil); code != 127 {
		t.Fatalf("exit %d, want 127", code)
	}
	if !strings.Contains(errb.String(), "lisp-test:") {
		t.Fatalf("stderr %q", errb.String())
	}
}

func TestLispTestRefusesAnUnknownArgument(t *testing.T) {
	t.Parallel()
	e, _, errb := lispEnv(t.TempDir(), t.TempDir())
	if code := lispTest(e, &fakeCmdRunner{}, 1, []string{"--nope"}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errb.String(), `unknown argument "--nope"`) {
		t.Fatalf("stderr %q", errb.String())
	}
}
