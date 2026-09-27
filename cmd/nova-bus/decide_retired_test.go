package main

import (
	"os"
	"path/filepath"
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

func TestInboxCarriesSameMessagesWithOrWithoutPublicMarker(t *testing.T) {
	t.Parallel()
	checkout, _ := busDir(t)
	args := []string{"inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "12", "--full"}
	private := invoke(t, "", args...).mustCode(t, 0)
	if err := os.WriteFile(filepath.Join(checkout, ".public"), []byte("public\n"), 0600); err != nil {
		t.Fatal(err)
	}
	public := invoke(t, "", args...).mustCode(t, 0)
	if public.stdout != private.stdout || public.stderr != private.stderr {
		t.Fatal("public marker changed ordinary message listing")
	}
	for _, unexpected := range []string{"INBOX DECIDED", "needs_reply=", "decider=", "conf="} {
		if strings.Contains(public.stdout, unexpected) {
			t.Errorf("inbox classified notes: %s", unexpected)
		}
	}
	if !strings.Contains(public.stdout, "A question about the gate") {
		t.Fatal("ordinary note disappeared")
	}
}
