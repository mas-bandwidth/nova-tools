package main

import (
	"strings"
	"testing"
)

func TestRetiredBusDecideFlags(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"--decide", "--floor=0.9", "--key-env=KEY", "--base-url=https://provider.invalid", "--allow-private"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			for _, verb := range []string{"inbox", "wait"} {
				r := invoke(t, "", verb, flag).mustCode(t, 2)
				if !strings.Contains(r.stderr, "flag provided but not defined") {
					t.Fatalf("%s %s: %s", verb, flag, r.stderr)
				}
				if strings.Contains(r.stdout, "INBOX DECIDED") {
					t.Fatal("retired decision receipt printed")
				}
			}
		})
	}
}
