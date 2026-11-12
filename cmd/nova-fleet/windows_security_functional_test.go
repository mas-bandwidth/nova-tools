//go:build functional

package main

import (
	"testing"
)

// windows_security_functional_test.go runs the admin runner's security decisions, the C# inside runner-windows.ps1
// (NovaTree: sealed trees, the PATH and tool folders, the folders above the admin root, the zip's extract and verify)
// and nova-ci-rules.ps1 (NovaCiAdmin: which jobs an admin runner takes), on fake ACLs and a real zip in a temp dir:
// testdata/winsec/Program.cs, built and run by runCSharp (csharp_functional_test.go). Nothing reaches Windows.
func TestFunctionalWindowsSecurityDecisions(t *testing.T) {
	t.Parallel()
	runCSharp(t, "testdata/winsec/Program.cs",
		[]string{"roles/github-runner/files/runner-windows.ps1", "roles/github-runner/files/ci-rules/nova-ci-rules.ps1"}, t.TempDir())
}
