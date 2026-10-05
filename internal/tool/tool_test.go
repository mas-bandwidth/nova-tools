package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parsed is one rendering read back into the parts of Out, for comparing the
// lines with the JSON field for field.
type parsed struct {
	Verb, Status, Remedy string
	Exit                 int
	Why                  []string
	Facts                map[string]string
	Items                []string // kind k=v ... with values as strings
	More                 []string
	Notes                []string
	Payload              string
}

// fromLines reads the text rendering. Exit is not in the text: the process's
// exit code carries it, so the caller copies it across. "-" is the empty value.
func fromLines(t *testing.T, token, text string) parsed {
	t.Helper()
	p := parsed{Facts: map[string]string{}}
	for i, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		rest, ok := strings.CutPrefix(l, token+" ")
		if !ok { // the payload: alone, or last
			if i == 0 {
				p.Status = "ok"
			}
			p.Payload = l
			continue
		}
		word, rest, _ := strings.Cut(rest, " ")
		if w, ok := strings.CutSuffix(word, ":"); ok {
			word, rest = w, ": "+rest
		}
		switch word {
		case "OK", "FAILED", "REFUSED":
			p.Status = map[string]string{"OK": "ok", "FAILED": "failed", "REFUSED": "refused"}[word]
			if i := strings.LastIndex(rest, "; run: "); i >= 0 {
				p.Remedy, rest = rest[i+len("; run: "):], rest[:i]
			}
			fields, why, found := strings.Cut(rest, ": ")
			if found {
				p.Why = append(p.Why, why)
			}
			if len(p.Why) > 1 {
				continue // the fields repeat on every why line
			}
			for _, kv := range tokens(fields) {
				k, v, _ := strings.Cut(kv, "=")
				if v == "-" {
					v = ""
				}
				p.Facts[k] = v
			}
		case "MORE":
			p.More = append(p.More, rest)
		case "NOTE":
			p.Notes = append(p.Notes, rest)
		default:
			var kv []string
			for _, f := range strings.Fields(rest) {
				k, v, _ := strings.Cut(f, "=")
				if v == "-" {
					v = ""
				}
				kv = append(kv, k+"="+v)
			}
			p.Items = append(p.Items, strings.Join(append([]string{strings.ToLower(word)}, kv...), " "))
		}
	}
	return p
}

// tokens splits s at spaces outside a quoted value (oneline.Quote's).
func tokens(s string) []string {
	var out []string
	var cur strings.Builder
	quoted, escaped := false, false
	for _, r := range s {
		switch {
		case escaped:
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == ' ' && !quoted:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// fromJSON reads the JSON rendering into the same parts.
func fromJSON(t *testing.T, raw string) parsed {
	t.Helper()
	var j struct {
		Result struct {
			Verb, Status, Remedy string
			Exit                 int
			Why                  []string
		}
		Facts map[string]any
		Items []struct {
			Kind   string
			Fields json.RawMessage
		}
		More    []More
		Notes   []string
		Payload string
	}
	err := json.Unmarshal([]byte(raw), &j)
	require.NoError(t, err, "not one JSON object: %v: %s", err, raw)
	p := parsed{Verb: j.Result.Verb, Status: j.Result.Status, Remedy: j.Result.Remedy, Exit: j.Result.Exit,
		Why: j.Result.Why, Facts: map[string]string{}, Notes: j.Notes, Payload: j.Payload}
	for k, v := range j.Facts {
		p.Facts[k] = fmt.Sprint(v)
	}
	for _, it := range j.Items {
		// The fields are an object in insertion order; decode them in order.
		dec := json.NewDecoder(bytes.NewReader(it.Fields))
		kv := []string{strings.ToLower(it.Kind)}
		_, err := dec.Token()
		require.NoError(t, err)
		for dec.More() {
			k, _ := dec.Token()
			var v any
			require.NoError(t, dec.Decode(&v))
			kv = append(kv, fmt.Sprint(k)+"="+fmt.Sprint(v))
		}
		p.Items = append(p.Items, strings.Join(kv, " "))
	}
	for _, m := range j.More {
		p.More = append(p.More, fmt.Sprintf("kind=%s shown=%d total=%d %s", m.Kind, m.Shown, m.Total, m.Remedy))
	}
	return p
}

// TestRender pins the encoder: each value's lines, exactly, and its lines and
// its JSON read back to the same parts, field for field.
func TestRender(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		out   *Out
		lines string
	}{
		{"ok with facts", Done().Fact("session", "s1").Fact("bytes", 17).Fact("persisted", true).Fact("source", ""),
			"DEMO OK session=s1 bytes=17 persisted=true source=-\n"},
		{"a value with a space and an equals sign is one quoted field", Done().Fact("path", "a b=c"),
			"DEMO OK path=\"a b=c\"\n"},
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
			if w := tc.out.Word; w != "" { // the text spells the status as the word; the JSON carries both
				assert.Contains(t, js.String(), `"word":"`+w+`"`)
				lines = strings.Replace(lines, "DEMO "+w+" ", "DEMO FAILED ", 1)
			}
			got, want := fromLines(t, "DEMO", lines), fromJSON(t, js.String())
			got.Verb, got.Exit = want.Verb, tc.out.Exit
			assert.True(t, want.Verb == "demo" && want.Exit == tc.out.Exit, "JSON result is %s exit %d, want demo exit %d", want.Verb, want.Exit, tc.out.Exit)
			assert.NotContains(t, js.String(), `\`+`u003c`, "the JSON is HTML-escaped")
			// The text quotes a value JSON carries raw; compare the quoted form.
			for i, n := range want.Notes {
				want.Notes[i] = strings.ReplaceAll(n, "\n", `\x0a`)
			}
			for k, v := range want.Facts {
				if oneline.Field(v) != v {
					want.Facts[k] = strconv.Quote(v)
				}
			}
			g, w := fmt.Sprintf("%+v", got), fmt.Sprintf("%+v", want)
			assert.Equal(t, w, g, "the lines and the JSON disagree:\nlines %s\njson  %s", g, w)
		})
	}
}

// TestAValueWithAWhitespaceIsQuoted pins one way to print a value (skeleton
// contract 1.14, STANDARD §2): a value that is one safe token prints bare, and
// anything else -- a space, an "=", a control character -- prints as
// strconv.Quote gives it, so no line holds `\x20`.
func TestAValueWithAWhitespaceIsQuoted(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		out   *Out
		lines string
	}{
		{"a typed value with a space is quoted, never hex-escaped", Done().Fact("name", "land in batches"),
			"DEMO OK name=\"land in batches\"\n"},
		{"a safe token stays bare", Done().Fact("name", "batches"),
			"DEMO OK name=batches\n"},
		{"a value with an equals sign is quoted", Done().Fact("raw", "go version go1.27.1"),
			"DEMO OK raw=\"go version go1.27.1\"\n"},
		{"a control character is quoted", Done().Fact("text", "a\nb"),
			"DEMO OK text=\"a\\nb\"\n"},
		{"an item field with a space is quoted too", Done().Item("entry", "at", "x y"),
			"DEMO OK\nDEMO ENTRY at=\"x y\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.out.Verb, tc.out.token = "demo", "DEMO"
			var text bytes.Buffer
			tc.out.Render(&text, false)
			assert.Equal(t, tc.lines, text.String())
			assert.NotContains(t, text.String(), `\x20`)
		})
	}
}

// TestADuplicateKeyOnOneLineIsAFail pins the render-time refusal of a key a
// line prints twice (skeleton contract 1.14, STANDARD §2): the result is a
// FAILED naming the bug, never a line that names a key twice.
func TestADuplicateKeyOnOneLineIsAFail(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		key  string
		out  *Out
	}{
		{"a repeated fact key", "at", Done().Fact("at", "a").Fact("at", "b")},
		{"a repeated item field key", "build", Done().Item("entry", "build", "a", "build", "b")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.out.Verb, tc.out.token = "demo", "DEMO"
			var text bytes.Buffer
			assert.Equal(t, 1, tc.out.Render(&text, false))
			assert.Equal(t, "DEMO FAILED: the key "+tc.key+" is printed twice on one line\n", text.String())
		})
	}
}

// TestTextIsTheProseTail pins free text in a line: after the typed fields,
// quoted, its spaces kept, in an item as in the first line; JSON a string
// under its key, in the order the verb gave.
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
	r := testkit.Main(nan.Run).Run("load", "--json")
	assert.Equal(t, 1, r.Code)
	assert.Empty(t, r.Stdout)
	assert.True(t, strings.HasPrefix(r.Stderr, "LOAD FAILED: the result is no JSON, so it is not printed: json: unsupported value: NaN\n"), r.Stderr)

	var w bytes.Buffer
	o := Done().Fact("load", math.Inf(1))
	o.Verb = "load"
	assert.Equal(t, 1, o.Render(&w, true))
	assert.Contains(t, w.String(), "LOAD FAILED: the result is no JSON")
}

// bidi spells a code point as a string, and bidiEscape its six-character JSON
// escape text, without putting either the character or a \u sequence into this
// file: an invisible bidi control in a source file is exactly the thing a reader
// could not see, the spelling internal/oneline's own test uses.
func bidi(cp rune) string       { return string(cp) }
func bidiEscape(cp rune) string { return fmt.Sprintf("%su%04x", backslash, cp) }

const backslash = "\x5c" // one backslash

// TestJSONRenderingEscapesBidiControlsInStrings pins the JSON rendering's half of
// the one-line guarantee: the typed rendering escapes the bidi controls
// (oneline.Escape), and --json must not hand a reader the raw runes instead. A
// stored snippet holding U+202E, the right-to-left override, reorders the line
// a person or terminal reads while the JSON parses to the same string, and the
// corpus writer controls the snippet.
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

// TestRun pins the dispatcher: the banner's sections, help, version, the
// refusals and their streams, the standard flags, and the exit codes.
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
		{name: "a bad value says what the flag wants and what it got", args: []string{"put", "--n", "x"}, code: 2, emptyStdout: true,
			stderr: []string{`PUT REFUSED: --n wants a whole number (how many rows), got "x"; run: nova-demo put -h`}},
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
			r := testkit.Main(demo().Run).Run(tc.args...)
			assert.Equal(t, tc.code, r.Code, "exit %d, want %d\nstdout: %s\nstderr: %s", r.Code, tc.code, r.Stdout, r.Stderr)
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

// TestBannerMeetsTheOnboardingStandard reads the banner the way the onboarding
// class test does: the example block's lines are the tool's commands, with no
// placeholder, and every usage verb answers -h at exit 0.
func TestBannerMeetsTheOnboardingStandard(t *testing.T) {
	t.Parallel()
	banner := demo().Banner()
	examples, err := onboarding.ExampleLines(banner, "nova-demo")
	require.True(t, err == nil && len(examples) == 1 && examples[0] == "nova-demo put --store ./s --key k", "example lines %q (%v) from:\n%s", examples, err, banner)
	for _, verb := range []string{"put", "who", "deny", "forget", "raw", "fn load", "fn ls", "careless", "version"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			r := testkit.Main(demo().Run).Run(append(strings.Fields(verb), "-h")...)
			assert.True(t, r.Code == 0 && r.Stderr == "" && strings.HasPrefix(r.Stdout, "usage: nova-demo "+verb) && strings.Contains(r.Stdout, "\nexit codes: 0 "),
				"%s -h: exit %d stderr %q stdout:\n%s", verb, r.Code, r.Stderr, r.Stdout)
		})
	}
}

// TestNamesInARefusal pins the two helpers every unknown name is answered
// with: the nearest name within its edit bound, and a list cut to one line.
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

// TestCmdRendersTheWordsAShellReadsBack pins Cmd (skeleton contract 1.9: a
// remedy is one runnable command): every word goes through oneline.ShellWord,
// so a word holding a blank, a quote or a $ is one word a shell reads back and
// a word no shell gives a meaning prints as it is. The words are split the way
// a shell splits them (onboarding.SplitShell, nothing executed), never run.
func TestCmdRendersTheWordsAShellReadsBack(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		words []string
		want  string
	}{
		{"words no shell reads print as they are", []string{"nova-demo", "put", "-h"}, "nova-demo put -h"},
		{"a blank, a quote and a $ stay one word each", []string{"work trees", "it's", "e$f"}, `'work trees' 'it'"'"'s' 'e$f'`},
		{"the empty word is quoted, so it is still one word", []string{"nova-demo", ""}, "nova-demo ''"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Cmd(tc.words...)
			assert.Equal(t, tc.want, got)
			back, err := onboarding.SplitShell(got)
			require.NoError(t, err)
			assert.Equal(t, tc.words, back, "a shell reads the command back as the words meant")
		})
	}
}

// TestAUniquePrefixIsTheNearestName pins the nearest-name rule an unknown name
// is answered with (STANDARD §2: the names there are and the nearest): a name
// within two edits, or the one name an unknown word is a unique prefix of, is
// the nearest; a prefix of two names and a word nothing is near guess nothing.
func TestAUniquePrefixIsTheNearestName(t *testing.T) {
	t.Parallel()
	names := []string{"configure", "recall", "scan", "shapes"}
	for _, tc := range []struct{ got, want string }{
		{"conf", " did you mean configure?"}, // a unique prefix, five edits away
		{"recal", " did you mean recall?"},   // one edit
		{"s", ""},                            // a prefix of scan and shapes: not unique
		{"zzz", ""},                          // nothing near and no prefix
	} {
		t.Run(tc.got, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, didYouMean(tc.got, names), tc.got)
		})
	}
}

// TestAnUnknownVerbListsEveryName pins that an unknown-verb refusal lists every
// verb and names a unique prefix, never cutting the list to "and <n> more"
// (STANDARD §2: an unknown verb is answered with the names there are and the
// nearest).
func TestAnUnknownVerbListsEveryName(t *testing.T) {
	t.Parallel()
	verbs := make([]Verb, 0, verbflag.ListMax+2)
	for i := 1; i <= verbflag.ListMax+2; i++ {
		verbs = append(verbs, Verb{Name: fmt.Sprintf("v%02d", i), Usage: "v", Effect: Inspection,
			Run: func(*Call) *Out { return Done() }})
	}
	verbs = append(verbs, Verb{Name: "configure", Usage: "configure", Effect: Inspection,
		Run: func(*Call) *Out { return Done() }})
	tool := &Tool{Name: "nova-many", What: "has many verbs", ExitTable: "0 done, 1 said no, 2 could not run.", Verbs: verbs}
	r := testkit.Main(tool.Run).Run("conf")
	assert.Equal(t, 2, r.Code, "stderr %q", r.Stderr)
	assert.Contains(t, r.Stderr, "did you mean configure?")
	assert.Contains(t, r.Stderr, "v18", "the last numbered verb is named")
	assert.Contains(t, r.Stderr, "version", "the version verb every tool has is named")
	assert.NotContains(t, r.Stderr, "and", "the list is never cut to 'and <n> more'")
	far := testkit.Main(tool.Run).Run("zzzz")
	assert.NotContains(t, far.Stderr, "did you mean", "nothing near is not a guess")
}

// TestAnUnknownFlagNamesTheNearestAndEveryFlag pins an unknown-flag refusal
// (STANDARD §2): one flag within two edits, a two-edit short flag, or a unique
// prefix is named, a word nothing is near is not, and every flag is listed,
// never "and <n> more".
func TestAnUnknownFlagNamesTheNearestAndEveryFlag(t *testing.T) {
	t.Parallel()
	n := verbflag.ListMax + 2
	tool := &Tool{Name: "nova-flags", What: "has many flags", ExitTable: "0 done, 1 said no, 2 could not run.",
		Verbs: []Verb{{Name: "put", Usage: "put", Effect: Inspection, Flags: func(f *Flags) {
			f.String("configure", "", "a long flag")
			f.String("op", "", "a short flag")
			for i := 1; i <= n; i++ {
				f.String(fmt.Sprintf("f%02d", i), "", "a flag")
			}
		}, Run: func(*Call) *Out { return Done() }}}}
	run := func(flag string) string {
		t.Helper()
		r := testkit.Main(tool.Run).Run("put", flag)
		assert.Equal(t, 2, r.Code, "%s: stderr %q", flag, r.Stderr)
		assert.Contains(t, r.Stderr, "run: nova-flags put -h", flag)
		assert.Contains(t, r.Stderr, "--f18", "the flag past the old list cut is named")
		assert.NotContains(t, r.Stderr, "and", "the list is never cut to 'and <n> more'")
		return r.Stderr
	}
	assert.Contains(t, run("--conf"), "did you mean --configure?", "a unique prefix")
	assert.Contains(t, run("--ope"), "did you mean --op?", "one edit")
	assert.Contains(t, run("--xy"), "did you mean --op?", "two edits on a short flag")
	assert.NotContains(t, run("--zzzz"), "did you mean", "nothing near is not a guess")
}

// oneOf is a flag.Value of a verb's own: it words its own reason when it
// refuses a value, and the refusal carries that reason after the value.
type oneOf struct{ s string }

func (v *oneOf) String() string { return v.s }
func (v *oneOf) Set(s string) error {
	if s != "a" && s != "b" {
		return fmt.Errorf("only a or b")
	}
	v.s = s
	return nil
}

// TestEveryBadValueIsNamedAtOnce pins every bad value at once (skeleton
// contract 1.8; STANDARD §3 point 2: one run reports every problem it can
// find): the skeleton parses every flag value, words each failure as one
// problem in the order the words were typed, and refuses once listing all, so
// two bad values and an unknown flag are three problems in one refusal, and no
// message holds the flag package's own `parse error` or `provided but not
// defined`.
func TestEveryBadValueIsNamedAtOnce(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		want string // the whole of stderr: one refusal, one line per problem, in order
	}{
		{"two bad values and an unknown flag are three problems in one refusal",
			[]string{"put", "--n", "x", "--fix", "--max", "y"},
			`PUT REFUSED: --n wants a whole number (how many rows), got "x"; run: nova-demo put -h` + "\n" +
				`PUT REFUSED: unknown flag --fix; the flags of put are --json, --key, --max, --n, --store; did you mean --max?; run: nova-demo put -h` + "\n" +
				`PUT REFUSED: --max wants a whole number (items listed before one MORE line stands for the rest; 0 lists all), got "y"; run: nova-demo put -h` + "\n"},
		{"a bad value and a flag missing its value are two problems",
			[]string{"put", "--n", "x", "--store"},
			`PUT REFUSED: --n wants a whole number (how many rows), got "x"; run: nova-demo put -h` + "\n" +
				`PUT REFUSED: --store needs a value: it wants a directory (required); run: nova-demo put -h` + "\n"},
		{"a word after an unknown flag hides no bad value behind it",
			[]string{"put", "--fix", "s", "--n", "x"},
			`PUT REFUSED: unknown flag --fix; the flags of put are --json, --key, --max, --n, --store; did you mean --max?; run: nova-demo put -h` + "\n" +
				`PUT REFUSED: --n wants a whole number (how many rows), got "x"; run: nova-demo put -h` + "\n"},
		{"a bad boolean value wants true or false, and the next flag is read on",
			[]string{"fn", "load", "--dry-run=x", "--name"},
			`FN-LOAD REFUSED: --dry-run wants true or false (print what the verb would write and write nothing), got "x"; run: nova-demo fn load -h` + "\n" +
				`FN-LOAD REFUSED: --name needs a value: it wants the function's name (required); run: nova-demo fn load -h` + "\n"},
		{"a malformed flag word is named and the reading goes on",
			[]string{"put", "--=x", "--n", "y"},
			`PUT REFUSED: bad flag syntax: --=x; run: nova-demo put -h` + "\n" +
				`PUT REFUSED: --n wants a whole number (how many rows), got "y"; run: nova-demo put -h` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := testkit.Main(demo().Run).Run(tc.args...)
			assert.Equal(t, 2, r.Code, "stdout %q stderr %q", r.Stdout, r.Stderr)
			assert.Empty(t, r.Stdout)
			assert.Equal(t, tc.want, r.Stderr)
			assert.NotContains(t, r.Stderr, "parse error", "the flag package's own text is no wording")
			assert.NotContains(t, r.Stderr, "provided but not defined", "the flag package's own text is no wording")
		})
	}
	// A flag.Value of the verb's own words its own reason: the refusal carries
	// it after the value, so the reason the value was refused is not lost.
	own := &Tool{Name: "nova-own", What: "reads one value of its own", ExitTable: "0 done, 2 could not run.",
		Verbs: []Verb{{Name: "put", Usage: "put --one <a|b>", Effect: Inspection,
			Flags: func(f *Flags) { f.Var(&oneOf{}, "one", "the letter a or b") },
			Run:   func(*Call) *Out { return Done() }}}}
	r := testkit.Main(own.Run).Run("put", "--one", "c")
	assert.Equal(t, 2, r.Code, "stderr %q", r.Stderr)
	assert.Equal(t, `PUT REFUSED: --one wants the letter a or b, got "c" (only a or b); run: nova-own put -h`+"\n", r.Stderr)
}

// TestEveryRefusalUnderJSONIsOneObjectOnStdout pins refusals under --json on
// stdout (skeleton contract 1.4 and 1.6: "--json always stdout"): when --json
// was given, a refusal is one JSON object on stdout and nothing on stderr,
// including a refusal raised before the verb is known — an unknown verb or an
// unknown flag with --json anywhere in argv, and the -h refusal of a tool that
// refuses help (Tool.HelpRefused). The why array holds each reason alone,
// never the whole `VERB REFUSED: ...; run: ...` line.
func TestEveryRefusalUnderJSONIsOneObjectOnStdout(t *testing.T) {
	t.Parallel()
	helpRefused := func() *Tool {
		d := demo()
		d.HelpRefused = true
		return d
	}
	for _, tc := range []struct {
		name string
		tool func() *Tool
		args []string
		verb string
		why  []string // each reason alone, in order
	}{
		{"an unknown verb with --json after it", demo, []string{"bogus", "--json"}, "",
			[]string{`unknown verb "bogus"; the verbs are put, who, deny, forget, raw, fn load, fn ls, careless, lib check, lib load, lib bogus, scan, version`}},
		{"an unknown flag with --json anywhere in argv", demo, []string{"put", "--fix", "s", "--json"}, "put",
			[]string{"unknown flag --fix; the flags of put are --json, --key, --max, --n, --store; did you mean --max?"}},
		{"every bad value at once under --json: why holds each reason alone", demo,
			[]string{"put", "--json", "--n", "x", "--fix", "--max", "y"}, "put",
			[]string{`--n wants a whole number (how many rows), got "x"`,
				"unknown flag --fix; the flags of put are --json, --key, --max, --n, --store; did you mean --max?",
				`--max wants a whole number (items listed before one MORE line stands for the rest; 0 lists all), got "y"`}},
		{"the -h refusal of a tool that refuses help", helpRefused, []string{"put", "--json", "-h"}, "put",
			[]string{"-h is not an answer this tool gives, its exit 0 means CLEAR"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := testkit.Main(tc.tool().Run).Run(tc.args...)
			assert.Equal(t, 2, r.Code, "stdout %q stderr %q", r.Stdout, r.Stderr)
			assert.Empty(t, r.Stderr, "a refusal under --json prints nothing on stderr")
			var j struct {
				Result struct {
					Verb   string   `json:"verb"`
					Status string   `json:"status"`
					Exit   int      `json:"exit"`
					Remedy string   `json:"remedy"`
					Why    []string `json:"why"`
				} `json:"result"`
			}
			require.NoError(t, json.Unmarshal([]byte(r.Stdout), &j), "stdout is one JSON object: %q", r.Stdout)
			assert.Equal(t, "refused", j.Result.Status)
			assert.Equal(t, 2, j.Result.Exit)
			assert.Equal(t, tc.verb, j.Result.Verb)
			assert.NotEmpty(t, j.Result.Remedy, "the refusal carries its remedy")
			assert.Equal(t, tc.why, j.Result.Why)
			for _, w := range j.Result.Why {
				assert.NotContains(t, w, "REFUSED", "why holds the reason alone, never the envelope")
				assert.NotContains(t, w, "; run:", "why holds the reason alone, never the remedy")
			}
		})
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

// TestADefaultVerb pins the dispatch of a tool with a default verb: a verb
// word is the verb, a flag, a path or a file that is there is the default
// verb's, and any other word is answered as no verb and no file, with the verbs.
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
			r := testkit.Main(selfTalk().Run).Run(tc.args...)
			assert.Equal(t, tc.code, r.Code)
			assert.Equal(t, tc.stdout, r.Stdout)
			assert.Equal(t, tc.stderr, r.Stderr)
		})
	}
	assert.Empty(t, selfTalk().Problems())
	broken := selfTalk()
	broken.Default = "sacn"
	assert.Equal(t, []string{`nova-talk: the default verb "sacn" is none of its verbs`}, broken.Problems())
}

// TestProblems holds a definition to the standard the banner cannot enforce by
// construction: every verb's effect, and the how text's size.
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

// TestTheFailureWordIsFAILED pins the failure word across text and JSON:
// Fail() renders as FAILED in text and failed in JSON with exit 1, and the three
// status words and their exits do not drift (STANDARD §2).
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

// TestItemTextRendersPlainAndAsAField pins the prose tail: the text renders
// plain after the row's typed fields in the line, and as the `text` field
// beside them in the JSON, so the line and the object stay one value.
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

// TestHelpRefusedAnswersDashH pins the refused help: with HelpRefused a
// verb's -h is a refusal at exit 2 naming help, never an answer at exit 0,
// while `help <verb>` still answers and the switch off changes nothing.
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
			r := testkit.Main(tool.Run).Run(tc.args...)
			assert.Equal(t, tc.code, r.Code, "stdout %q stderr %q", r.Stdout, r.Stderr)
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

// TestAStageLineIsWhereEveryReaderMeetsTheTool: a tool's Stage is the
// banner's line 2, the second line of every verb's -h, and an indented NOTE
// line under a bare command's one-line refusal (STANDARD section 3 point 1); a tool without one prints none.
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
				assert.True(t, strings.HasSuffix(got, "\n  NOTE "+stage+"\n"), "the bare refusal has no indented NOTE stage hint:\n%s", got)
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
