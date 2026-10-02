package main

import (
	"bytes"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/update"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionStampAndUsage(t *testing.T) {
	t.Parallel()

	for _, verb := range []string{"version", "--version"} {
		var out, err bytes.Buffer
		{
			code := update.Main("nova-version", []string{verb}, "v91.2.3", &out, &err)
			require.Equal(t, 0, code, "%d %s %s", code, out.String(), err.String())
			require.True(t, strings.HasPrefix(out.String(), "nova-version v91.2.3 "), "%d %s %s", code, out.String(), err.String())
		}
	}
	var out, err bytes.Buffer
	{
		code := update.Main("nova-version", []string{"version", "extra"}, "v91.2.3", &out, &err)
		require.Equal(t, 2, code, fmt.Sprint(code))
	}
	out.Reset()
	err.Reset()
	{
		code := update.Main("nova-version", []string{"help"}, "", &out, &err)
		require.Equal(t, 0, code, fmt.Sprint(code))
	}
	require.Contains(t, out.String(), "version", "help omitted version")
}

func TestRefusalNamesVersionNotUpdate(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{}, {"--no-such-flag-zz"}} {
		var out, errs bytes.Buffer
		{
			code := update.Main("nova-version", args, "", &out, &errs)
			require.Equal(t, 2, code, "args %v: exit %d, want 2 (stderr=%q)", args, code, errs.String())
		}
		assert.Contains(t, errs.String(), "VERSION REFUSED", "args %v: refusal does not name nova-version: %q", args, errs.String())
		assert.NotContains(t, errs.String(), "UPDATE", "args %v: refusal names nova-update: %q", args, errs.String())
	}
}

func TestVersionHelpDoesNotDemandAnApplyVerb(t *testing.T) {
	t.Parallel()

	var out, err bytes.Buffer
	{
		code := update.Main("nova-version", []string{"help"}, "", &out, &err)
		require.Equal(t, 0, code, "help exit=%d stderr=%s", code, err.String())
	}
	require.NotContains(t, out.String(), "apply name", "nova-version help tells a stranger to apply a name it has no verb for:\n%s", out.String())
	var upd, updErr bytes.Buffer
	{
		code := update.Main("nova-update", []string{"help"}, "", &upd, &updErr)
		require.Equal(t, 0, code, "nova-update help exit=%d stderr=%s", code, updErr.String())
	}
	require.Contains(t, upd.String(), "apply name", "nova-update help lost its apply verb:\n%s", upd.String())
}

func TestVersionReportFileUsageStatesShape(t *testing.T) {
	t.Parallel()

	var out, err bytes.Buffer
	{
		code := update.Main("nova-version", []string{"help"}, "", &out, &err)
		require.Equal(t, 0, code, "help exit=%d stderr=%s", code, err.String())
	}
	for _, want := range []string{"--file <manifest: ", "one line per tool", "written by hand"} {
		require.Contains(t, out.String(), want, "--file usage does not name the file's shape (%q):\n%s", want, out.String())
	}
}
