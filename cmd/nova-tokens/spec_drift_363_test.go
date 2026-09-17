package main

// Red test for nova-tools #363 (Go cards 109-115): spec-versus-code drift.
// Every verb the binary accepts and every first-two-token output line it
// prints must be promised by docs/SPEC-TOKENS.md (verbs block + output
// grammar). Card 110 closed `publish` (struck, not shipped) but the binary
// since gained `profiles`, `session` and `fold-pool`, which print PROFILES,
// SESSION and FOLD lines the spec never promises.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func specTokensText(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "docs", "SPEC-TOKENS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestSpec363PromisesEveryVerbAndTokenItPrints(t *testing.T) {
	spec := specTokensText(t)
	for _, verb := range []string{"nova-tokens profiles", "nova-tokens session", "nova-tokens fold-pool"} {
		if !strings.Contains(spec, verb) {
			t.Errorf("the binary accepts `%s` but docs/SPEC-TOKENS.md promises no such verb (spec-versus-code drift, #363)", verb)
		}
	}
	for _, token := range []string{"PROFILES OK", "PROFILES MODEL", "SESSION turns=", "FOLD OK"} {
		if !strings.Contains(spec, token) {
			t.Errorf("the binary prints %q but docs/SPEC-TOKENS.md output grammar has no line for it (printed-not-promised, #363)", token)
		}
	}
}
