package main

import (
	"bytes"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/update"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionStampAndUsage(t *testing.T) {
	t.Parallel()

	for _, verb := range []string{"version", "--version"} {
		var out, err bytes.Buffer
		{
			code := update.Main("nova-update", []string{verb}, "v91.2.3", &out, &err)
			require.Equal(t, 0, code, "%d %s %s", code, out.String(), err.String())
			require.True(t, strings.HasPrefix(out.String(), "nova-update v91.2.3 "), "%d %s %s", code, out.String(), err.String())
		}
	}
	var out, err bytes.Buffer
	{
		code := update.Main("nova-update", []string{"version", "extra"}, "v91.2.3", &out, &err)
		require.Equal(t, 2, code, fmt.Sprint(code))
	}
	out.Reset()
	err.Reset()
	{
		code := update.Main("nova-update", []string{"help"}, "", &out, &err)
		require.Equal(t, 0, code, fmt.Sprint(code))
	}
	require.Contains(t, out.String(), "version", "help omitted version")
}
