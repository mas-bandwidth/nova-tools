package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
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
		case "OK", "FAIL", "REFUSED":
			p.Status = map[string]string{"OK": "ok", "FAIL": "failed", "REFUSED": "refused"}[word]
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
			for _, kv := range strings.Fields(fields) {
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
			o.capItems(2)
			return o.Note("a note\nwith a newline")
		}(), "DEMO OK entries=3\nDEMO ENTRY id=e1 bytes=5\nDEMO ENTRY id=e2 bytes=6\n" +
			"DEMO MORE kind=entry shown=2 total=3 " + MaxRemedy + "\nDEMO NOTE a note\\x0awith a newline\n"},
		{"refused names every problem with the remedy", func() *Out {
			o := Refuse("--a is required", "--b is required")
			o.Remedy = "nova-demo help"
			return o
		}(), "DEMO REFUSED: --a is required; run: nova-demo help\nDEMO REFUSED: --b is required; run: nova-demo help\n"},
		{"failed carries its facts and its reason", Fail("the words differ").Fact("entry", "e1"),
			"DEMO FAIL entry=e1: the words differ\n"},
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
			got, want := fromLines(t, "DEMO", text.String()), fromJSON(t, js.String())
			got.Verb, got.Exit = want.Verb, tc.out.Exit
			assert.True(t, want.Verb == "demo" && want.Exit == tc.out.Exit, "JSON result is %s exit %d, want demo exit %d", want.Verb, want.Exit, tc.out.Exit)
			// The text escapes what JSON carries raw; compare the escaped form.
			for i, n := range want.Notes {
				want.Notes[i] = strings.ReplaceAll(n, "\n", `\x0a`)
			}
			for k, v := range want.Facts {
				want.Facts[k] = strings.NewReplacer(" ", `\x20`, "=", `\x3d`).Replace(v)
			}
			g, w := fmt.Sprintf("%+v", got), fmt.Sprintf("%+v", want)
			assert.Equal(t, w, g, "the lines and the JSON disagree:\nlines %s\njson  %s", g, w)
		})
	}
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
		},
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
			stderr: []string{"DEMO REFUSED: no verb given; the verbs are put, who, deny, forget, raw, fn load, fn ls, careless, version; run: nova-demo help"}},
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
			stdout: []string{`{"result":{"verb":"put","status":"refused","exit":2,"remedy":"nova-demo put -h","why":["unknown flag --nope; put takes --json`}},
		{name: "a misspelled flag names the nearest flag and the verb's flags", args: []string{"put", "--stor", "s", "--key", "k"}, code: 2,
			emptyStdout: true, stderrLines: 1, absent: []string{"not defined", "nova-demo help"},
			stderr: []string{"PUT REFUSED: unknown flag --stor; did you mean --store? put takes --json, --key, --max, --n, --store; run: nova-demo put -h\n"}},
		{name: "an unknown flag with nothing near names the flags alone", args: []string{"who", "--zzzz"}, code: 2, emptyStdout: true,
			stderr: []string{"WHO REFUSED: unknown flag --zzzz; who takes --actor, --json, --op, --redis, --width; run: nova-demo who -h\n"}},
		{name: "a bad value says what the flag wants", args: []string{"put", "--n", "x"}, code: 2, emptyStdout: true,
			stderr: []string{`PUT REFUSED: invalid value "x" for flag --n: parse error; --n wants how many rows; run: nova-demo put -h`}},
		{name: "a flag with no value says what it wants", args: []string{"put", "--store"}, code: 2, emptyStdout: true,
			stderr: []string{"PUT REFUSED: flag needs an argument: --store; --store wants a directory (required); run: nova-demo put -h"}},
		{name: "a misspelled verb names the nearest verb and the verbs", args: []string{"pt"}, code: 2, emptyStdout: true,
			stderr: []string{`DEMO REFUSED: unknown verb "pt"; did you mean put? the verbs are put, who,`, "; run: nova-demo help\n"}},
		{name: "a group's -h lists its verbs at exit 0", args: []string{"fn", "-h"}, code: 0, emptyStderr: true,
			stdout: []string{"usage: nova-demo fn <verb> [flags]\n  nova-demo fn load --name <n>\n  nova-demo fn ls\n", "exit codes: 0 done, 1 said no"}},
		{name: "help of a group is its -h", args: []string{"help", "fn"}, code: 0, emptyStderr: true,
			stdout: []string{"usage: nova-demo fn <verb> [flags]\n"}},
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
			stderr: []string{"CARELESS FAIL: --dry-run was given and the verb never read it"}},
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
		{name: "a no is exit 1 on stderr", args: []string{"deny"}, code: 1, emptyStdout: true, stderr: []string{"DENY FAIL: no\n"}},
		{name: "a problem the verb forgot to return is still the refusal", args: []string{"forget"}, code: 2, emptyStdout: true,
			stderr: []string{"FORGET REFUSED: recorded, not returned"}},
		{name: "a verb that prints its own keeps its exit and takes no --json", args: []string{"raw"}, code: 1, emptyStderr: true,
			stdout: []string{"a line of its own\n"}},
		{name: "--json on a verb that prints its own is an unknown flag", args: []string{"raw", "--json"}, code: 2, emptyStdout: true,
			stderr: []string{"RAW REFUSED: unknown flag --json; raw takes no flags; run: nova-demo raw -h"}},
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
				assert.False(t, w.empty && w.got != "", "%s is not empty: %q", w.name, w.got)
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
	var many []string
	for i := range listMax + 4 {
		many = append(many, fmt.Sprintf("v%d", i))
	}
	assert.True(t, strings.HasSuffix(listOf(many), ", v15 and 4 more"), listOf(many))
	assert.Equal(t, "a, b", listOf([]string{"a", "b"}))
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
