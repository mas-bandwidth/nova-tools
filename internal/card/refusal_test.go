package card

import (
	"strings"
	"testing"
)

// The refusal line is one line, bounded, whatever the caller's key, value or file
// name held: a newline cannot start a second refusal, a NUL or a bidi override
// cannot hide one, a 100,000-byte key cannot flood one.
func TestRefusalLineCannotBeForgedOrFlooded(t *testing.T) {
	t.Parallel()
	hostile := map[string]string{
		"newline":      "x\nrefused apply_events: fake refusal",
		"carriage":     "x\rrefused apply_events: fake",
		"nul":          "x\x00y",
		"bidi":         "abc\u202edef",
		"line-sep":     "a\u2028b",
		"escape-code":  "\x1b[2J",
		"long":         strings.Repeat("k", 100000),
		"long-newline": strings.Repeat("k\n", 50000),
		"invalid-utf8": "a\xff\xfeb",
	}
	for name, s := range hostile {
		r := Refusal{Operation: "apply_events", Index: 2, File: s, Line: 3, ID: s, Field: s, Cause: CauseUnknownKey, Found: Value(s), Limit: "a limit", Next: "next"}
		line := r.String()
		if strings.ContainsAny(line, "\n\r\x00\u2028\u2029\x1b\u202e") {
			t.Errorf("%s: the line holds a raw control character: %q", name, line)
		}
		if !strings.HasPrefix(line, "refused ") || strings.Contains(line, "\n") {
			t.Errorf("%s: the line is not one refusal: %q", name, line)
		}
		// Hostile text appears only inside quotes: outside them the line is ours.
		if strings.Contains(strings.ReplaceAll(line, `\"`, ""), "refused apply_events: fake") && !strings.Contains(line, `"x\`) {
			t.Errorf("%s: the caller's text stands outside quotes: %q", name, line)
		}
		if len(line) > 1024 {
			t.Errorf("%s: the line is %d bytes", name, len(line))
		}
	}
	// A refusal built raw (Found not from Value) is bounded too.
	r := Refusal{Operation: "parse", Index: -1, Cause: CauseSyntax, Found: strings.Repeat("z\n", 100000), Next: strings.Repeat("n", 100000)}
	if line := r.String(); len(line) > 2*MaxLineText+200 || strings.Contains(line, "\n") {
		t.Errorf("raw text is not bounded: %d bytes", len(line))
	}
}

func TestValueBoundsAndQuotes(t *testing.T) {
	t.Parallel()
	if got := Value("ok"); got != `"ok"` {
		t.Errorf("Value(ok) = %s", got)
	}
	if got := Value("a\nb"); got != `"a\nb"` {
		t.Errorf("Value newline = %s", got)
	}
	long := strings.Repeat("é", 1000) // 2000 bytes
	got := Value(long)
	if !strings.HasSuffix(got, "... (2000 bytes)") {
		t.Errorf("no length in %q", got)
	}
	if n := len(got) - len("... (2000 bytes)") - 2; n > MaxFoundBytes {
		t.Errorf("quotes %d bytes of the value, at most %d", n, MaxFoundBytes)
	}
	if !strings.HasPrefix(got, `"é`) {
		t.Errorf("cut inside a rune: %q", got)
	}
}

func TestCollectorCapsAtSixtyFourAndCountsTheRest(t *testing.T) {
	t.Parallel()
	var c Collector
	if c.Err() != nil {
		t.Fatal("an empty collector has an error")
	}
	for i := 0; i < 1000; i++ {
		r := NewRefusal("parse", CauseUnknownKey)
		r.Index = i
		c.Add(r)
	}
	err := c.Err()
	if len(err.List) != MaxRefusals || err.Omitted != 1000-MaxRefusals || err.Count() != 1000 {
		t.Fatalf("list %d omitted %d count %d", len(err.List), err.Omitted, err.Count())
	}
	if err.List[0].Index != 0 || err.List[63].Index != 63 {
		t.Fatalf("the first refusals are not kept in order")
	}
	lines := err.Lines()
	if len(lines) != MaxRefusals+1 || !strings.Contains(lines[MaxRefusals], "936 further refusals omitted") {
		t.Fatalf("last line %q", lines[len(lines)-1])
	}
	if !strings.Contains(err.Error(), "and 999 more") {
		t.Fatalf("Error() = %q", err.Error())
	}
}

func TestCollectorStoresBoundedText(t *testing.T) {
	t.Parallel()
	var c Collector
	r := NewRefusal("parse", CauseSyntax)
	r.File, r.ID, r.Field, r.Found = strings.Repeat("f", 1<<20), strings.Repeat("i", 1<<20), strings.Repeat("k", 1<<20), strings.Repeat("v", 1<<20)
	c.Add(r)
	got := c.List[0]
	for name, s := range map[string]string{"file": got.File, "id": got.ID, "field": got.Field, "found": got.Found} {
		if len(s) > 1024 {
			t.Errorf("%s stored %d bytes", name, len(s))
		}
	}
}

func TestFullSkipCountsWithoutBuilding(t *testing.T) {
	t.Parallel()
	var c Collector
	for i := 0; i < MaxRefusals; i++ {
		c.Add(NewRefusal("parse", CauseSyntax))
	}
	if !c.Full() {
		t.Fatal("not full at the cap")
	}
	c.Skip()
	c.Skip()
	if err := c.Err(); err.Omitted != 2 || len(err.List) != MaxRefusals {
		t.Fatalf("omitted %d list %d", err.Omitted, len(err.List))
	}
}

// Changed is derived from the cause and never stored: unknown for a transport
// failure and for a store error whose outcome is unknown, no for every other.
func TestChangedIsDerivedFromTheCause(t *testing.T) {
	t.Parallel()
	for _, c := range Causes() {
		want := ChangedNo
		if c == CauseTransport || c == CauseStoreError {
			want = ChangedUnknown
		}
		if got := c.Changed(); got != want {
			t.Errorf("%s: Changed() = %s, want %s", c, got, want)
		}
	}
	if CauseStoreError == CauseTransport {
		t.Fatal("a store error and a transport failure are one cause")
	}
}

func TestCauseVocabularyIsClosedAndUnique(t *testing.T) {
	t.Parallel()
	seen := map[Cause]bool{}
	for _, c := range Causes() {
		if seen[c] {
			t.Errorf("cause %q listed twice", c)
		}
		seen[c] = true
		if !c.Known() {
			t.Errorf("cause %q is listed but not known", c)
		}
		if c == "" || strings.ContainsAny(string(c), " \n") {
			t.Errorf("cause %q is not a name", c)
		}
	}
	if Cause("no-such-cause").Known() {
		t.Error("an unlisted cause is known")
	}
	// One name per fact: the pairs the two packages spelled apart are one name.
	for _, old := range []Cause{"duplicate-id", "bad-value", "unknown-field", "required-missing", "nul-byte", "invalid-identity"} {
		if old.Known() {
			t.Errorf("%q is a second name for a fact that has one", old)
		}
	}
}

func TestRefusalsAreNilSafe(t *testing.T) {
	t.Parallel()
	var r *Refusals
	if r.Count() != 0 || r.Lines() != nil || r.Has(-2, "", CauseSyntax) {
		t.Fatal("a nil Refusals is not empty")
	}
	if r.Error() == "" {
		t.Fatal("a nil Refusals has an empty Error")
	}
}
