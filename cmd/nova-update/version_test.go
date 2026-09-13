package main

import (
	"bytes"
	"github.com/mas-bandwidth/nova-tools/internal/update"
	"strings"
	"testing"
)

func TestVersionStampAndUsage(t *testing.T) {
	for _, verb := range []string{"version", "--version"} {
		var out, err bytes.Buffer
		if code := update.Main("nova-update", []string{verb}, "v91.2.3", &out, &err); code != 0 || !strings.HasPrefix(out.String(), "nova-update v91.2.3 ") {
			t.Fatalf("%d %s %s", code, out.String(), err.String())
		}
	}
	var out, err bytes.Buffer
	if code := update.Main("nova-update", []string{"version", "extra"}, "v91.2.3", &out, &err); code != 2 {
		t.Fatal(code)
	}
	out.Reset()
	err.Reset()
	if code := update.Main("nova-update", []string{"help"}, "", &out, &err); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), "version") {
		t.Fatal("help omitted version")
	}
}
