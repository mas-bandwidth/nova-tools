package definition

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/card"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// propertySeeds is the fixed seed list: no clock, no entropy, so a failure names
// the seed that reproduces it.
var propertySeeds = []uint64{1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 144, 233, 377, 610, 987, 1597}

// equalContent compares what Render writes and Parse reads back: everything but the
// file name, the line numbers and the digests, which describe the bytes.
func equalContent(a, b Definition) bool {
	strip := func(d Definition) Definition {
		d.File, d.Lines, d.BriefLine, d.Digest, d.BriefDigest = "", nil, 0, "", ""
		return d
	}
	return reflect.DeepEqual(strip(a), strip(b))
}

var (
	wordPool = []string{"queue", "name", "refuse", "empty", "é", "日本語", "retry", "limit", "lookup", "guide", "a-b", "x_y", "1.5", "<b>", "&", "\"q\"", "back\\slash"}
	globPool = []string{"internal/queue/name.go", "internal/queue/*.go", "docs/**", "cmd/tool/main.go", "testdata/a.txt", "**/*.md"}
	testPkgs = []string{"internal/queue", "./internal/queue", ".", "cmd/tool"}
	idChars  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
)

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

func words(r *rand.Rand, n int) string {
	w := make([]string, n)
	for i := range w {
		w[i] = pick(r, wordPool)
	}
	return strings.Join(w, " ")
}

func genID(r *rand.Rand) string {
	for {
		n := 1 + r.IntN(20)
		b := make([]byte, n)
		for i := range b {
			b[i] = idChars[r.IntN(len(idChars))]
		}
		if !card.IsReserved(string(b)) {
			return string(b)
		}
	}
}

// genDefinition builds a definition that meets every rule.
func genDefinition(r *rand.Rand, id string) Definition {
	kind := pick(r, hygiene.Kinds())
	cs, _ := Classify([]string{kind})
	d := Definition{
		Schema: SchemaV2, ID: id, Title: words(r, 1+r.IntN(5)), Kind: kind, Tier: pick(r, cardhdr.Routes),
		DoneWhen: words(r, 1+r.IntN(8)), Doors: "none", Probes: "none",
		Brief: "# " + words(r, 2) + "\n" + words(r, 6) + "\n\n" + words(r, 4) + "\n",
	}
	if r.IntN(2) == 0 {
		d.BaseCommit = strings.Repeat("0123456789abcdef", 4)[:40+24*r.IntN(2)]
		if r.IntN(2) == 0 {
			d.ContractNote = words(r, 3)
		}
	}
	if r.IntN(3) == 0 {
		d.Entry = "work/" + genID(r) + "/" + genID(r)
	}
	if r.IntN(2) == 0 {
		d.Doors = words(r, 3)
	}
	if r.IntN(2) == 0 {
		d.Probes = words(r, 3)
	}
	seen := map[string]bool{}
	for n := r.IntN(4); n > 0; n-- {
		if g := pick(r, globPool); !seen[g] {
			seen[g] = true
			d.Paths = append(d.Paths, g)
		}
	}
	for n := r.IntN(4); n > 0; n-- {
		if dep := genID(r); dep != id && !seen["dep:"+dep] {
			seen["dep:"+dep] = true
			d.DependsOn = append(d.DependsOn, dep)
		}
	}
	if cs[0] == CompletionNoPR && r.IntN(2) == 0 {
		d.Test = cardhdr.TestLine{None: true, Why: words(r, 3)}
	} else {
		d.Test = cardhdr.TestLine{Package: pick(r, testPkgs), Name: "Test" + genID(r)}
		if strings.ContainsRune(d.Test.Name, '-') {
			d.Test.Name = strings.ReplaceAll(d.Test.Name, "-", "_")
		}
		if r.IntN(3) == 0 {
			d.Test.Tags = "functional"
		}
	}
	return d
}

func TestPropertyRenderedDefinitionsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, seed := range propertySeeds {
		r := rand.New(rand.NewPCG(seed, seed*7919))
		for i := 0; i < 40; i++ {
			d := genDefinition(r, genID(r))
			text := render(d)
			defs, refs := parseL([]Source{{Name: "gen.md", Data: text}})
			if len(refs) > 0 {
				t.Fatalf("seed %d case %d: Parse refused a generated card: %v\n%s", seed, i, Lines(refs), text)
			}
			if !equalContent(d, defs[0]) {
				t.Fatalf("seed %d case %d: round trip differs:\n%+v\n%+v\n%s", seed, i, d, defs[0], text)
			}
			if _, refs := validateL(defs); len(refs) > 0 {
				t.Fatalf("seed %d case %d: Validate refused: %v", seed, i, Lines(refs))
			}
		}
	}
}

// mutate applies a few random edits to a card's bytes.
func mutate(r *rand.Rand, in []byte) []byte {
	b := append([]byte(nil), in...)
	frag := []string{"\r", "\x00", "\xff", "\xef\xbb\xbf", "KIND: read", "ID: x", ":", "\n", "SCHEMA: v3", "```", "> ", " ", "PATHS: ../", "DEPENDS-ON: a,,b", "\t", "RESULT: ", "TEST: none x", "-"}
	for n := 1 + r.IntN(4); n > 0; n-- {
		if len(b) == 0 {
			b = append(b, 'x')
		}
		at := r.IntN(len(b))
		switch r.IntN(6) {
		case 0:
			end := min(len(b), at+1+r.IntN(20))
			b = append(b[:at], b[end:]...)
		case 1:
			b = append(b[:at], append([]byte(pick(r, frag)), b[at:]...)...)
		case 2:
			b = b[:at]
		case 3:
			ls := strings.Split(string(b), "\n")
			i := r.IntN(len(ls))
			ls = append(ls[:i+1], ls[i:]...)
			b = []byte(strings.Join(ls, "\n"))
		case 4:
			ls := strings.Split(string(b), "\n")
			i, j := r.IntN(len(ls)), r.IntN(len(ls))
			ls[i], ls[j] = ls[j], ls[i]
			b = []byte(strings.Join(ls, "\n"))
		default:
			b[at] = byte(r.IntN(256))
		}
	}
	return b
}

func TestPropertyParseNeverPanicsAndAnswersOneWay(t *testing.T) {
	t.Parallel()
	corpus := [][]byte{[]byte(baseCard)}
	for _, n := range []string{"card-alpha", "card-beta", "card-gamma", "card-old"} {
		corpus = append(corpus, readTestdata(t, "cards/"+n+".md"))
	}
	accepted, refused := 0, 0
	for _, seed := range propertySeeds {
		r := rand.New(rand.NewPCG(seed, seed*104729))
		for i := 0; i < 150; i++ {
			in := mutate(r, pick(r, corpus))
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("seed %d case %d: Parse panicked: %v\ninput %q", seed, i, p, in)
					}
				}()
				defs, refs := parseL([]Source{{Name: "m.md", Data: in}, {Name: "n.md", Data: pick(r, corpus)}})
				switch {
				case len(refs) == 0 && len(defs) == 2:
					accepted++
					if _, vr := validateL(defs); len(vr) > 0 {
						for _, x := range vr {
							wellFormed(t, x)
						}
					}
					again, refs := parseL([]Source{{Name: "m.md", Data: render(defs[0])}})
					if len(refs) > 0 || !equalContent(defs[0], again[0]) {
						t.Fatalf("seed %d case %d: an accepted card does not round-trip: %v\ninput %q", seed, i, Lines(refs), in)
					}
				case len(refs) > 0 && defs == nil:
					refused++
					for _, x := range refs {
						wellFormed(t, x)
					}
				default:
					t.Fatalf("seed %d case %d: %d definitions and %d refusals", seed, i, len(defs), len(refs))
				}
			}()
		}
	}
	// The mutations must reach both answers, or the property says nothing.
	if accepted < 100 || refused < 100 {
		t.Fatalf("the mutations reached %d accepted and %d refused cases", accepted, refused)
	}
}

func TestPropertyValidateNeverPanicsOnHandBuiltInput(t *testing.T) {
	t.Parallel()
	for _, seed := range propertySeeds {
		r := rand.New(rand.NewPCG(seed, seed*31))
		for i := 0; i < 60; i++ {
			var defs []Definition
			for n := r.IntN(6); n >= 0; n-- {
				d := genDefinition(r, pick(r, []string{"a", "b", "c", "d", ""}))
				switch r.IntN(6) {
				case 0:
					d.DependsOn = append(d.DependsOn, pick(r, []string{"a", "b", "c", "zz", "", "-"}))
				case 1:
					d.Kind = pick(r, []string{"", "read", "nonsense"})
				case 2:
					d.Test = cardhdr.TestLine{}
				case 3:
					d.Lines = nil
				}
				defs = append(defs, d)
			}
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("seed %d case %d: panic: %v", seed, i, p)
					}
				}()
				_, refs := validateL(defs)
				for _, x := range refs {
					wellFormed(t, x)
				}
				_ = EncodeAdmissions([]Admission{{}, {ID: "\xff"}})
			}()
		}
	}
}

func TestGeneratedCardsAreDistinctSoTheRoundTripMeansSomething(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(propertySeeds[0], 1))
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		seen[fmt.Sprint(genDefinition(r, genID(r)).ID)] = true
	}
	if len(seen) < 30 {
		t.Fatalf("only %d distinct IDs in 40 generated cards", len(seen))
	}
}
