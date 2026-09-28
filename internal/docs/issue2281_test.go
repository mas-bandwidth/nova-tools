package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestIssue2281BehavioursAreProvedInNovaRedis ties the serve behaviours of
// docs/SPEC-REDIS.md "Tests this spec demands" (bind, auth, persistence,
// restart) to the tests that prove them: each name the spec lists must be
// declared as a test in cmd/nova-redis, where `serve` lives, so the spec cannot
// claim a proof the tree does not carry.
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
		6:  "TestBoundToLocalhostAndTailnetOnly",
		7:  "TestAuthFromNovaSecretsNeverAPlaintextArgument",
		8:  "TestPersistenceIsAOFWithNoEviction",
		10: "TestRestartOnTheSameDirKeepsTheStore",
	} {
		item := regexp.MustCompile(`(?m)^` + strconv.Itoa(n) + `\. ` + "`" + name + "`")
		if !item.Match(spec) {
			t.Errorf("docs/SPEC-REDIS.md does not list behaviour %d as `%s`", n, name)
		}
		decl := regexp.MustCompile(`(?m)^func ` + name + `\(t \*testing\.T\) \{`)
		if !decl.MatchString(tests.String()) {
			t.Errorf("behaviour %d: cmd/nova-redis declares no %s; the spec claims a proof the tree does not carry", n, name)
		}
	}
	if !strings.Contains(string(spec), "`cmd/nova-redis/serve_test.go` proves 6, 7 and 8") {
		t.Error("docs/SPEC-REDIS.md does not say serve_test.go proves 6-8")
	}
}
