//go:build unix

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/mas-bandwidth/nova-tools/internal/update"

	"github.com/stretchr/testify/require"
)

// THE SEQUENCE A FRIEND RUNS THROUGH THIS TOOL: snapshot the binaries in a bin
// directory to record what they report, then report on the hand-written
// manifest of installed commands. The stubs are shell scripts, so this file is
// unix-only.
func TestFriendSequenceSnapshotReport(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	{
		err := os.MkdirAll(bin, 0o755)
		require.NoError(t, err, err)
	}
	stub := func(name, line string) {
		t.Helper()
		{
			err := testbin.WriteExecutable(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf '%s\\n' '"+line+"'\n"), 0o755)
			require.NoError(t, err, err)
		}
	}
	stub("nova-bus", "nova-bus v0.15.0 darwin/arm64 go1.26.0")
	stub("nova-check", "nova-check v0.15.0 darwin/arm64 go1.26.0")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	manifest := filepath.Join(dir, "versions.tsv")
	body := update.Header + "\n" +
		"nova-bus\ttool\tnova-bus version\t-\t-\trowan\n" +
		"nova-check\ttool\tnova-check version\t-\t-\trowan\n"
	{
		err := os.WriteFile(manifest, []byte(body), 0o644)
		require.NoError(t, err, err)
	}

	snap := filepath.Join(dir, "snapshot.tsv")
	var out, errs bytes.Buffer
	{
		code := update.Main("nova-version", []string{"snapshot", "--bin", bin, "--out", snap}, "", &out, &errs)
		require.Equal(t, 0, code, "snapshot: exit %d\nstdout: %s\nstderr: %s", code, out.String(), errs.String())
	}
	require.Contains(t, out.String(), "SNAPSHOT OK bin=", "snapshot did not record both binaries:\n%s", out.String())
	require.Contains(t, out.String(), "tools=2", "snapshot did not record both binaries:\n%s", out.String())

	out.Reset()
	errs.Reset()
	// Generous probe and run deadlines: the report shells out to every adopted
	// tool, and the default five-second probe timeout asserts the machine's
	// load under a shared runner rather than the manifest's contents.
	{
		code := update.Main("nova-version", []string{"report", "--file", manifest, "--timeout", "30s", "--budget", "60s"}, "", &out, &errs)
		require.Equal(t, 0, code, "report on the adopted manifest: exit %d\nstdout: %s\nstderr: %s", code, out.String(), errs.String())
	}
	require.Contains(t, out.String(), "REPORT OK checked=2 known=2 unknown=0", "report did not read the adopted manifest:\n%s", out.String())
	for _, tool := range []string{"nova-bus", "nova-check"} {
		require.Contains(t, out.String(), "REPORT TOOL name="+tool, "report did not name %s:\n%s", tool, out.String())
	}
}
