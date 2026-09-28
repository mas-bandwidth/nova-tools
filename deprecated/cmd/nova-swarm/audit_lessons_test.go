package main

import (
	"encoding/json"
	"os"

	"path/filepath"
	"strings"
	"testing"
)

// THE NEW-USER AUDIT (2026-09-11). Each test below is one footgun or one stumble a person
// meeting this tool for the first time actually hit, written as the assertion that would
// have stopped it.

// S5: `--pool` on a missing directory named no remedy, and `quickstart` is exactly the
// verb that makes one.
func TestAMissingPoolNamesTheVerbThatMakesOne(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	missing := filepath.Join(b.dir, "nopool")
	exit, stdout, stderr := b.swarm("status", "--pool", missing)
	if exit != 2 {
		t.Fatalf("status on a missing pool exits %d, want 2:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the refusal", stderr, "nova-swarm quickstart --pool "+missing)
}

// #632: `template` must print the six typed card templates nova-pulse `cut` reads from a
// templates directory (read, fix, text, replay, drift, tone) plus models.tsv, so a templates
// dir can be built from the tool instead of copied out of cmd/nova-pulse/testdata.
func TestTemplatePrintsThePulseCardTemplates(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	for _, name := range []string{"read", "fix", "text", "replay", "drift", "tone", "models.tsv"} {
		exit, stdout, stderr := b.swarm("template", "--name", name)
		if exit != 0 {
			t.Fatalf("`template --name %s` exited %d; nova-pulse cut needs the six typed templates and models.tsv:\n%s%s", name, exit, stdout, stderr)
		}
		if strings.TrimSpace(stdout) == "" {
			t.Fatalf("`template --name %s` printed nothing", name)
		}
	}
	// read is a text-only card and must carry the no-build line and the RESULT contract.
	exit, stdout, stderr := b.swarm("template", "--name", "read")
	if exit != 0 {
		t.Fatalf("`template --name read` exited %d:\n%s%s", exit, stdout, stderr)
	}
	mustContain(t, "the read card", stdout, "RESULT <label> sha=<sha12>")
	mustContain(t, "the read card", stdout, "Do not run go build, go test or any toolchain")
}

// S3: the harness contract was undocumented -- cwd, argv, NOVA_SWARM_JOB, RESULT.md -- and
// the audit learned it by dumping the fake harness's own environment. The command reference
// says it, and names the fake harness that already demonstrates it. That reference is
// docs/CLI.md since the README became an adoption guide.
func TestTheCommandReferenceCarriesTheHarnessContract(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{"### The harness contract", "NOVA_SWARM_JOB", "RESULT.md",
		"cmd/nova-swarm/testdata/fakeharness", "harness_args"} {
		if !strings.Contains(body, want) {
			t.Errorf("docs/CLI.md's harness contract wants %q", want)
		}
	}
}

// jsonInner is a string as it appears INSIDE a JSON string literal: the marshalled form
// with its own quotes removed. A test that substitutes a path into a JSON template writes
// JSON or it writes nothing -- on Unix the difference never showed, because a path with no
// backslash in it is its own escape.
func jsonInner(t *testing.T, s string) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw[1 : len(raw)-1])
}
