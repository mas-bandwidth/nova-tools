package main

import (
	"fmt"
	"strings"
)

func init() {
	register(verb{
		name:    "functional-run",
		summary: "run the functional tests of the given packages: select them, then go test them",
		help: `usage: go run ./tools/ci functional-run [--go GO] [--p N] [--timeout D] [PKG...]

The functional tier (make test-functional): a functional test is one in a
_test.go built only under //go:build functional; it starts a real redis-server, a
real binary, a real process. ` + "`go run ./cmd/nova-ci functional PKG...`" + ` picks, among
the packages, those holding such files and a -run pattern naming exactly their
tests, so the unit tests of those packages are not run a second time. It prints
either one "CI FUNCTIONAL OK packages=0 reason=<why>" line (nothing to run: this
verb prints it after "functional: " and exits 0) or two lines, the packages and
the -run pattern; then this verb runs

  GO test -tags functional -p N -count=1 -timeout D -run PATTERN PACKAGES

and exits with its code. Nothing printed by the selector is refused: nothing
must never run in silence.

--go    the go command (default go)
--p     go test -p, the packages at once (default 2)
--timeout  go test -timeout per test binary (default 100s)

exit 0  nothing to run, or every functional test passed
exit 1  a functional test failed
exit 2  the selector failed or printed nothing, or usage
`,
		do: func(e env, args []string) int { return functionalRun(e, osCmdRunner{}, args) },
	})
}

// functionalRun is the verb over a runner.
func functionalRun(e env, r cmdRunner, args []string) int {
	goCmd, p, timeout := "go", "2", "100s"
	var pkgs []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--go", "--p", "--timeout":
			if i+1 >= len(args) {
				fmt.Fprintf(e.stderr, "functional-run: %s needs a value\n", a)
				return 2
			}
			i++
			switch a {
			case "--go":
				goCmd = args[i]
			case "--p":
				p = args[i]
			default:
				timeout = args[i]
			}
		default:
			if strings.HasPrefix(a, "--") {
				fmt.Fprintf(e.stderr, "functional-run: unknown flag %s\n", a)
				return 2
			}
			pkgs = append(pkgs, a)
		}
	}

	sel, code, err := capture(r, cmdSpec{Name: goCmd, Args: append([]string{"run", "./cmd/nova-ci", "functional"}, pkgs...), Dir: e.dir, Stderr: e.stderr})
	if err != nil || code != 0 {
		if err != nil {
			fmt.Fprintf(e.stderr, "functional-run: %v\n", err)
		}
		return 2
	}
	if strings.HasPrefix(sel, "CI FUNCTIONAL OK ") {
		fmt.Fprintf(e.stdout, "functional: %s\n", sel)
		return 0
	}
	if sel == "" {
		fmt.Fprintln(e.stderr, "functional: nova-ci functional printed nothing; refusing to run nothing in silence")
		return 2
	}
	lines := strings.Split(sel, "\n")
	selected := strings.Fields(lines[0])
	run := ""
	if len(lines) > 1 {
		run = lines[1]
	}
	if len(selected) == 0 || run == "" {
		fmt.Fprintf(e.stderr, "functional: nova-ci functional printed %q, not a package list and a -run pattern; refusing to run it\n", sel)
		return 2
	}
	fmt.Fprintf(e.stdout, "functional: %s\n", lines[0])
	// -tags functional: only the tests behind //go:build functional; -count=1:
	// never a cached answer.
	argv := []string{"test", "-tags", "functional", "-p", p, "-count=1", "-timeout", timeout, "-run", run}
	argv = append(argv, selected...)
	code, err = r.Run(cmdSpec{Name: goCmd, Args: argv, Dir: e.dir, Stdout: e.stdout, Stderr: e.stderr})
	if err != nil {
		fmt.Fprintf(e.stderr, "functional-run: %v\n", err)
		return 2
	}
	return code
}
