package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func init() {
	register(verb{
		name:    "lisp-test",
		summary: "run the Lisp acceptance suite under SBCL (nothing to test while lisp/ is absent)",
		help: `usage: go run ./tools/ci lisp-test [--root DIR]

Runs the nova-work acceptance suite under SBCL, non-interactively, from the
checkout (DIR, default the working directory). The system is loaded with the
ASDF that SBCL bundles: no quicklisp, no network. The 120 s budget is the job's
timeout-minutes in .github/workflows/ci.yml, not a timeout(1) wrapper.

With no lisp/ tree there is nothing to test: it says so and exits 0, the way
ci.yml passes an empty Go selection. A lisp/ tree that holds no nova-work is
refused, so a new system cannot land untested behind a green "nothing".

Each run gets its own short TMPDIR under $LISP_TEST_TMPROOT (default /tmp),
because the suite keys its journals, exports and socket bases off the ambient
TMPDIR and clears stale directories first, so two runs on one host would wreck
each other's state. It is short because an AF_UNIX path is bounded. It is
removed on exit, whatever the verdict.

exit 0  every case passes, or there is nothing to test
exit 1  a case fails, or lisp/ holds a system this verb does not know
`,
		do: func(e env, args []string) int { return lispTest(e, osCmdRunner{}, os.Getpid(), args) },
	})
}

// lispTest is the verb over a runner and a process id (the run's TMPDIR name).
func lispTest(e env, r cmdRunner, pid int, args []string) int {
	root := e.dir
	if root == "" {
		root = "."
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "--root" && i+1 < len(args) {
			root = args[i+1]
			i++
			continue
		}
		fmt.Fprintf(e.stderr, "lisp-test: unknown argument %q; usage: go run ./tools/ci lisp-test [--root DIR]\n", args[i])
		return 2
	}
	root, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(e.stderr, "lisp-test: %v\n", err)
		return 1
	}
	lisp := filepath.Join(root, "lisp", "nova-work")
	if fi, err := os.Stat(lisp); err != nil || !fi.IsDir() {
		if _, err := os.Lstat(filepath.Join(root, "lisp")); err == nil {
			fmt.Fprintln(e.stderr, "lisp-test: lisp/ exists and holds no nova-work; this verb does not know how to test it")
			return 1
		}
		fmt.Fprintln(e.stdout, "lisp-test: nothing to test (lisp/ holds no system; the old nova-work kernel lives in the nova-work-old repository)")
		return 0
	}

	tmpRoot := e.getenv("LISP_TEST_TMPROOT")
	if tmpRoot == "" {
		tmpRoot = "/tmp"
	}
	tmp := filepath.Join(tmpRoot, "nw-"+strconv.Itoa(pid))
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		fmt.Fprintf(e.stderr, "lisp-test: cannot create %s\n", tmp)
		return 1
	}
	defer os.RemoveAll(tmp)
	fmt.Fprintf(e.stderr, "lisp-test: TMPDIR=%s\n", tmp)

	code, err := r.Run(cmdSpec{
		Name: "sbcl",
		Args: []string{
			"--non-interactive",
			"--eval", "(require :asdf)",
			"--eval", fmt.Sprintf("(push #p\"%s/\" asdf:*central-registry*)", lisp),
			"--eval", "(handler-bind ((warning #'muffle-warning)) (asdf:load-system :nova-work/tests))",
			"--eval", "(nova-work/tests:main)",
		},
		Env:    []string{"TMPDIR=" + tmp},
		Stdout: e.stdout,
		Stderr: e.stderr,
	})
	if err != nil {
		fmt.Fprintf(e.stderr, "lisp-test: %v\n", err)
		return 127
	}
	return code
}
