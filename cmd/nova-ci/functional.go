// functional.go holds the functional verb: its flags, its run and the helpers only it uses.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ci/functional"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
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
	verbflag.HelpIfAsked(args, "functional", verbflag.Flag{Name: "json", Bool: true, Wants: "print the same package selection and run pattern as JSON"})
	asJSON := verbflag.BoolAsked(args, "json")
	refusal := func(why string) int {
		if asJSON {
			return (&tool.Out{Verb: "functional", Status: tool.Refused, Exit: 2, Why: []string{why}, Remedy: "nova-ci functional -h"}).Render(stdout, true)
		}
		return refuse(stderr, " functional", why)
	}
	if len(args) == 0 {
		return refusal("no package directory given; pass the packages the change touched (./cmd/nova-table ...)")
	}
	var problems, patterns []string
	for _, arg := range args {
		switch {
		case arg == "--json" || arg == "-json":
			asJSON = true
		case strings.HasPrefix(arg, "--json=") || strings.HasPrefix(arg, "-json="):
			_, value, _ := strings.Cut(arg, "=")
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				problems = append(problems, "--json wants true or false")
			} else {
				asJSON = parsed
			}
		case strings.HasPrefix(arg, "-"):
			problems = append(problems, fmt.Sprintf("unknown flag %q (functional takes --json and package directories such as ./cmd/nova-table or ./internal/...)", arg))
		default:
			patterns = append(patterns, arg)
		}
	}
	if len(patterns) == 0 && len(problems) == 0 {
		problems = append(problems, "no package directory given; pass the packages the change touched (./cmd/nova-table ...)")
	}
	problems = append(problems, functional.Unmatched(patterns)...)
	if len(problems) > 0 {
		return refusal(strings.Join(problems, "; "))
	}
	dirs, err := functional.Expand(patterns)
	if err != nil {
		return refusal(oneline.Err(err))
	}
	pkgs, err := functional.Select(dirs)
	if err != nil {
		return refusal(oneline.Err(err))
	}
	selection := struct {
		Packages []string `json:"packages"`
		Pattern  string   `json:"pattern"`
	}{Packages: []string{}}
	for _, p := range pkgs {
		selection.Packages = append(selection.Packages, packagePath(p.Dir))
	}
	if len(pkgs) > 0 {
		selection.Pattern = functional.RunPattern(pkgs)
	}
	if asJSON {
		// The machine-facing JSON is a projection of the legacy selection, never a second selection pass.
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(selection); err != nil {
			return refusal("could not print the selection: " + oneline.Err(err))
		}
		return 0
	}
	if len(pkgs) == 0 {
		fmt.Fprintf(stdout, "CI FUNCTIONAL OK packages=0 reason=no-functional-tag-in-%d-dirs\n", len(dirs))
		return 0
	}
	dirs = selection.Packages
	fmt.Fprintln(stdout, strings.Join(dirs, " "))
	fmt.Fprintln(stdout, selection.Pattern)
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
