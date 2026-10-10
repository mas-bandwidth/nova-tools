package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSandboxPolicyCoverAncestorCount verifies AncestorCount on a hand-built Policy.
func TestSandboxPolicyCoverAncestorCount(t *testing.T) {
	t.Parallel()
	needUnixPaths(t)

	base := t.TempDir()
	read1 := filepath.Join(base, "r1")
	read2 := filepath.Join(base, "r2")
	write1 := filepath.Join(base, "w1")
	require.NoError(t, os.MkdirAll(read1, 0o755))
	require.NoError(t, os.MkdirAll(read2, 0o755))
	require.NoError(t, os.MkdirAll(write1, 0o755))

	p := &Policy{
		Reads:    []string{read1, read2},
		Writes:   []string{write1},
		Cwd:      filepath.Join(base, "cwd"),
		Tmp:      filepath.Join(base, "tmp"),
		OptRoots: []string{filepath.Join(base, "opt")},
		PathDirs: []string{filepath.Join(base, "path")},
	}
	// Manually create the dirs so they exist
	for _, d := range []string{p.Cwd, p.Tmp, p.OptRoots[0], p.PathDirs[0]} {
		os.MkdirAll(d, 0o755)
	}

	count := p.AncestorCount()
	ancestors := Ancestors(p.ancestorPaths()...)
	require.Equal(t, len(ancestors), count, "AncestorCount should equal len(Ancestors(p.ancestorPaths()...))")
	// Root "/" is never counted
	for _, a := range ancestors {
		require.NotEqual(t, "/", a, "/ should never be an ancestor")
	}
	// A path shared by two lists is counted once (deduplication in Ancestors)
	readWrite := filepath.Join(base, "rw")
	require.NoError(t, os.MkdirAll(readWrite, 0o755))
	p2 := &Policy{
		Reads:  []string{readWrite},
		Writes: []string{readWrite},
	}
	count2 := p2.AncestorCount()
	ancestors2 := Ancestors(p2.ancestorPaths()...)
	require.Equal(t, len(ancestors2), count2, "Deduplication should count shared path once")
}

// TestSandboxPolicyCoverLoopbackHostText verifies loopbackHostText for various hosts.
func TestSandboxPolicyCoverLoopbackHostText(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		host  string
		valid bool
	}{
		{"localhost", "localhost", true},
		{"LOCALHOST", "LOCALHOST", true},
		{"127.0.0.1", "127.0.0.1", true},
		{"127.8.9.10", "127.8.9.10", true},
		{"::1", "::1", true},
		{"example.com", "example.com", false},
		{"10.0.0.1", "10.0.0.1", false},
		{"0.0.0.0", "0.0.0.0", false},
		{"::", "::", false},
		{"empty string", "", false},
		{"localhost.example", "localhost.example", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := loopbackHostText(tc.host)
			require.Equal(t, tc.valid, result, "loopbackHostText(%q)=%v want %v", tc.host, result, tc.valid)
		})
	}
}

// TestSandboxPolicyCoverBuildNetAllow verifies build with NetAllow scenarios.
func TestSandboxPolicyCoverBuildNetAllow(t *testing.T) {
	t.Parallel()
	needUnixPaths(t)

	write, read, home, _ := scratch(t)
	executable := anExecutable(t)

	// Valid NetAllow: all three should pass
	write2, read2, home2, _ := scratch(t)
	valid := Input{
		Reads:    []string{read2},
		Writes:   []string{write2},
		Home:     home2,
		NetAllow: []string{"localhost:11434", "127.0.0.1:8080", "[::1]:9000"},
		Argv:     []string{executable},
	}
	p, bad := build(valid, func() []string { return nil })
	require.Empty(t, bad, "valid NetAllow was refused: %v", bad)
	require.Len(t, p.NetAllow, 3, "expected 3 valid entries in NetAllow")

	// Missing port
	missingPort := Input{
		Reads:    []string{read},
		Writes:   []string{write},
		Home:     home,
		NetAllow: []string{"localhost"},
		Argv:     []string{executable},
	}
	_, bad = build(missingPort, func() []string { return nil })
	require.NotEmpty(t, bad, "missing port was not refused")
	require.Equal(t, "bad_net", bad[0].Reason, "expected bad_net reason")
	require.Contains(t, bad[0].Text, "--net-allow", "refusal should name --net-allow")

	// Empty host
	emptyHost := Input{
		Reads:    []string{read},
		Writes:   []string{write},
		Home:     home,
		NetAllow: []string{":8080"},
		Argv:     []string{executable},
	}
	_, bad = build(emptyHost, func() []string { return nil })
	require.NotEmpty(t, bad, "empty host was not refused")
	require.Equal(t, "bad_net", bad[0].Reason, "expected bad_net reason")

	// Remote host
	remoteHost := Input{
		Reads:    []string{read},
		Writes:   []string{write},
		Home:     home,
		NetAllow: []string{"example.com:8080"},
		Argv:     []string{executable},
	}
	_, bad = build(remoteHost, func() []string { return nil })
	require.NotEmpty(t, bad, "remote host was not refused")
	require.Equal(t, "bad_net", bad[0].Reason, "expected bad_net reason")

	// 10.x address
	remoteAddr := Input{
		Reads:    []string{read},
		Writes:   []string{write},
		Home:     home,
		NetAllow: []string{"10.0.0.1:8080"},
		Argv:     []string{executable},
	}
	_, bad = build(remoteAddr, func() []string { return nil })
	require.NotEmpty(t, bad, "10.x address was not refused")
	require.Equal(t, "bad_net", bad[0].Reason, "expected bad_net reason")
}

// TestSandboxPolicyCoverDenyPath verifies denyPath function.
func TestSandboxPolicyCoverDenyPath(t *testing.T) {
	t.Parallel()

	base := t.TempDir()

	// Symlink to directory
	realDir := filepath.Join(base, "real")
	require.NoError(t, os.MkdirAll(realDir, 0o755))
	symlink := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(realDir, symlink))
	result := denyPath(symlink)
	require.Equal(t, realDir, result, "symlink should be resolved to target")

	// Missing absolute path with /x/../ segment (cleaned, not resolved)
	missingPath := filepath.Join(base, "x", "..", "y")
	result = denyPath(missingPath)
	require.Contains(t, result, "y", "missing path should be cleaned")

	// Relative name (made absolute)
	relative := "rel"
	result = denyPath(relative)
	require.True(t, filepath.IsAbs(result), "relative path should be made absolute")
}

// TestSandboxPolicyCoverBuildDeny verifies build with Deny scenarios.
func TestSandboxPolicyCoverBuildDeny(t *testing.T) {
	t.Parallel()

	write, read, home, _ := scratch(t)
	executable := anExecutable(t)

	// Deny equals a write
	denyEqualsWrite := filepath.Join(filepath.Dir(write), "deny")
	require.NoError(t, os.MkdirAll(denyEqualsWrite, 0o755))
	homeDeny := filepath.Join(denyEqualsWrite, "home")
	require.NoError(t, os.MkdirAll(homeDeny, 0o755))
	iv := Input{
		Reads:  []string{read},
		Writes: []string{denyEqualsWrite},
		Home:   homeDeny,
		Deny:   []string{denyEqualsWrite},
		Argv:   []string{executable},
	}
	_, bad := build(iv, func() []string { return nil })
	require.NotEmpty(t, bad, "deny equal to write was not refused")
	require.Equal(t, "denied_write", bad[0].Reason, "expected denied_write reason")

	// Deny equals cwd (cwd is inside write, deny equals cwd)
	denyEqualsCwd := filepath.Join(write, "denied")
	require.NoError(t, os.MkdirAll(denyEqualsCwd, 0o755))
	homeCwd := filepath.Join(write, "home2")
	require.NoError(t, os.MkdirAll(homeCwd, 0o755))
	iv2 := Input{
		Reads:  []string{read},
		Writes: []string{write},
		Cwd:    denyEqualsCwd,
		Home:   homeCwd,
		Deny:   []string{denyEqualsCwd},
		Argv:   []string{executable},
	}
	_, bad = build(iv2, func() []string { return nil })
	require.NotEmpty(t, bad, "deny equal to cwd was not refused")
	require.Equal(t, "denied_write", bad[0].Reason, "expected denied_write reason")

	// Deny holds a write
	denyHoldsWrite := filepath.Join(filepath.Dir(write), "parent")
	childWrite := filepath.Join(denyHoldsWrite, "child")
	require.NoError(t, os.MkdirAll(childWrite, 0o755))
	childHome := filepath.Join(childWrite, "home")
	require.NoError(t, os.MkdirAll(childHome, 0o755))
	iv3 := Input{
		Reads:  []string{read},
		Writes: []string{childWrite},
		Home:   childHome,
		Deny:   []string{denyHoldsWrite},
		Argv:   []string{executable},
	}
	_, bad = build(iv3, func() []string { return nil })
	require.NotEmpty(t, bad, "deny holding write was not refused")
	require.Equal(t, "denied_write", bad[0].Reason, "expected denied_write reason")

	// Deny is a sibling (not refused)
	siblingDeny := filepath.Join(filepath.Dir(write), "sibling")
	require.NoError(t, os.MkdirAll(siblingDeny, 0o755))
	iv4 := Input{
		Reads:  []string{read},
		Writes: []string{write},
		Home:   home,
		Deny:   []string{siblingDeny},
		Argv:   []string{executable},
	}
	_, bad = build(iv4, func() []string { return nil })
	require.Empty(t, bad, "sibling deny should not cause refusal")
}
