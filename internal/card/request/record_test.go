package request

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/card"
)

func TestRecordLineAndIDAreCanonical(t *testing.T) {
	t.Parallel()
	r := rec(KindCI, DispRed, "ci:unit", g40, "run:9003")
	want := "v1 ci ci:unit red " + g40 + " sha256:" + dig + " forge run:9003"
	if r.Line() != want {
		t.Fatalf("Line() = %q, want %q", r.Line(), want)
	}
	id := r.ID()
	if len(id) != 16 || id != string(card.Sum([]byte(want)))[:16] {
		t.Fatalf("ID() = %q is not the first 16 hex of the SHA-256 of the line", id)
	}
	// Two different observations do not share an ID, one observation has one.
	other := r
	other.Artifact = "run:9004"
	if other.ID() == id || rec(KindCI, DispRed, "ci:unit", g40, "run:9003").ID() != id {
		t.Fatal("record IDs are not content addressed")
	}
	if !r.Negative() || rec(KindRead, DispAccept, "a", g40, "r:1").Negative() {
		t.Fatal("Negative")
	}
	if len(want) > MaxRecordBytes || MaxRecordBytes != 391 {
		t.Fatalf("MaxRecordBytes = %d for a line of %d", MaxRecordBytes, len(want))
	}
}

func TestParseRecordRoundTripAndRefusals(t *testing.T) {
	t.Parallel()
	for _, k := range EvidenceKinds() {
		for _, d := range DispositionsOf(k) {
			r := rec(k, d, "who:1", g64, "art/1@x+y_z.-")
			got, err := ParseRecord(r.Line())
			if err != nil || got != r {
				t.Fatalf("%s %s: %+v, %v", k, d, got, err)
			}
			if got.Line() != r.Line() || got.ID() != r.ID() {
				t.Fatalf("%s %s: line or id changed", k, d)
			}
		}
	}
	// tagged head (a non-code card's result artifact)
	tagged := rec(KindRead, DispAccept, "r", Digest(dig2).Tagged(), "review:1")
	if got, err := ParseRecord(tagged.Line()); err != nil || got != tagged {
		t.Fatalf("tagged head: %+v %v", got, err)
	}
	good := rec(KindCI, DispGreen, "ci:unit", g40, "run:9").Line()
	f := strings.Split(good, " ")
	join := func(i int, v string) string { g := append([]string(nil), f...); g[i] = v; return strings.Join(g, " ") }
	bad := map[string]string{
		"empty":            "",
		"seven tokens":     strings.Join(f[:7], " "),
		"nine tokens":      good + " x",
		"double space":     strings.Replace(good, " ", "  ", 1),
		"tab":              strings.Replace(good, " ", "\t", 1),
		"newline":          good + "\n",
		"version v2":       join(0, "v2"),
		"kind":             join(1, "vibes"),
		"kind queue ok?":   join(1, "read"), // ci disposition green is not a read disposition
		"issuer":           join(2, "a,b"),
		"disposition":      join(3, "ok"),
		"head short":       join(4, "abc"),
		"head upper":       join(4, strings.ToUpper(g40)),
		"digest untagged":  join(5, dig),
		"digest short":     join(5, "sha256:abc"),
		"verifier":         join(6, "a b"),
		"artifact unicode": join(7, "run-é"),
		"too long":         good + strings.Repeat("x", MaxRecordBytes),
	}
	for name, line := range bad {
		if _, err := ParseRecord(line); err == nil {
			t.Errorf("%s: %q accepted", name, line)
		}
	}
	// a queue record is parseable (it is stored) though never submitted
	q := Record{Kind: KindQueue, Issuer: "merge-queue", Disposition: DispReject, Head: g40, Def: Digest(dig), Verifier: "forge", Artifact: "queue:9"}
	if got, err := ParseRecord(q.Line()); err != nil || got != q {
		t.Fatalf("queue record: %+v %v", got, err)
	}
	// the refusal says which field, and quotes bounded text
	_, err := ParseRecord(join(2, "a b"))
	if err == nil {
		t.Fatal("space in the issuer accepted")
	}
	_, err = ParseRecord(join(3, strings.Repeat("z", 100000)))
	if e := err.(*Refusals); e.List[0].Cause != CauseTooLong || len(e.List[0].String()) > 1024 {
		t.Fatalf("refusal %v", e.List[0])
	}
	_, err = ParseRecord(join(3, strings.Repeat("z", 40)))
	if e := err.(*Refusals); e.List[0].Field != "disposition" || !strings.Contains(e.List[0].Found, "zzz") {
		t.Fatalf("refusal %v", e.List[0])
	}
}

func TestParseRecordProperty(t *testing.T) {
	t.Parallel()
	for _, seed := range seeds {
		rng := rand.New(rand.NewSource(seed))
		for i := 0; i < 200; i++ {
			// random bytes never panic and, when accepted, are their own canonical form
			n := rng.Intn(120)
			b := make([]byte, n)
			for j := range b {
				b[j] = " abcdefv1sha256:.-_/@+\n\t"[rng.Intn(24)]
			}
			if r, err := ParseRecord(string(b)); err == nil && r.Line() != string(b) {
				t.Fatalf("seed %d: accepted %q but its line is %q", seed, b, r.Line())
			}
		}
		// a valid record's every single-byte mutation is refused or is another record
		kind := EvidenceKinds()[rng.Intn(4)]
		ds := DispositionsOf(kind)
		r := rec(kind, ds[rng.Intn(len(ds))], fmt.Sprintf("who%d", rng.Intn(9)), g40, fmt.Sprintf("art:%d", rng.Intn(99)))
		line := r.Line()
		for j := 0; j < len(line); j++ {
			m := line[:j] + "\x01" + line[j+1:]
			if _, err := ParseRecord(m); err == nil {
				t.Fatalf("seed %d: a control byte at %d accepted in %q", seed, j, m)
			}
		}
	}
}

// Every submitted record fits the largest record bound, whatever its fields hold.
func TestRecordAtEveryBoundFits(t *testing.T) {
	t.Parallel()
	r := Record{Kind: KindLanding, Issuer: longString(MaxIdentityBytes), Disposition: DispLanded, Head: Digest(dig).Tagged(), Def: Digest(dig), Verifier: longString(MaxVerifierBytes), Artifact: longString(MaxRefBytes)}
	if len(r.Line()) > MaxRecordBytes {
		t.Fatalf("line %d > %d", len(r.Line()), MaxRecordBytes)
	}
	if _, err := ParseRecord(r.Line()); err != nil {
		t.Fatal(err)
	}
}
