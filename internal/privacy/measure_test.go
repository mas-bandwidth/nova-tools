package privacy_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/privacy"
)

// The normalisation measurement. An invented corpus: 40 private entries and
// 40 ordinary ones, 300 background documents of ordinary prose, and 60
// payloads: 20 quote a private entry's rare words as written, 20 carry them
// in another form (plural, possessive, accents, a hyphen or invisible
// character inside, fullwidth letters, a -- dash join, spaced where the
// entry hyphenates), and 20 are innocent. Innocent payloads are ordinary
// prose with plurals, possessives, hyphenated compounds and accented
// borrowings, and half of them also mention one or two of a private entry's
// rare words, as a person naming a project might. Every word is invented or
// ordinary; no text is real.

// commonVocabulary is ordinary English, most frequent first, so the tail is
// rare enough in the background to be distinctive, as real writing is.
var commonVocabulary = strings.Fields(`morning evening garden letter window kitchen table chair
coffee bread water river bridge road station train platform ticket village market
basket apple orange lemon honey butter cheese kettle teapot cupboard shelf drawer
blanket pillow lantern candle mirror picture frame painting pencil paper notebook
journal diary calendar season winter spring summer autumn weather cloud rain storm
thunder sunshine shadow meadow forest valley mountain harbour island beach shore
wave tide boat sail anchor rope net fisher cottage chimney fireplace ladder roof
fence gate hedge path stone pebble sand shell feather nest robin sparrow owl heron
badger rabbit squirrel hedgehog pony stable barn field wheat barley orchard cider
walker traveller neighbour friend cousin uncle grandmother teacher doctor baker
farmer carpenter tailor painter singer dancer stranger visitor guest supper dinner
breakfast picnic holiday journey voyage parcel envelope stamp postcard message
answer question story chapter poem song melody music piano violin drum whistle
festival parade lantern ribbon button thread needle cotton linen wool sweater
scarf glove boot umbrella suitcase pocket wallet coin receipt bargain shopkeeper
library museum gallery theatre concert school lesson homework puzzle riddle game
marble kite balloon bicycle wheel engine motor tractor wagon cart harness saddle
blossom petal thorn ivy moss fern acorn chestnut walnut hazel willow birch maple
granite marble slate copper silver bronze iron timber plank hammer chisel saw
spade rake trowel bucket barrel crate sack flour sugar salt pepper vinegar pickle
jam biscuit pastry pudding soup stew roast pie tart custard lemonade cocoa tea
quilt rug curtain carpet lamp clock bell whistle signal beacon lighthouse compass
atlas globe telescope magnet crystal fossil ember cinder smoke steam frost dew
puddle stream brook pond lake marsh reed rush bank ferry quay jetty pier wharf`)

// measureRand is a small fixed generator, so the corpus is the same on every
// platform and every Go version.
type measureRand uint64

func (r *measureRand) next() uint64 {
	*r ^= *r << 13
	*r ^= *r >> 7
	*r ^= *r << 17
	return uint64(*r)
}

func (r *measureRand) intn(n int) int { return int(r.next() % uint64(n)) }

// zipf picks a common word, the early ones far more often.
func (r *measureRand) zipf() string {
	n := len(commonVocabulary)
	a, b := r.intn(n), r.intn(n)
	if b < a {
		a = b
	}
	c := r.intn(n)
	if c < a {
		a = c
	}
	return commonVocabulary[a]
}

func (r *measureRand) prose(words int) string {
	out := make([]string, words)
	for i := range out {
		w := r.zipf()
		switch r.intn(10) {
		case 0:
			w += "s"
		case 1:
			w += "'s"
		}
		out[i] = w
	}
	return strings.Join(out, " ")
}

// invented builds a word no dictionary holds, from syllables with no e, so
// no suffix rule touches it by accident; end is appended.
func (r *measureRand) invented(seen map[string]bool, end string) string {
	cons, vows := "bdfgklmnprtvz", "aiou"
	for {
		var b strings.Builder
		for s := 0; s < 3; s++ {
			b.WriteByte(cons[r.intn(len(cons))])
			b.WriteByte(vows[r.intn(len(vows))])
		}
		b.WriteByte(cons[r.intn(len(cons))])
		w := b.String() + end
		if !seen[w] {
			seen[w] = true
			return w
		}
	}
}

type measured struct {
	group, form, text string
}

var paraphraseForms = []string{
	"plural -s", "plural -es", "possessive 's", "accents", "hyphen inside",
	"-- dash join", "zero-width space inside", "soft hyphen inside", "fullwidth letters", "spaced where the entry hyphenates",
}

func accent(w string) string {
	return strings.NewReplacer("a", "á", "o", "ö", "u", "ú", "i", "ï").Replace(w)
}

func fullwidth(w string) string {
	var b strings.Builder
	for _, c := range w {
		if c >= 'a' && c <= 'z' {
			c = c - 'a' + 'ａ'
		}
		b.WriteRune(c)
	}
	return b.String()
}

func insertMiddle(w, s string) string { return w[:len(w)/2] + s + w[len(w)/2:] }

// buildMeasurement writes the corpus under dir and returns its spec and the
// payloads.
func buildMeasurement(t *testing.T) (privacy.Spec, []measured) {
	t.Helper()
	f := &fixture{root: t.TempDir()}
	rng := measureRand(0x9e3779b97f4a7c15)
	seen := map[string]bool{}
	rare := make([][]string, 40)
	var src strings.Builder
	src.WriteString("Ideas, one per heading.\n\n")
	for i := range rare {
		end := ""
		if i >= 20 && paraphraseForms[(i-20)%10] == "plural -es" {
			end = "us"
		}
		for k := 0; k < 6; k++ {
			e := end
			if k >= 4 {
				e = ""
			}
			rare[i] = append(rare[i], rng.invented(seen, e))
		}
		r := rare[i]
		body := fmt.Sprintf("%s %s %s %s the %s-%s one", r[0], r[1], r[2], r[3], r[4], r[5])
		if i >= 20 && paraphraseForms[(i-20)%10] == "spaced where the entry hyphenates" {
			body = fmt.Sprintf("%s-%s and %s-%s, with %s %s", r[0], r[1], r[2], r[3], r[4], r[5])
		}
		fmt.Fprintf(&src, "## Plan %d (private)\n%s, %s\n\n", i, body, rng.prose(12))
		fmt.Fprintf(&src, "## Note %d\n%s\n\n", i, rng.prose(16))
	}
	f.write(t, "private/ideas.md", src.String())
	for d := 0; d < 300; d++ {
		f.write(t, fmt.Sprintf("journal/%03d.md", d), rng.prose(120)+"\n")
	}
	spec := rootSpec(t, f, "source private/ideas.md\nbackground flat *.md journal\n")

	var out []measured
	for i := 0; i < 20; i++ {
		r := rare[i]
		out = append(out, measured{"quoted", "as written", fmt.Sprintf("%s, and the %s with the %s by the %s %s, %s.", rng.prose(6), r[0], r[1], r[2], r[3], rng.prose(6))})
	}
	for i := 20; i < 40; i++ {
		r := rare[i][:4]
		form := paraphraseForms[(i-20)%10]
		w := make([]string, 4)
		for k, x := range r {
			switch form {
			case "plural -s":
				w[k] = x + "s"
			case "plural -es":
				w[k] = x + "es"
			case "possessive 's":
				w[k] = x + "'s"
			case "accents":
				w[k] = accent(x)
			case "hyphen inside":
				w[k] = insertMiddle(x, "-")
			case "zero-width space inside":
				w[k] = insertMiddle(x, "\u200b")
			case "soft hyphen inside":
				w[k] = insertMiddle(x, "\u00ad")
			case "fullwidth letters":
				w[k] = fullwidth(x)
			default:
				w[k] = x
			}
		}
		mention := fmt.Sprintf("the %s with the %s by the %s %s", w[0], w[1], w[2], w[3])
		if form == "-- dash join" {
			mention = "the " + strings.Join(w, "--")
		}
		out = append(out, measured{"paraphrased", form, fmt.Sprintf("%s, and %s, %s.", rng.prose(6), mention, rng.prose(6))})
	}
	innocentExtras := []string{
		"the well-known long-term plan", "a café on the corner, naïve and warm",
		"the neighbour's cottages and their gardens", "a résumé of the week’s errands",
		"second-hand bicycles and hand-made quilts", "the teachers' lessons and the doctors' rounds",
		"a self-evident, old-fashioned supper", "the harbour-master's lanterns", "the ferries, the quays and the wharves",
		"up-to-date timetables for the trains",
	}
	for k := 0; k < 20; k++ {
		text := rng.prose(10) + ", " + innocentExtras[k%10] + ", " + rng.prose(10)
		form := "ordinary prose"
		if k >= 10 {
			r := rare[k-10]
			switch {
			case k < 14:
				form = "two rare words, inflected"
				text += fmt.Sprintf(", as the %ss said to the %s's cousin", r[0], r[1])
			case k < 17:
				form = "one private hyphenated compound"
				text += fmt.Sprintf(", at the %s-%s meeting", r[4], r[5])
			default:
				form = "two rare words, accented and fullwidth"
				text += fmt.Sprintf(", near %s and %s", accent(r[2]), fullwidth(r[3]))
			}
		}
		out = append(out, measured{"innocent", form, text + "."})
	}
	return spec, out
}

// measure screens every payload and counts the flags per group and form.
func measure(t *testing.T) (map[string]int, map[string]int, []string) {
	t.Helper()
	spec, payloads := buildMeasurement(t)
	c := privacy.Load(spec)
	if len(c.Private) != 40 || c.BackgroundDocs != 300 {
		t.Fatalf("the corpus is %d private entries and %d documents, want 40 and 300", len(c.Private), c.BackgroundDocs)
	}
	flags, byForm := map[string]int{}, map[string]int{}
	var flagged []string
	for _, p := range payloads {
		r := privacy.Judge(c, p.text)
		if r.Outcome == privacy.Flagged {
			flags[p.group]++
			byForm[p.group+"/"+p.form]++
			flagged = append(flagged, p.group+"/"+p.form)
		} else if r.Outcome != privacy.UnprovenClean {
			t.Fatalf("%s/%s: outcome %s", p.group, p.form, r.Outcome)
		}
	}
	return flags, byForm, flagged
}

// The measurement pins the alarm. Before words were normalised: quoted 20,
// paraphrased 0, innocent 0 flagged. After: quoted 20, paraphrased 20,
// innocent 3, and the three are the payloads that name a private hyphenated
// compound whole: one compound is three keys (the whole and its two parts),
// which reaches the threshold alone.
func TestTheNormalisationMeasurement(t *testing.T) {
	t.Parallel()
	flags, byForm, _ := measure(t)
	t.Logf("flags of 20: quoted %d, paraphrased %d, innocent %d", flags["quoted"], flags["paraphrased"], flags["innocent"])
	for k, v := range byForm {
		t.Logf("  %-60s %d", k, v)
	}
	if flags["quoted"] != 20 {
		t.Errorf("quoted leaks flagged %d of 20", flags["quoted"])
	}
	if flags["paraphrased"] != 20 {
		t.Errorf("paraphrased leaks flagged %d of 20", flags["paraphrased"])
	}
	if flags["innocent"] > 3 || byForm["innocent/one private hyphenated compound"] != flags["innocent"] {
		t.Errorf("innocent payloads flagged %d of 20, %d of them by a private compound", flags["innocent"], byForm["innocent/one private hyphenated compound"])
	}
}
