package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// A second test step in the same root takes the next round and leaves the first
// file byte for byte. <root>/<name> is what the next run removes; these files are not in it.
func TestTestStreamRoundsDoNotReplaceEachOther(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first, err := writeTestStream(dir, "round-one\n")
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeTestStream(dir, "round-two\n")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first) != "test-1.jsonl" || filepath.Base(second) != "test-2.jsonl" {
		t.Fatalf("rounds = %s then %s, want test-1.jsonl then test-2.jsonl", first, second)
	}
	got, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "round-one\n" {
		t.Fatalf("the first round was replaced: %q", got)
	}
}

// Two differently named batches may share one --root. The writers reserve
// test-<round>.jsonl with O_EXCL, so each keeps its own path and its own
// bytes. A stat-then-truncate would hand both the same file.
func TestConcurrentWritersSharingARootKeepDistinctStreams(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const n = 16
	paths := make([]string, n)
	payloads := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		payloads[i] = fmt.Sprintf("stream-%02d\n%s\n", i, strings.Repeat("abcdefghij", 20+i))
		go func(i int) {
			defer wg.Done()
			paths[i], errs[i] = writeTestStream(dir, payloads[i])
		}(i)
	}
	wg.Wait()
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("writer %d: %v", i, errs[i])
		}
		if !strings.HasPrefix(paths[i], dir+string(os.PathSeparator)) {
			t.Fatalf("writer %d wrote outside the root: %s", i, paths[i])
		}
		if _, ok := testStreamRound(filepath.Base(paths[i])); !ok {
			t.Fatalf("writer %d path %s is not test-<round>.jsonl", i, paths[i])
		}
		if seen[paths[i]] {
			t.Fatalf("two writers got %s", paths[i])
		}
		seen[paths[i]] = true
		got, err := os.ReadFile(paths[i])
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != payloads[i] {
			t.Fatalf("writer %d payload at %s is not intact (%d bytes, want %d)", i, paths[i], len(got), len(payloads[i]))
		}
	}
}

// THE STEP THE TESTS DO NOT RUN IS STILL THE STEP THE GATE RUNS, and this is what says so.
//
// It reads the two lists rather than starting anything: the product's gate must carry the
// cross vet, immediately after `vet`, under GOOS=windows; and the list the tests run must
// differ from it by THAT ONE NAME AND NO OTHER, so a second step can never be quietly
// added to the exemption and go untested everywhere.
func TestTheGateCrossVetsForWindows(t *testing.T) {
	t.Parallel()
	at := -1
	for i, step := range batchGate {
		if step.name == crossVetStep {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("the gate no longer carries a %s step; the gate runs on one operating system and CI runs on three, and three members green here were red on CI's windows legs the day it did not", crossVetStep)
	}
	if at == 0 || batchGate[at-1].name != "vet" {
		t.Errorf("%s does not follow vet in the gate; it is the same read on another platform and belongs beside it", crossVetStep)
	}
	var goos string
	for _, kv := range batchGate[at].env {
		if name, value, _ := strings.Cut(kv, "="); name == "GOOS" {
			goos = value
		}
	}
	if goos != "windows" {
		t.Errorf("the %s step's GOOS is %q, want windows; a cross vet that does not cross checks the platform it already checked", crossVetStep, goos)
	}

	real, lab := map[string]bool{}, map[string]bool{}
	for _, step := range batchGate {
		real[step.name] = true
	}
	for _, step := range labBatchGate() {
		lab[step.name] = true
	}
	var missing []string
	for name := range real {
		if !lab[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) != 1 || missing[0] != crossVetStep {
		t.Errorf("the tests' gate omits %v; it may omit %s and nothing else, or a step goes untested on every bench and every runner", missing, crossVetStep)
	}
	for name := range lab {
		if !real[name] {
			t.Errorf("the tests' gate runs a step %q the real gate does not; a test of a suite nobody runs is not a test", name)
		}
	}
}

// Rule 13, carried into the CHILDREN the gate starts: every check runs with its temp
// directory inside the batch's own working directory. An sbcl suite and a go build that
// key off the ambient one write into /tmp instead, and two gates on one host wrecked
// each other's state that way (tools/ci/lisp-test.sh, 2026-09-18).
func TestPrivateTempEnvPointsEveryTempVariableAtTheBatchsOwnDirectory(t *testing.T) {
	t.Parallel()
	got := privateTempEnv([]string{"PATH=/bin", "TMPDIR=/tmp/ambient", "HOME=/home/x", "GOTMPDIR=/tmp/ambient"}, "/w/tmp")
	seen := map[string]int{}
	for _, kv := range got {
		key, value, _ := strings.Cut(kv, "=")
		seen[key]++
		if strings.Contains(key, "TMP") || strings.Contains(key, "TEMP") {
			if value != "/w/tmp" {
				t.Errorf("%s is %q, want the batch's own temp directory", key, value)
			}
		}
	}
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "GOTMPDIR", "LISP_TEST_TMPROOT"} {
		if seen[key] != 1 {
			t.Errorf("the child environment holds %s %d times, want exactly one", key, seen[key])
		}
	}
}

// makeRecipe returns the tab-indented recipe lines of one Makefile target, joined by
// newlines, or "" when the target has none. A target may be written more than once -- the
// CL test is a `test: PKGS := ...` line and then a `test:` with the recipe -- so every
// recipe line under any occurrence of the name belongs to it.
func makeRecipe(makefile, target string) string {
	var out []string
	in := false
	for _, line := range strings.Split(makefile, "\n") {
		if strings.HasPrefix(line, "\t") {
			if in {
				out = append(out, strings.TrimPrefix(line, "\t"))
			}
			continue
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		in = strings.HasPrefix(line, target+":")
	}
	recipe := strings.Join(out, "\n")
	// A flag the recipe carries through a variable -- `$(GOTEST_COUNT_FLAG)`,
	// whose default is `-count=1` -- is still the flag `make test` runs by hand, so
	// each `$(NAME)` is expanded from the Makefile's own `NAME ?= value` line the
	// way make expands it. A variable with no default expands to nothing, which is
	// what make does too, and the flag check below then names the loss.
	for _, m := range regexp.MustCompile(`\$\(([A-Z_]+)\)`).FindAllStringSubmatch(recipe, -1) {
		def := regexp.MustCompile(`(?m)^` + m[1] + `\s*\??=[ \t]*(.*)$`).FindStringSubmatch(makefile)
		value := ""
		if def != nil {
			value = strings.TrimSpace(def[1])
		}
		recipe = strings.ReplaceAll(recipe, m[0], value)
	}
	return recipe
}

// THE GATE TESTS THE WAY CI TESTS, and this reads every side so it goes red the day any
// of them moves. integration-4 ran green on hulk under a plain `go test ./...` and three
// CI legs then failed, because CI does not run a plain `go test ./...`; a gate that tests
// differently from CI is a gate that passes what CI fails.
//
// integration-4 also moved the command itself: ci.yml's `test` step is now `make test
// PKGS=...` and the Makefile's `test` target holds the flags. So this reads BOTH -- that
// ci.yml still delegates to `make test`, and what that target actually runs -- and
// the merge step table must mirror the target.
func TestTheGateTestsTheWayCIDoes(t *testing.T) {
	t.Parallel()
	yml, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(yml), "make test PKGS=") {
		t.Fatal("ci.yml's test step no longer runs `make test PKGS=...`; the gate mirrors whatever CI runs, so find the command CI runs now and update the test step in internal/merge/step.go with it")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	recipe := makeRecipe(string(raw), "test")
	if !strings.Contains(recipe, "test") {
		t.Fatalf("the Makefile's `test` target has no recipe, but ci.yml runs `make test`; update the test step in internal/merge/step.go with whatever CI runs now\nrecipe: %q", recipe)
	}
	got := strings.Join(ciTestArgs(), " ")
	// -json travels as GOFLAGS=-json on CI's outer command and as an argv flag on the
	// gate's, which is the same thing for that command; both spellings contain "-json".
	for _, flag := range []string{"-json", "-count=1"} {
		if !strings.Contains(recipe, flag) {
			t.Errorf("the Makefile's `test` target no longer carries %q; whatever it carries now is what ciTestArgs must mirror\nrecipe: %s", flag, recipe)
		}
		if !strings.Contains(got, flag) {
			t.Errorf("the gate's test command is %q and does not carry %q, which the Makefile's `test` target does", got, flag)
		}
	}
	// A -timeout the gate sets and CI does not is a gate that can go red on a tree CI
	// passes, which is the same divergence from the other side.
	if strings.Contains(got, "-timeout") && !strings.Contains(recipe, "-timeout") {
		t.Errorf("the gate's test command is %q and sets a per-package -timeout the Makefile's `test` target does not:\n%s", got, recipe)
	}
	if !strings.HasPrefix(got, "go test ") || !strings.HasSuffix(got, " ./...") {
		t.Errorf("the gate's test command is %q; it is `go test <the Makefile's flags> ./...`, the whole merged tree in one run where CI splits it across the matrix", got)
	}
}

// The -json stream is read with the SAME decoder cmd/nova-ci slowtests reads it with, so
// the gate and the budget check cannot disagree about what the stream said.
func TestTestFailuresReadsTheJSONStream(t *testing.T) {
	t.Parallel()
	stream := `{"Action":"run","Package":"example.com/batch/pkg/c","Test":"TestBroken"}
{"Action":"output","Package":"example.com/batch/pkg/c","Test":"TestBroken","Output":"    c_test.go:5: the poison\n"}
{"Action":"fail","Package":"example.com/batch/pkg/c","Test":"TestBroken","Elapsed":0}
{"Action":"pass","Package":"example.com/batch/pkg/a","Elapsed":0.01}
{"Action":"fail","Package":"example.com/batch/pkg/c","Elapsed":0.123}
`
	pkgs, tests, ok := testFailures(stream)
	if !ok {
		t.Fatal("a stream that is all JSON must read as JSON")
	}
	if len(pkgs) != 1 || pkgs[0] != "example.com/batch/pkg/c" {
		t.Errorf("packages = %v, want [example.com/batch/pkg/c]", pkgs)
	}
	if len(tests) != 1 || tests[0] != "TestBroken" {
		t.Errorf("tests = %v, want [TestBroken]", tests)
	}
	// A build failure writes plain text on stderr and runCheck captures both streams, so
	// a stream that is not all JSON is NOT read as an empty one -- it falls back to the
	// text reader, and a gate that read it as empty would print no failing package at all.
	if _, _, ok := testFailures("# example.com/batch/pkg/c\nc.go:3: undefined: X\nFAIL\texample.com/batch/pkg/c [build failed]\n"); ok {
		t.Error("a build failure's plain text read as a JSON stream; it must fall back to the text reader")
	}
}

// #2499 item 3 / #2508. go build writes `# package` then the compiler lines.
// firstLine kept only the header, so a gate that failed in
// bench/tools/realpacket-gen named the package and not `undefined: Foo`.
func TestStepFailureKeepsTheBuildCompilerLine(t *testing.T) {
	t.Parallel()
	out := "# example.com/batch/bench/tools/realpacket-gen\n./main.go:3: undefined: Foo\n"
	_, _, reason := stepFailure(batchStep{name: "build", command: "go build ./..."}, out, fmt.Errorf("exit status 1"))
	if !strings.Contains(reason, "undefined: Foo") {
		t.Errorf("reason = %q; a failing go build's compiler line must appear on the verdict, not only the # package header", reason)
	}
	if !strings.Contains(reason, "realpacket-gen") {
		t.Errorf("reason = %q; the package header is still part of the captured stderr", reason)
	}
}

// The BATCH FAIL reason is capped at oneline.TailBytes (500). A pathological
// compiler dump cannot be the whole of a reader's context; the mark ...+<n>B
// says when more was dropped.
func TestStepFailureCapsTheBuildStderr(t *testing.T) {
	t.Parallel()
	if stepReasonBytes != oneline.TailBytes {
		t.Errorf("stepReasonBytes = %d, want oneline.TailBytes=%d; the receipt names that cap", stepReasonBytes, oneline.TailBytes)
	}
	out := "# example.com/batch/pkg/d\n" + strings.Repeat("x", 2000) + "undefined: Foo\n"
	_, _, got := stepFailure(batchStep{name: "build", command: "go build ./..."}, out, nil)
	if len(got) > stepReasonBytes {
		t.Errorf("reason is %d bytes, want at most stepReasonBytes=%d (oneline.TailBytes)", len(got), stepReasonBytes)
	}
	if !strings.Contains(got, "...+") || !strings.HasSuffix(got, "B") {
		t.Errorf("a stderr longer than stepReasonBytes=%d must carry the cap mark ...+<n>B, got %q", stepReasonBytes, got)
	}
}

// The failing packages and tests on the FAIL line are read out of the go test run's own
// output, so a caller sees what to look at without opening the log.
func TestFailuresInReadsThePackagesAndTests(t *testing.T) {
	t.Parallel()
	out := "--- FAIL: TestBroken (0.00s)\n    c_test.go:5: the poison\nFAIL\nFAIL\texample.com/batch/pkg/c\t0.123s\nok  \texample.com/batch/pkg/a\t0.010s\nFAIL\n"
	pkgs, tests := failuresIn(out)
	if len(pkgs) != 1 || pkgs[0] != "example.com/batch/pkg/c" {
		t.Errorf("packages = %v, want [example.com/batch/pkg/c]", pkgs)
	}
	if len(tests) != 1 || tests[0] != "TestBroken" {
		t.Errorf("tests = %v, want [TestBroken]", tests)
	}
}

// The required check's name is a flag, else .nova-merge required-check=, else ci-ok
// (nova-tools #2499). These tests are the file's own grammar, with no clone and no forge.
func TestRequiredCheckFromNovaMerge(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		raw     string
		want    string
		errPart string
	}{
		{name: "empty file is unset", raw: "", want: ""},
		{name: "comments only", raw: "# schema\n\n", want: ""},
		{name: "schema tests", raw: "required-check=tests\n", want: "tests"},
		{name: "spaces around the equals", raw: " required-check = tests \n", want: "tests"},
		{name: "unknown keys ignored", raw: "siblings=foo\nrequired-check=tests\n", want: "tests"},
		{name: "CRLF", raw: "required-check=tests\r\n", want: "tests"},
		{name: "empty value", raw: "required-check=\n", errPart: "empty"},
		{name: "duplicate", raw: "required-check=tests\nrequired-check=ci-ok\n", errPart: "more than once"},
		{name: "not key=value", raw: "required-check tests\n", errPart: "key=value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := requiredCheckFromNovaMerge([]byte(tc.raw))
			if tc.errPart != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errPart) {
					t.Fatalf("want an error naming %q, got %q / %v", tc.errPart, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveRequiredCheckPrefersTheFlagThenTheFileThenCiOk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	got, err := resolveRequiredCheck("", dir)
	if err != nil {
		t.Fatalf("no file, no flag: %v", err)
	}
	if got != batchRequiredCheckDefault {
		t.Fatalf("default is %q, got %q", batchRequiredCheckDefault, got)
	}

	if err := os.WriteFile(filepath.Join(dir, novaMergeFile), []byte("required-check=tests\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = resolveRequiredCheck("", dir)
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	if got != "tests" {
		t.Fatalf("file names tests, got %q", got)
	}

	got, err = resolveRequiredCheck("ci-ok", dir)
	if err != nil {
		t.Fatalf("flag: %v", err)
	}
	if got != "ci-ok" {
		t.Fatalf("the flag wins over the file, got %q", got)
	}
}

// ciTestArgs is the gate's test command, read off internal/merge's step table --
// the ONE list the gate runs -- so these assertions follow it the day it moves.
// Every flag on it is there for a reason: -json because CI reads that stream
// with cmd/nova-ci slowtests (and it changes what a tool under test sees), and
// -count=1 because no cached result may stand in for a run on the merged tree.
// TestTheGateTestsTheWayCIDoes holds it to ci.yml and the Makefile.
func ciTestArgs() []string {
	for _, step := range merge.FullClassSteps {
		if step.Name == "test" {
			return strings.Fields(step.Command)
		}
	}
	panic("internal/merge FullClassSteps has no test step")
}
