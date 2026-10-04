package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRetiredBusDecideFlags(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"--decide", "--floor=0.9", "--key-env=KEY", "--base-url=https://provider.invalid", "--allow-private"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			for _, verb := range []string{"inbox", "wait"} {
				r := invoke(t, "", verb, flag).mustCode(t, 2)
				require.Containsf(t, r.stderr, strings.ToUpper(verb)+" REFUSED: unknown flag --", "%s %s: %s", verb, flag, r.stderr)
				require.NotContains(t, r.stderr, "flag provided but not defined", "the flag package's stock line")
				require.NotContains(t, r.stdout, "INBOX DECIDED", "retired decision receipt printed")
			}
		})
	}
}
