package main

import (
	"github.com/mas-bandwidth/nova-tools/internal/friendwatch"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if handled, code := friendwatch.OwnedHelper(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}
