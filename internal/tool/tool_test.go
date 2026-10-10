package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRender pins the encoder output and JSON round-trip.
func TestRender(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		out   *Out
		lines string
	}{
		{"ok with facts", Done().Fact("session", "s1").Fact("bytes", 17).Fact("persisted", true).Fact("source", ""),
			"DEMO OK session=s1 bytes=17 persisted=true source=-\n"},
		{"a value with a space and an equals sign is one field", Done().Fact("path", "a b=c"),
			"DEMO OK path=a\\x20b\\x3dc\n"},
		{"items, a MORE and notes", func() *Out {
			o := Done().Fact("entries", 3)
			o.Item("entry", "id", "e1", "bytes", 5).Item("entry", "id", "e2", "bytes", 6).Item("entry", "id", "e3", "bytes", 7)
			return o.Cap(2).Note("a note\nwith a newline")
		}(), "DEMO OK entries=3\nDEMO ENTRY id=e1 bytes=5\nDEMO ENTRY id=e2 bytes=6\n" +
			"DEMO MORE kind=entry shown=2 total=3 " + MaxRemedy + "\nDEMO NOTE a note\\x0awith a newline\n"},
		{"free text is quoted with its spaces, a typed value is one token", Done().Fact("reason", Text("the store is gone")).Fact("dir", "<dir>"),
			"DEMO OK dir=<dir> reason=\"the store is gone\"\n"},
		{"a tool's own word stands for FAILED, the status and exit kept", Fail("the library differs").As("STALE").Fact("lib", "nova"),
			"DEMO STALE lib=nova: the library differs\n"},
		{"refused names every problem with the remedy", func() *Out {
			o := Refuse("--a is required", "--b is required")
			o.Remedy = "nova-demo help"
			return o
		}(), "DEMO REFUSED: --a is required; run: nova-demo help\nDEMO REFUSED: --b is required; run: nova-demo help\n"},
		{"failed carries its facts and its reason", Fail("the words differ").Fact("entry", "e1"),
			"DEMO FAILED entry=e1: the words differ\n"},
		{"a payload is printed as it is", Payload("nova-demo v1 darwin/arm64 go1"),
			"nova-demo v1 darwin/arm64 go1\n"},
		{"a payload beside a fact prints both, the payload last", Payload("the document").Fact("path", "p"),
			"DEMO OK path=p\nthe document\n"},
		{"a refusal with a payload prints the refusal and the payload", func() *Out {
			o := Refuse("half written")
			o.Payload = "the part"
			return o
		}(), "DEMO REFUSED: half written\nthe part\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.out.Verb, tc.out.token = "demo", "DEMO"
			var text, js bytes.Buffer
			tc.out.Render(&text, false)
			tc.out.Render(&js, true)
			assert.Equal(t, tc.lines, text.String(), "lines:\n%s\nwant:\n%s", text.String(), tc.lines)
			assert.Equal(t, 1, strings.Count(js.String(), "\n"), "JSON is not one line: %q", js.String())
			lines := text.String()
			if w := tc.out.Word; w != "" {
				assert.Contains(t, js.String(), `"word":"`+w+`"`)
				lines = strings.Replace(lines, "DEMO "+w+" ", "DEMO FAILED ", 1)
			}
			rig := NewRig(t, demo())
			got, want := rig.FromLines("DEMO", lines), rig.FromJSON(js.String())
			got.Verb, got.Exit = want.Verb, tc.out.Exit
			assert.True(t, want.Verb == "demo" && want.Exit == tc.out.Exit, "JSON result is %s exit %d, want demo exit %d", want.Verb, want.Exit, tc.out.Exit)
			assert.NotContains(t, js.String(), `\`+`u003c`, "the JSON is HTML-escaped")
			for i, n := range want.Notes {
				want.Notes[i] = strings.ReplaceAll(n, "\n", `\x0a`)
			}
			for k, v := range want.Facts {
				want.Facts[k] = strings.NewReplacer(" ", `\x20`, "=", `\x3d`).Replace(v)
			}
			for _, f := range tc.out.Facts {
				if s, ok := f.V.(Text); ok {
					want.Facts[f.K] = strconv.Quote(string(s))
				}
			}
			g, w := fmt.Sprintf("%+v", got), fmt.Sprintf("%+v", want)
			assert.Equal(t, w, g, "the lines and the JSON disagree:\nlines %s\njson  %s", g, w)
		})
	}
}

// TestTextIsTheProseTail pins Text in a line, in an item, and in JSON.
func TestTextIsTheProseTail(t *testing.T) {
	t.Parallel()
	o := Done().Fact("why", Text("all answered")).Fact("entries", 1)
	o.Item("unknown", "reason", Text("no version line: it printed nothing"), "name", "nova-x", "run", Text("nova-x version"), "raw", "")
	o.Verb, o.token = "report", "REPORT"
	var text, js bytes.Buffer
	assert.Equal(t, 0, o.Render(&text, false))
	assert.Equal(t, "REPORT OK entries=1 why=\"all answered\"\n"+
		"REPORT UNKNOWN name=nova-x raw=- reason=\"no version line: it printed nothing\" run=\"nova-x version\"\n", text.String())
	assert.Equal(t, 0, o.Render(&js, true))
	assert.JSONEq(t, `{"result":{"verb":"report","status":"ok","exit":0},"facts":{"why":"all answered","entries":1},
		"items":[{"kind":"unknown","fields":{"reason":"no version line: it printed nothing","name":"nova-x","run":"nova-x version","raw":""}}]}`, js.String())
}

// TestAResultThatIsNoJSONIsAFail pins the one value JSON cannot carry: a NaN
// fact under --json is a FAILED line naming the verb on stderr at exit 1, never
// an empty line and a success; Render returns that exit to a tool that renders
// for itself.
func TestAResultThatIsNoJSONIsAFail(t *testing.T) {
	t.Parallel()
	nan := &Tool{Name: "nova-nan", What: "measures", ExitTable: "0 done, 2 could not run.", Verbs: []Verb{
		{Name: "load", Usage: "load", Effect: Inspection, Run: func(*Call) *Out { return Done().Fact("load", math.NaN()) }},
	}}
	r := NewRig(t, nan).Run(1, "load", "--json")
	assert.Empty(t, r.Stdout)
	assert.True(t, strings.HasPrefix(r.Stderr, "LOAD FAILED: the result is no JSON, so it is not printed: json: unsupported value: NaN\n"), r.Stderr)

	var w bytes.Buffer
	o := Done().Fact("load", math.Inf(1))
	o.Verb = "load"
	assert.Equal(t, 1, o.Render(&w, true))
	assert.Contains(t, w.String(), "LOAD FAILED: the result is no JSON")
}

// bidiEscape is a six-character JSON escape, keeping invisible controls out of this file.
func bidi(cp rune) string       { return string(cp) }
func bidiEscape(cp rune) string { return fmt.Sprintf("%su%04x", backslash, cp) }

const backslash = "\x5c" // one backslash

// TestJSONRenderingEscapesBidiControlsInStrings pins that JSON escaping handles control characters and remains lossless.
func TestJSONRenderingEscapesBidiControlsInStrings(t *testing.T) {
	t.Parallel()
	fact := "ok" + bidi(0x202e) + "hello" + bidi(0x2067) + "there"
	snippet := "plain" + bidi(0x202e) + "snippet" + bidi(0x2067) + "tail"
	o := Done().Fact("why", fact).Item("hit", "snippet", snippet)
	o.Verb, o.token = "demo", "DEMO"
	var js bytes.Buffer
	require.Equal(t, 0, o.Render(&js, true))
	raw := js.String()
	assert.Contains(t, raw, bidiEscape(0x202e), "the override must be spelled as its escape, not carried raw")
	assert.Contains(t, raw, bidiEscape(0x2067), "the isolate must be spelled as its escape, not carried raw")
	assert.NotContains(t, raw, bidi(0x202e), "a raw override reorders the line for the reader")
	assert.NotContains(t, raw, bidi(0x2067), "a raw isolate reorders the line for the reader")
	var j struct {
		Facts map[string]string `json:"facts"`
		Items []struct {
			Fields map[string]string `json:"fields"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &j))
	assert.Equal(t, fact, j.Facts["why"], "the escape decodes to the same string: the JSON is lossless")
	assert.Equal(t, snippet, j.Items[0].Fields["snippet"], "the escape decodes to the same string: the JSON is lossless")
}

func demo() *Tool {
	return &Tool{
		Name:      "nova-demo",
		What:      "a tool that exists to be tested",
		How:       "It keeps nothing.\nIt reads its flags and says what it read.",
		ExitTable: "0 done, 1 said no, 2 could not run.",
		Stamp:     "v9.9.9",
		Verbs: []Verb{
			{
				Name: "put", Usage: "put --store <dir> --key <k> [--max <n>]", Example: "put --store ./s --key k",
				Effect: LocalWrite,
				Detail: "THE STORE is a directory the verb creates.",
				Flags: func(f *Flags) {
					f.Required("store", "a directory")
					f.Required("key", "a name")
					f.Int("n", 0, "how many rows")
					f.Check(func(c *Call) {
						if c.Int("n") < 0 {
							c.Problem("--n must be zero or more")
						}
					})
					f.Max()
				},
				Run: func(c *Call) *Out {
					o := Done().Fact("store", c.Str("store")).Fact("key", c.Str("key"))
					for i := 0; i < c.Int("n"); i++ {
						o.Item("row", "i", i)
					}
					return o
				},
			},
			{Name: "who", Usage: "who --width <n> [--actor <a>] [--op <id>] [--redis <addr>]", Flags: func(f *Flags) {
				f.String("actor", "", "who is acting")
				f.String("op", "", "the caller's operation id")
				f.String("redis", "seat.example:6379", "the Redis address")
				f.Int("width", 0, "slots")
			}, Run: func(c *Call) *Out {
				width := c.Int("width")
				if width < 1 {
					c.Problem(fmt.Sprintf("--width is required and is at least 1, got %d", width))
				}
				redis := c.Want("redis", "host:port")
				if o := c.Refused(); o != nil {
					return o
				}
				return Done().Fact("actor", c.Str("actor")).Fact("op", c.Str("op")).Fact("redis", redis).Fact("width", width)
			}},
			{Name: "deny", Usage: "deny", Run: func(*Call) *Out { return Fail("no") }},
			{Name: "forget", Usage: "forget", Run: func(c *Call) *Out { c.Problem("recorded, not returned"); return Done() }},
			{Name: "raw", Usage: "raw", Flags: func(f *Flags) { f.Prints() }, Run: func(c *Call) *Out {
				fmt.Fprintln(c.Stdout, "a line of its own")
				return Exit(1)
			}},
			{Name: "fn load", Usage: "fn load --name <n>", Effect: LocalWrite, DryRun: true, ExitTable: "0 loaded, 2 could not run.",
				Flags: func(f *Flags) { f.Required("name", "the function's name") },
				Run: func(c *Call) *Out {
					if c.DryRun() {
						return Done().Fact("would_load", c.Str("name"))
					}
					return Done().Fact("loaded", c.Str("name"))
				}},
			{Name: "fn ls", Usage: "fn ls", Effect: Inspection, Run: func(*Call) *Out { return Done() }},
			{Name: "careless", Usage: "careless", Effect: LocalWrite, DryRun: true, Run: func(*Call) *Out { return Done() }},
			{Name: "lib check", Usage: "lib check", Effect: Inspection, Run: func(*Call) *Out {
				o := Fail("the library differs").As("STALE")
				o.Remedy = "nova-demo lib load"
				return o
			}},
			{Name: "lib load", Usage: "lib load", Effect: LocalWrite, Run: func(*Call) *Out { return Done().As("UNCHANGED") }},
			{Name: "lib bogus", Usage: "lib bogus", Effect: Inspection, Run: func(*Call) *Out { return Done().As("GONE") }},
			{Name: "scan", Usage: "scan", Effect: Inspection, Run: func(*Call) *Out {
				o := Fail().Fact("files", 1).Item("finding", "at", "f:4").Item("dated", "n", 1).Note("a green clears the known shapes")
				return o.Findings("finding")
			}},
		},
		Words: []string{"STALE", "UNCHANGED"},
	}
}

// TestRun pins the dispatcher: banner, help, version, refusals, flags, and exits.
func TestRun(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		args        []string
		code        int
		stdout      []string // substrings, in order
		stderr      []string
		absent      []string // in neither stream
		emptyStdout bool
		emptyStderr bool
		stderrLines int
	}{
		{name: "bare refuses in one line naming the door", args: nil, code: 2, emptyStdout: true, stderrLines: 1,
			stderr: []string{"DEMO REFUSED: no verb given; the verbs are put, who, deny, forget, raw, fn load, fn ls, careless, lib check, lib load, lib bogus, scan, version; run: nova-demo help"}},
		{name: "help is the banner: what, how, usage, exit codes, example last", args: []string{"help"}, code: 0, emptyStderr: true,
			stdout: []string{"nova-demo: a tool that exists to be tested\n\nhow it works: It keeps nothing.\n", "usage:\n  nova-demo put --store <dir> --key <k> [--max <n>]\n",
				"  nova-demo version\n  nova-demo help [<verb>]\n", "Every verb but raw takes --json", "\nexit codes: 0 done, 1 said no, 2 could not run.\n", "\nexample:\n  nova-demo put --store ./s --key k\n"}},
		{name: "help of a verb is its -h", args: []string{"help", "put"}, code: 0, emptyStderr: true,
			stdout: []string{"usage: nova-demo put [flags]", "nova-demo put --store <dir>", "--json", "--max", "exit codes: 0 done, 1 said no, 2 could not run."}},
		{name: "a verb's -h touches nothing", args: []string{"put", "--store", "x", "-h"}, code: 0, emptyStderr: true,
			stdout: []string{"usage: nova-demo put [flags]"}},
		{name: "version is the build line", args: []string{"version"}, code: 0, emptyStderr: true, stdout: []string{"nova-demo v9.9.9 "}},
		{name: "--version is version", args: []string{"--version"}, code: 0, emptyStderr: true, stdout: []string{"nova-demo v9.9.9 "}},
		{name: "version --json carries the line as the payload", args: []string{"version", "--json"}, code: 0, emptyStderr: true,
			stdout: []string{`{"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-demo v9.9.9 `}},
		{name: "version refuses an argument", args: []string{"version", "x"}, code: 2, emptyStdout: true,
			stderr: []string{`VERSION REFUSED: takes no positional arguments, got "x" (flags come before arguments); run: nova-demo help`}},
		{name: "an unknown verb is refused", args: []string{"seal"}, code: 2, emptyStdout: true,
			stderr: []string{`DEMO REFUSED: unknown verb "seal"`}},
		{name: "every missing flag is named at once", args: []string{"put"}, code: 2, emptyStdout: true, stderrLines: 2,
			stderr: []string{"PUT REFUSED: --store is required; it wants a directory; refusing to guess; run: nova-demo help\n",
				"PUT REFUSED: --key is required; it wants a name; refusing to guess; run: nova-demo help\n"}},
		{name: "every problem at once: required, a check, --max and an argument", args: []string{"put", "--n", "-1", "--max", "-2", "stray"}, code: 2,
			emptyStdout: true, stderrLines: 5, stderr: []string{"PUT REFUSED: --store is required", "PUT REFUSED: --key is required",
				"PUT REFUSED: --n must be zero or more", "PUT REFUSED: --max must be zero or more", `PUT REFUSED: takes no positional arguments, got "stray"`}},
		{name: "two bad things are two lines", args: []string{"put", "--store", "s", "--key", "k", "--n", "-1", "stray"}, code: 2,
			emptyStdout: true, stderrLines: 2, stderr: []string{"PUT REFUSED: --n must be zero or more", `PUT REFUSED: takes no positional arguments, got "stray"`}},
		{name: "a bare tool with --json refuses in JSON", args: []string{"--json"}, code: 2, emptyStderr: true,
			stdout: []string{`{"result":{"verb":"","status":"refused","exit":2,"remedy":"nova-demo help","why":["unknown verb \"--json\"`}},
		{name: "an unknown verb with --json refuses in JSON", args: []string{"bogus", "--json"}, code: 2, emptyStderr: true,
			stdout: []string{`"status":"refused"`, `unknown verb \"bogus\"`}},
		{name: "help of a verb carries its detail above its flags and states its effect", args: []string{"help", "put"}, code: 0, emptyStderr: true,
			stdout: []string{"THE STORE is a directory the verb creates.\nflags:\n", "exit codes: 0 done", "effect: local write: writes files on this machine\n"}},
		{name: "a verb that states none says so", args: []string{"deny", "-h"}, code: 0, emptyStderr: true,
			stdout: []string{"effect: unstated\n"}},
		{name: "a refusal under --json is one object on stdout", args: []string{"put", "--json"}, code: 2, emptyStderr: true,
			stdout: []string{`{"result":{"verb":"put","status":"refused","exit":2,"remedy":"nova-demo help","why":["--store is required`}},
		{name: "an unknown flag under --json is still JSON, with the reason and the remedy", args: []string{"put", "--json", "--nope"}, code: 2, emptyStderr: true,
			stdout: []string{`{"result":{"verb":"put","status":"refused","exit":2,"remedy":"nova-demo put -h","why":["unknown flag --nope; the flags of put are --json`}},
		{name: "a --json that is a flag's value asks for no JSON", args: []string{"put", "--store", "--json", "--nope"}, code: 2, emptyStdout: true,
			stderr: []string{"PUT REFUSED: unknown flag --nope; the flags of put are"}},
		{name: "a --json after an unknown flag asks for JSON", args: []string{"put", "--bogus", "--json"}, code: 2, emptyStderr: true,
			stdout: []string{`{"result":{"verb":"put","status":"refused","exit":2,"remedy":"nova-demo put -h","why":["unknown flag --bogus;`}},
		{name: "a misspelled flag names the verb's flags and the nearest", args: []string{"put", "--stor", "s", "--key", "k"}, code: 2,
			emptyStdout: true, stderrLines: 1, absent: []string{"not defined", "nova-demo help"},
			stderr: []string{"PUT REFUSED: unknown flag --stor; the flags of put are --json, --key, --max, --n, --store; did you mean --store?; run: nova-demo put -h\n"}},
		{name: "an unknown flag with nothing near names the flags alone", args: []string{"who", "--zzzz"}, code: 2, emptyStdout: true,
			stderr: []string{"WHO REFUSED: unknown flag --zzzz; the flags of who are --actor, --json, --op, --redis, --width; run: nova-demo who -h\n"}},
		{name: "a bad value says what the flag wants and never repeats the value", args: []string{"put", "--n", "x"}, code: 2, emptyStdout: true,
			absent: []string{`"x"`}, stderr: []string{`PUT REFUSED: invalid value for --n: it wants a whole number (how many rows); run: nova-demo put -h`}},
		{name: "a flag with no value says what it wants", args: []string{"put", "--store"}, code: 2, emptyStdout: true,
			stderr: []string{"PUT REFUSED: --store needs a value: it wants a directory (required); run: nova-demo put -h"}},
		{name: "a misspelled verb names the nearest verb and the verbs", args: []string{"pt"}, code: 2, emptyStdout: true,
			stderr: []string{`DEMO REFUSED: unknown verb "pt"; did you mean put? the verbs are put, who,`, "; run: nova-demo help\n"}},
		{name: "a group's -h lists its verbs at exit 0", args: []string{"fn", "-h"}, code: 0, emptyStderr: true,
			stdout: []string{"usage: nova-demo fn <load|ls> [flags]\n  nova-demo fn load --name <n>\n  nova-demo fn ls\n", "exit codes: 0 done, 1 said no"}},
		{name: "help of a group is its -h", args: []string{"help", "fn"}, code: 0, emptyStderr: true,
			stdout: []string{"usage: nova-demo fn <load|ls> [flags]\n"}},
		{name: "a bare group is refused with its verbs", args: []string{"fn"}, code: 2, emptyStdout: true, stderrLines: 1,
			stderr: []string{"DEMO REFUSED: fn wants one of its verbs; the verbs are fn load, fn ls; run: nova-demo fn -h\n"}},
		{name: "a bare group under --json refuses in JSON", args: []string{"fn", "--json"}, code: 2, emptyStderr: true,
			stdout: []string{`"status":"refused"`, `"remedy":"nova-demo fn -h"`}},
		{name: "a misspelled verb of a group names the nearest", args: []string{"fn", "lod"}, code: 2, emptyStdout: true,
			stderr: []string{`DEMO REFUSED: unknown verb "fn lod" in fn; did you mean fn load? the verbs are fn load, fn ls; run: nova-demo fn -h`}},
		{name: "a verb of a group runs", args: []string{"fn", "load", "--name", "a"}, code: 0, emptyStderr: true,
			stdout: []string{"FN-LOAD OK loaded=a\n"}},
		{name: "a dry run plans and says so", args: []string{"fn", "load", "--name", "a", "--dry-run"}, code: 0, emptyStderr: true,
			stdout: []string{"FN-LOAD OK would_load=a dry_run=true\n"}},
		{name: "a verb's own exit table stands in its -h for the tool's", args: []string{"fn", "load", "-h"}, code: 0, emptyStderr: true,
			absent: []string{"1 said no"}, stdout: []string{"--dry-run", "exit codes: 0 loaded, 2 could not run.\neffect: local write"}},
		{name: "a dry run the verb never read is a failure, never an OK", args: []string{"careless", "--dry-run"}, code: 1, emptyStdout: true,
			stderr: []string{"CARELESS FAILED: --dry-run was given and the verb never read it"}},
		{name: "a verb that does not write takes no --dry-run", args: []string{"fn", "ls", "--dry-run"}, code: 2, emptyStdout: true,
			stderr: []string{"FN-LS REFUSED: unknown flag --dry-run;"}},
		{name: "ok goes to stdout", args: []string{"put", "--store", "s", "--key", "k"}, code: 0, emptyStderr: true,
			stdout: []string{"PUT OK store=s key=k\n"}},
		{name: "--max caps the items and says MORE", args: []string{"put", "--store", "s", "--key", "k", "--n", "3", "--max", "1"}, code: 0,
			emptyStderr: true, stdout: []string{"PUT OK store=s key=k\nPUT ROW i=0\nPUT MORE kind=row shown=1 total=3 --max <n>"}},
		{name: "--max 0 lists all", args: []string{"put", "--store", "s", "--key", "k", "--n", "25", "--max", "0"}, code: 0,
			emptyStderr: true, stdout: []string{"PUT ROW i=24\n"}},
		{name: "--max defaults to twenty", args: []string{"put", "--store", "s", "--key", "k", "--n", "21"}, code: 0,
			emptyStderr: true, stdout: []string{"PUT ROW i=19\nPUT MORE kind=row shown=20 total=21"}},
		{name: "a negative --max is refused", args: []string{"put", "--store", "s", "--key", "k", "--max", "-1"}, code: 2,
			emptyStdout: true, stderr: []string{"PUT REFUSED: --max must be zero or more"}},
		{name: "a positional is refused", args: []string{"put", "--store", "s", "--key", "k", "extra"}, code: 2,
			emptyStdout: true, stderr: []string{`PUT REFUSED: takes no positional arguments, got "extra"`}},
		{name: "the opt-in flags read their values and --redis its seat-first default", args: []string{"who", "--actor", "a", "--op", "o1", "--width", "2"},
			code: 0, emptyStderr: true, stdout: []string{"WHO OK actor=a op=o1 redis=seat.example:6379 width=2\n"}},
		{name: "a count under one and an empty address are both refused", args: []string{"who", "--redis", ""}, code: 2, emptyStdout: true, stderrLines: 2,
			stderr: []string{"WHO REFUSED: --width is required and is at least 1, got 0", "WHO REFUSED: --redis is required; it wants host:port"}},
		{name: "a no is exit 1 on stderr", args: []string{"deny"}, code: 1, emptyStdout: true, stderr: []string{"DENY FAILED: no\n"}},
		{name: "a problem the verb forgot to return is still the refusal", args: []string{"forget"}, code: 2, emptyStdout: true,
			stderr: []string{"FORGET REFUSED: recorded, not returned"}},
		{name: "a verb that prints its own keeps its exit and takes no --json", args: []string{"raw"}, code: 1, emptyStderr: true,
			stdout: []string{"a line of its own\n"}},
		{name: "--json on a verb that prints its own is an unknown flag", args: []string{"raw", "--json"}, code: 2, emptyStdout: true,
			stderr: []string{"RAW REFUSED: unknown flag --json; raw takes no flags; run: nova-demo raw -h"}},
		{name: "a declared word is printed and keeps the exit of the status under it", args: []string{"lib", "check"}, code: 1, emptyStdout: true,
			stderr: []string{"LIB-CHECK STALE: the library differs; run: nova-demo lib load\n"}},
		{name: "a declared word on an OK keeps exit 0", args: []string{"lib", "load"}, code: 0, emptyStderr: true,
			stdout: []string{"LIB-LOAD UNCHANGED\n"}},
		{name: "an undeclared word is a FAILED naming the bug", args: []string{"lib", "bogus"}, code: 1, emptyStdout: true,
			stderr: []string{"LIB-BOGUS FAILED: the verb answered ok GONE, a status word nova-demo does not declare"}},
		{name: "findings to stderr, the rest of a no to stdout", args: []string{"scan"}, code: 1,
			stdout: []string{"SCAN FAILED files=1\nSCAN DATED n=1\nSCAN NOTE a green clears the known shapes\n"},
			stderr: []string{"SCAN FINDING at=f:4\n"}, stderrLines: 1},
		{name: "a group of a verbflag-shaped -h", args: []string{"lib", "-h"}, code: 0, emptyStderr: true,
			stdout: []string{"usage: nova-demo lib <check|load|bogus> [flags]\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := NewRig(t, demo()).Run(tc.code, tc.args...)
			check := func(name, got string, want []string, empty bool) {
				assert.True(t, got == "" || !empty, "%s is not empty: %q", name, got)
				rest := got
				for _, s := range want {
					i := strings.Index(rest, s)
					if !assert.GreaterOrEqual(t, i, 0, "%s lacks %q (in order):\n%s", name, s, got) {
						break
					}
					rest = rest[i+len(s):]
				}
			}
			check("stdout", r.Stdout, tc.stdout, tc.emptyStdout)
			check("stderr", r.Stderr, tc.stderr, tc.emptyStderr)
			for _, s := range tc.absent {
				assert.NotContains(t, r.Stdout+r.Stderr, s)
			}
			if tc.stderrLines > 0 {
				n := strings.Count(r.Stderr, "\n")
				assert.Equal(t, tc.stderrLines, n, "stderr has %d lines, want %d:\n%s", n, tc.stderrLines, r.Stderr)
			}
		})
	}
}

// TestEveryBadFlagValueIsNamedAtOnce pins that every flag problem is collected at once.
func TestEveryBadFlagValueIsNamedAtOnce(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		tool        func() *Tool
		args        []string
		lines       int      // the refusal's lines on stderr
		stderr      []string // substrings, in order
		why         []string // under --json: the result's why, in order
		notRepeated []string // values given, absent from both streams
	}{
		{name: "two bad values and an unknown flag are three problems in one refusal",
			tool: demo, args: []string{"put", "--n", "x", "--max", "many", "--bogus"}, lines: 3,
			stderr: []string{"PUT REFUSED: invalid value for --n: it wants a whole number (how many rows)",
				"PUT REFUSED: invalid value for --max: it wants a whole number (items listed before one MORE line stands for the rest; 0 lists all)",
				"PUT REFUSED: unknown flag --bogus; the flags of put are", "run: nova-demo put -h"},
			notRepeated: []string{`"x"`, `"many"`}},
		{name: "under --json the problems are the entries of why",
			tool: demo, args: []string{"put", "--json", "--n", "x", "--max", "many", "--bogus"}, lines: 0,
			why: []string{`invalid value for --n: it wants a whole number (how many rows)`,
				`invalid value for --max: it wants a whole number (items listed before one MORE line stands for the rest; 0 lists all)`,
				`unknown flag --bogus; the flags of put are`},
			notRepeated: []string{`"many"`}},
		{name: "a bad duration and an unknown flag name the flag that wants it",
			tool: durationTool, args: []string{"wait", "--for", "soon", "--zz"}, lines: 2,
			stderr: []string{"WAIT REFUSED: invalid value for --for: it wants a duration such as 30s or 5m",
				"WAIT REFUSED: unknown flag --zz; the flags of wait are --for, --json", "run: nova-cover wait -h"}},
		{name: "a flag with no value and a bad value are two problems",
			tool: demo, args: []string{"put", "--n", "x", "--store"}, lines: 2,
			stderr: []string{"PUT REFUSED: invalid value for --n: it wants a whole number (how many rows)",
				"PUT REFUSED: --store needs a value: it wants a directory (required)", "run: nova-demo put -h"}},
		{name: "the same bad flag twice is named once", tool: demo,
			args: []string{"put", "--n", "x", "--n", "y", "--max", "many"}, lines: 2,
			stderr: []string{"PUT REFUSED: invalid value for --n: it wants a whole number (how many rows)",
				"PUT REFUSED: invalid value for --max: it wants a whole number"}},
		{name: "one bad value stays one line", tool: demo, args: []string{"put", "--n", "x"}, lines: 1,
			stderr: []string{"PUT REFUSED: invalid value for --n: it wants a whole number (how many rows); run: nova-demo put -h"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := NewRig(t, tc.tool()).Run(2, tc.args...)
			assert.NotContains(t, r.Stdout+r.Stderr, "parse error")
			assert.NotContains(t, r.Stdout+r.Stderr, "provided but not defined")
			for _, v := range tc.notRepeated {
				assert.NotContains(t, r.Stdout+r.Stderr, v, "the value given may be a secret")
			}
			if tc.lines > 0 {
				assert.Empty(t, r.Stdout)
				assert.Equal(t, tc.lines, strings.Count(r.Stderr, "\n"), "stderr:\n%s", r.Stderr)
				rest := r.Stderr
				for _, s := range tc.stderr {
					i := strings.Index(rest, s)
					if !assert.GreaterOrEqual(t, i, 0, "stderr lacks %q (in order):\n%s", s, r.Stderr) {
						break
					}
					rest = rest[i+len(s):]
				}
				return
			}
			assert.Empty(t, r.Stderr, "a refusal under --json leaves nothing on stderr")
			var j struct {
				Result struct {
					Verb, Status string
					Exit         int
					Remedy       string
					Why          []string
				} `json:"result"`
			}
			require.NoError(t, json.Unmarshal([]byte(r.Stdout), &j))
			assert.Equal(t, "refused", j.Result.Status)
			assert.Equal(t, 2, j.Result.Exit)
			assert.Equal(t, "put", j.Result.Verb)
			assert.NotEmpty(t, j.Result.Remedy)
			require.Len(t, j.Result.Why, len(tc.why))
			for i, w := range tc.why {
				assert.Contains(t, j.Result.Why[i], w)
			}
		})
	}
}

// TestARefusalUnderJSONIsOneObjectOnStdout pins one JSON object per refusal on stdout.
func TestARefusalUnderJSONIsOneObjectOnStdout(t *testing.T) {
	t.Parallel()
	clearTool := func() *Tool { // a tool whose exit 0 means CLEAR refuses `<verb> -h` (Tool.HelpRefused)
		return &Tool{Name: "nova-clear", What: "clears one gate", ExitTable: "0 clear, 1 not clear, 2 could not run.",
			HelpRefused: true,
			Verbs: []Verb{{Name: "check", Usage: "check [--gate <g>]", Effect: Inspection,
				Flags: func(f *Flags) { f.String("gate", "", "the gate to clear") },
				Run:   func(*Call) *Out { return Done() }}}}
	}
	for _, tc := range []struct {
		name string
		tool func() *Tool
		args []string
		verb string // the result's verb: "" while the verb is unknown
	}{
		{"an unknown verb names the verbs it does not know", demo, []string{"seal", "--json"}, ""},
		{"an unknown flag names the flags of its verb", demo, []string{"put", "--bogus", "--json"}, "put"},
		{"a bare group names its verbs", demo, []string{"fn", "--json"}, ""},
		{"a verb's own problems are the reasons", demo, []string{"put", "--json"}, "put"},
		{"every bad flag value is a reason", demo, []string{"put", "--n", "x", "--max", "many", "--json"}, "put"},
		{"a refused -h is a refusal like any other", clearTool, []string{"check", "-h", "--json"}, "check"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := NewRig(t, tc.tool()).Run(2, tc.args...)
			assert.Empty(t, r.Stderr, "a refusal under --json leaves nothing on stderr")
			require.Equal(t, 1, strings.Count(r.Stdout, "\n"), "stdout is the one object:\n%s", r.Stdout)
			var j struct {
				Result struct {
					Verb, Status string
					Exit         int
					Remedy       string
					Why          []string
				} `json:"result"`
			}
			require.NoError(t, json.Unmarshal([]byte(r.Stdout), &j))
			assert.Equal(t, "refused", j.Result.Status)
			assert.Equal(t, 2, j.Result.Exit)
			assert.Equal(t, tc.verb, j.Result.Verb)
			assert.NotEmpty(t, j.Result.Remedy, "a refusal names the command to run next")
			require.NotEmpty(t, j.Result.Why)
			for _, w := range j.Result.Why {
				assert.NotContains(t, w, "REFUSED:", "the envelope is not repeated inside why")
				assert.NotContains(t, w, "; run: ", "the remedy stands in its own field, not inside a reason")
			}
		})
	}
}

// TestBannerMeetsTheOnboardingStandard reads example lines and checks verb -h.
func TestBannerMeetsTheOnboardingStandard(t *testing.T) {
	t.Parallel()
	banner := demo().Banner()
	examples, err := onboarding.ExampleLines(banner, "nova-demo")
	require.True(t, err == nil && len(examples) == 1 && examples[0] == "nova-demo put --store ./s --key k", "example lines %q (%v) from:\n%s", examples, err, banner)
	for _, verb := range []string{"put", "who", "deny", "forget", "raw", "fn load", "fn ls", "careless", "version"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			r := NewRig(t, demo()).Capture(append(strings.Fields(verb), "-h")...)
			assert.True(t, r.Code == 0 && r.Stderr == "" && strings.HasPrefix(r.Stdout, "usage: nova-demo "+verb) && strings.Contains(r.Stdout, "\nexit codes: 0 "),
				"%s -h: exit %d stderr %q stdout:\n%s", verb, r.Code, r.Stderr, r.Stdout)
		})
	}
}

// TestNamesInARefusal pins the two helpers for unknown names: nearest match and cut list.
func TestNamesInARefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ got, want string }{
		{"--sesion", " did you mean --session?"},
		{"--sessoin", " did you mean --session?"},
		{"--zzzz", ""},
		{"opne", " did you mean open?"},
		{"seal", ""},
	} {
		assert.Equal(t, tc.want, didYouMean(tc.got, []string{"--session", "--store", "--key", "open", "put", "deny"}), tc.got)
	}
}

// selfTalk is a tool whose plain use is `<tool> <file>...`: its default verb.
func selfTalk() *Tool {
	return &Tool{Name: "nova-talk", What: "scans files", ExitTable: "0 none, 1 findings, 2 could not run.", Default: "scan",
		Verbs: []Verb{
			{Name: "scan", Usage: "[scan] <file>...", Effect: Inspection, Flags: func(f *Flags) {
				f.Bool("strict", false, "count every shape")
			}, Run: func(c *Call) *Out {
				return Done().Fact("files", c.flags.NArg()).Fact("strict", c.Bool("strict"))
			}},
			{Name: "shapes", Usage: "shapes", Effect: Inspection, Run: func(*Call) *Out { return Done() }},
		}}
}

// TestADefaultVerb pins dispatch of a default-verb tool: verb word, flag, or path are all routed to the default.
func TestADefaultVerb(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "notes.md")
	for _, tc := range []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"a verb word is the verb", []string{"shapes"}, 0, "SHAPES OK\n", ""},
		{"another verb refuses positional arguments", []string{"shapes", file}, 2, "",
			"SHAPES REFUSED: takes no positional arguments, got " + fmt.Sprintf("%q", file) + " (flags come before arguments); run: nova-talk help\n"},
		{"the verb named is the verb too", []string{"scan", file}, 0, "SCAN OK files=1 strict=false\n", ""},
		{"a path is the default verb's", []string{file, file}, 0, "SCAN OK files=2 strict=false\n", ""},
		{"a flag is the default verb's", []string{"--strict", file}, 0, "SCAN OK files=1 strict=true\n", ""},
		{"a bare word naming a file that is there is the default verb's", []string{"tool.go"}, 0, "SCAN OK files=1 strict=false\n", ""},
		{"a bare word that is no file is answered with the verbs", []string{"shapse"}, 2, "",
			`TALK REFUSED: "shapse" is no verb and no file; did you mean shapes? the verbs are scan, shapes, version, and a file is given by its path (./shapse); run: nova-talk help` + "\n"},
		{"bare names both doors", nil, 2, "", "TALK REFUSED: no verb and no file given; the verbs are scan, shapes, version; run: nova-talk help\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := NewRig(t, selfTalk()).Run(tc.code, tc.args...)
			assert.Equal(t, tc.stdout, r.Stdout)
			assert.Equal(t, tc.stderr, r.Stderr)
		})
	}
	assert.Empty(t, selfTalk().Problems())
	broken := selfTalk()
	broken.Default = "sacn"
	assert.Equal(t, []string{`nova-talk: the default verb "sacn" is none of its verbs`}, broken.Problems())
}

// TestProblems holds definition checks not enforced by construction: effects, how size, words.
func TestProblems(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", HowWidth+1)
	for _, tc := range []struct {
		name       string
		allEffects Effect // set on every verb before edit; "" leaves them unstated
		edit       func(*Tool)
		want       []string
	}{
		{"the demo's unstated effects", "", func(*Tool) {}, []string{
			`nova-demo who: the effect ""`, `nova-demo deny: the effect ""`, `nova-demo forget: the effect ""`, `nova-demo raw: the effect ""`}},
		{"a complete tool has none", Inspection + "; a clause is fine", func(*Tool) {}, nil},
		{"six how lines and a long one", Delivery, func(d *Tool) {
			d.How = "1\n2\n3\n4\n5\n" + long
		}, []string{"the how text is 6 lines, at most 5", "how line 6 is 101 characters, at most 100"}},
		{"an effect that is none of the three", LocalWrite, func(d *Tool) {
			d.Verbs[0].Effect = "writes a little"
		}, []string{`nova-demo put: the effect "writes a little"`}},
		{"a flag with no description", Inspection, func(d *Tool) {
			d.Verbs[1].Flags = func(f *Flags) { f.Int("width", 0, " ") }
		}, []string{"nova-demo who: --width has no description; say what it wants"}},
		{"status words: too many, one lower case, one every tool's", Inspection, func(d *Tool) {
			d.Words = []string{"A", "B", "C", "D", "E", "stale", "MORE"}
		}, []string{"7 status words of its own, at most 6", `the status word "stale" is not`, `the status word "MORE" is not`}},
		{"no what and no exit table", LocalWrite, func(d *Tool) {
			d.What, d.ExitTable = "", ""
		}, []string{"What and ExitTable are required"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := demo()
			if tc.allEffects != "" {
				for i := range d.Verbs {
					d.Verbs[i].Effect = tc.allEffects
				}
			}
			tc.edit(d)
			got := strings.Join(d.Problems(), "\n")
			if len(tc.want) == 0 {
				assert.Empty(t, got, "problems: %s", got)
			}
			for _, w := range tc.want {
				assert.Contains(t, got, w, "problems lack %q:\n%s", w, got)
			}
			n := len(d.Problems())
			assert.Equal(t, len(tc.want), n, "%d problems, want %d:\n%s", n, len(tc.want), got)
		})
	}
}

// TestTheFailureWordIsFAILED pins FAILED/failed across text and JSON, exit 1, with the three words.
func TestTheFailureWordIsFAILED(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		out          func() *Out
		wantTextWord string
		wantJSONWord string
		wantExit     int
	}{
		{"Done", func() *Out { return Done() }, "OK", "ok", 0},
		{"Fail", func() *Out { return Fail() }, "FAILED", "failed", 1},
		{"Fail with reason", func() *Out { return Fail("it failed") }, "FAILED", "failed", 1},
		{"Refuse", func() *Out { return Refuse() }, "REFUSED", "refused", 2},
		{"Refuse with reason", func() *Out { return Refuse("cannot run") }, "REFUSED", "refused", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := tc.out()
			o.Verb = "test"

			var text bytes.Buffer
			textExit := o.Render(&text, false)
			assert.Equal(t, tc.wantExit, textExit, "text exit code")
			assert.Equal(t, tc.wantExit, o.Exit, "Out.Exit")

			fields := strings.Fields(text.String())
			require.GreaterOrEqual(t, len(fields), 2, "text output has verb and status word: %q", text.String())
			assert.Equal(t, "TEST", fields[0])
			statusWord := strings.TrimSuffix(fields[1], ":")
			assert.Equal(t, tc.wantTextWord, statusWord)

			var js bytes.Buffer
			jsonExit := o.Render(&js, true)
			assert.Equal(t, tc.wantExit, jsonExit, "JSON exit code")

			var j struct {
				Result struct {
					Verb   string `json:"verb"`
					Status string `json:"status"`
					Exit   int    `json:"exit"`
				} `json:"result"`
			}
			err := json.Unmarshal(js.Bytes(), &j)
			require.NoError(t, err, "JSON is valid: %s", js.String())
			assert.Equal(t, "test", j.Result.Verb)
			assert.Equal(t, tc.wantJSONWord, j.Result.Status)
			assert.Equal(t, tc.wantExit, j.Result.Exit)
		})
	}
}

// TestItemTextRendersPlainAndAsAField pins the prose tail: text and JSON stay one value.
func TestItemTextRendersPlainAndAsAField(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		kind     string
		text     string
		kv       []any
		wantLine string
		wantJSON string
	}{
		{"spaces stay spaces, never one escaped field", "entry", "no version line: it printed nothing",
			[]any{"name", "nova-x"},
			"DEMO OK\nDEMO ENTRY name=nova-x: no version line: it printed nothing\n",
			`{"result":{"verb":"demo","status":"ok","exit":0},"facts":{},"items":[{"kind":"entry","fields":{"name":"nova-x"},"text":"no version line: it printed nothing"}]}`},
		{"a newline in the tail is escaped, the spaces are not", "entry", "first\nsecond",
			[]any{"name", "nova-x"},
			"DEMO OK\nDEMO ENTRY name=nova-x: first\\x0asecond\n",
			`{"result":{"verb":"demo","status":"ok","exit":0},"facts":{},"items":[{"kind":"entry","fields":{"name":"nova-x"},"text":"first\nsecond"}]}`},
		{"an empty tail is no tail and no field", "entry", "",
			[]any{"name", "nova-x"},
			"DEMO OK\nDEMO ENTRY name=nova-x\n",
			`{"result":{"verb":"demo","status":"ok","exit":0},"facts":{},"items":[{"kind":"entry","fields":{"name":"nova-x"}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := Done().ItemText(tc.kind, tc.text, tc.kv...)
			o.Verb, o.token = "demo", "DEMO"
			var text, js bytes.Buffer
			require.Equal(t, 0, o.Render(&text, false))
			assert.Equal(t, tc.wantLine, text.String())
			require.Equal(t, 0, o.Render(&js, true))
			assert.JSONEq(t, tc.wantJSON, js.String())
			var j struct {
				Items []struct {
					Text string `json:"text"`
				} `json:"items"`
			}
			require.NoError(t, json.Unmarshal(js.Bytes(), &j))
			require.Len(t, j.Items, 1)
			assert.Equal(t, tc.text, j.Items[0].Text, "the JSON text and the line tail are one value")
		})
	}
}

// TestHelpRefusedAnswersDashH pins HelpRefused: -h is a refusal, help <verb> answers.
func TestHelpRefusedAnswersDashH(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		helpRefused bool
		args        []string
		code        int
		stdout      string
		stderr      string
	}{
		{"-h is refused at exit 2", true, []string{"put", "-h"}, 2, "",
			"PUT REFUSED: -h is not an answer this tool gives, its exit 0 means CLEAR; run: nova-demo help\n"},
		{"--help is refused the same way", true, []string{"put", "--help"}, 2, "",
			"PUT REFUSED: -h is not an answer this tool gives, its exit 0 means CLEAR; run: nova-demo help\n"},
		{"help of the verb still answers at exit 0", true, []string{"help", "put"}, 0,
			"usage: nova-demo put [flags]", ""},
		{"with the switch off -h still answers at exit 0", false, []string{"put", "-h"}, 0,
			"usage: nova-demo put [flags]", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tool := &Tool{Name: "nova-demo", What: "a tool that exists to be tested",
				How:       "It keeps nothing.",
				ExitTable: "0 done, 1 said no, 2 could not run.", HelpRefused: tc.helpRefused,
				Verbs: []Verb{{Name: "put", Usage: "put --store <dir>", Effect: Inspection,
					Flags: func(f *Flags) { f.String("store", "", "a directory") },
					Run:   func(*Call) *Out { return Done() }}}}
			r := NewRig(t, tool).Run(tc.code, tc.args...)
			check := func(got, want string) {
				switch {
				case want == "":
					assert.Empty(t, got)
				case tc.code == 2:
					assert.Equal(t, want, got)
				default:
					assert.Contains(t, got, want)
				}
			}
			check(r.Stdout, tc.stdout)
			check(r.Stderr, tc.stderr)
		})
	}
}

// hiddenTool is a tool with one shown verb and one Verb.Hidden probe step.
func hiddenTool() *Tool {
	return &Tool{
		Name: "nova-hide", What: "a tool with a hidden verb", ExitTable: "0 done, 1 said no, 2 could not run.",
		Verbs: []Verb{
			{Name: "scan", Usage: "scan", Example: "scan", Effect: Inspection,
				Run: func(*Call) *Out { return Done() }},
			{Name: "probe-step", Usage: "probe-step <nonce>", Example: "probe-step <nonce>", Effect: Inspection, Hidden: true,
				Run: func(*Call) *Out { return Done().Fact("probed", true) }},
		},
	}
}

// TestAHiddenVerbRunsAndNoListShowsIt pins Verb.Hidden: runs, answers -h, stays out of lists.
func TestAHiddenVerbRunsAndNoListShowsIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		args        []string
		code        int
		stdout      []string // substrings, in order
		stderr      []string
		absent      []string // in neither stream
		emptyStderr bool
	}{
		{name: "the hidden verb runs", args: []string{"probe-step"}, code: 0, emptyStderr: true,
			stdout: []string{"PROBE-STEP OK probed=true\n"}},
		{name: "the banner's usage and example blocks do not show it", args: []string{"help"}, code: 0, emptyStderr: true,
			stdout: []string{"usage:\n  nova-hide scan\n", "\nexample:\n  nova-hide scan\n"}, absent: []string{"probe-step"}},
		{name: "a bare command's verb list does not name it", args: nil, code: 2,
			stderr: []string{"HIDE REFUSED: no verb given; the verbs are scan, version; run: nova-hide help\n"},
			absent: []string{"probe-step"}},
		{name: "an unknown verb is not answered with it", args: []string{"probe"}, code: 2,
			stderr: []string{`HIDE REFUSED: unknown verb "probe"; the verbs are scan, version; run: nova-hide help` + "\n"},
			absent: []string{"probe-step", "did you mean"}},
		{name: "help of it still prints its help", args: []string{"help", "probe-step"}, code: 0, emptyStderr: true,
			stdout: []string{"usage: nova-hide probe-step [flags]", "exit codes: 0 done, 1 said no, 2 could not run.", "effect: inspection"}},
		{name: "its -h still answers", args: []string{"probe-step", "-h"}, code: 0, emptyStderr: true,
			stdout: []string{"usage: nova-hide probe-step [flags]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := NewRig(t, hiddenTool()).Run(tc.code, tc.args...)
			check := func(name, got string, want []string) {
				rest := got
				for _, s := range want {
					i := strings.Index(rest, s)
					if !assert.GreaterOrEqual(t, i, 0, "%s lacks %q:\n%s", name, s, got) {
						break
					}
					rest = rest[i+len(s):]
				}
			}
			check("stdout", r.Stdout, tc.stdout)
			check("stderr", r.Stderr, tc.stderr)
			if tc.emptyStderr {
				assert.Empty(t, r.Stderr)
			}
			for _, s := range tc.absent {
				assert.NotContains(t, r.Stdout+r.Stderr, s)
			}
		})
	}
}

// TestAStageLineIsWhereEveryReaderMeetsTheTool pins Stage in the banner, -h, and bare refusal.
func TestAStageLineIsWhereEveryReaderMeetsTheTool(t *testing.T) {
	t.Parallel()
	const stage = "nova-demo is pre-alpha: not ready for production use."
	staged := demo()
	staged.Stage = stage
	for _, tc := range []struct {
		name   string
		tool   *Tool
		args   []string
		stream string // "out" or "err"
		want   string // a prefix of that stream; "" when the stage must be absent
	}{
		{"banner line 2", staged, []string{"help"}, "out", "nova-demo: a tool that exists to be tested\n" + stage + "\n\nhow it works:"},
		{"-h line 2", staged, []string{"put", "-h"}, "out", "usage: nova-demo put [flags]\n" + stage + "\n"},
		{"help <verb> line 2", staged, []string{"help", "version"}, "out", "usage: nova-demo version [flags]\n" + stage + "\n"},
		{"bare hint", staged, nil, "err", "DEMO REFUSED: no verb given;"},
		{"no stage, no line", demo(), []string{"help"}, "out", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errs bytes.Buffer
			tc.tool.Run(tc.args, strings.NewReader(""), &out, &errs)
			got := out.String()
			if tc.stream == "err" {
				got = errs.String()
				assert.True(t, strings.HasSuffix(got, "\n  NOTE "+stage+"\n"), "bare refusal stage hint missing:\n%s", got)
			}
			if tc.want == "" {
				assert.NotContains(t, got, "pre-alpha", got)
				return
			}
			assert.True(t, strings.HasPrefix(got, tc.want), "want the stream to open %q:\n%s", tc.want, got)
			assert.Equal(t, 1, strings.Count(got, stage), "the stage appears more than once:\n%s", got)
		})
	}
}

// topicsTool is a tool whose reference text lives in help topics instead of its banner.
func topicsTool() *Tool {
	return &Tool{
		Name:      "nova-demo",
		What:      "a tool that exists to be tested",
		How:       "It keeps nothing.",
		ExitTable: "0 done, 1 said no, 2 could not run.",
		Verbs: []Verb{
			{Name: "put", Usage: "put --key <k>", Effect: LocalWrite, Example: "put --key k",
				Flags: func(f *Flags) { f.Required("key", "a name") },
				Run:   func(c *Call) *Out { return Done().Fact("key", c.Str("key")) }},
		},
		Topics: []Topic{
			{Name: "keys", Text: "A key is one line.\nIt holds no spaces."},
			{Name: "exit", Text: "0 done, 1 said no."},
		},
	}
}

// TestAHelpTopicPrintsItsText pins Tool.Topics: help <topic>, banner lists topics, unknown refused.
func TestAHelpTopicPrintsItsText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		tool   func() *Tool
		args   []string
		code   int
		stdout string // the whole stream, exact
		stderr string // the whole stream, exact
		absent string // in neither stream
	}{
		{name: "a topic prints its text at exit 0", tool: topicsTool, args: []string{"help", "keys"}, code: 0,
			stdout: "A key is one line.\nIt holds no spaces.\n"},
		{name: "another topic prints its own text", tool: topicsTool, args: []string{"help", "exit"}, code: 0,
			stdout: "0 done, 1 said no.\n"},
		{name: "the banner lists the topic names on one line", tool: topicsTool, args: []string{"help"}, code: 0,
			stdout: "  nova-demo help [<verb>]\ntopics: keys, exit; run: nova-demo help <topic>\n\nEvery verb takes --json"},
		{name: "a tool with no topics prints no topics line", tool: demo, args: []string{"help"}, code: 0,
			absent: "topics:"},
		{name: "an unknown name is refused with the verbs and the topics", tool: topicsTool, args: []string{"help", "keey"}, code: 2,
			stderr: `DEMO REFUSED: unknown verb "keey"; the verbs are put, version, and the help topics are keys, exit; run: nova-demo help` + "\n"},
		{name: "a verb's help is still the verb's help", tool: topicsTool, args: []string{"help", "put"}, code: 0,
			stdout: "usage: nova-demo put [flags]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := NewRig(t, tc.tool()).Run(tc.code, tc.args...)
			if tc.stdout != "" {
				assert.Contains(t, r.Stdout, tc.stdout)
				assert.Empty(t, r.Stderr)
			}
			if tc.stderr != "" {
				assert.Equal(t, tc.stderr, r.Stderr)
				assert.Empty(t, r.Stdout)
			}
			if tc.absent != "" {
				assert.NotContains(t, r.Stdout+r.Stderr, tc.absent)
			}
		})
	}
}

// TestATopicIsNoneOfTheVerbs pins Problems' refusal of a topic named as one of the verbs.
func TestATopicIsNoneOfTheVerbs(t *testing.T) {
	t.Parallel()
	assert.Empty(t, topicsTool().Problems())
	for _, tc := range []struct {
		name  string
		topic string
		want  string
	}{
		{"a verb's name", "put", `nova-demo: the help topic "put" is one of its verbs; a topic is a name of its own`},
		{"the version verb every tool has", "version", `nova-demo: the help topic "version" is one of its verbs; a topic is a name of its own`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := topicsTool()
			d.Topics = append(d.Topics, Topic{Name: tc.topic, Text: "x"})
			assert.Equal(t, []string{tc.want}, d.Problems())
		})
	}
}

// walkTool is a verb that prints rows as it goes and returns the closing line.
func walkTool(run func(c *Call) *Out) *Tool {
	return &Tool{
		Name: "nova-walk", What: "walks", ExitTable: "0 done, 1 said no, 2 could not run.",
		Verbs: []Verb{{Name: "walk", Usage: "walk", Effect: Inspection, Run: run}},
	}
}

// Emit prints one item now as a line or JSON (skeleton contract 2.4, STANDARD §2).
func (c *Call) Emit(kind string, kv ...any) {
	o := &Out{token: c.token}
	o.Item(kind, kv...)
	it := o.Items[0]
	if c.asJSON {
		raw, err := marshal(struct {
			Item Item `json:"item"`
		}{it})
		if err != nil {
			f := Fail("the result is no JSON, so it is not printed: " + err.Error())
			f.token = c.token
			f.Render(c.Stderr, false)
			return
		}
		fmt.Fprintf(c.Stdout, "%s\n", raw)
		return
	}
	fmt.Fprintln(c.Stdout, oneline.Field(c.token)+" "+oneline.Field(strings.ToUpper(it.Kind))+it.Fields.text())
}

// TestEmitPrintsItemsThenTheClosingLine pins Call.Emit: items then closing line, text and JSON.
func TestEmitPrintsItemsThenTheClosingLine(t *testing.T) {
	t.Parallel()
	run := func(c *Call) *Out {
		if c.Ctx == nil || c.Ctx.Err() != nil {
			return Fail("no live context")
		}
		c.Emit("row", "i", 1)
		c.Emit("row", "i", 2)
		return Done()
	}
	rig := NewRig(t, walkTool(run))
	text := rig.Run(0, "walk")
	assert.Equal(t, "WALK ROW i=1\nWALK ROW i=2\nWALK OK\n", text.Stdout)
	assert.Empty(t, text.Stderr)
	js := rig.Run(0, "walk", "--json")
	assert.Equal(t, "{\"item\":{\"kind\":\"row\",\"fields\":{\"i\":1}}}\n"+
		"{\"item\":{\"kind\":\"row\",\"fields\":{\"i\":2}}}\n"+
		"{\"result\":{\"verb\":\"walk\",\"status\":\"ok\",\"exit\":0},\"facts\":{}}\n", js.Stdout)
	assert.Empty(t, js.Stderr)
}

// TestACancelledContextEndsTheVerb pins RunContext: cancelled context ends verb, text and JSON.
func TestACancelledContextEndsTheVerb(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		during bool
		json   bool
		stdout string
		stderr string
	}{
		{"text", false, false, "", "WALK FAILED: context canceled\n"},
		{"json", false, true, "{\"result\":{\"verb\":\"walk\",\"status\":\"failed\",\"exit\":1,\"why\":[\"context canceled\"]},\"facts\":{}}\n", ""},
		{"cancelled while the verb runs", true, false, "", "WALK FAILED: context canceled\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if !tc.during {
				cancel()
			}
			ran := false
			tool := walkTool(func(c *Call) *Out {
				ran = true
				if tc.during {
					cancel()
				}
				return Done()
			})
			args := []string{"walk"}
			if tc.json {
				args = append(args, "--json")
			}
			var out, errb bytes.Buffer
			code := tool.RunContext(ctx, args, strings.NewReader(""), &out, &errb)
			assert.Equal(t, 1, code, "stdout %q stderr %q", out.String(), errb.String())
			assert.Equal(t, tc.during, ran)
			assert.Equal(t, tc.stdout, out.String())
			assert.Equal(t, tc.stderr, errb.String())
		})
	}
}

// TestProblemAsCarriesTheReason pins Call.ProblemAs: reason in text and JSON.
func TestProblemAsCarriesTheReason(t *testing.T) {
	t.Parallel()
	const one = "WALK REFUSED reason=home_outside: the path is outside the home; run: nova-walk help\n"
	const two = one + "WALK REFUSED reason=bad_flag: --n wants a number; run: nova-walk help\n"
	const oneJSON = "{\"result\":{\"verb\":\"walk\",\"status\":\"refused\",\"exit\":2,\"remedy\":\"nova-walk help\",\"why\":[\"the path is outside the home\"],\"reasons\":[\"home_outside\"]},\"facts\":{}}\n"
	const twoJSON = "{\"result\":{\"verb\":\"walk\",\"status\":\"refused\",\"exit\":2,\"remedy\":\"nova-walk help\",\"why\":[\"the path is outside the home\",\"--n wants a number\"],\"reasons\":[\"home_outside\",\"bad_flag\"]},\"facts\":{}}\n"
	for _, tc := range []struct {
		name   string
		n      int
		json   bool
		stdout string
		stderr string
	}{
		{"one reason, text", 1, false, "", one},
		{"one reason, json", 1, true, oneJSON, ""},
		{"two reasons, text", 2, false, "", two},
		{"two reasons, json", 2, true, twoJSON, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tool := walkTool(func(c *Call) *Out {
				c.ProblemAs("home_outside", "the path is outside the home")
				if tc.n == 2 {
					c.ProblemAs("bad_flag", "--n wants a number")
				}
				return c.Refused()
			})
			args := []string{"walk"}
			if tc.json {
				args = append(args, "--json")
			}
			r := NewRig(t, tool).Run(2, args...)
			assert.Equal(t, tc.stdout, r.Stdout)
			assert.Equal(t, tc.stderr, r.Stderr)
		})
	}
}

// TestWritingVerbUnderDryRunCarriesFactInBothRenderings pins the dry_run fact in text and JSON.
func TestWritingVerbUnderDryRunCarriesFactInBothRenderings(t *testing.T) {
	t.Parallel()
	tool := &Tool{
		Name:      "nova-write",
		What:      "writes state",
		ExitTable: "0 done, 2 could not run.",
		Verbs: []Verb{
			{
				Name:   "save",
				Usage:  "save --file <f>",
				Effect: LocalWrite,
				DryRun: true,
				Flags:  func(f *Flags) { f.Required("file", "target file") },
				Run: func(c *Call) *Out {
					if c.DryRun() {
						return Done().Fact("file", c.Str("file"))
					}
					return Done().Fact("file", c.Str("file")).Fact("saved", true)
				},
			},
			{
				Name:   "custom",
				Usage:  "custom",
				Effect: LocalWrite,
				DryRun: true,
				Run: func(c *Call) *Out {
					if c.DryRun() {
						return Done().Fact("dry_run", true).Fact("explicit", true)
					}
					return Done()
				},
			},
		},
	}

	t.Run("skeleton adds dry_run in text line and JSON", func(t *testing.T) {
		t.Parallel()
		rig := NewRig(t, tool)
		rText := rig.Run(0, "save", "--file", "out.txt", "--dry-run")
		assert.Empty(t, rText.Stderr)
		assert.Equal(t, "SAVE OK file=out.txt dry_run=true\n", rText.Stdout)

		rJSON := rig.Run(0, "save", "--file", "out.txt", "--dry-run", "--json")
		assert.Empty(t, rJSON.Stderr)
		assert.JSONEq(t, `{"result":{"verb":"save","status":"ok","exit":0},"facts":{"file":"out.txt","dry_run":true}}`, rJSON.Stdout)
	})

	t.Run("verb that sets dry_run fact is not duplicated", func(t *testing.T) {
		t.Parallel()
		rig := NewRig(t, tool)
		rText := rig.Run(0, "custom", "--dry-run")
		assert.Empty(t, rText.Stderr)
		assert.Equal(t, "CUSTOM OK dry_run=true explicit=true\n", rText.Stdout)
		assert.Equal(t, 1, strings.Count(rText.Stdout, "dry_run=true"))

		rJSON := rig.Run(0, "custom", "--dry-run", "--json")
		assert.Empty(t, rJSON.Stderr)
		assert.JSONEq(t, `{"result":{"verb":"custom","status":"ok","exit":0},"facts":{"dry_run":true,"explicit":true}}`, rJSON.Stdout)
		assert.Equal(t, 1, strings.Count(rJSON.Stdout, `"dry_run"`))
	})

	t.Run("real run without dry-run carries no dry_run fact", func(t *testing.T) {
		t.Parallel()
		rig := NewRig(t, tool)
		rText := rig.Run(0, "save", "--file", "out.txt")
		assert.Equal(t, "SAVE OK file=out.txt saved=true\n", rText.Stdout)
		assert.NotContains(t, rText.Stdout, "dry_run")

		rJSON := rig.Run(0, "save", "--file", "out.txt", "--json")
		assert.NotContains(t, rJSON.Stdout, "dry_run")
	})
}

// TestToolExistsSeam pins Tool.Exists: a map seam for testing, defaulting to os.Stat.
func TestToolExistsSeam(t *testing.T) {
	t.Parallel()
	files := map[string]bool{
		"virtual.txt": true,
		"other.log":   true,
	}
	tl := &Tool{
		Name:      "nova-seam",
		What:      "tests the exists seam",
		ExitTable: "0 done, 2 could not run.",
		Default:   "read",
		Exists:    func(path string) bool { return files[path] },
		Verbs: []Verb{
			{
				Name:   "read",
				Usage:  "[read] <file>...",
				Effect: Inspection,
				Run: func(c *Call) *Out {
					return Done().Fact("files", c.flags.NArg())
				},
			},
			{
				Name:   "other",
				Usage:  "other",
				Effect: Inspection,
				Run:    func(*Call) *Out { return Done() },
			},
		},
	}

	t.Run("map answers existing file as default verb", func(t *testing.T) {
		t.Parallel()
		r := NewRig(t, tl).Run(0, "virtual.txt")
		assert.Equal(t, "READ OK files=1\n", r.Stdout)
	})

	t.Run("map answers absent file with refusal naming verbs and path remedy", func(t *testing.T) {
		t.Parallel()
		r := NewRig(t, tl).Run(2, "missing.txt")
		assert.Contains(t, r.Stderr, `"missing.txt" is no verb and no file; the verbs are read, other, version, and a file is given by its path (./missing.txt)`)
	})

	t.Run("nil Exists defaults to os.Stat", func(t *testing.T) {
		t.Parallel()
		tlDefault := &Tool{
			Name:      "nova-stat",
			What:      "tests default os.Stat",
			ExitTable: "0 done, 2 could not run.",
			Default:   "read",
			Verbs: []Verb{
				{
					Name:   "read",
					Usage:  "[read] <file>",
					Effect: Inspection,
					Run: func(c *Call) *Out {
						return Done().Fact("files", c.flags.NArg())
					},
				},
			},
		}
		// tool.go is in the package's working directory and stat sees it.
		rig := NewRig(t, tlDefault)
		r := rig.Run(0, "tool.go")
		assert.Equal(t, "READ OK files=1\n", r.Stdout)

		// non-existent file is refused.
		rMissing := rig.Run(2, "nonexistent_file_xyz_123.txt")
		assert.Contains(t, rMissing.Stderr, "is no verb and no file")
	})
}

// TestSetupTokenAndVerbHelpPlacement pins the skeleton's three additions:
// Tool.Setup is printed in the banner after the exit codes, Verb.Token is the
// leading word of a verb's lines, and helpArgs places --help after a multi-word
// verb name so the verb still answers help before it reads a trailing operand.
func TestSetupTokenAndVerbHelpPlacement(t *testing.T) {
	t.Parallel()
	tl := &Tool{
		Name:      "nova-demo",
		What:      "pins the skeleton's Setup, Token and help placement",
		ExitTable: "0 done, 1 said no, 2 could not run.",
		Setup:     "setup:\n  mkdir -p ./out",
		Verbs: []Verb{
			{Name: "fold", Token: "TOKENS", Usage: "fold", Effect: Inspection,
				Run: func(*Call) *Out { return Done() }},
			{Name: "lib check", Usage: "lib check <ref>", Effect: Inspection,
				Run: func(*Call) *Out { return Done() }},
		},
	}
	rig := NewRig(t, tl)

	// Tool.Setup: the block stands between the exit codes and the example block.
	help := rig.Run(0, "help")
	assert.Contains(t, help.Stdout, "exit codes: 0 done, 1 said no, 2 could not run.\n\nsetup:\n  mkdir -p ./out\n\nexample:\n",
		"the setup block prints after the exit codes and before the examples:\n%s", help.Stdout)

	// Verb.Token: the verb's line leads with its token, not its name.
	fold := rig.Run(0, "fold")
	assert.Equal(t, "TOKENS OK\n", fold.Stdout, "Verb.Token is the first word of the verb's lines")

	// helpArgs: --help lands after the multi-word verb name, before the operand.
	assert.Equal(t, []string{"lib", "check", "--help", "operand"}, tl.helpArgs([]string{"help", "lib", "check", "operand"}),
		"helpArgs places --help after the longest verb name, before the arguments")
	lib := rig.Run(0, "help", "lib", "check", "operand")
	assert.True(t, strings.HasPrefix(lib.Stdout, "usage: nova-demo lib check [flags]\n"),
		"a trailing operand does not stop help answering:\nstdout %q\nstderr %q", lib.Stdout, lib.Stderr)
}
