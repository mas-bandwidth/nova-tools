package card

import (
	"strings"
	"testing"
)

func TestIDGrammar(t *testing.T) {
	t.Parallel()
	good := []string{"a", "A", "0", "_", "card-alpha", "card_1", "None", "NONE", "a-", "-a", "--", strings.Repeat("x", 64)}
	for _, s := range good {
		if !ValidID(s) {
			t.Errorf("ValidID(%q) = false", s)
		}
	}
	bad := map[string]Cause{
		"":                      CauseRequired,
		"-":                     CauseReservedWord,
		"none":                  CauseReservedWord,
		strings.Repeat("x", 65): CauseTooLong,
		"a b":                   CauseInvalidValue,
		"a,b":                   CauseInvalidValue,
		"a:b":                   CauseInvalidValue,
		"a.b":                   CauseInvalidValue,
		"a/b":                   CauseInvalidValue,
		"caf\u00e9":             CauseInvalidValue,
		"a\nb":                  CauseInvalidValue,
		"a\x00":                 CauseInvalidValue,
		"card:x":                CauseInvalidValue,
	}
	for s, want := range bad {
		c, why := IDFault(s)
		if c != want || why == "" {
			t.Errorf("IDFault(%q) = %q, %q; want %q", s, c, why, want)
		}
		if ID(s).Valid() {
			t.Errorf("ID(%q).Valid()", s)
		}
	}
	if !IsReserved("-") || !IsReserved("none") || IsReserved("None") || IsReserved("") {
		t.Error("IsReserved")
	}
}

func TestDigestAndObjectID(t *testing.T) {
	t.Parallel()
	h64, h40 := strings.Repeat("a", 64), strings.Repeat("0", 40)
	if !Digest(h64).Valid() || Digest(h40).Valid() || Digest(strings.ToUpper(h64)).Valid() || Digest("").Valid() || Digest(h64+"a").Valid() || Digest(strings.Repeat("g", 64)).Valid() {
		t.Error("Digest grammar")
	}
	for _, s := range []string{h40, h64} {
		if !ValidObjectID(s) {
			t.Errorf("ValidObjectID(%d chars) = false", len(s))
		}
	}
	for _, s := range []string{"", "abc", strings.Repeat("a", 39), strings.Repeat("a", 41), strings.Repeat("a", 63), strings.Repeat("A", 40), strings.Repeat("g", 40), strings.Repeat("a", 65)} {
		if ValidObjectID(s) {
			t.Errorf("ValidObjectID(%q) = true", s)
		}
	}
	if got := Sum([]byte("abc")); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" || !got.Valid() {
		t.Errorf("Sum = %s", got)
	}
}

func TestPrintableASCIIAndTextFault(t *testing.T) {
	t.Parallel()
	if !PrintableASCII("a/b#1:x") || PrintableASCII("") || PrintableASCII("a b") || PrintableASCII("caf\u00e9") || PrintableASCII("a\x7f") || PrintableASCII("a\n") {
		t.Error("PrintableASCII")
	}
	for name, s := range map[string]string{
		"bidi override":   "a\u202eb",
		"bidi isolate":    "a\u2066b",
		"zero width":      "a\u200bb",
		"zero width join": "a\u200db",
		"line separator":  "a\u2028b",
		"para separator":  "a\u2029b",
		"bom":             "a\ufeffb",
		"soft hyphen":     "a\u00adb",
		"word joiner":     "a\u2060b",
		"tag char":        "a\U000e0041b",
		"nel":             "a\u0085b",
		"newline":         "a\nb",
		"tab":             "a\tb",
		"nul":             "a\x00b",
		"del":             "a\x7fb",
	} {
		if TextFault(s, 100) != CauseControlChar {
			t.Errorf("%s: %q not refused", name, s)
		}
	}
	if TextFault("a\xffb", 100) != CauseInvalidUTF8 || TextFault("a\ufffdb", 100) != CauseInvalidUTF8 {
		t.Error("invalid UTF-8")
	}
	if TextFault(strings.Repeat("x", 101), 100) != CauseTooLong {
		t.Error("too long")
	}
	for _, ok := range []string{"", "plain", "caf\u00e9 \u4e16\u754c", "quotes \" and \\ and <&>"} {
		if c := TextFault(ok, 100); c != "" {
			t.Errorf("TextFault(%q) = %s", ok, c)
		}
	}
}

func TestNormalizeOrigin(t *testing.T) {
	t.Parallel()
	same := "github.com/Owner/repo"
	for _, raw := range []string{
		"https://github.com/Owner/repo",
		"https://GitHub.COM/Owner/repo.git",
		"https://user:secret@github.com/Owner/repo.git",
		"https://github.com:443/Owner/repo/",
		"ssh://git@github.com:22/Owner/repo.git",
		"ssh://GIT@GITHUB.com/Owner/repo",
		"git@github.com:Owner/repo.git",
		"github.com:Owner/repo",
		"git+ssh://git@github.com/Owner/repo.git",
		"  https://github.com/Owner/repo.git\n",
	} {
		got, why := NormalizeOrigin(raw)
		if why != "" || string(got) != same {
			t.Errorf("NormalizeOrigin(%q) = %q, %q; want %q", raw, got, why, same)
		}
	}
	// A non-default port is part of the identity; the path case is kept.
	if got, why := NormalizeOrigin("https://Example.com:8443/a/B.git"); why != "" || got != "example.com:8443/a/B" {
		t.Errorf("port kept: %q %q", got, why)
	}
	if a, _ := NormalizeOrigin("https://h.com/Owner/repo"); a == "h.com/owner/repo" {
		t.Error("path case folded")
	}
	if !Repository("github.com/Owner/repo").Valid() {
		t.Error("identity not valid")
	}
}

// A refusal about an origin says which rule failed and never quotes the origin,
// which can carry a credential.
func TestOriginRefusalNeverEchoesTheOrigin(t *testing.T) {
	t.Parallel()
	const secret = "ghp_SECRETTOKEN123"
	bad := []string{
		"",
		"file:///srv/git/" + secret,
		"/srv/" + secret + "/repo",
		"./" + secret,
		"~/" + secret,
		"https://x-access-token:" + secret + "@github.com/o/r?token=" + secret,
		"https://" + secret + "@/o/r",
		"https://github.com/o/r#" + secret,
		"ftp://" + secret + "@host/o/r",
		"https://github.com/o/" + secret + "/../r",
		"https://github.com/o/r/%zz" + secret,
		"https://github.com/" + strings.Repeat("a", 200) + "/" + secret,
		"just-a-word-" + secret,
		"https://github.com/o/r with space " + secret,
		"https://[::1" + secret,
		"https://github.com/o:" + secret + "/r",
		"https://github.com:" + secret + "/o/r",
		"https://github.com/o/r\x00" + secret,
		"\xff\xfe" + secret,
	}
	for _, raw := range bad {
		got, why := NormalizeOrigin(raw)
		if got != "" && why == "" {
			// some of these normalise to a (secret-free) identity; that is fine
			// only if no secret survived into it.
			if strings.Contains(string(got), "SECRET") {
				t.Errorf("identity %q carries the secret from %q", got, raw)
			}
			continue
		}
		if why == "" {
			t.Errorf("NormalizeOrigin(%q): no identity and no rule", raw)
		}
		if strings.Contains(why, "SECRET") || (raw != "" && strings.Contains(why, raw)) {
			t.Errorf("the rule %q echoes the origin %q", why, raw)
		}
	}
}

func TestRepositoryWhy(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]string{
		"":                              RuleEmpty,
		strings.Repeat("a", 129) + "/b": RuleTooLong,
		"github.com":                    RuleGrammar,
		"https://github.com/o/r":        RuleGrammar,
		"user@github.com/o/r":           RuleGrammar,
		"github.com/o/..":               RuleDotSegment,
		"GitHub.com/o/r":                RuleNotCanonical,
		"github.com:443/o/r":            RuleNotCanonical,
		"github.com:22/o/r":             RuleNotCanonical,
		"github.com/o/r.git":            RuleNotCanonical,
		"github.com/o/r":                "",
		"github.com:8443/o/r":           "",
		"h.example/a/b/c":               "",
	} {
		if got := RepositoryWhy(s); got != want {
			t.Errorf("RepositoryWhy(%q) = %q, want %q", s, got, want)
		}
	}
}

func TestPathGrammar(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"a", "cards/a.md", "a/b/c.md", "a b/c.md", "caf\u00e9/x.md", ".github/x.md", "a/.gitignore", "a/.gitx", "a-b/-c", strings.Repeat("a", 512)} {
		if !ValidPath(p) {
			t.Errorf("ValidPath(%q) = false", p)
		}
	}
	bad := map[string]Cause{
		"":                       CauseRequired,
		".":                      CauseInvalidPath,
		"..":                     CausePathEscapes,
		"a/..":                   CausePathEscapes,
		"../a":                   CausePathEscapes,
		"a/../b":                 CausePathEscapes,
		"a/./b":                  CauseInvalidPath,
		"a//b":                   CauseInvalidPath,
		"a/":                     CauseInvalidPath,
		"/a":                     CausePathEscapes,
		"C:/a":                   CausePathEscapes,
		"c:a":                    CausePathEscapes,
		`a\b`:                    CauseInvalidPath,
		"-a":                     CauseInvalidPath,
		"--option":               CauseInvalidPath,
		".git":                   CauseInvalidPath,
		".git/config":            CauseInvalidPath,
		"a/.git/config":          CauseInvalidPath,
		"a/.GIT/config":          CauseInvalidPath,
		"a/.Git":                 CauseInvalidPath,
		"a\x00b":                 CauseControlChar,
		"a\nb":                   CauseControlChar,
		"a\rb":                   CauseControlChar,
		"a\tb":                   CauseControlChar,
		"a\u202eb":               CauseControlChar,
		"a\xffb":                 CauseInvalidUTF8,
		strings.Repeat("a", 513): CauseTooLong,
	}
	for p, want := range bad {
		c, why := PathFault(p)
		if c != want || why == "" {
			t.Errorf("PathFault(%q) = %q, %q; want %q", p, c, why, want)
		}
		if strings.Contains(why, p) && len(p) > 8 {
			t.Errorf("the reason for %q quotes it: %q", p, why)
		}
	}
}
