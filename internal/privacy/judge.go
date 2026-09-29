package privacy

import (
	"errors"
	"fmt"
	"strings"
)

// Outcome is what one screen means.
type Outcome string

const (
	// UnprovenClean is the only outcome that permits an outbound action.
	// Nothing was proven; that is the strongest claim this screen can make.
	UnprovenClean Outcome = "UNPROVEN-CLEAN"
	// Flagged means the payload shares distinctive vocabulary with a private
	// entry, or carries a refusing structure shape. A mind reads it first.
	Flagged Outcome = "FLAGGED"
	// CorpusUnreadable means a declared source of private material could not
	// be read, so the corpus is not the one the configuration names.
	CorpusUnreadable Outcome = "CORPUS-UNREADABLE"
	// NoPrivateCorpus means every source loaded and no entry is marked
	// private: the payload was compared with nothing.
	NoPrivateCorpus Outcome = "NO-PRIVATE-CORPUS"
	// NothingCanEverFire means private entries exist but none has enough
	// distinctive terms to raise a flag against any payload.
	NothingCanEverFire Outcome = "NOTHING-CAN-EVER-FIRE"
	// PayloadHasNoWords means the payload was read and holds no word, so
	// there was nothing to compare: invisible characters, punctuation or
	// digits alone.
	PayloadHasNoWords Outcome = "PAYLOAD-HAS-NO-WORDS"
)

// Cleared reports whether this outcome permits an outbound action. It is an
// equality against the one good value, so an outcome added later cannot
// clear by default.
func (o Outcome) Cleared() bool { return o == UnprovenClean }

// CouldNotVerify reports whether the screen ran and verified nothing.
func (o Outcome) CouldNotVerify() bool {
	return o == CorpusUnreadable || o == NoPrivateCorpus || o == NothingCanEverFire || o == PayloadHasNoWords
}

// Flag is one reading assignment: the private entry and the shared terms.
type Flag struct {
	Source string
	Index  int
	Title  string
	Shared []string
}

// Result is everything one screen measured and everything it could not.
type Result struct {
	Outcome Outcome
	// Sources has one row per declared source, loaded or not.
	Sources []SourceLoad
	// Roots has one row per background root.
	Roots []RootLoad
	// Blocks, Private and Checkable size the corpus: every entry, the private
	// ones, and the private ones able to raise a flag at all.
	Blocks    int
	Private   int
	Checkable int
	Bounds    Bounds
	// PayloadChars and PayloadTerms say what was measured.
	PayloadChars int
	PayloadTerms int
	Flags        []Flag
	Structure    []StructureHit
	// Reason and Remedy are empty only on UnprovenClean.
	Reason   string
	Remedy   string
	Warnings []string
}

// The remedies, each spelled once.
const (
	vocabRemedy     = "a mind reads this before it goes out; it is a reading assignment, not a verdict. If it derives from the private entry, it does not ship"
	structureRemedy = "rewrite each refused specimen in general terms before it goes out"
)

// Judge measures a payload against a loaded corpus and returns one outcome.
// The payload is data: it is counted as words and matched against patterns,
// and nothing in it is interpreted.
//
// The order is fixed. A refusing structure hit is a positive finding about
// the payload and outranks every could-not-verify outcome, which is still
// spoken beside it. Then an unreadable source, then an empty private corpus,
// then a payload with no words, then vocabulary flags, then the corpus that
// can never fire.
func Judge(c Corpus, payload string) Result { return judge(c, payload, true) }

// JudgeCorpus judges the corpus alone, as the corpus verb reports it: no
// payload is measured and no structure shape is matched, so its outcome is
// UnprovenClean when a screen could reach a verdict and a could-not-verify
// outcome when every screen would come back unverified.
func JudgeCorpus(c Corpus) Result { return judge(c, "", false) }

func judge(c Corpus, payload string, withPayload bool) Result {
	rules := c.Rules
	if rules.stop == nil {
		rules = DefaultRules()
	}
	r := Result{
		Sources:      c.Sources,
		Roots:        c.Roots,
		Blocks:       len(c.Blocks),
		Private:      len(c.Private),
		PayloadChars: len(payload),
		Warnings:     append([]string(nil), c.Warnings...),
	}

	if withPayload {
		r.Structure = rules.Structure(payload)
	}
	refusals := r.StructureRefusals()
	for _, h := range r.Structure {
		if !h.Refuse {
			r.Warnings = append(r.Warnings, fmt.Sprintf("structure: %s %q is in the payload; it warns and does not refuse", h.Class, h.Specimen))
		}
	}
	refuseOnStructure := func(alsoBroken string) Result {
		if alsoBroken != "" {
			r.Warnings = append(r.Warnings, alsoBroken)
		}
		r.Outcome = Flagged
		r.Reason = fmt.Sprintf("%d refused structure specimen(s) in the payload; it is not cleared", len(refusals))
		r.Remedy = structureRemedy
		return r
	}

	if bad := c.FirstErr(); bad != nil {
		if len(refusals) > 0 {
			return refuseOnStructure(fmt.Sprintf("a declared source could not be read (%v), so the vocabulary screen did not run; the next screen refuses for the corpus until it is fixed", bad.Err))
		}
		r.Outcome = CorpusUnreadable
		r.Reason = fmt.Sprintf("declared source %s could not be read: %v", bad.Path, bad.Err)
		if n := c.unreadable(); n > 1 {
			r.Reason += fmt.Sprintf(" (and %d more; nova-privacy corpus lists each)", n-1)
		}
		r.Remedy = unreadableRemedy(c.Config, bad, rules)
		return r
	}

	if len(c.Private) == 0 {
		if len(refusals) > 0 {
			return refuseOnStructure(fmt.Sprintf("no %s entries were found, so the vocabulary screen verified nothing", rules.Marker))
		}
		r.Outcome = NoPrivateCorpus
		r.Reason = fmt.Sprintf("no %s entries in %d entries across %d sources; this screen verified nothing", rules.Marker, len(c.Blocks), len(c.Sources))
		r.Remedy = fmt.Sprintf("mark private entries with %s in the title or the opening of the entry in %s", rules.Marker, sourceList(c.Sources))
		return r
	}

	if withPayload && CountWords(payload) == 0 {
		r.Outcome = PayloadHasNoWords
		r.Reason = fmt.Sprintf("the payload holds %d bytes and no word (invisible characters, punctuation or digits alone); there was nothing to compare", len(payload))
		r.Remedy = "check that the file is the text meant to go out; a payload of words is screened"
		return r
	}

	r.Bounds = Bounds{Rare: RareBound(len(c.Blocks)), Background: BackgroundBound(c.BackgroundDocs), Docs: c.BackgroundDocs}
	if c.BackgroundDocs == 0 {
		r.Warnings = append(r.Warnings, "background: no documents were read, so rarity is measured against the private sources alone and ordinary words may flag")
	}

	df := rules.DocFrequency(c.Blocks)
	payloadTerms := rules.Terms(payload)
	r.PayloadTerms = len(payloadTerms)
	for _, p := range c.Private {
		fingerprint := rules.Distinctive(p.Text(), df, c.BackgroundFreq, r.Bounds)
		if CanEverFire(fingerprint) {
			r.Checkable++
		}
		shared := SharedRareTerms(payloadTerms, fingerprint)
		if MayStaySilent(shared) {
			continue
		}
		r.Flags = append(r.Flags, Flag{Source: p.Source, Index: p.Index, Title: p.Title, Shared: shared})
	}

	switch {
	case len(r.Flags) > 0 && len(refusals) > 0:
		r.Outcome = Flagged
		r.Reason = fmt.Sprintf("%d refused structure specimen(s) and %d private entr%s share vocabulary with the payload; it is not cleared", len(refusals), len(r.Flags), plural(len(r.Flags)))
		r.Remedy = vocabRemedy + "; and " + structureRemedy
		return r
	case len(refusals) > 0:
		return refuseOnStructure("")
	case len(r.Flags) > 0:
		r.Outcome = Flagged
		r.Reason = fmt.Sprintf("%d private entr%s share vocabulary with the payload; it is not cleared", len(r.Flags), plural(len(r.Flags)))
		r.Remedy = vocabRemedy
		return r
	}

	if r.Checkable == 0 {
		r.Outcome = NothingCanEverFire
		r.Reason = fmt.Sprintf("%d private entries loaded and none has the %d distinctive terms a flag needs, so no payload could have been flagged", r.Private, FlagThreshold)
		r.Remedy = fmt.Sprintf("give the private entries their own words, or widen the background (rarity bounds: entries df<=%d over %d, background df<=%d over %d documents)", r.Bounds.Rare, r.Blocks, r.Bounds.Background, r.Bounds.Docs)
		return r
	}

	r.Outcome = UnprovenClean
	return r
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// unreadableRemedy says what to do about one source that did not load: take
// it out of the configuration that declares it, or put the file back.
func unreadableRemedy(config string, bad *SourceLoad, rules Rules) string {
	if errors.Is(bad.Err, ErrNoEntries) {
		where := "remove it from " + config
		if bad.FromFlag || config == "" {
			where = "drop --source " + bad.Path
		}
		return fmt.Sprintf("open each entry in %s with a line starting %s, or %s", bad.Path, tokenList(rules.EntryTokens), where)
	}
	if bad.FromFlag || config == "" {
		return fmt.Sprintf("drop --source %s or restore the file", bad.Path)
	}
	return fmt.Sprintf("remove it from %s or restore the file", config)
}

func sourceList(s []SourceLoad) string {
	names := make([]string, 0, len(s))
	for _, x := range s {
		names = append(names, x.Path)
	}
	return strings.Join(names, ", ")
}

// Summary is the one-line account of what was measured, naming coverage as
// well as size.
func (res Result) Summary() string {
	return fmt.Sprintf("%d chars against %d private entries (%d able to raise a flag) drawn from %d entries, rarity weighted by %d background documents",
		res.PayloadChars, res.Private, res.Checkable, res.Blocks, res.Bounds.Docs)
}
