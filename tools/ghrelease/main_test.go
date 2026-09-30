package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestNoVerbPrintsTheBannerAndRefuses(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.wantRC(h.do(), 2)
	if !strings.Contains(h.errb.String(), "usage: go run ./tools/ghrelease <verb>") {
		t.Fatalf("no banner on stderr:\n%s", h.errb.String())
	}
}

func TestUnknownVerbIsRefusedByName(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.wantRC(h.do("no-such-verb"), 2)
	h.mustContain(`unknown verb "no-such-verb"`)
}

func TestEveryVerbHasASummaryAndHelp(t *testing.T) {
	t.Parallel()
	want := []string{"attach", "build", "certified", "controls", "ldflags", "stamp", "sums", "upload"}
	if got := strings.Join(names(), ","); got != strings.Join(want, ",") {
		t.Fatalf("verbs are %s, want %s", got, strings.Join(want, ","))
	}
	for _, n := range names() {
		v := registry[n]
		if strings.TrimSpace(v.summary) == "" || strings.TrimSpace(v.help) == "" || v.do == nil {
			t.Errorf("verb %s: summary, help and do are all required", n)
		}
		var out bytes.Buffer
		h := newHarness(t)
		h.e.stdout = &out
		if rc := h.do("help", n); rc != 0 || out.String() != v.help {
			t.Errorf("help %s: exit %d, printed %q", n, rc, out.String())
		}
		if !strings.HasPrefix(v.help, "usage: go run ./tools/ghrelease "+n) {
			t.Errorf("help %s does not open with its usage line", n)
		}
	}
}

func TestHelpWithNoVerbPrintsTheBannerOnStdout(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.wantRC(h.do("help"), 0)
	for _, n := range names() {
		if !strings.Contains(h.out.String(), n) {
			t.Errorf("banner does not list %s", n)
		}
	}
	h2 := newHarness(t)
	h2.wantRC(h2.do("help", "nope"), 2)
	h2.mustContain(`unknown verb "nope"`)
}

func TestRegisteringAVerbTwiceIsAProgrammingError(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("a second verb under one name did not panic")
		}
	}()
	register(verb{name: "ldflags"})
}

func TestARequiredVariableThatIsUnsetIsNamed(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"certified", "upload", "attach"} {
		h := newHarness(t)
		h.wantRC(h.do(v), 2)
		h.mustContain("is not set")
	}
}

func TestTheEmbeddedTargetsAreTheShippedPlatforms(t *testing.T) {
	t.Parallel()
	ts, err := parseTargets(releaseTargets)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) == 0 {
		t.Fatal("release-targets names no platform; a release would ship nothing")
	}
	seen := map[target]bool{}
	for _, x := range ts {
		if seen[x] {
			t.Errorf("%s is listed twice", x)
		}
		seen[x] = true
	}
	for _, x := range []target{{"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}} {
		if !seen[x] {
			t.Errorf("%s is not shipped", x)
		}
	}
	if (target{"windows", "amd64"}).ext() != ".exe" || (target{"linux", "amd64"}).ext() != "" {
		t.Error("only windows binaries carry .exe")
	}
}

func TestATargetLineOfAnyOtherShapeIsAnErrorNotASkip(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"linux", "linux amd64 extra", "linux  amd64", " linux amd64"} {
		if _, err := parseTargets("# c\n\n" + bad + "\n"); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	ts, err := parseTargets("# c\n\nlinux amd64\r\n")
	if err != nil || len(ts) != 1 {
		t.Fatalf("a comment, a blank and one line: %v %v", ts, err)
	}
}
