package definition

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fixtureCommit = "0123456789abcdef0123456789abcdef01234567"
	fixtureRepo   = "example.com/owner/queue"
)

// goldenNames are the golden cards; the pins for them are computed, not stored.
var goldenNames = []string{"card-alpha", "card-beta", "card-gamma", "card-old"}

func blobID(data []byte) string {
	h := sha1.New()
	_, _ = fmt.Fprintf(h, "blob %d\x00", len(data))
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func goldenPins(t *testing.T) []Pinned {
	t.Helper()
	var pins []Pinned
	for _, n := range goldenNames {
		data := readTestdata(t, "cards/"+n+".md")
		pins = append(pins, Pinned{Repository: fixtureRepo, Commit: fixtureCommit, Path: "cards/" + n + ".md",
			ObjectID: blobID(data), SHA256: sum(data), Mode: "100644", Size: len(data), Data: data})
	}
	return pins
}

func TestGoldenCardsAdmitToGoldenRecords(t *testing.T) {
	t.Parallel()
	pins := goldenPins(t)
	defs := mustParse(t, Sources(pins))
	rep, refs := Validate(defs)
	if len(refs) > 0 {
		t.Fatalf("%v", Lines(refs))
	}
	if len(rep.External) != 0 {
		t.Fatalf("external %+v", rep.External)
	}
	as, refs := Admissions(defs, pins)
	if len(refs) > 0 {
		t.Fatalf("%v", Lines(refs))
	}
	enc, refs := EncodeAdmissions(as)
	if len(refs) > 0 {
		t.Fatalf("%v", Lines(refs))
	}
	for i, n := range goldenNames {
		want := strings.TrimSuffix(string(readTestdata(t, "admissions/"+n+".json")), "\n")
		if string(enc[i]) != want {
			t.Errorf("%s: canonical record differs from testdata/admissions/%s.json:\n got %s\nwant %s", n, n, enc[i], want)
		}
	}
	// The classes the fixtures name: two pull-request cards and two that need none.
	got := map[string]Completion{}
	for _, a := range as {
		got[a.ID] = a.Completion
	}
	want := map[string]Completion{"card-alpha": CompletionPR, "card-beta": CompletionPR, "card-gamma": CompletionNoPR, "card-old": CompletionNoPR}
	for id, c := range want {
		if got[id] != c {
			t.Errorf("%s completes as %q, want %q", id, got[id], c)
		}
	}
}

func TestEncodingIsCanonical(t *testing.T) {
	t.Parallel()
	pins := goldenPins(t)
	defs := mustParse(t, Sources(pins))
	as, _ := Admissions(defs, pins)
	enc, _ := EncodeAdmissions(as)
	digests, refs := DigestAdmissions(as)
	if len(refs) > 0 {
		t.Fatal(Lines(refs))
	}
	for i, b := range enc {
		// It decodes as an object of strings and arrays of strings only: no number,
		// no null, no nested object.
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: %v", b, err)
		}
		last := ""
		for _, k := range keysInOrder(t, b) {
			if k <= last {
				t.Errorf("keys not sorted: %q after %q", k, last)
			}
			last = k
		}
		for k, v := range m {
			switch x := v.(type) {
			case string:
			case []any:
				for _, e := range x {
					if _, ok := e.(string); !ok {
						t.Errorf("%s holds a non-string array element %v", k, e)
					}
				}
			default:
				t.Errorf("%s is %T, want string or array of strings", k, v)
			}
		}
		// Go's own compact, sorted-key encoding of the decoded object is these bytes: the
		// goldens carry nothing it would escape.
		if std, err := json.Marshal(m); err != nil || string(std) != string(b) {
			t.Errorf("not the compact sorted encoding:\n got %s\nwant %s", b, std)
		}
		if digests[i] != sum(b) {
			t.Errorf("digest is not the SHA-256 of the encoding")
		}
		// Deterministic.
		again, _ := EncodeAdmissions(as[i : i+1])
		if string(again[0]) != string(b) {
			t.Errorf("encoding is not deterministic")
		}
	}
	// The brief, DONE-WHEN and PROBES are not in the record.
	for _, b := range enc {
		s := string(b)
		for _, banned := range []string{"The queue constructor accepts", "fails at the base", "brief\":", "done_when", "probes"} {
			if strings.Contains(s, banned) {
				t.Errorf("record carries %q: %s", banned, s)
			}
		}
	}
}

// keysInOrder reads the top-level object's keys in the order the bytes carry them.
func keysInOrder(t *testing.T, b []byte) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(b)))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not an object: %v", err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func TestEncodingDoesNotEscapeHTMLAndEscapesControls(t *testing.T) {
	t.Parallel()
	a := Admission{ID: "a", Title: "<b>&</b> \"q\" \\ \u2028 \x7f é 日", Doors: "tab\there"}
	enc, refs := EncodeAdmissions([]Admission{a})
	if len(refs) > 0 {
		t.Fatal(Lines(refs))
	}
	s := string(enc[0])
	for _, want := range []string{"<b>&</b>", "\\\"q\\\"", "q\\\" \\\\ ", "\\u2028", "\\u007f", "tab\\u0009here", " é 日"} {
		if !strings.Contains(s, want) {
			t.Errorf("%s lacks %s", s, want)
		}
	}
	// Two encodings of records that differ in one field differ.
	b := a
	b.Title += "x"
	e2, _ := EncodeAdmissions([]Admission{b})
	if string(e2[0]) == s {
		t.Errorf("distinct records encode alike")
	}
	// An empty entry is omitted; a set one is written.
	if strings.Contains(s, `"entry"`) {
		t.Errorf("empty entry is written")
	}
	a.Entry = "work/x"
	if e, _ := EncodeAdmissions([]Admission{a}); !strings.Contains(string(e[0]), `"entry":"work/x"`) {
		t.Errorf("entry not written: %s", e[0])
	}
	// Invalid UTF-8 is refused, never repaired.
	if _, refs := EncodeAdmissions([]Admission{{ID: "\xff"}}); len(refs) != 1 {
		t.Errorf("invalid UTF-8 accepted")
	}
	if _, refs := DigestAdmissions([]Admission{{DependsOn: []string{"\xff"}}}); len(refs) != 1 {
		t.Errorf("invalid UTF-8 in a list accepted")
	}
}

func TestAdmissionsRefuseAPinThatIsNotTheDefinitionsBytes(t *testing.T) {
	t.Parallel()
	pins := goldenPins(t)
	defs := mustParse(t, Sources(pins))
	t.Run("length", func(t *testing.T) {
		t.Parallel()
		as, refs := Admissions(defs, pins[:1])
		if as != nil || len(refs) != 1 || refs[0].Cause != CausePinMismatch {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("digest", func(t *testing.T) {
		t.Parallel()
		bad := append([]Pinned(nil), pins...)
		bad[2].SHA256 = strings.Repeat("0", 64)
		as, refs := Admissions(defs, bad)
		if as != nil {
			t.Fatal("records beside a refusal")
		}
		if _, ok := hasRefusal(refs, "cards/card-gamma.md", 0, "", CausePinMismatch); !ok {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("path", func(t *testing.T) {
		t.Parallel()
		bad := append([]Pinned(nil), pins...)
		bad[0].Path = "cards/other.md"
		if _, refs := Admissions(defs, bad); len(refs) != 1 {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("a definition that fails the field rules", func(t *testing.T) {
		t.Parallel()
		d := append([]Definition(nil), defs...)
		d[1].Tier = "huge"
		if _, refs := Admissions(d, pins); len(refs) != 1 || refs[0].Cause != CauseInvalidTier || refs[0].Operation != OpAdmit {
			t.Fatalf("%v", Lines(refs))
		}
	})
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		if _, refs := Admissions(nil, nil); len(refs) != 1 {
			t.Fatalf("%v", Lines(refs))
		}
	})
}

// TestGoldenAdmissionFilesAreWritten regenerates the golden records when the
// file named by NOVA_CARD_GOLDEN_DIR is set, so a deliberate change to the record
// is one command; without it the test does nothing.
func TestGoldenAdmissionFilesAreWritten(t *testing.T) {
	t.Parallel()
	dir := os.Getenv("NOVA_CARD_GOLDEN_DIR")
	if dir == "" {
		t.Skip("NOVA_CARD_GOLDEN_DIR is not set")
	}
	pins := goldenPins(t)
	as, refs := Admissions(mustParse(t, Sources(pins)), pins)
	if len(refs) > 0 {
		t.Fatal(Lines(refs))
	}
	enc, _ := EncodeAdmissions(as)
	for i, n := range goldenNames {
		if err := os.WriteFile(filepath.Join(dir, n+".json"), append(enc[i], '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
