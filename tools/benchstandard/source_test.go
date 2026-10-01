package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files: %v", err)
	}
	out := map[string][]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
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
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	doc = doc[:strings.Index(doc, "package main")]
	if !regexp.MustCompile(`(?i)a witness and never a provisioner`).MatchString(doc) {
		t.Errorf("the doc does not name the tool a witness, not a provisioner")
	}
	if !regexp.MustCompile(`(?s)Its one mutation is --apply.*No other action is taken`).MatchString(doc) {
		t.Errorf("the doc does not state that --apply is the one mutation")
	}
}

func TestTheWitnessCarriesNoProvisioningPrimitive(t *testing.T) {
	t.Parallel()
	forbidden := regexp.MustCompile(`apt-get|apt install|dnf install|yum install|brew install|useradd|usermod|groupadd|"mount"|"umount"|systemctl (enable|start|disable|mask|preset)|"(enable|disable|mask|preset|daemon-reload)"|curl .*\| *(sh|bash)|terraform apply|pip3? install|go install |npm install|os\.Chmod|os\.Chown|os\.Rename|os\.Symlink|os\.WriteFile|os\.Create|os\.OpenFile|os\.Remove\(|os\.RemoveAll`)
	for f, lines := range sources(t) {
		for i, l := range lines {
			if forbidden.MatchString(l) {
				t.Errorf("%s: line %d carries a provisioning primitive the witness role forbids: %s", f, i+1, strings.TrimSpace(l))
			}
		}
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
	if len(uses["Kill"]) != 1 {
		t.Errorf("Kill is called from %v, want once, in killStrays", uses["Kill"])
	}
	// MkdirTemp and RemoveUnder pair off in the canary and the network probe.
	if len(uses["MkdirTemp"]) != 2 || len(uses["RemoveUnder"]) != 2 || len(uses["MkdirAll"]) != 2 {
		t.Errorf("scratch directory calls: MkdirTemp %v MkdirAll %v RemoveUnder %v, want two each", uses["MkdirTemp"], uses["MkdirAll"], uses["RemoveUnder"])
	}
}

func TestKillIsOnlyReachedUnderApplyAndOnlyOnAStrayPid(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("checks.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	i := strings.Index(src, "func (w *witness) killStrays()")
	if i < 0 {
		t.Fatal("killStrays is gone")
	}
	body := src[i:]
	body = body[:strings.Index(body, "\n}\n")]
	if !strings.Contains(body, "!w.apply") {
		t.Errorf("killStrays does not return early without --apply:\n%s", body)
	}
	if !strings.Contains(body, "range w.strays") || !strings.Contains(body, "w.h.Kill(n)") {
		t.Errorf("killStrays does not signal each stray and nothing else:\n%s", body)
	}
	if n := strings.Count(body, ".Kill("); n != 1 {
		t.Errorf("killStrays signals %d times in its body", n)
	}
}

// The wanted go is go.mod's, never a default copied into the tool.
func TestNoGoVersionIsHardCoded(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`go1\.\d+`)
	for f, lines := range sources(t) {
		for i, l := range lines {
			if re.MatchString(l) {
				t.Errorf("%s:%d names a go version: %s", f, i+1, strings.TrimSpace(l))
			}
		}
	}
}

// The bench user is per bench, a registry field: no row checks the user's name,
// and no line reads a coordinator.
func TestThereIsNoBenchUserRowAndNoCoordinatorLine(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`NOVA_BENCH_USER|whoami|NOVA_COORDINATOR|user\.Current`)
	for f, lines := range sources(t) {
		for i, l := range lines {
			if re.MatchString(l) {
				t.Errorf("%s:%d carries a bench-user row or a coordinator line: %s", f, i+1, strings.TrimSpace(l))
			}
		}
	}
}

// Every line the tool can print that is a finding starts DRIFT, because a person
// and a workflow grep for it.
func TestEveryFindingGoesThroughDrift(t *testing.T) {
	t.Parallel()
	for f, lines := range sources(t) {
		if f == "host.go" {
			continue
		}
		for i, l := range lines {
			if strings.Contains(l, `"DRIFT `) && !strings.Contains(l, `"DRIFT "+format`) && !strings.Contains(l, "DRIFT unknown argument") {
				t.Errorf("%s:%d prints a DRIFT line outside drift(): %s", f, i+1, strings.TrimSpace(l))
			}
		}
	}
}
