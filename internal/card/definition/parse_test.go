package definition

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseBaseCard(t *testing.T) {
	t.Parallel()
	d := mustParse(t, one("cards/base.md", baseCard))[0]
	if d.ID != "card-alpha" || d.Schema != "v2" || d.Kind != "fix-red" || d.Tier != "flash" {
		t.Fatalf("fields: %+v", d)
	}
	if d.ContractSHA != "0123456789ab" || d.File != "cards/base.md" {
		t.Fatalf("contract or file: %+v", d)
	}
	if len(d.Paths) != 2 || d.Paths[1] != "internal/queue/name_test.go" || d.DependsOn != nil {
		t.Fatalf("paths or deps: %+v", d)
	}
	if d.Test.Package != "internal/queue" || d.Test.Name != "TestNameRefusesEmpty" {
		t.Fatalf("test: %+v", d.Test)
	}
	if d.BriefLine != 14 || !strings.HasPrefix(d.Brief, "# Reject") || d.Lines[KeyKind] != 5 || d.Lines[KeyProbes] != 12 {
		t.Fatalf("lines: brief at %d, %v", d.BriefLine, d.Lines)
	}
	if d.Digest != sum([]byte(baseCard)) || d.BriefDigest != sum([]byte(d.Brief)) {
		t.Fatalf("digests")
	}
}

func TestParseRefusals(t *testing.T) {
	t.Parallel()
	nineGlobs := "PATHS: a/1, a/2, a/3, a/4, a/5, a/6, a/7, a/8, a/9"
	cases := []struct {
		name  string
		text  string
		line  int
		key   string
		cause Cause
		found string // a substring of Found, "" for any
	}{
		{"duplicate key", insertAfter(5, "KIND: read"), 6, "KIND", CauseDuplicateKey, "lines 5 and 6"},
		{"schema v3 is refused by name", replaceLine(2, "SCHEMA: v3"), 2, "SCHEMA", CauseUnsupportedSchema, "v3"},
		{"schema v1", replaceLine(2, "SCHEMA: v1"), 2, "SCHEMA", CauseUnsupportedSchema, `"v1"`},
		{"required field missing", deleteLine(8), 0, "TIER", CauseRequiredMissing, ""},
		{"required field missing, ID", deleteLine(3), 0, "ID", CauseRequiredMissing, ""},
		{"stranded after the brief begins", join(append(baseLines()[:11], append(baseLines()[12:], "PROBES: none")...)), 16, "PROBES", CauseStranded, "line 16"},
		{"stranded by a prose line ending the header", insertAfter(7, "Some words."), 9, "TIER", CauseStranded, ""},
		{"case variant", replaceLine(5, "Kind: fix-red"), 5, "Kind", CauseAmbiguousSpelling, "KIND"},
		{"spacing variant before the colon", replaceLine(5, "KIND : fix-red"), 5, "KIND", CauseAmbiguousSpelling, "KIND"},
		{"leading blank variant", replaceLine(5, " KIND: fix-red"), 5, "KIND", CauseAmbiguousSpelling, "KIND"},
		{"underscore variant", replaceLine(7, "DEPENDS_ON: -"), 7, "DEPENDS_ON", CauseAmbiguousSpelling, "DEPENDS-ON"},
		{"hyphen variant", replaceLine(10, "DONEWHEN: x"), 10, "DONEWHEN", CauseAmbiguousSpelling, "DONE-WHEN"},
		{"unknown key", insertAfter(8, "ROUTE: flash"), 9, "ROUTE", CauseUnknownKey, ""},
		{"ID disagrees with the contract line", replaceLine(3, "ID: card-other"), 3, "ID", CauseIDMismatch, "card-other"},
		{"ID with a colon", replaceLine(3, "ID: card:alpha"), 3, "ID", CauseInvalidID, ""},
		{"ID with a comma", replaceLine(3, "ID: card,alpha"), 3, "ID", CauseInvalidID, ""},
		{"ID with non-ASCII", replaceLine(3, "ID: carté"), 3, "ID", CauseInvalidID, ""},
		{"ID that is the none token", replaceLine(3, "ID: -"), 3, "ID", CauseInvalidID, ""},
		{"ID over the bound", replaceLine(3, "ID: "+strings.Repeat("a", MaxIDBytes+1)), 3, "ID", CauseInvalidID, "128"},
		{"ENTRY with a comma", insertAfter(3, "ENTRY: work/a,work/b"), 4, "ENTRY", CauseInvalidEntry, "comma"},
		{"ENTRY with a control character", insertAfter(3, "ENTRY: work/a\x07b"), 4, "ENTRY", CauseInvalidValue, ""},
		{"empty value", replaceLine(4, "TITLE:"), 4, "TITLE", CauseEmptyValue, ""},
		{"TITLE with a tab", replaceLine(4, "TITLE: a\tb"), 4, "TITLE", CauseInvalidValue, ""},
		{"unknown kind", replaceLine(5, "KIND: nonsense"), 5, "KIND", CauseInvalidKind, "nonsense"},
		{"kind spelled in another case", replaceLine(5, "KIND: Fix-Red"), 5, "KIND", CauseInvalidKind, ""},
		{"paths that climb", replaceLine(6, "PATHS: ../x.go"), 6, "PATHS", CauseInvalidPaths, "climbs"},
		{"paths that match everything", replaceLine(6, "PATHS: **/*"), 6, "PATHS", CauseInvalidPaths, "every file"},
		{"paths with an empty entry", replaceLine(6, "PATHS: a/b.go,,c/d.go"), 6, "PATHS", CauseInvalidPaths, "empty entry"},
		{"paths over the bound", replaceLine(6, nineGlobs), 6, "PATHS", CauseInvalidPaths, "at most 8"},
		{"paths repeated", replaceLine(6, "PATHS: a/b.go, a/b.go"), 6, "PATHS", CauseInvalidPaths, "twice"},
		{"paths absolute", replaceLine(6, "PATHS: /etc/x"), 6, "PATHS", CauseInvalidPaths, "absolute"},
		{"depends-on a Work path", replaceLine(7, "DEPENDS-ON: work/a/b"), 7, "DEPENDS-ON", CauseInvalidDependsOn, ""},
		{"depends-on a reference", replaceLine(7, "DEPENDS-ON: owner/repo#4"), 7, "DEPENDS-ON", CauseInvalidDependsOn, ""},
		{"depends-on with an empty entry", replaceLine(7, "DEPENDS-ON: a,,b"), 7, "DEPENDS-ON", CauseInvalidDependsOn, "empty entry"},
		{"depends-on repeated", replaceLine(7, "DEPENDS-ON: a, a"), 7, "DEPENDS-ON", CauseInvalidDependsOn, "twice"},
		{"depends-on mixes none and an ID", replaceLine(7, "DEPENDS-ON: -, a"), 7, "DEPENDS-ON", CauseInvalidDependsOn, ""},
		{"tier that is not a route", replaceLine(8, "TIER: huge"), 8, "TIER", CauseInvalidTier, "huge"},
		{"test that is not the grammar", replaceLine(9, "TEST: nonsense"), 9, "TEST", CauseInvalidTest, ""},
		{"test none on a kind that lands a pull request", replaceLine(9, "TEST: none because"), 9, "TEST", CauseInvalidTest, "pull request"},
		{"test that names an Example", replaceLine(9, "TEST: internal/queue ExampleName"), 9, "TEST", CauseInvalidTest, "Test function"},
		{"test that names a package pattern", replaceLine(9, "TEST: ./... TestX"), 9, "TEST", CauseInvalidTest, ""},
		{"test package that climbs", replaceLine(9, "TEST: ../x TestX"), 9, "TEST", CauseInvalidTest, ""},
		{"contract line missing", replaceLine(1, "# a heading"), 1, "", CauseContractLine, ""},
		{"contract line without an ID", replaceLine(1, "RESULT: sha=0123456789ab"), 1, "", CauseContractLine, "no card ID"},
		{"contract sha that is not hex", replaceLine(1, "RESULT: card-alpha sha=zz"), 1, "", CauseContractSHA, ""},
		{"contract ID with a colon", replaceLine(1, "RESULT: card:alpha sha=0123456789ab"), 1, "", CauseContractLine, ""},
		{"no brief", join(baseLines()[:12]), 12, "", CauseNoBrief, ""},
		{"empty file", "", 1, "", CauseEmptyFile, ""},
		{"byte-order mark", "\xef\xbb\xbf" + baseCard, 1, "", CauseBOM, ""},
		{"invalid UTF-8", replaceLine(4, "TITLE: bad \xff byte"), 4, "", CauseInvalidUTF8, "0xff"},
		{"invalid UTF-8 in the brief", baseCard + "\xc3\x28\n", 17, "", CauseInvalidUTF8, ""},
		{"CRLF line endings", strings.ReplaceAll(baseCard, "\n", "\r\n"), 1, "", CauseCarriageReturn, ""},
		{"a bare carriage return", replaceLine(4, "TITLE: a\rb"), 4, "", CauseCarriageReturn, ""},
		{"a NUL byte", baseCard + "a\x00b\n", 17, "", CauseNUL, ""},
		{"file over the bound", baseCard + strings.Repeat("x", MaxCardBytes), 0, "", CauseFileTooLarge, "262144"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			defs, refs := Parse(one("cards/x.md", c.text))
			if defs != nil {
				t.Fatalf("Parse returned definitions beside refusals: %+v", defs)
			}
			r, ok := hasRefusal(refs, "cards/x.md", c.line, c.key, c.cause)
			if !ok {
				t.Fatalf("no refusal (file, line %d, key %q, cause %s) in %v", c.line, c.key, c.cause, Lines(refs))
			}
			if !strings.Contains(r.Found, c.found) {
				t.Errorf("found %q does not name %q", r.Found, c.found)
			}
			for _, r := range refs {
				wellFormed(t, r)
				if r.Operation != OpParse {
					t.Errorf("operation %q, want parse", r.Operation)
				}
			}
		})
	}
}

func TestParseAcceptsWhatTheFormatAllows(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"no sha on the contract line":      replaceLine(1, "RESULT: card-alpha"),
		"the colon-less contract line":     replaceLine(1, "RESULT card-alpha sha=0123456789ab"),
		"a note after the sha":             replaceLine(1, "RESULT: card-alpha sha=0123456789ab -- a note"),
		"blank lines inside the header":    insertAfter(7, ""),
		"an ENTRY line":                    insertAfter(3, "ENTRY: work/queue/names"),
		"an indented key line in the body": baseCard + "  KIND: indented text declares nothing\n",
		"a quoted key line in the body":    baseCard + "> KIND: quoted text declares nothing\n",
		"a fenced key line in the body":    baseCard + "```\nKIND: fenced text declares nothing\n```\n",
		"a tilde fence":                    baseCard + "~~~~\nID: nothing\n~~~~\n",
		"no final newline":                 strings.TrimSuffix(baseCard, "\n"),
		"tags on the test":                 replaceLine(9, "TEST: -tags functional internal/queue TestNameRefusesEmpty"),
		"a lone-file package":              replaceLine(9, "TEST: . TestRoot"),
		"the case of the words, not keys":  replaceLine(4, "TITLE: kind: a title with a colon and KIND in it"),
		"brief starting with a fence":      join(append(baseLines()[:13], "```", "KIND: read", "```")),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			defs, refs := Parse(one("x.md", text))
			if len(refs) > 0 || len(defs) != 1 {
				t.Fatalf("refused: %v", Lines(refs))
			}
		})
	}
	// A fence that never closes hides everything below it.
	if _, refs := Parse(one("x.md", baseCard+"```\nPROBES: never closed\n")); len(refs) > 0 {
		t.Fatalf("an open fence is text to its end: %v", Lines(refs))
	}
}

func TestParseArrayIsAllOrNothing(t *testing.T) {
	t.Parallel()
	good := Source{Name: "good.md", Data: []byte(baseCard)}
	bad := Source{Name: "bad.md", Data: []byte(replaceLine(5, "KIND: nonsense"))}
	worse := Source{Name: "worse.md", Data: []byte(replaceLine(2, "SCHEMA: v3"))}
	defs, refs := Parse([]Source{good, bad, worse})
	if defs != nil {
		t.Fatalf("definitions beside refusals: %v", defs)
	}
	if _, ok := hasRefusal(refs, "bad.md", 5, "KIND", CauseInvalidKind); !ok {
		t.Fatalf("bad.md not named: %v", Lines(refs))
	}
	if _, ok := hasRefusal(refs, "worse.md", 2, "SCHEMA", CauseUnsupportedSchema); !ok {
		t.Fatalf("worse.md not named: %v", Lines(refs))
	}
	for _, r := range refs {
		if r.File == "good.md" {
			t.Fatalf("the good file is named in a refusal: %s", r)
		}
	}
	// One file, or many, the same path.
	if defs, refs := Parse([]Source{good}); len(refs) != 0 || len(defs) != 1 {
		t.Fatalf("an array of one: %v", Lines(refs))
	}
}

func TestParseArrayBounds(t *testing.T) {
	t.Parallel()
	good := []byte(baseCard)
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		_, refs := Parse(nil)
		if len(refs) != 1 || refs[0].Cause != CauseEmptyArray {
			t.Fatalf("%v", Lines(refs))
		}
		wellFormed(t, refs[0])
	})
	t.Run("too many files names the limit", func(t *testing.T) {
		t.Parallel()
		var srcs []Source
		for i := 0; i <= MaxFiles; i++ {
			srcs = append(srcs, Source{Name: string(rune('a'+i%26)) + strings.Repeat("x", i), Data: good})
		}
		_, refs := Parse(srcs)
		if len(refs) != 1 || refs[0].Cause != CauseTooManyFiles || !strings.Contains(refs[0].Found, "128") {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("the same name twice", func(t *testing.T) {
		t.Parallel()
		_, refs := Parse([]Source{{Name: "a.md", Data: good}, {Name: "a.md", Data: good}})
		if _, ok := hasRefusal(refs, "a.md", 0, "", CauseDuplicateFile); !ok {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("no name", func(t *testing.T) {
		t.Parallel()
		_, refs := Parse([]Source{{Data: good}})
		if _, ok := hasRefusal(refs, "#1", 0, "", CauseFileName); !ok {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("total bytes over the bound", func(t *testing.T) {
		t.Parallel()
		big := []byte(baseCard + strings.Repeat("x", MaxCardBytes-len(baseCard)-1) + "\n")
		var srcs []Source
		for i := 0; i < MaxTotalBytes/len(big)+1; i++ {
			srcs = append(srcs, Source{Name: strings.Repeat("f", i+1), Data: big})
		}
		_, refs := Parse(srcs)
		found := false
		for _, r := range refs {
			found = found || r.Cause == CauseTotalTooLarge && strings.Contains(r.Found, "8388608")
		}
		if !found {
			t.Fatalf("%v", Lines(refs))
		}
	})
}

func TestRefusalRendersOnOneLine(t *testing.T) {
	t.Parallel()
	r := Refusal{Operation: OpParse, File: "a b\n.md", Line: 3, Key: "KIND", Cause: CauseDuplicateKey,
		Found: "two\nlines\rand \x1b[31mescapes", Next: "fix it", Also: []string{"other file.md"}}
	s := r.String()
	if strings.ContainsAny(s, "\n\r\x1b") {
		t.Fatalf("not one line: %q", s)
	}
	for _, want := range []string{"REFUSED parse", "line=3", "key=KIND", "cause=duplicate-key", "also=", "found=", "next="} {
		if !strings.Contains(s, want) {
			t.Errorf("%q lacks %q", s, want)
		}
	}
	if len(Lines([]Refusal{r, r})) != 2 {
		t.Fatal("Lines")
	}
}

func TestRenderRoundTripsGoldenCards(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"card-alpha", "card-beta", "card-gamma", "card-old"} {
		raw := readTestdata(t, "cards/"+name+".md")
		d := mustParse(t, []Source{{Name: name + ".md", Data: raw}})[0]
		again := mustParse(t, []Source{{Name: name + ".md", Data: Render(d)}})[0]
		if !equalContent(d, again) {
			t.Errorf("%s: Parse(Render(d)) != d:\n%+v\n%+v", name, d, again)
		}
		if !bytes.Equal(Render(again), Render(d)) {
			t.Errorf("%s: Render is not a fixed point", name)
		}
	}
}
