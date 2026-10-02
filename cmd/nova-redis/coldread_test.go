package main

// coldread_test.go holds what an AI meeting nova-redis cold needs from it: every
// problem with a line named in one run, in the one refusal grammar with the
// verb's own help as the next command; help for a verb group, never a refusal;
// every flag described and every verb's effect stated; and a spill that can be
// tried with no store (--dry-run).

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coldRun is run() with no store behind any address and an empty environment
// that fails the test when it is read: a refusal comes before the login.
func coldRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	d := deps{
		now: func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC) },
		getenv: func(k string) string {
			assert.Failf(t, "", "%q read %s; a refusal or help reads no login", args, k)
			return ""
		},
	}
	code := run(args, &out, &errb, d)
	return code, out.String(), errb.String()
}

// refusalLine is the one grammar of an invocation refusal (STANDARD §3.1).
var refusalLine = regexp.MustCompile(`^nova-redis( [a-z]+){0,2} REFUSED: .+; run: nova-redis help( [a-z]+){0,2}$`)

func TestARefusalNamesEveryProblemInTheOneGrammar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want []string // one line each, in any order
	}{
		{"a bad address does not hide a zero ttl", []string{"spill", "--addr", "nohost", "--ttl", "0s", "--owner", "a", "--name", "b", "--value", "c"},
			[]string{`nova-redis spill REFUSED: --addr "nohost" is not <host:port>`, "nova-redis spill REFUSED: --ttl is required and must be above zero"}},
		{"a bad owner, name and ttl at once", []string{"spill", "--addr", "127.0.0.1:1", "--owner", "a:b", "--name", "x y", "--ttl", "banana", "--value", "c"},
			[]string{`--ttl "banana" is not a duration`, "--owner is required and may not", "--name is required and may not"}},
		{"every missing flag says what it wants", []string{"spill"},
			[]string{"--addr is required: the store's address", "--owner is required and may not", "--name is required and may not", "--ttl is required and must be above zero", "--value is required: the text"}},
		{"recall's missing owner beside a bad address", []string{"recall", "--addr", ":6379", "--name", "n"},
			[]string{`--addr ":6379" names no host`, "--owner is required"}},
		{"serve's bad bind and bad port at once", []string{"serve", "--bind", "0.0.0.0", "--port", "0", "--dir", "relative"},
			[]string{`--bind "0.0.0.0" binds every interface`, `--port "0" needs a port`, `--dir "relative" is not absolute`}},
		{"a misspelled flag lists the verb's flags", []string{"spill", "--zzz"},
			[]string{"nova-redis spill REFUSED: unknown flag --zzz; the flags of spill are --addr, --dry-run, --json, --name, --owner, --password-env, --ttl, --user, --value; run: nova-redis help spill"}},
		{"an unknown verb lists the verbs", []string{"zzz"},
			[]string{`nova-redis REFUSED: unknown verb "zzz"; the verbs are serve, spill, recall, fn load, fn check, acl render, acl check, acl apply, version, help; run: nova-redis help`}},
		{"an unknown fn subverb points at the group's help", []string{"fn", "deploy"},
			[]string{`nova-redis fn REFUSED: unknown subverb "deploy"; want load or check; run: nova-redis help fn`}},
		{"the bare command names its door", nil,
			[]string{"nova-redis REFUSED: no verb given;"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			code, out, errs := coldRun(t, c.args...)
			assert.Equal(t, 2, code)
			assert.Empty(t, out)
			lines := strings.Split(strings.TrimSuffix(errs, "\n"), "\n")
			for _, l := range lines {
				assert.Regexp(t, refusalLine, l)
			}
			assert.Len(t, lines, len(c.want), "one line per problem:\n%s", errs)
			for _, w := range c.want {
				assert.Contains(t, errs, w)
			}
		})
	}
}

// Help is never a refusal, for a verb group too, and every verb's help states
// its effect and describes every flag in words a cold reader can act on.
func TestEveryVerbsHelpStatesItsEffectAndDescribesEveryFlag(t *testing.T) {
	t.Parallel()
	flagLine := regexp.MustCompile(`^  --([a-z-]+)(?: <[a-z]+>)?(?:  (.*))?$`)
	for _, verb := range []string{"serve", "spill", "recall", "fn", "fn load", "fn check", "acl", "acl render", "acl check", "acl apply", "version"} {
		for _, form := range [][]string{append(strings.Fields(verb), "-h"), append([]string{"help"}, strings.Fields(verb)...)} {
			t.Run(strings.Join(form, " "), func(t *testing.T) {
				t.Parallel()
				code, out, errs := coldRun(t, form...)
				require.Equal(t, 0, code, errs)
				assert.Empty(t, errs)
				assert.Regexp(t, `(?m)^(effect|subverbs): \S`, out)
				for _, l := range strings.Split(out, "\n") {
					m := flagLine.FindStringSubmatch(l)
					if m == nil {
						continue
					}
					assert.GreaterOrEqual(t, len(strings.Fields(m[2])), 6, "--%s is described in fewer than six words: %q", m[1], l)
				}
			})
		}
	}
	code, out, _ := coldRun(t, "fn", "-h")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "nova-redis fn load")
	assert.Contains(t, out, "nova-redis fn check")
}

// spill --dry-run checks the whole line and the login and prints the write it
// would make, with no store behind the address: nothing is dialled.
func TestSpillDryRunNeedsNoStore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code, out, errs := h.run("spill", "--dry-run", "--owner", "ada", "--name", "note", "--ttl", "10m", "--value", "hi")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, "SPILL OK dry-run=true key=ada:note ttl=10m0s expires=2026-09-23T12:10:00Z bytes=2 store="+h.mr.Addr()+" written=0\n", out)
	assert.Empty(t, errs)
	assert.Zero(t, h.mr.TotalConnectionCount(), "a dry run dials nothing")
	assert.Empty(t, h.mr.Keys())

	code, _, errs = h.run("spill", "--dry-run", "--owner", "ada", "--name", "note", "--ttl", "0s", "--value", "hi")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--ttl is required and must be above zero")
}

// jsonOut is the one JSON object --json prints (internal/tool's Out).
type jsonOut struct {
	Result struct {
		Verb, Status, Remedy string
		Exit                 int
		Why                  []string
	}
	Items []struct {
		Kind   string
		Fields map[string]any
	}
	Notes   []string
	Payload string
}

func decodeOne(t *testing.T, out string) jsonOut {
	t.Helper()
	require.Equal(t, 1, strings.Count(out, "\n"), "one JSON object on one line: %q", out)
	var v jsonOut
	require.NoError(t, json.Unmarshal([]byte(out), &v), out)
	return v
}

// Every verb but serve takes --json and prints one object of the same value
// its lines print, on stdout, whatever the outcome: done, said no, refused.
func TestEveryVerbPrintsOneJSONObjectWithJSON(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	stored := "a b=c\\x20 d" // a space, an '=' and a backslash: the line escapes them, JSON does not
	code, _, errs := h.run("spill", "--owner", "ada", "--name", "note", "--ttl", "10m", "--value", stored)
	require.Equal(t, 0, code, errs)

	cases := []struct {
		name     string
		args     []string
		code     int
		status   string
		kind     string
		field    string
		want     any
		whyCount int
	}{
		{"recall carries the value exactly", []string{"recall", "--addr", h.mr.Addr(), "--owner", "ada", "--name", "note", "--json"}, 0, "ok", "RECALL OK", "value", stored, 0},
		{"recall of a missing key says no", []string{"recall", "--json", "--addr", h.mr.Addr(), "--owner", "ada", "--name", "gone"}, 1, "failed", "RECALL MISSING", "key", "ada:gone", 0},
		{"a dry run", []string{"spill", "--dry-run", "--json", "--addr", h.mr.Addr(), "--owner", "ada", "--name", "n", "--ttl", "1m", "--value", "v"}, 0, "ok", "SPILL OK", "written", float64(0), 0},
		{"a refusal names every problem", []string{"spill", "--json", "--addr", "nohost", "--ttl", "0s"}, 2, "refused", "", "", nil, 5},
		{"a store that does not answer", []string{"spill", "--json", "--addr", "127.0.0.1:1", "--owner", "a", "--name", "b", "--ttl", "1m", "--value", "c"}, 2, "refused", "SPILL FAIL", "class", "unreachable", 0},
		{"acl render", []string{"acl", "render", "--json"}, 0, "ok", "ACL RENDER OK", "users", float64(4), 0},
		{"version", []string{"version", "--json"}, 0, "ok", "", "", nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			code, out, errs := h.runBare(c.args...)
			assert.Equal(t, c.code, code)
			assert.Empty(t, errs, "with --json everything is the one object on stdout")
			v := decodeOne(t, out)
			assert.Equal(t, c.status, v.Result.Status)
			assert.Equal(t, c.code, v.Result.Exit)
			assert.Len(t, v.Result.Why, c.whyCount)
			if c.kind == "" {
				return
			}
			found := false
			for _, it := range v.Items {
				if it.Kind == c.kind {
					found = true
					assert.Equal(t, c.want, it.Fields[c.field])
				}
			}
			assert.True(t, found, "no item of kind %q in %s", c.kind, out)
		})
	}
	_, out, _ := h.runBare("version", "--json")
	assert.True(t, strings.HasPrefix(decodeOne(t, out).Payload, "nova-redis "))
	_, out, _ = h.runBare("spill", "--json")
	assert.Equal(t, "nova-redis help spill", decodeOne(t, out).Result.Remedy)
}

// A serve that could not start says what to do next: with no redis-server on
// PATH, install it; with one that would not run, inspect the directory and launch inputs.
func TestServeFailureNamesTheNextStep(t *testing.T) {
	t.Parallel()
	h := newServeHarness(t, "pw")
	h.d.lookPath = func(string) (string, error) { return "", errors.New("executable file not found in $PATH") }
	code, _, errs := h.run("serve", "--bind", "127.0.0.1", "--port", "6379", "--dir", h.dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, `remedy="install redis-server (Redis 7 or later) so it is on PATH, then run nova-redis serve again"`)

	h = newServeHarness(t, "pw")
	h.onLaunch = func(launchSpec) error { return errors.New("exit status 1") }
	code, _, errs = h.run("serve", "--bind", "127.0.0.1", "--port", "6379", "--dir", h.dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "remedy=")
	assert.Contains(t, errs, "run: ls -ld -- "+shellWord(h.dir))
	assert.Contains(t, errs, "compare directory access and the explicit --bind/--port")
	assert.Contains(t, errs, "with the launch error and any redis-server output")
}

// acl render prints what an operator reads, not the names of this repository's
// source: a family is its name and its key patterns.
func TestACLRenderPrintsNoSourceNames(t *testing.T) {
	t.Parallel()
	code, out, _ := aclRun(t, &fakeACL{}, "render")
	require.Equal(t, 0, code)
	assert.NotContains(t, out, "from=")
	assert.NotContains(t, out, "internal/")
}

// The banner says what the verbs do: acl apply's usage names
// --password-env-for, and the banner never claims no acl verb sets a password.
func TestTheBannerAgreesWithTheVerbs(t *testing.T) {
	t.Parallel()
	assert.Regexp(t, `(?m)^  nova-redis acl apply .*--password-env-for`, usage)
	assert.NotContains(t, usage, "No acl verb sets or reads")
	assert.Contains(t, usage, "nova-redis spill --dry-run")
}
