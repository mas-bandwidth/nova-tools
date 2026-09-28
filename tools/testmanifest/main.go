// Command testmanifest runs an exact list of Go tests and refuses missing,
// duplicate, skipped, or failed results. It also refuses a red go test command,
// even when the requested tests happened to print PASS before another failure.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

type event struct {
	Action  string
	Package string
	Test    string
}

type counts struct {
	run, pass, fail, skip int
}

func main() {
	var goCommand, pkg string
	flag.StringVar(&goCommand, "go", "go", "Go command used to run the manifested tests")
	flag.StringVar(&pkg, "package", "", "one Go package containing the manifested tests")
	flag.Parse()
	tests := flag.Args()
	if pkg == "" || len(tests) == 0 {
		fmt.Fprintln(os.Stderr, "testmanifest: want --package <package> -- <TestName>...")
		os.Exit(2)
	}
	if err := validateNames(tests); err != nil {
		fmt.Fprintf(os.Stderr, "testmanifest: %v\n", err)
		os.Exit(2)
	}

	pattern := "^(?:" + strings.Join(quoteNames(tests), "|") + ")$"
	cmd := exec.Command(goCommand, "test", "-json", "-count=1", "-run", pattern, pkg)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	_, _ = os.Stdout.Write(stdout.Bytes())
	_, _ = os.Stderr.Write(stderr.Bytes())
	statusOK := err == nil
	if err := check(bytes.NewReader(stdout.Bytes()), tests, statusOK); err != nil {
		fmt.Fprintf(os.Stderr, "testmanifest: FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("TEST-MANIFEST PASS package=%s tests=%d\n", pkg, len(tests))
}

func validateNames(tests []string) error {
	seen := make(map[string]bool, len(tests))
	for _, name := range tests {
		if !regexp.MustCompile(`^Test[A-Za-z0-9_]+$`).MatchString(name) {
			return fmt.Errorf("invalid top-level test name %q", name)
		}
		if seen[name] {
			return fmt.Errorf("duplicate manifested test %q", name)
		}
		seen[name] = true
	}
	return nil
}

func quoteNames(tests []string) []string {
	quoted := make([]string, len(tests))
	for i, name := range tests {
		quoted[i] = regexp.QuoteMeta(name)
	}
	return quoted
}

func check(r io.Reader, tests []string, statusOK bool) error {
	if err := validateNames(tests); err != nil {
		return err
	}
	want := make(map[string]bool, len(tests))
	got := make(map[string]counts, len(tests))
	for _, name := range tests {
		want[name] = true
	}

	packagePasses := 0
	var problems []string
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for s.Scan() {
		var e event
		if err := json.Unmarshal(s.Bytes(), &e); err != nil {
			problems = append(problems, "malformed go test JSON")
			continue
		}
		if e.Test == "" {
			switch e.Action {
			case "pass":
				packagePasses++
			case "fail", "skip":
				problems = append(problems, "package action "+e.Action)
			}
			continue
		}

		top := e.Test
		if i := strings.IndexByte(top, '/'); i >= 0 {
			top = top[:i]
			if want[top] && (e.Action == "fail" || e.Action == "skip") {
				problems = append(problems, fmt.Sprintf("nested %s %s", e.Action, e.Test))
			}
			continue
		}
		if !want[top] {
			if e.Action == "run" || e.Action == "pass" || e.Action == "fail" || e.Action == "skip" {
				problems = append(problems, "unexpected test "+top)
			}
			continue
		}
		c := got[top]
		switch e.Action {
		case "run":
			c.run++
		case "pass":
			c.pass++
		case "fail":
			c.fail++
		case "skip":
			c.skip++
		}
		got[top] = c
	}
	if err := s.Err(); err != nil {
		return fmt.Errorf("read go test JSON: %w", err)
	}
	if !statusOK {
		problems = append(problems, "go test command exited nonzero")
	}
	if packagePasses != 1 {
		problems = append(problems, fmt.Sprintf("package PASS count=%d want=1", packagePasses))
	}
	for _, name := range tests {
		c := got[name]
		if c.run != 1 || c.pass != 1 || c.fail != 0 || c.skip != 0 {
			problems = append(problems, fmt.Sprintf("%s run=%d pass=%d fail=%d skip=%d; want 1,1,0,0", name, c.run, c.pass, c.fail, c.skip))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}
