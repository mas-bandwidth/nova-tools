// functional.go holds the functional verb: its flags, its run and the helpers only it uses.

package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ci/functional"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// cmdFunctional prints the functional tier's selection for `make
// test-functional`: the package directories among args that hold functional
// tests, all on one line, then one -run pattern naming exactly those tests.
// A change whose packages carry none prints one `CI FUNCTIONAL OK packages=0
// reason=<why>` line and exits 0, and the target runs nothing. An unknown flag
// and a pattern that matches no package are refused, every one in one line: a
// typo in CI's package list must never skip the functional tier in silence.
func cmdFunctional(args []string, stdout, stderr io.Writer) int {
	// -h and --help are the verb's help on stdout at exit 0, never silence and never a
	// package list: make test-functional would hand the help text to go test, which
	// fails on it out loud.
	verbflag.HelpIfAsked(args, "functional")
	if len(args) == 0 {
		return refuse(stderr, " functional", "no package directory given; pass the packages the change touched (./cmd/nova-table ...)")
	}
	var problems, patterns []string
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "-"):
			problems = append(problems, fmt.Sprintf("unknown flag %q (functional takes no flags, only package directories such as ./cmd/nova-table or ./internal/...)", arg))
		default:
			patterns = append(patterns, arg)
		}
	}
	problems = append(problems, functional.Unmatched(patterns)...)
	if len(problems) > 0 {
		return refuse(stderr, " functional", strings.Join(problems, "; "))
	}
	dirs, err := functional.Expand(patterns)
	if err != nil {
		return refuse(stderr, " functional", oneline.Err(err))
	}
	pkgs, err := functional.Select(dirs)
	if err != nil {
		return refuse(stderr, " functional", oneline.Err(err))
	}
	if len(pkgs) == 0 {
		fmt.Fprintf(stdout, "CI FUNCTIONAL OK packages=0 reason=no-functional-tag-in-%d-dirs\n", len(dirs))
		return 0
	}
	dirs = make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		dirs = append(dirs, packagePath(p.Dir))
	}
	fmt.Fprintln(stdout, strings.Join(dirs, " "))
	fmt.Fprintln(stdout, functional.RunPattern(pkgs))
	return 0
}

// packagePath returns dir in the form `go test -timeout 600s` accepts (./internal/x/).
func packagePath(dir string) string {
	d := filepath.ToSlash(filepath.Clean(dir))
	if d == "." {
		return "./"
	}
	if !strings.HasPrefix(d, "./") && !strings.HasPrefix(d, "../") && !strings.HasPrefix(d, "/") {
		d = "./" + d
	}
	if !strings.HasSuffix(d, "/") {
		d += "/"
	}
	return d
}
