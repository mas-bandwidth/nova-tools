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
		if code := update.Main("nova-version", []string{verb}, "v91.2.3", &out, &err); code != 0 || !strings.HasPrefix(out.String(), "nova-version v91.2.3 ") {
			t.Fatalf("%d %s %s", code, out.String(), err.String())
		}
	}
	var out, err bytes.Buffer
	if code := update.Main("nova-version", []string{"version", "extra"}, "v91.2.3", &out, &err); code != 2 {
		t.Fatal(code)
	}
	out.Reset()
	err.Reset()
	if code := update.Main("nova-version", []string{"help"}, "", &out, &err); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), "version") {
		t.Fatal("help omitted version")
	}
}

func TestRefusalNamesVersionNotUpdate(t *testing.T) {
	for _, args := range [][]string{{}, {"--no-such-flag-zz"}} {
		var out, errs bytes.Buffer
		if code := update.Main("nova-version", args, "", &out, &errs); code != 2 {
			t.Fatalf("args %v: exit %d, want 2 (stderr=%q)", args, code, errs.String())
		}
		if !strings.Contains(errs.String(), "VERSION REFUSED") {
			t.Errorf("args %v: refusal does not name nova-version: %q", args, errs.String())
		}
		if strings.Contains(errs.String(), "UPDATE") {
			t.Errorf("args %v: refusal names nova-update: %q", args, errs.String())
		}
	}
}

func TestVersionHelpDoesNotDemandAnApplyVerb(t *testing.T) {
	var out, err bytes.Buffer
	if code := update.Main("nova-version", []string{"help"}, "", &out, &err); code != 0 {
		t.Fatalf("help exit=%d stderr=%s", code, err.String())
	}
	if strings.Contains(out.String(), "apply name") {
		t.Fatalf("nova-version help tells a stranger to apply a name it has no verb for:\n%s", out.String())
	}
	var upd, updErr bytes.Buffer
	if code := update.Main("nova-update", []string{"help"}, "", &upd, &updErr); code != 0 {
		t.Fatalf("nova-update help exit=%d stderr=%s", code, updErr.String())
	}
	if !strings.Contains(upd.String(), "apply name") {
		t.Fatalf("nova-update help lost its apply verb:\n%s", upd.String())
	}
}

func TestVersionReportFileUsageStatesShape(t *testing.T) {
	var out, err bytes.Buffer
	if code := update.Main("nova-version", []string{"help"}, "", &out, &err); code != 0 {
		t.Fatalf("help exit=%d stderr=%s", code, err.String())
	}
	for _, want := range []string{"--file <manifest: ", "one line per tool", "written by hand"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("--file usage does not name the file's shape (%q):\n%s", want, out.String())
		}
	}
}
