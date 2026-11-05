package contract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// missingCase is a run with a required input absent: it must exit 2 in the
// one-line shape and name every wanted item at once. A want entry with "|"
// accepts any one of its alternatives. "@tmp" in args is the working directory.
type missingCase struct {
	args []string
	want []string
}

// The default verb of reserve and sandbox ("run", no name on the command line) has no
// verb path to test and is not listed.

// toolSpec is what the test knows about one tool that its banner does not give
// in a parseable form.
type toolSpec struct {
	dir     string     // directory in the module; the binary is nova-<dir>
	verbs   [][]string // every verb path
	extra   []int      // documented exit-code passthroughs beyond 0, 1, 2
	missing []missingCase
}

var toolSpecs = []toolSpec{
	{dir: "bus", verbs: [][]string{{"send"}, {"hook"}, {"who"}, {"pending"}},
		missing: []missingCase{{[]string{"send"}, []string{"--as", "--to", "--addr|NOVA_BUS_REDIS"}}}},
	{dir: "cairn", verbs: [][]string{{"open"}, {"append"}, {"index"}, {"receipt"}, {"self-talk"},
		{"memory", "stats"}, {"memory", "search"}, {"memory", "check"}, {"memory", "verify"}, {"help"}, {"version"}},
		missing: []missingCase{
			{[]string{"open"}, []string{"--store", "--session", "--publish"}},
			{[]string{"memory", "search"}, []string{"--root"}}}},
	{dir: "config", verbs: [][]string{
		{"machine", "list"}, {"machine", "show"}, {"machine", "add"}, {"machine", "set"}, {"machine", "remove"},
		{"loop", "list"}, {"loop", "show"}, {"loop", "add"}, {"loop", "set"}, {"loop", "remove"},
		{"history"}, {"inventory"}, {"schema"}},
		missing: []missingCase{{[]string{"machine", "list"}, []string{"--pg"}}}},
	{dir: "reserve", verbs: [][]string{{"set"}, {"hold"}, {"status"}}, extra: []int{126, 127},
		missing: []missingCase{{[]string{"status", "--dir", "@tmp/no-such-dir"}, []string{"--dir"}}}},
	{dir: "sandbox", verbs: [][]string{{"check"}, {"probe"}, {"show"}}, extra: []int{125, 126, 127},
		missing: []missingCase{{[]string{"--read", "@tmp/no-such-dir"}, []string{"--read|command"}}}},
	{dir: "secrets", verbs: [][]string{{"exec"}, {"set"}, {"seat", "add"}, {"init"}, {"names"}}, extra: []int{125},
		missing: []missingCase{{[]string{"exec"}, []string{"--only", "--store", "--key"}}}},
}

var goFlagNoise = []string{"flag provided but not defined", "Usage of", "invalid value", "parse error", "flag needs an argument"}

type result struct {
	code           int
	stdout, stderr string
}

// harness runs one tool's binary in a scrubbed environment under one temp dir.
type harness struct {
	t     *testing.T
	tool  string // nova-<dir>
	bin   string
	tmp   string
	extra []int
}

func (h *harness) env() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + h.tmp,
		"TMPDIR=" + h.tmp,
		"NOVA_RESERVE_DIR=" + filepath.Join(h.tmp, "reserve"),
	}
}

// run executes the tool; an exit outside {0,1,2}+passthroughs is A6, wherever it occurs.
func (h *harness) run(args ...string) result {
	h.t.Helper()
	for i, a := range args {
		args[i] = strings.ReplaceAll(a, "@tmp", h.tmp)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.bin, args...)
	cmd.Dir = h.tmp
	cmd.Env = h.env()
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	r := result{code, so.String(), se.String()}
	ok := code >= 0 && code <= 2
	for _, e := range h.extra {
		ok = ok || code == e
	}
	if !ok {
		h.t.Errorf("A6 %s %s: expected exit 0, 1 or 2%s; actual %d (stderr=%q)", h.tool, strings.Join(args, " "), allowNote(h.extra), code, trim(r.stderr))
	}
	return r
}

func allowNote(extra []int) string {
	if len(extra) == 0 {
		return ""
	}
	return fmt.Sprint(" (or documented passthrough ", extra, ")")
}

func trim(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// snapshot lists every path under dir with size and mtime, to detect side effects.
func snapshot(dir string) string {
	var lines []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return nil
		}
		fi, e := d.Info()
		if e == nil {
			lines = append(lines, fmt.Sprintf("%s %d %d", p, fi.Size(), fi.ModTime().UnixNano()))
		}
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func (h *harness) noNoise(id, what string, r result) {
	h.t.Helper()
	for _, bad := range goFlagNoise {
		if strings.Contains(r.stderr, bad) || strings.Contains(r.stdout, bad) {
			h.t.Errorf("%s %s %s: expected no Go flag-package text; actual %q in output (stderr=%q)", id, h.tool, what, bad, trim(r.stderr))
		}
	}
}

func TestCLIContract(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	for _, spec := range toolSpecs {
		spec := spec
		t.Run(spec.dir, func(t *testing.T) {
			if fi, err := os.Stat(filepath.Join(root, spec.dir)); err != nil || !fi.IsDir() {
				t.Skipf("tool directory %s is not on this checkout", spec.dir)
			}
			t.Parallel()
			tmp := t.TempDir()
			name := "nova-" + spec.dir
			bin := filepath.Join(tmp, "bin", name)
			if runtime.GOOS == "windows" {
				bin += ".exe"
			}
			build := exec.Command("go", "build", "-o", bin, "./"+spec.dir)
			build.Dir = root
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("A0 %s: expected go build ./%s to succeed; actual %v: %s", name, spec.dir, err, trim(string(out)))
			}
			work := filepath.Join(tmp, "work")
			if err := os.Mkdir(work, 0o755); err != nil {
				t.Fatal(err)
			}
			h := &harness{t: t, tool: name, bin: bin, tmp: work, extra: spec.extra}
			checkTool(h, spec)
		})
	}
}

func checkTool(h *harness, spec toolSpec) {
	t := h.t
	name := h.tool

	// A1: help.
	banner := h.run("help")
	if banner.code != 0 || strings.TrimSpace(banner.stdout) == "" || banner.stderr != "" {
		t.Errorf("A1 %s help: expected exit 0, non-empty stdout, empty stderr; actual exit %d, stdout %d bytes, stderr=%q", name, banner.code, len(banner.stdout), trim(banner.stderr))
	}

	for _, v := range spec.verbs {
		vp := strings.Join(v, " ")
		if !strings.Contains(banner.stdout, name+" "+vp) && !strings.Contains(banner.stdout, name+" "+v[0]+"|") {
			t.Errorf("A2 %s %s: expected the help banner to list the verb as %q; actual banner has no such line", name, vp, name+" "+vp)
		}

		// A2: verb help, exit 0, usage on stdout, quiet stderr, no side effects.
		forms := [][]string{append(append([]string{}, v...), "-h"), append(append([]string{}, v...), "--help"), append([]string{"help"}, v...)}
		for _, f := range forms {
			before := snapshot(h.tmp)
			r := h.run(append([]string{}, f...)...)
			after := snapshot(h.tmp)
			form := strings.Join(f, " ")
			if r.code != 0 {
				t.Errorf("A2 %s %s: expected exit 0; actual %d (stderr=%q)", name, form, r.code, trim(r.stderr))
			}
			if !strings.Contains(r.stdout, v[len(v)-1]) || !strings.Contains(r.stdout, strings.TrimPrefix(name, "nova-")) {
				t.Errorf("A2 %s %s: expected the verb's usage on stdout naming %q; actual stdout=%q", name, form, v[len(v)-1], trim(r.stdout))
			}
			if r.stderr != "" {
				t.Errorf("A2 %s %s: expected empty stderr; actual %q", name, form, trim(r.stderr))
			}
			if before != after {
				t.Errorf("A2 %s %s: expected no side effects; actual the working directory changed:\nbefore:\n%s\nafter:\n%s", name, form, before, after)
			}
		}

		// A3: unknown flag, one line, exit 2, nothing on stdout, no Go text.
		args := append(append([]string{}, v...), "--no-such-flag")
		r := h.run(args...)
		form := strings.Join(args, " ")
		if r.code != 2 {
			t.Errorf("A3 %s %s: expected exit 2; actual %d", name, form, r.code)
		}
		if r.stdout != "" {
			t.Errorf("A3 %s %s: expected empty stdout; actual %q", name, form, trim(r.stdout))
		}
		h.oneLine("A3", vp, form, r)
		if !strings.Contains(r.stderr, "--no-such-flag") {
			t.Errorf("A3 %s %s: expected the stderr line to name --no-such-flag; actual %q", name, form, trim(r.stderr))
		}
		h.noNoise("A3", form, r)
	}

	// A4: unknown verb.
	r := h.run("no-such-verb")
	if r.code != 2 {
		t.Errorf("A4 %s no-such-verb: expected exit 2; actual %d (stdout=%q stderr=%q)", name, r.code, trim(r.stdout), trim(r.stderr))
	}
	if !strings.Contains(r.stderr, "no-such-verb") {
		t.Errorf("A4 %s no-such-verb: expected stderr to name the verb no-such-verb; actual %q", name, trim(r.stderr))
	}
	seen := map[string]bool{}
	for _, v := range spec.verbs {
		if seen[v[0]] {
			continue
		}
		seen[v[0]] = true
		if !strings.Contains(r.stderr, v[0]) {
			t.Errorf("A4 %s no-such-verb: expected stderr to list the verb %q; actual %q", name, v[0], trim(r.stderr))
		}
	}
	if !strings.Contains(r.stderr, name+" help") {
		t.Errorf("A4 %s no-such-verb: expected stderr to point at %q; actual %q", name, name+" help", trim(r.stderr))
	}
	if r.stdout != "" {
		t.Errorf("A4 %s no-such-verb: expected empty stdout; actual %q", name, trim(r.stdout))
	}
	h.noNoise("A4", "no-such-verb", r)

	// A5: missing required input, exit 2, one-line shape, every missing item named at once.
	for _, m := range spec.missing {
		vp := strings.Join(firstVerb(m.args, spec), " ")
		r := h.run(append([]string{}, m.args...)...)
		form := strings.Join(m.args, " ")
		if r.code != 2 {
			t.Errorf("A5 %s %s: expected exit 2 for the missing input; actual %d (stdout=%q stderr=%q)", name, form, r.code, trim(r.stdout), trim(r.stderr))
		}
		if r.stdout != "" {
			t.Errorf("A5 %s %s: expected empty stdout; actual %q", name, form, trim(r.stdout))
		}
		h.oneLine("A5", vp, form, r)
		for _, w := range m.want {
			found := false
			for _, alt := range strings.Split(w, "|") {
				found = found || strings.Contains(r.stderr, alt)
			}
			if !found {
				t.Errorf("A5 %s %s: expected one refusal naming every missing input, including %q; actual %q", name, form, w, trim(r.stderr))
			}
		}
		h.noNoise("A5", form, r)
	}
}

// firstVerb returns the longest spec verb path that prefixes args, else the first word.
// A flag-first command line (nova-sandbox --read /x -- cmd) has no verb: nil.
func firstVerb(args []string, spec toolSpec) []string {
	if strings.HasPrefix(args[0], "-") {
		return nil
	}
	best := args[:1]
	for _, v := range spec.verbs {
		if len(v) <= len(args) && len(v) > len(best) && strings.Join(args[:len(v)], " ") == strings.Join(v, " ") {
			best = v
		}
	}
	return best
}

// oneLine checks the CLI-STYLE (f) shape: one line, plus at most one hint line.
func (h *harness) oneLine(id, verb, form string, r result) {
	h.t.Helper()
	who := h.tool
	if verb != "" {
		who += " " + verb
	}
	re := regexp.MustCompile("^" + regexp.QuoteMeta(who) + `: .*; run: ` + regexp.QuoteMeta(who) + ` -h$`)
	lines := strings.Split(strings.TrimRight(r.stderr, "\n"), "\n")
	if r.stderr == "" {
		h.t.Errorf("%s %s %s: expected one stderr line %q; actual empty stderr", id, h.tool, form, who+": <what is wrong>; run: "+who+" -h")
		return
	}
	if len(lines) > 2 {
		h.t.Errorf("%s %s %s: expected one refusal line plus at most one hint line; actual %d lines: %q", id, h.tool, form, len(lines), trim(r.stderr))
	}
	if !re.MatchString(lines[0]) {
		h.t.Errorf("%s %s %s: expected first stderr line to match %s; actual %q", id, h.tool, form, re, lines[0])
	}
}
