package tool

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
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
			if w := tc.out.Word; w != "" { // the text spells the status as the word; the JSON carries both
				assert.Contains(t, js.String(), `"word":"`+w+`"`)
				lines = strings.Replace(lines, "DEMO "+w+" ", "DEMO FAILED ", 1)
			}
			got, want := fromLines(t, "DEMO", lines), fromJSON(t, js.String())
			got.Verb, got.Exit = want.Verb, tc.out.Exit
			assert.True(t, want.Verb == "demo" && want.Exit == tc.out.Exit, "JSON result is %s exit %d, want demo exit %d", want.Verb, want.Exit, tc.out.Exit)
			assert.NotContains(t, js.String(), `\`+`u003c`, "the JSON is HTML-escaped")
			// The text escapes what JSON carries raw; compare the escaped form.
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
	var out, errs bytes.Buffer
	code := nan.Run([]string{"load", "--json"}, strings.NewReader(""), &out, &errs)
	assert.Equal(t, 1, code)
	assert.Empty(t, out.String())
	assert.True(t, strings.HasPrefix(errs.String(), "LOAD FAILED: the result is no JSON, so it is not printed: json: unsupported value: NaN\n"), errs.String())

	var w bytes.Buffer
	o := Done().Fact("load", math.Inf(1))
	o.Verb = "load"
	assert.Equal(t, 1, o.Render(&w, true))
	assert.Contains(t, w.String(), "LOAD FAILED: the result is no JSON")
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
				f.Actor()
				f.Op()
				f.Redis("seat.example:6379")
				f.Int("width", 0, "slots")
			}, Run: func(c *Call) *Out {
				width, redis := c.WantCount("width", "the slots"), c.Want("redis", "host:port")
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
			var out, errs bytes.Buffer
			code := demo().Run(tc.args, strings.NewReader(""), &out, &errs)
			assert.Equal(t, tc.code, code, "exit %d, want %d\nstdout: %s\nstderr: %s", code, tc.code, out.String(), errs.String())
			for _, w := range []struct {
				name  string
				got   string
				want  []string
				empty bool
			}{{"stdout", out.String(), tc.stdout, tc.emptyStdout}, {"stderr", errs.String(), tc.stderr, tc.emptyStderr}} {
				assert.True(t, w.got == "" || !w.empty, "%s is not empty: %q", w.name, w.got)
				rest := w.got
				for _, s := range w.want {
					i := strings.Index(rest, s)
					if !assert.GreaterOrEqual(t, i, 0, "%s lacks %q (in order):\n%s", w.name, s, w.got) {
						break
					}
					rest = rest[i+len(s):]
				}
			}
			for _, s := range tc.absent {
				assert.NotContains(t, out.String()+errs.String(), s)
			}
			if tc.stderrLines > 0 {
				n := strings.Count(errs.String(), "\n")
				assert.Equal(t, tc.stderrLines, n, "stderr has %d lines, want %d:\n%s", n, tc.stderrLines, errs.String())
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
			var out, errs bytes.Buffer
			code := demo().Run(append(strings.Fields(verb), "-h"), strings.NewReader(""), &out, &errs)
			assert.True(t, code == 0 && errs.Len() == 0 && strings.HasPrefix(out.String(), "usage: nova-demo "+verb) && strings.Contains(out.String(), "\nexit codes: 0 "),
				"%s -h: exit %d stderr %q stdout:\n%s", verb, code, errs.String(), out.String())
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
			var out, errs bytes.Buffer
			code := selfTalk().Run(tc.args, strings.NewReader(""), &out, &errs)
			assert.Equal(t, tc.code, code)
			assert.Equal(t, tc.stdout, out.String())
			assert.Equal(t, tc.stderr, errs.String())
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
		name string
		edit func(*Tool)
		want []string
	}{
		{"the demo's unstated effects", func(*Tool) {}, []string{
			`nova-demo who: the effect ""`, `nova-demo deny: the effect ""`, `nova-demo forget: the effect ""`, `nova-demo raw: the effect ""`}},
		{"a complete tool has none", func(d *Tool) {
			for i := range d.Verbs {
				d.Verbs[i].Effect = Inspection + "; a clause is fine"
			}
		}, nil},
		{"six how lines and a long one", func(d *Tool) {
			for i := range d.Verbs {
				d.Verbs[i].Effect = Delivery
			}
			d.How = "1\n2\n3\n4\n5\n" + long
		}, []string{"the how text is 6 lines, at most 5", "how line 6 is 101 characters, at most 100"}},
		{"an effect that is none of the three", func(d *Tool) {
			for i := range d.Verbs {
				d.Verbs[i].Effect = LocalWrite
			}
			d.Verbs[0].Effect = "writes a little"
		}, []string{`nova-demo put: the effect "writes a little"`}},
		{"a flag with no description", func(d *Tool) {
			for i := range d.Verbs {
				d.Verbs[i].Effect = Inspection
			}
			d.Verbs[1].Flags = func(f *Flags) { f.Int("width", 0, " ") }
		}, []string{"nova-demo who: --width has no description; say what it wants"}},
		{"status words: too many, one lower case, one every tool's", func(d *Tool) {
			for i := range d.Verbs {
				d.Verbs[i].Effect = Inspection
			}
			d.Words = []string{"A", "B", "C", "D", "E", "stale", "MORE"}
		}, []string{"7 status words of its own, at most 6", `the status word "stale" is not`, `the status word "MORE" is not`}},
		{"no what and no exit table", func(d *Tool) {
			for i := range d.Verbs {
				d.Verbs[i].Effect = LocalWrite
			}
			d.What, d.ExitTable = "", ""
		}, []string{"What and ExitTable are required"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := demo()
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

// TestRepeatableIsAGetter pins the one repeatable string flag: repeated or
// comma-separated on the command line, read back as []string through Call.Get.
func TestRepeatableIsAGetter(t *testing.T) {
	t.Parallel()
	var r Repeatable
	assert.Implements(t, (*flag.Getter)(nil), &r)
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"repeated", []string{"--tag", "a", "--tag", "b"}, []string{"a", "b"}},
		{"comma-separated", []string{"--tag", "a,b"}, []string{"a", "b"}},
		{"comma or repeat together", []string{"--tag", "a,b", "--tag", "c"}, []string{"a", "b", "c"}},
		{"spaces trimmed, empties skipped", []string{"--tag", "a, b,,c "}, []string{"a", "b", "c"}},
		{"absent is empty", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var tags Repeatable
			var got any
			tool := &Tool{Name: "nova-demo", What: "a tool that exists to be tested",
				ExitTable: "0 done, 1 said no, 2 could not run.",
				Verbs: []Verb{{Name: "ls", Usage: "ls", Effect: Inspection,
					Flags: func(f *Flags) { f.Var(&tags, "tag", "a tag to keep") },
					Run: func(c *Call) *Out {
						got = c.Get("tag")
						return Done().Fact("n", len(c.Get("tag").([]string)))
					}}}}
			var out, errs bytes.Buffer
			code := tool.Run(append([]string{"ls"}, tc.args...), strings.NewReader(""), &out, &errs)
			require.Equal(t, 0, code, "stdout %q stderr %q", out.String(), errs.String())
			vals, ok := got.([]string)
			require.True(t, ok, "Call.Get is %T, want []string", got)
			assert.Equal(t, tc.want, vals)
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
			var out, errs bytes.Buffer
			code := tool.Run(tc.args, strings.NewReader(""), &out, &errs)
			assert.Equal(t, tc.code, code, "stdout %q stderr %q", out.String(), errs.String())
			if tc.stdout == "" {
				assert.Empty(t, out.String())
			} else if tc.code == 2 {
				assert.Equal(t, tc.stdout, out.String())
			} else {
				assert.Contains(t, out.String(), tc.stdout)
			}
			if tc.stderr == "" {
				assert.Empty(t, errs.String())
			} else if tc.code == 2 {
				assert.Equal(t, tc.stderr, errs.String())
			} else {
				assert.Contains(t, errs.String(), tc.stderr)
			}
		})
	}
}

// TestPositionalArguments pins a verb that declares Positional: it reads its
// arguments through Call.Args, a verb that does not declare it refuses them,
// and the default verb reads them named or given as a bare path.
func TestPositionalArguments(t *testing.T) {
	t.Parallel()
	tool := &Tool{Name: "nova-demo", What: "a tool that exists to be tested",
		How:       "It keeps nothing.",
		ExitTable: "0 done, 1 said no, 2 could not run.", Default: "scan",
		Verbs: []Verb{
			{Name: "check", Usage: "check [surface]", Effect: Inspection,
				Flags: func(f *Flags) { f.Positional() },
				Run:   func(c *Call) *Out { return Done().Fact("args", strings.Join(c.Args(), ",")) }},
			{Name: "scan", Usage: "[scan] <file>...", Effect: Inspection,
				Flags: func(f *Flags) { f.Positional() },
				Run:   func(c *Call) *Out { return Done().Fact("args", strings.Join(c.Args(), ",")) }},
			{Name: "shapes", Usage: "shapes", Effect: Inspection,
				Run: func(*Call) *Out { return Done() }},
		}}
	for _, tc := range []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"a declaring verb reads its arguments", []string{"check", "a", "b"}, 0, "CHECK OK args=a,b\n", ""},
		{"a non-declaring verb refuses them", []string{"shapes", "a"}, 2, "",
			`SHAPES REFUSED: takes no positional arguments, got "a" (flags come before arguments); run: nova-demo help` + "\n"},
		{"the default verb reads them", []string{"scan", "a", "b"}, 0, "SCAN OK args=a,b\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errs bytes.Buffer
			code := tool.Run(tc.args, strings.NewReader(""), &out, &errs)
			assert.Equal(t, tc.code, code, "stdout %q stderr %q", out.String(), errs.String())
			assert.Equal(t, tc.stdout, out.String())
			assert.Equal(t, tc.stderr, errs.String())
		})
	}
}

// TestHelpRefusedReachesAGroup pins the group form of the refused help: a
// group's -h is answered at exit 0 by default, and refused at exit 2 naming
// help when the tool sets HelpRefused, since exit 0 would read as CLEAR.
func TestHelpRefusedReachesAGroup(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		helpRefused bool
		code        int
	}{
		{"a group's -h is refused with HelpRefused", true, 2},
		{"a group's -h answers at exit 0 by default", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tool := &Tool{Name: "nova-demo", What: "a tool that exists to be tested",
				How:       "It keeps nothing.",
				ExitTable: "0 done, 1 said no, 2 could not run.", HelpRefused: tc.helpRefused,
				Verbs: []Verb{
					{Name: "lift quarantine", Usage: "lift quarantine <surface>", Effect: LocalWrite,
						Run: func(*Call) *Out { return Done() }},
					{Name: "lift lockdown", Usage: "lift lockdown", Effect: Inspection,
						Run: func(*Call) *Out { return Done() }},
				}}
			var out, errs bytes.Buffer
			code := tool.Run([]string{"lift", "-h"}, strings.NewReader(""), &out, &errs)
			assert.Equal(t, tc.code, code, "stdout %q stderr %q", out.String(), errs.String())
			if tc.helpRefused {
				assert.Equal(t, "DEMO REFUSED: -h is not an answer this tool gives, its exit 0 means CLEAR; run: nova-demo help\n", errs.String())
				assert.Empty(t, out.String())
			} else {
				assert.Contains(t, out.String(), "usage: nova-demo lift <quarantine|lockdown>")
				assert.Empty(t, errs.String())
			}
		})
	}
}
