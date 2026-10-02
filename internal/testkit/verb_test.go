package testkit_test

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/stretchr/testify/assert"
)

// demo is a tool built on the shared skeleton, so the refusal checks are held
// against the grammar the tools really print, not a copy of it.
var demo = testkit.Main((&tool.Tool{
	Name: "nova-demo", What: "demo is a tool the kit's tests run", How: "It has one verb.",
	ExitTable: "0 sent, 2 refused",
	Verbs: []tool.Verb{{
		Name: "send", Usage: "send --to <name>", Example: "send --to reader", Effect: tool.Inspection,
		Flags: func(f *tool.Flags) { f.Required("to", "the reader's name") },
		Run:   func(c *tool.Call) *tool.Out { return tool.Done() },
	}},
}).Run)

// says prints its first argument on stderr, its second on stdout, and exits
// with the code its third names: a refusal shape a test can bend.
var says = testkit.Main(func(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprint(stderr, args[0])
	fmt.Fprint(stdout, args[1])
	code, err := strconv.Atoi(args[2])
	if err != nil {
		return -1
	}
	return code
})

func TestRanChecksPassOnARunThatHoldsThem(t *testing.T) {
	t.Parallel()
	r := echo.DoIn(t, "in", "fail", "x").Exit(2).Out(`stdin="in"`, `"x"`).NotOut("nowhere").Err("err").NotErr("nowhere")
	assert.Equal(t, []string{"fail", "x"}, r.Args)
	assert.Equal(t, testkit.Result{Code: 2, Stdout: `args=["fail" "x"] stdin="in"`, Stderr: "err"}, r.Result)
	demo.Do(t, "send").Exit(2).Refused("--to is required").NotOut("OK")
	demo.Do(t, "send", "--to", "reader").Exit(0).Out("SEND OK")
}

func TestEachRanCheckFailsNamingTheRunAndBothStreams(t *testing.T) {
	t.Parallel()
	for name, check := range map[string]func(r testkit.Ran){
		"exit":    func(r testkit.Ran) { r.Exit(0) },
		"out":     func(r testkit.Ran) { r.Out("nowhere") },
		"not out": func(r testkit.Ran) { r.NotOut("args=") },
		"err":     func(r testkit.Ran) { r.Err("nowhere") },
		"not err": func(r testkit.Ran) { r.NotErr("err") },
		"refused": func(r testkit.Ran) { r.Refused("err") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{TB: t}
			runs(rec, func() { check(echo.Do(rec, "fail", "x")) })
			assert.True(t, rec.failed, "the check passed a run that breaks it")
			assert.Contains(t, rec.msg, `run ["fail" "x"]: exit=2 stdout="args=[\"fail\" \"x\"] stdin=\"\"" stderr="err"`, "the failure does not name the run")
		})
	}
}

func TestRefusedNamesEveryClauseItMisses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, stderr, code string
		misses             []string
	}{
		{"the grammar", "SEND REFUSED: --to is required; run: nova-demo help\n", "2", nil},
		{"exit 0", "SEND REFUSED: --to is required; run: nova-demo help\n", "0", []string{"a refusal never exits 0"}},
		{"no status word", "SEND: --to is required; run: nova-demo help\n", "2", []string{"no status word REFUSED"}},
		{"no remedy", "SEND REFUSED: --to is required\n", "2", []string{`no remedy "; run: "`}},
		{"the word on another line", "SEND REFUSED: see below; run: x\n--to is required\n", "2", []string{"no status word", "no remedy"}},
		{"not said", "SEND REFUSED: something else; run: x\n", "2", []string{`no stderr line says "--to is required"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &recorder{TB: t}
			runs(rec, func() { says.Do(rec, tc.stderr, "", tc.code).Refused("--to is required") })
			assert.Equal(t, tc.misses != nil, rec.failed, rec.msg)
			for _, m := range tc.misses {
				assert.Contains(t, rec.msg, m)
			}
		})
	}
}

func TestRefusalsRunsEachRowAndReportsTheBadOnes(t *testing.T) {
	t.Parallel()
	testkit.Refusals(t, demo, []testkit.Refusal{
		{Args: []string{"send"}, Code: 2, Says: "--to is required; it wants the reader's name"},
		{Args: []string{"bogus"}, Code: 2, Says: `unknown verb "bogus"`},
		{Args: nil, Code: 2, Says: "no verb given"},
	})
	rec := &recorder{TB: t}
	runs(rec, func() {
		testkit.Refusals(rec, demo, []testkit.Refusal{
			{Args: []string{"send"}, Code: 1, Says: "--to is required"},
			{Args: []string{"send"}, Code: 2, Says: "--from is required"},
			{Args: []string{"send", "--to", "x"}, Code: 2, Says: "SEND"},
			{Args: []string{"bogus"}, Code: 2, Says: "unknown verb"},
		})
	})
	assert.True(t, rec.failed, "Refusals passed three bad rows")
	assert.Equal(t, 3, strings.Count(rec.msg, "not the refusal"), "a bad row hid the rows after it: %s", rec.msg)
	for _, want := range []string{"exit 2, want 1", `no stderr line says "--from is required"`, `run ["send" "--to" "x"]: exit=0`, "a refusal never exits 0"} {
		assert.Contains(t, rec.msg, want)
	}
	rec = &recorder{TB: t}
	runs(rec, func() { testkit.Refusals(rec, demo, nil) })
	assert.True(t, rec.failed, "Refusals passed a table of no rows")
}
