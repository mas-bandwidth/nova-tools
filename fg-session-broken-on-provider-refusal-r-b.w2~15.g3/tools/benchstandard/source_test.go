package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The witness is a witness and never a provisioner (nova-tools #2230): the tool
// documents that role, carries no provisioning primitive, and makes exactly one
// mutation of the machine, --apply's SIGTERM to a stray runner listener. These
// read the source of this package, because what a program can do to a machine is
// what its code names, and a later edit that adds a second mutation is a red
// test and not a review comment.

// sources are this package's non-test Go files' lines, comments dropped.
func sources(t *testing.T) map[string][]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	require.NotEmpty(t, files, "no Go files: %v", err)
	out := map[string][]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		var code []string
		for _, l := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "//") {
				continue
			}
			code = append(code, l)
		}
		out[f] = code
	}
	return out
}

func TestTheDocumentationNamesTheToolAWitnessWithApplyTheOnlyMutation(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("main.go")
	require.NoError(t, err)
	doc := string(raw)
	doc = doc[:strings.Index(doc, "package main")]
	assert.Regexp(t, `(?i)a witness and never a provisioner`, doc)
	assert.Regexp(t, `(?s)Its one mutation is --apply.*No other action is taken`, doc)
}

// The source is scanned for shapes it must never carry, each row a rule the
// witness role keeps; host.go is exempt where its own plumbing is allowed.
func TestTheSourceCarriesNoForbiddenShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		re       *regexp.Regexp
		allowed  func(string) bool
		skipHost bool
		why      string
	}{
		{"TestTheWitnessCarriesNoProvisioningPrimitive",
			regexp.MustCompile(`apt-get|apt install|dnf install|yum install|brew install|useradd|usermod|groupadd|"mount"|"umount"|systemctl (enable|start|disable|mask|preset)|"(enable|disable|mask|preset|daemon-reload)"|curl .*\| *(sh|bash)|terraform apply|pip3? install|go install |npm install|os\.Chmod|os\.Chown|os\.Rename|os\.Symlink|os\.WriteFile|os\.Create|os\.OpenFile|os\.Remove\(|os\.RemoveAll`),
			nil, false, "a provisioning primitive the witness role forbids"},
		{"TestNoGoVersionIsHardCoded", regexp.MustCompile(`go1\.\d+`), nil, false, "a go version"},
		{"TestThereIsNoBenchUserRowAndNoCoordinatorLine", regexp.MustCompile(`NOVA_BENCH_USER|whoami|NOVA_COORDINATOR|user\.Current`), nil, false, "a bench-user row or a coordinator line"},
		{"TestEveryFindingGoesThroughDrift", regexp.MustCompile(`"DRIFT `), func(l string) bool {
			return strings.Contains(l, `"DRIFT "+format`) || strings.Contains(l, "DRIFT unknown argument")
		}, true, "a DRIFT line outside drift()"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for f, lines := range sources(t) {
				if tc.skipHost && f == "host.go" {
					continue
				}
				for i, l := range lines {
					if tc.re.MatchString(l) && (tc.allowed == nil || !tc.allowed(l)) {
						t.Errorf("%s:%d carries %s: %s", f, i+1, tc.why, strings.TrimSpace(l))
					}
				}
			}
		})
	}
}

// The mutating calls of the host are each used once, for the one thing each is
// for: Kill under --apply, and a scratch directory made and removed by the two
// probes that run inside the wall.
func TestTheMutationsAreKillAndTheProbesOwnScratchDirectories(t *testing.T) {
	t.Parallel()
	uses := map[string][]string{}
	mutators := regexp.MustCompile(`\.(Kill|MkdirTemp|MkdirAll|RemoveUnder)\(`)
	for f, lines := range sources(t) {
		if f == "host.go" {
			continue
		}
		for _, l := range lines {
			for _, m := range mutators.FindAllStringSubmatch(l, -1) {
				uses[m[1]] = append(uses[m[1]], f)
			}
		}
	}
	assert.Len(t, uses["Kill"], 1, "Kill is called from %v, want once, in killStrays", uses["Kill"])
	// MkdirTemp and RemoveUnder pair off in the canary and the network probe.
	assert.Len(t, uses["MkdirTemp"], 2, "scratch directory calls: MkdirTemp %v MkdirAll %v RemoveUnder %v, want two each", uses["MkdirTemp"], uses["MkdirAll"], uses["RemoveUnder"])
	assert.Len(t, uses["RemoveUnder"], 2)
	assert.Len(t, uses["MkdirAll"], 2)
}

func TestKillIsOnlyReachedUnderApplyAndOnlyOnAStrayPid(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("checks.go")
	require.NoError(t, err)
	src := string(raw)
	i := strings.Index(src, "func (w *witness) killStrays()")
	require.GreaterOrEqual(t, i, 0, "killStrays is gone")
	body := src[i:]
	body = body[:strings.Index(body, "\n}\n")]
	assert.Contains(t, body, "!w.apply", "killStrays does not return early without --apply")
	assert.Contains(t, body, "range w.strays")
	assert.Contains(t, body, "w.h.Kill(n)")
	assert.Equal(t, 1, strings.Count(body, ".Kill("), "killStrays signals more than once")
}
