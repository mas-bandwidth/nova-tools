package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestIssue2281BehavioursAreProvedInNovaRedis ties behaviours 22-25 of
// docs/SPEC-REDIS.md "Tests this spec demands" (bind, auth, persistence,
// restart; nova-tools #2281) to the tests that prove them: each name the spec
// lists must be declared as a test in cmd/nova-redis, where `serve` lives, so
// the spec cannot claim a proof the tree does not carry.
func TestIssue2281BehavioursAreProvedInNovaRedis(t *testing.T) {
	t.Parallel()

	spec, err := os.ReadFile("../../docs/SPEC-REDIS.md")
	if err != nil {
		t.Fatalf("docs/SPEC-REDIS.md: %v", err)
	}
	files, err := filepath.Glob("../../cmd/nova-redis/*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("cmd/nova-redis holds no test files (err %v)", err)
	}
	var tests strings.Builder
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tests.Write(b)
	}
	for n, name := range map[int]string{
		22: "TestBoundToLocalhostAndTailnetOnly",
		23: "TestAuthFromNovaSecretsNeverAPlaintextArgument",
		24: "TestPersistenceOffNoRDBNoAOF",
		25: "TestRestartIsACleanSlate",
	} {
		item := regexp.MustCompile(`(?m)^\d+\. ` + "`" + name + "`")
		if !item.Match(spec) {
			t.Errorf("docs/SPEC-REDIS.md does not list behaviour %d as `%s`", n, name)
		}
		decl := regexp.MustCompile(`(?m)^func ` + name + `\(t \*testing\.T\) \{`)
		if !decl.MatchString(tests.String()) {
			t.Errorf("behaviour %d: cmd/nova-redis declares no %s; the spec claims a proof the tree does not carry", n, name)
		}
	}
	if !strings.Contains(string(spec), "`cmd/nova-redis/serve_test.go` proves 22, 23, 24 and 25") {
		t.Error("docs/SPEC-REDIS.md does not say serve_test.go proves 22-25")
	}
}
