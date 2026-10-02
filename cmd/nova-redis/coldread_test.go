package main

// coldread_test.go holds what an AI meeting nova-redis cold needs from it: every
// problem with a line named in one run, in the one refusal grammar with the
// verb's own help as the next command; help for a verb group, never a refusal;
// every flag described and every verb's effect stated; and a spill that can be
// tried with no store (--dry-run).

import (
	"bytes"
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
			t.Errorf("%q read %s; a refusal or help reads no login", args, k)
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
			[]string{"nova-redis spill REFUSED: unknown flag --zzz; the flags of spill are --addr, --dry-run, --name, --owner, --password-env, --ttl, --user, --value; run: nova-redis help spill"}},
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
