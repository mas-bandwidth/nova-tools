package definition

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

// update rewrites the golden admission records under testdata/admissions:
//
//	go test ./internal/card/definition -run Golden -update
var update = flag.Bool("update", false, "rewrite the golden admission records")

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

func goldenPins(t *testing.T) []pinned {
	t.Helper()
	var pins []pinned
	for _, n := range goldenNames {
		data := readTestdata(t, "cards/"+n+".md")
		pins = append(pins, pinned{Repository: fixtureRepo, Commit: fixtureCommit, Path: "cards/" + n + ".md",
			ObjectID: blobID(data), SHA256: sum(data), Mode: "100644", Size: len(data), Data: data})
	}
	return pins
}

func TestGoldenCardsAdmitToGoldenRecords(t *testing.T) {
	t.Parallel()
	pins := goldenPins(t)
	as, refs := fromPins(pins)
	if refs != nil {
		t.Fatalf("%v", refs.Lines())
	}
	enc := EncodeAdmissions(as)
	for i, n := range goldenNames {
		path := filepath.Join("testdata", "admissions", n+".json")
		if *update {
			if err := os.WriteFile(path, append(enc[i], '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want := strings.TrimSuffix(string(readTestdata(t, "admissions/"+n+".json")), "\n")
		if string(enc[i]) != want {
			t.Errorf("%s: canonical record differs from testdata/admissions/%s.json (run with -update):\n got %s\nwant %s", n, n, enc[i], want)
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
	as, _ := fromPins(pins)
	enc := EncodeAdmissions(as)
	digests := DigestAdmissions(as)
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
		if std, err := json.Marshal(m); err != nil || string(std) != string(b) {
			t.Errorf("not the compact sorted encoding:\n got %s\nwant %s", b, std)
		}
		if digests[i] != sum(b) {
			t.Errorf("digest is not the SHA-256 of the encoding")
		}
		if again := EncodeAdmissions(as[i : i+1]); string(again[0]) != string(b) {
			t.Errorf("encoding is not deterministic")
		}
		if len(b) > card.MaxAdmissionRecordBytes {
			t.Errorf("record of %d bytes exceeds %d", len(b), card.MaxAdmissionRecordBytes)
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

// The record is encoded by the card layer's one encoder: the same escape rule as a
// request's, no HTML escape, arrays sorted, an empty optional field left out.
func TestEncodingUsesTheSharedEncoder(t *testing.T) {
	t.Parallel()
	a := Admission{ID: "a", Title: "<b>&</b> \"q\" \\   \x7f é 日", Doors: "tab\there", DependsOn: []string{"z", "b"}, Paths: []string{"y/*", "a/*"}}
	enc := EncodeAdmissions([]Admission{a})
	s := string(enc[0])
	for _, want := range []string{"<b>&</b>", "\\\"q\\\"", "q\\\" \\\\ ", "\\u2028", "\\u007f", "tab\\u0009here", " é 日", `"depends_on":["b","z"]`, `"paths":["a/*","y/*"]`} {
		if !strings.Contains(s, want) {
			t.Errorf("%s lacks %s", s, want)
		}
	}
	if want := string(card.Encode(a.tree())); s != want {
		t.Errorf("not the card encoder's bytes")
	}
	b := a
	b.Title += "x"
	if e2 := EncodeAdmissions([]Admission{b}); string(e2[0]) == s {
		t.Errorf("distinct records encode alike")
	}
	// An empty optional field is omitted, an absent list too; a set one is written.
	for _, absent := range []string{`"entry"`, `"base_commit"`} {
		if strings.Contains(s, absent) {
			t.Errorf("empty %s is written", absent)
		}
	}
	c := Admission{ID: "a"}
	if got := string(EncodeAdmissions([]Admission{c})[0]); strings.Contains(got, "depends_on") || strings.Contains(got, `"paths"`) {
		t.Errorf("an empty list is written: %s", got)
	}
	a.Entry, a.BaseCommit = "work/x", strings.Repeat("a", 40)
	if e := string(EncodeAdmissions([]Admission{a})[0]); !strings.Contains(e, `"entry":"work/x"`) || !strings.Contains(e, `"base_commit":"`+strings.Repeat("a", 40)+`"`) {
		t.Errorf("entry or base commit not written: %s", e)
	}
	// Invalid UTF-8 is never carried through: the shared encoder writes it as one
	// replacement escape (a record built by Admissions has none).
	if got := string(EncodeAdmissions([]Admission{{ID: "\xff"}})[0]); !strings.Contains(got, `"id":"`+`\`+`ufffd"`) {
		t.Errorf("invalid UTF-8 carried: %s", got)
	}
}

// A record's shared fields are a request's admission under the same names: the
// record, with the row and the review policy's identity a request adds, is an
// admission the request package accepts.
func TestRecordIsAnAdmissionOfTheRequestPackage(t *testing.T) {
	t.Parallel()
	pins := goldenPins(t)
	as, refs := fromPins(pins)
	if refs != nil {
		t.Fatal(refs.Lines())
	}
	for _, a := range as {
		var rec map[string]any
		if err := json.Unmarshal(EncodeAdmissions([]Admission{a})[0], &rec); err != nil {
			t.Fatal(err)
		}
		shared := []string{"id", "digest", "object_id", "commit", "repository", "path", "kind", "depends_on", "entry", "title"}
		adm := map[string]any{"row": "build", "policy_version": "1", "policy_digest": strings.Repeat("b", 64)}
		for _, k := range shared {
			if v, ok := rec[k]; ok {
				adm[k] = v
			}
		}
		doc, _ := json.Marshal(map[string]any{
			"schema": 1, "operation": "admit", "table": "work", "epoch": "3", "expected_table_revision": "12", "actor": "coordinator",
			"admissions": []any{adm},
		})
		if err := parseAsAdmit(doc); err != nil {
			t.Errorf("%s: the record's shared fields are not an admission: %v", a.ID, err)
		}
	}
}
