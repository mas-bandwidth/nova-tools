package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type fakePortHost struct {
	headSHA  string
	baseName string
	baseSHA  string
	files    map[string]map[string]string
	trees    map[string][]string
	diff     string
	calls    int
}

func (f *fakePortHost) Head(pr int) (string, error) {
	f.calls++
	return f.headSHA, nil
}

func (f *fakePortHost) Base(pr int) (string, string, error) {
	f.calls++
	return f.baseName, f.baseSHA, nil
}

func (f *fakePortHost) File(sha, p string) ([]byte, error) {
	f.calls++
	m := f.files[sha]
	if m == nil {
		return nil, os.ErrNotExist
	}
	c, ok := m[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(c), nil
}

func (f *fakePortHost) List(sha string) ([]string, error) {
	f.calls++
	return f.trees[sha], nil
}

func (f *fakePortHost) Diff(base, head string) (string, error) {
	f.calls++
	return f.diff, nil
}

func withFakePortHost(t *testing.T, f *fakePortHost) {
	t.Helper()
	old := openPortHost
	openPortHost = func(ctx context.Context, lane string) (portHost, error) { return f, nil }
	t.Cleanup(func() { openPortHost = old })
}

func portDiffFile(path string, newStart int, adds ...string) string {
	var b strings.Builder
	b.WriteString("diff --git a/" + path + " b/" + path + "\n")
	b.WriteString("--- a/" + path + "\n")
	b.WriteString("+++ b/" + path + "\n")
	b.WriteString("@@ -1,0 +" + strconv.Itoa(newStart) + "," + strconv.Itoa(len(adds)) + " @@\n")
	for _, a := range adds {
		b.WriteString("+" + a + "\n")
	}
	return b.String()
}

const portDoc = `# Porting rules

## port-gate
rule: port-gate
positive: **/*_test.go
negative: **/*_negative_test.go
`

func newPortFixture(diff string) *fakePortHost {
	head := strings.Repeat("a", 40)
	base := strings.Repeat("b", 40)
	return &fakePortHost{
		headSHA:  head,
		baseName: "main",
		baseSHA:  base,
		diff:     diff,
		files: map[string]map[string]string{
			head: {
				"docs/PORTING.md":           portDoc,
				"src/gate_test.go":          "package src\n\n// the ported behaviour runs here\n",
				"src/gate_negative_test.go": "package src\n\n// the control\n// fails without the gate\n// end\n",
			},
		},
		trees: map[string][]string{
			head: {"docs/PORTING.md", "src/gate_test.go", "src/gate_negative_test.go"},
		},
	}
}

func runPort(t *testing.T, f *fakePortHost, args ...string) (int, string, string) {
	t.Helper()
	withFakePortHost(t, f)
	var out, errb bytes.Buffer
	code := run(append([]string{"port"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

// 1. both witnesses: PORT OK, the packet lists both with their rules.
func TestPortBothWitnessesOK(t *testing.T) {
	lane := t.TempDir()
	dest := filepath.Join(lane, "port.md")
	diff := portDiffFile("src/gate_test.go", 3, "// the ported behaviour runs here")
	diff += portDiffFile("src/gate_negative_test.go", 4, "// fails without the gate")
	f := newPortFixture(diff)
	code, out, errb := runPort(t, f,
		"--lane", lane, "--table", "port-gate", "--pr", "7", "--out", dest)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, errb, out)
	}
	if !strings.Contains(out, "PORT OK") ||
		!strings.Contains(out, "positive=src/gate_test.go:3") ||
		!strings.Contains(out, "negative=src/gate_negative_test.go:4") {
		t.Fatalf("PORT OK line missing witnesses: %s", out)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("no packet: %v", err)
	}
	pkt := string(b)
	if !strings.Contains(pkt, "## Witnesses") ||
		!strings.Contains(pkt, "src/gate_test.go:3") ||
		!strings.Contains(pkt, "src/gate_negative_test.go:4") ||
		!strings.Contains(pkt, "rule=port-gate") {
		t.Fatalf("packet missing witnesses: %s", pkt)
	}
}

// 2. an empty +0 -0 diff over a tree that already holds the gate.
func TestPortEmptyDiffRefusesNamingExistingGate(t *testing.T) {
	lane := t.TempDir()
	dest := filepath.Join(lane, "port.md")
	f := newPortFixture("")
	f.files[f.headSHA]["src/gate_test.go"] = "package src\n// gate already carried\n"
	code, out, errb := runPort(t, f,
		"--lane", lane, "--table", "port-gate", "--pr", "7", "--out", dest)
	if code != 2 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, errb, out)
	}
	if !strings.Contains(errb, "PORT REFUSED") ||
		!strings.Contains(errb, "existing_gate=src/gate_test.go:2") {
		t.Fatalf("refusal missing existing_gate: %s", errb)
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty PR wrote a packet: %v", err)
	}
}

// 3. the positive witness only: the remedy names the negative rule.
func TestPortMissingNegativeRemedyNamesRule(t *testing.T) {
	lane := t.TempDir()
	diff := portDiffFile("src/gate_test.go", 3, "// the ported behaviour runs here")
	f := newPortFixture(diff)
	code, out, errb := runPort(t, f,
		"--lane", lane, "--table", "port-gate", "--pr", "7", "--out", filepath.Join(lane, "port.md"))
	if code != 2 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, errb, out)
	}
	if !strings.Contains(errb, "PORT REFUSED") || !strings.Contains(errb, "port-gate") ||
		!strings.Contains(errb, "unverified-negative-control") {
		t.Fatalf("negative remedy does not name the rule and the mistake: %s", errb)
	}
}

// 4. docs/PORTING.md holds no such section: the remedy names the headings.
func TestPortUnknownSectionNamesHeadings(t *testing.T) {
	lane := t.TempDir()
	doc := "# Porting rules\n\n## only-rule\nrule: only-rule\npositive: a\nnegative: b\n"
	f := newPortFixture("")
	f.files[f.headSHA]["docs/PORTING.md"] = doc
	code, out, errb := runPort(t, f,
		"--lane", lane, "--table", "missing-rule", "--pr", "7", "--out", filepath.Join(lane, "port.md"))
	if code != 2 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, errb, out)
	}
	if !strings.Contains(errb, "PORT REFUSED") || !strings.Contains(errb, "only-rule") {
		t.Fatalf("section remedy does not name the headings: %s", errb)
	}
}

// 5. a witness whose file:line the head does not hold.
func TestPortWitnessHeadDoesNotHoldRefused(t *testing.T) {
	lane := t.TempDir()
	diff := portDiffFile("src/gate_test.go", 99, "// the ported behaviour runs here")
	diff += portDiffFile("src/gate_negative_test.go", 4, "// fails without the gate")
	f := newPortFixture(diff)
	code, out, errb := runPort(t, f,
		"--lane", lane, "--table", "port-gate", "--pr", "7", "--out", filepath.Join(lane, "port.md"))
	if code != 2 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, errb, out)
	}
	if !strings.Contains(errb, "src/gate_test.go:99") {
		t.Fatalf("witness remedy does not print the file:line: %s", errb)
	}
}

// 6. a missing --table and a missing --pr each refuse before any host call.
func TestPortMissingFlagsRefuseBeforeHostCalls(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"table", []string{"--lane", "L", "--pr", "7"}},
		{"pr", []string{"--lane", "L", "--table", "port-gate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPortFixture("")
			code, _, errb := runPort(t, f, tc.args...)
			if code != 2 {
				t.Fatalf("code=%d stderr=%s", code, errb)
			}
			if !strings.Contains(errb, "refusing to guess") {
				t.Fatalf("missing %s did not refuse: %s", tc.name, errb)
			}
			if f.calls != 0 {
				t.Fatalf("missing %s made %d host calls", tc.name, f.calls)
			}
		})
	}
}

// 7. a kill leaves no --out; two concurrent runs to one --out leave one packet.
func TestPortKilledRunLeavesNoOut(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "port.md")
	old := linkFile
	linkFile = func(oldname, newname string) error { return errors.New("killed before publish") }
	t.Cleanup(func() { linkFile = old })
	if err := writeExclusive(dest, []byte("whole packet\n")); err == nil {
		t.Fatal("killed publish reported success")
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("killed run left --out: %v", err)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(left) != 0 {
		t.Fatalf("killed run left temp files: %v", left)
	}
}

func TestPortConcurrentRunsToOneOut(t *testing.T) {
	lane := t.TempDir()
	dest := filepath.Join(lane, "port.md")
	diff := portDiffFile("src/gate_test.go", 3, "// the ported behaviour runs here")
	diff += portDiffFile("src/gate_negative_test.go", 4, "// fails without the gate")
	f := newPortFixture(diff)
	withFakePortHost(t, f)
	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var out, errb bytes.Buffer
			codes[idx] = run([]string{"port", "--lane", lane, "--table", "port-gate", "--pr", "7", "--out", dest}, &out, &errb)
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		if c == 0 {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("expected exactly one successful port, got %d", ok)
	}
	b, err := os.ReadFile(dest)
	if err != nil || len(b) == 0 || !strings.HasSuffix(string(b), "\n") {
		t.Fatalf("published packet is not whole: err=%v %q", err, b)
	}
	left, _ := filepath.Glob(filepath.Join(lane, "*.tmp"))
	if len(left) != 0 {
		t.Fatalf("concurrent runs left temp files: %v", left)
	}
}

// 8. the source uses the injected host and holds no short wall-clock literal.
func TestPortSourceInjectedHostNoShortClock(t *testing.T) {
	b, err := os.ReadFile("port.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, bad := range []string{"net/http", "http.Get", "net.Dial", "time.Sleep"} {
		if strings.Contains(src, bad) {
			t.Errorf("port.go reaches the network or sleeps: %q", bad)
		}
	}
	re := regexp.MustCompile(`([0-9]+)\s*\*\s*time\.(Second|Millisecond)`)
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		n, _ := strconv.Atoi(m[1])
		if m[2] == "Millisecond" || (m[2] == "Second" && n < 10) {
			t.Errorf("wall-clock literal under ten seconds: %q", m[0])
		}
	}
}
