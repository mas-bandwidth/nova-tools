package sprint_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassGateNamesTheFirstFailureAndItsRepairCard(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, test, output string }{
		{name: "staticcheck", test: "TestStaticcheckFindings", output: "unused.go:3:6: func helper is unused (U1000)"},
		{name: "errcheck", test: "TestUncheckedErrors", output: "unused.go:3:6: error was not checked"},
		{name: "dead-code", test: "TestDeadCode", output: "unused.go:3:6: unreachable function"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "unused.go"), []byte("package m\n"), 0600))
			var run []string
			for _, c := range sprint.BaseClasses {
				if c.Name == tc.name {
					run = c.Run
				}
			}
			require.NotEmpty(t, run)
			why := strings.Join(run, " ") + ": " + "exit status 1" + ": --- FAIL: " + tc.test + " | " + tc.output + " | FAIL github.com/mas-bandwidth/nova-tools/internal/ci"
			got := sprint.ClassGateWhy(dir, "main", "0123456789abcdef", why)
			assert.Contains(t, got, "red on its class "+tc.name)
			assert.Contains(t, got, "fix-red-"+tc.name+"-main")
			assert.Contains(t, got, "TEST: internal/ci "+tc.test)
			assert.Contains(t, got, tc.output)
		})
	}

	for _, separator := range []string{"\t", `\x09`} {
		why := "go test -count=1 -timeout 600s ./internal/docs/ ./internal/ci/: exit status 1: --- FAIL: TestDocs (0.00s) | FAIL" + separator + "github.com/mas-bandwidth/nova-tools/internal/docs" + separator + "0.002s | FAIL"
		assert.Contains(t, sprint.ClassGateWhy(t.TempDir(), "main", "sha", why), "TEST: internal/docs TestDocs")
	}
	assert.Equal(t, "an unclassified environment refusal", sprint.ClassGateWhy(t.TempDir(), "main", "sha", "an unclassified environment refusal"))
}
