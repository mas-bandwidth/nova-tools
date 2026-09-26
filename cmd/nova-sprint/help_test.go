package main

import (
	"context"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// THE CLASS RULE: -h ON EVERY NOUN AND EVERY PATH PRINTS THAT PATH'S OWN
// USAGE, FROM THE ONE TABLE; EVERY REFUSAL ENDS IN ITS PATH'S OWN USAGE OR
// THE CORRECTED LINE, NEVER "run: nova-sprint help" (nova-tools#4352 A, the
// cold read of #4399: 21 of 64 lines ended in the help tail; `stream open
// -h` printed land stream's usage; noun -h printed a wall).
//
// The walk runs in a child (this binary, TestGrammarEveryPathHelpChild)
// whose environment names a miniredis in NOVA_SPRINT_REDIS and no seat, so
// the parent can hold it to zero dials: -h and a bad flag never reach a
// store. It stays parallel and under 2 s.
func TestGrammarEveryPathHelp(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	home := t.TempDir()
	cmd := exec.Command(self, "-test.run=^TestGrammarEveryPathHelpChild$", "-test.count=1")
	cmd.Env = []string{grammarChildEnv + "=1", "NOVA_SPRINT_REDIS=" + mr.Addr(), "HOME=" + home, "XDG_CONFIG_HOME=" + home,
		"PATH=" + os.Getenv("PATH"), "USER=rowan", seatEnv + "=rowan"}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "NOVA_TEST_NO_HOST=") || strings.HasPrefix(kv, "TMPDIR=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "PASS") {
		t.Fatalf("the walk failed (%v):\n%s", err, out)
	}
	if n := mr.TotalConnectionCount(); n != 0 {
		t.Fatalf("-h or a bad flag dialled the store %d time(s)", n)
	}
}

// grammarChildEnv runs TestGrammarEveryPathHelpChild; the parent sets it.
const grammarChildEnv = "NOVA_SPRINT_GRAMMAR_CHILD"

// positionalPaths take positionals, so an example is checked flag by flag
// against the path's set rather than run with -h after it (a positional
// ends flag parsing, and the verb would run).
var positionalPaths = map[string]bool{"redis": true, "redis-cli": true, "refresh": true, "fleet build set": true,
	"result check": true, "result disposition": true, "card push": true}

var flagTokenRE = regexp.MustCompile(`^--?([A-Za-z0-9][A-Za-z0-9-]*)(=.*)?$`)

func TestGrammarEveryPathHelpChild(t *testing.T) {
	t.Parallel()
	if os.Getenv(grammarChildEnv) == "" {
		t.Skip("the child of TestGrammarEveryPathHelp")
	}
	var paths []string
	for p := range verbflag.Usages {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	nouns := map[string]bool{"table": true, "refresh": true}
	for n := range verbs {
		nouns[n] = true
	}
	// every noun is in the table, and every path's noun is a verb
	for n := range nouns {
		if !verbflag.Known(n) {
			t.Errorf("noun %s has no entry in verbflag.Usages", n)
		}
	}
	for _, p := range paths {
		if !nouns[strings.Fields(p)[0]] {
			t.Errorf("path %s: its noun is no verb", p)
		}
	}
	// noun -h: the noun's own usage and every subverb's forms, stdout, exit 2
	for n := range nouns {
		code, out, errOut := runSprint(n, "-h")
		if code != 2 || errOut != "" || !strings.HasPrefix(out, "usage: nova-sprint "+n+" ") && !strings.HasPrefix(out, "usage: nova-sprint "+n+"\n") {
			t.Errorf("%s -h: exit %d stderr %q stdout %q", n, code, errOut, head(out))
			continue
		}
		for _, s := range verbflag.Subs(n) {
			if !strings.Contains(out, "  nova-sprint "+s) {
				t.Errorf("%s -h lacks its subverb %s:\n%s", n, s, out)
			}
		}
		if strings.Contains(out, "nova-sprint help") {
			t.Errorf("%s -h names nova-sprint help:\n%s", n, out)
		}
	}
	for _, p := range paths {
		u := verbflag.Usages[p]
		words := strings.Fields(p)
		run, _ := helpRunner(words[0])
		// path -h: its forms, its flags, its examples; its own set
		code, out, errOut := runSprint(append(append([]string{}, words...), "-h")...)
		if code != 2 || errOut != "" || !strings.HasPrefix(out, "usage: nova-sprint "+p) || !strings.Contains(out, "example:\n  nova-sprint "+p) {
			t.Errorf("%s -h: exit %d stderr %q stdout %q", p, code, errOut, head(out))
		}
		fs := helpFlags(run, p)
		want := p
		if u.SetOf != "" {
			want = u.SetOf
		}
		switch {
		case u.NoFlags && fs != nil:
			t.Errorf("%s is NoFlags and reached flag set %s", p, fs.Name())
		case !u.NoFlags && fs == nil:
			t.Errorf("%s -h reached no flag set: a verb that parses by hand, or a missing path", p)
		case fs != nil && fs.Name() != want:
			t.Errorf("%s -h reached flag set %q: it prints another verb's usage", p, fs.Name())
		}
		if fs != nil {
			fs.VisitAll(func(f *flag.Flag) {
				if !strings.Contains(out, "\n  --"+f.Name+" ") && !strings.Contains(out, "\n  --"+f.Name+"\n") {
					t.Errorf("%s -h does not name --%s", p, f.Name)
				}
			})
		}
		// every example is the path's, and every flag it spells is defined
		for _, e := range u.Examples {
			argv := splitLine(e)
			if strings.Join(argv[:min(len(words), len(argv))], " ") != p {
				t.Errorf("%s: example %q is another path's", p, e)
				continue
			}
			if positionalPaths[p] || u.NoFlags {
				for _, a := range argv[len(words):] {
					if m := flagTokenRE.FindStringSubmatch(a); m != nil && (fs == nil || fs.Lookup(m[1]) == nil) {
						t.Errorf("%s: example %q spells --%s, which the path does not define", p, e, m[1])
					}
				}
				continue
			}
			got := helpFlagsOf(run, append(argv[1:], "-h"))
			if got == nil || got.Name() != want {
				t.Errorf("%s: example %q with -h did not reach the path's flag set (a flag it does not define?)", p, e)
			}
		}
		// a flag nobody spells: the grammar's refusal and the path's own usage
		if fs != nil && !positionalPaths[p] {
			_, out, errOut := runSprint(append(append([]string{}, words...), "--zz-not-a-flag")...)
			all := out + errOut
			if !strings.Contains(all, "--zz-not-a-flag is not a flag of nova-sprint "+want) || !strings.Contains(all, "usage: nova-sprint "+want) ||
				strings.Contains(all, "nova-sprint help") || strings.Contains(all, "provided but not defined") {
				t.Errorf("%s --zz-not-a-flag: %s", p, strings.TrimSpace(all))
			}
		}
	}
}

// helpFlagsOf runs the verb with argv and returns the flag set that raised
// verbflag.Help, or nil.
func helpFlagsOf(run runFunc, argv []string) (fs *flag.FlagSet) {
	defer func() {
		if r := recover(); r != nil {
			h, ok := r.(verbflag.Help)
			if !ok {
				panic(r)
			}
			fs = h.FS
		}
	}()
	run(context.Background(), argv, io.Discard, io.Discard)
	return nil
}

// splitLine is a line split the way sh would split its single quotes.
func splitLine(s string) []string {
	var out []string
	var cur strings.Builder
	in, have := false, false
	for _, r := range s {
		switch {
		case r == '\'':
			in, have = !in, true
		case r == ' ' && !in:
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	return out
}

func head(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// TestGrammarNoHelpTailInTheSource: no refusal printer in nova-sprint's
// source spells the help tail; each ends in its path's usage
// (verbflag.Refusal) or a corrected line.
func TestGrammarNoHelpTailInTheSource(t *testing.T) {
	t.Parallel()
	tail := regexp.MustCompile(`run:? nova-sprint help`)
	for _, dir := range []string{".", "../../internal/nsprint"} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if tail.MatchString(line) {
					t.Errorf("%s:%d spells the help tail: %s", path, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestGrammarOneRedisResolver (#4399 item 10): no verb reads a Redis address
// variable itself; each goes through the one resolver (seat.go's
// redisDefault and redisOr: the seat's, else NOVA_SPRINT_REDIS,
// NOVA_REDIS_ADDR, NOVA_REDIS). The jev ledger's env is the one other
// reader: a test passes its own env there, and the verb's registration sets
// jev.DefaultAddr to the resolver.
func TestGrammarOneRedisResolver(t *testing.T) {
	t.Parallel()
	read := regexp.MustCompile(`[gG]etenv\("NOVA_(SPRINT_)?REDIS(_ADDR)?"\)`)
	allowed := map[string]bool{filepath.Join("..", "..", "internal", "nsprint", "jev", "main.go"): true}
	for _, dir := range []string{".", filepath.Join("..", "..", "internal", "nsprint")} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || allowed[path] {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if read.MatchString(line) {
					t.Errorf("%s:%d reads a Redis address itself, not through the one resolver: %s", path, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
