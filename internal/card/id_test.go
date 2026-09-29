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
	same := "example.com/Owner/repo"
	for _, raw := range []string{
		"https://example.com/Owner/repo",
		"https://Example.COM/Owner/repo.git",
		"https://user:secret@example.com/Owner/repo.git",
		"https://example.com:443/Owner/repo/",
		"ssh://git@example.com:22/Owner/repo.git",
		"ssh://GIT@EXAMPLE.com/Owner/repo",
		"git@example.com:Owner/repo.git",
		"example.com:Owner/repo",
		"git+ssh://git@example.com/Owner/repo.git",
		"  https://example.com/Owner/repo.git\n",
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
	if a, _ := NormalizeOrigin("https://example.com/Owner/repo"); a == "example.com/owner/repo" {
		t.Error("path case folded")
	}
	if !Repository("example.com/Owner/repo").Valid() {
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
		strings.ReplaceAll("https://x-access-token:TOKEN@example.com/o/r?token=TOKEN", "TOKEN", secret),
		"https://" + secret + "@/o/r",
		"https://example.com/o/r#" + secret,
		"ftp://" + secret + "@host/o/r",
		"https://example.com/o/" + secret + "/../r",
		"https://example.com/o/r/%zz" + secret,
		"https://example.com/" + strings.Repeat("a", 200) + "/" + secret,
		"just-a-word-" + secret,
		"https://example.com/o/r with space " + secret,
		"https://[::1" + secret,
		"https://example.com/o:" + secret + "/r",
		"https://example.com:" + secret + "/o/r",
		"https://example.com/o/r\x00" + secret,
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
		"example.com":                   RuleGrammar,
		"https://example.com/o/r":       RuleGrammar,
		"user@example.com/o/r":          RuleGrammar,
		"example.com/o/..":              RuleDotSegment,
		"GitHub.com/o/r":                RuleNotCanonical,
		"example.com:443/o/r":           RuleNotCanonical,
		"example.com:22/o/r":            RuleNotCanonical,
		"example.com/o/r.git":           RuleNotCanonical,
		"example.com/o/r":               "",
		"example.com:8443/o/r":          "",
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

func TestTokenNameKindCounterHead(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"a", "ci:unit", "review:12", "org/repo@main", "a+b_c-d.e", "sha256:abc"} {
		if !ValidToken(s, 64) {
			t.Errorf("ValidToken(%q) = false", s)
		}
	}
	for _, s := range []string{"", "a b", "a,b", `a"b`, "a\\b", "caf\u00e9", "a#b", "a\nb", "a?b"} {
		if ValidToken(s, 64) {
			t.Errorf("ValidToken(%q) = true", s)
		}
	}
	if ValidToken(strings.Repeat("a", 65), 64) || !ValidToken(strings.Repeat("a", 64), 64) {
		t.Error("token bound")
	}
	if !ValidName("work", 64) || !ValidName("t.1-x_y", 64) || ValidName(".x", 64) || ValidName("-x", 64) || ValidName("a b", 64) || ValidName("", 64) {
		t.Error("ValidName")
	}
	if !ValidKind("fix-red") || ValidKind("Fix") || ValidKind("-a") || ValidKind("") || ValidKind(strings.Repeat("a", 33)) {
		t.Error("ValidKind")
	}
	for s, want := range map[string]bool{"0": true, "1": true, "18446744073709551615": true, "18446744073709551616": false, "01": false, "-0": false, "+1": false, "1.0": false, "1e3": false, "": false, " 1": false, "\u0661": false} {
		if got := ValidCounter(s); got != want {
			t.Errorf("ValidCounter(%q) = %v", s, got)
		}
	}
	if CounterAtLeastOne("0") || !CounterAtLeastOne("1") {
		t.Error("CounterAtLeastOne")
	}
	if n, ok := Count(""); n != 0 || !ok {
		t.Error("an absent counter reads as zero")
	}
	if n, ok := Count("7"); n != 7 || !ok {
		t.Error("Count(7)")
	}
	if _, ok := Count("x"); ok {
		t.Error("Count(x)")
	}
	d := Digest(strings.Repeat("a", 64))
	if got, ok := ParseTagged(d.Tagged()); !ok || got != d || d.Tagged() != "sha256:"+strings.Repeat("a", 64) {
		t.Error("Tagged round trip")
	}
	for _, s := range []string{"", string(d), "sha256:" + strings.Repeat("A", 64), "sha1:" + strings.Repeat("a", 64), "sha256:abc"} {
		if _, ok := ParseTagged(s); ok {
			t.Errorf("ParseTagged(%q)", s)
		}
	}
	if !ValidHead(strings.Repeat("a", 40)) || !ValidHead(d.Tagged()) || ValidHead(string(d)+"a") || ValidHead("main") || ValidHead("") {
		t.Error("ValidHead")
	}
}
