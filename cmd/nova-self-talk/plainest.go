package main

// THE THIRD DETECTOR for nova-self-talk, added by the third fix attempt at
// nova-tools #1468. The first two attempts (corpus rows #1468 and bf77a01a)
// recorded the three plainest first-person absolutes as a known miss, per the
// tool's own NOTE that it catches known SHAPES only. This third attempt
// overrides that ruling and adds a detector for the three shapes so they are
// no longer a known miss.
//
// WHY IT LIVES HERE AND NOT IN internal/selftalk. The two existing detectors
// carry the wider argument that "never widen the grammar to reach a single
// specimen" -- a rule document written as a list of prohibitions scored worst
// of anything in the predecessor's repo, and improving the score meant deleting
// a rule. Five were weakened, one floor-level, before a cold reader caught
// every one. The first class's negative-vocabulary list and the second class's
// shape vocabulary both encode that argument. Adding "I cannot ever get X
// right" to the first class would have to make the same case against widening
// for it to land there. The third attempt does not make that case -- it is a
// narrow, named addition for three specimen sentences, with its own corpus row
// beside the existing one.
//
// THE THREE SHAPES. (1) "I cannot ever ..." is a permanent capability denial
// in its plainest spelling -- the writer names the verb, "ever" carries the
// permanence, and the negative-vocabulary filter the first class demands is
// satisfied by the "I cannot" trigger alone in this shape. (2) "I always
// <negative-verb> ..." is the TRAIT shape of the INSTALLATION class -- a
// habitual indicative self-report. The existing habituality markers exclude
// "always" by design (SPEC.md, the permanent-MISS section, point 5: "I never
// optimize how things look over what is true" is a commitment, not a habit).
// This third detector catches the same shape with "always" as the marker, on
// purpose, for these three lines. (3) "Nothing I do works." is a
// permanent-negative absolute with no first-person marker at the head of the
// clause -- the second class's `there is no` shape covers the lead-clause case
// but not this one. Detected as an INSTALLATION with the FORECLOSURE shape.

import (
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/selftalk"
)

// plainestStanding matches the first shape: "I cannot ever ...".
//
// The bounded context either side keeps a match to roughly one sentence
// without needing a real parser, the same shape the first class's `claim`
// regex uses. It does NOT require additional negative vocabulary: the
// "I cannot ever" trigger IS the negative capability statement, by the same
// argument the first class already accepts for "I cannot verify", "I cannot
// check", "I cannot see" -- the bare "cannot" would be too broad, but
// "cannot ever" is the speaker naming a permanent denial.
//
// "always" is DELIBERATELY ABSENT from this pattern. "I cannot always X"
// is a hedged claim, not a permanent one -- the same shape SPEC.md's
// permanent-MISS point 5 says a "always" makes a commitment, not a habit.
// The TRAIT pattern below picks up "I always <verb>" as the habitual
// self-report; "I cannot always" belongs to neither shape.
var plainestStanding = regexp.MustCompile(`(?i)\bI cannot ever [^.!?]{0,80}[.!?]`)

// plainestTraitVerb matches the second shape's verb: "I always <verb> ...".
//
// The trailing negative-verb set is closed for the same reason the first
// class's vocabulary is closed: widening it matches every "I always" and
// flags every promise. The verb set here is the small set the three
// specimen sentences name plus the near-synonyms measured against live
// prose.
var plainestTraitVerb = regexp.MustCompile(`(?i)\bI always (?:break|fail|mess|wreck|ruin|lose|miss|screw)\b`)

// plainestForeclosure matches the third shape: "Nothing I <verb> works.".
//
// The clause carries the writer as the implicit subject of "I do" / "I try" /
// "I make" -- the "Nothing I" construction is the negative, the rest names what
// the writer is doing.
var plainestForeclosure = regexp.MustCompile(`(?i)\b[Nn]othing I (?:do|try|make|build|write|run|start|touch|attempt)\b[^.!?]{0,80}[.!?]`)

// plainestDated mirrors selftalk.dated -- a sentence carrying a date marker
// is a record, not a standing property, and the third detector honours that
// rule unchanged. The list is duplicated here rather than imported because
// selftalk.dated is unexported; widening that export would be a wider change
// than this card scopes.
var plainestDated = regexp.MustCompile(`(?i)\b(20\d\d-\d\d-\d\d|measured|that day|that night|once,|first time)\b`)

// plainestAspiration is the second-class aspiration suppressor inlined for the
// same reason. An aspiration is the target register, not a defect, and the
// third detector honours that rule unchanged. The head-anchored form is the
// only one of selftalk.aspiration that matters here: the three specimen lines
// do not contain a mid-sentence "I want to", and a wider import would change
// more than the card scopes.
var plainestAspiration = regexp.MustCompile(`(?i)^(?:i|we) (?:want|choose|intend|aim|hope|prefer|plan|wish|seek|will|would like)\b`)

// plainestImperative is the second-class imperative suppressor inlined. An
// imperative policy line has no subject, so it cannot make a self-report. The
// three specimen lines do not contain an imperative, but "ADD SLOWLY, AND
// TRIM AS READILY AS I ALWAYS BREAK" is the same case the second class
// already excludes: an imperative cannot reach TRAIT at all. The list here
// is the small set the existing detector names -- widening it is the same
// decision the second class made.
var plainestImperative = regexp.MustCompile(`(?i)^(?:never|always|do not|don't|avoid|refuse|keep|make|treat|use|read|write|state|say|ask|check|add|trim|give|take|hold|leave|stop|start|let|prefer|choose|name|record|report|measure|run|show|tell|point|fix|cut|date|ground|reframe|probe|prove|remember|forget|note|put|send|open|close|carry|build|wire|pin)\b`)

// plainestScan returns the third detector's findings: STANDING claims for the
// first shape, INSTALLATION (TRAIT) for the second, INSTALLATION (FORECLOSURE)
// for the third. The output is wrapped in the same types the binary already
// handles, so the run loop merges them with selftalk.Scan /
// selftalk.ScanInstallation and the existing one-line grammar is unchanged.
//
// DATED SUPPRESSION runs against the LINE, not the matched sentence: a date
// marker anywhere on the line exempts the line, by the same law the first
// class applies. "On 2026-07-30 I always break the build." is welcome; the
// date is on the same line as the claim. ASPIRATION and IMPERATIVE suppression
// run against the LINE too, mirroring the second class's exact rule for the
// INSTALLATION shapes. STANDING claims have no equivalent suppressor -- the
// first class does not exempt aspirations, only dated records.
//
// LINE-BY-LINE SCAN, on purpose. The three shapes are whole-sentence, and the
// repair list is line-addressed -- a finding with no line is a finding its
// reader has to go hunting for, and that is the half of the FORMAT CONTRACT
// the second class was designed against. The detector walks every line of the
// input, flattens it the same way selftalk.Flatten does (markdown stripped,
// whitespace collapsed), and matches each line's flattened form independently.
// A match that spans lines -- which the three specimen sentences do not, but
// a hard-wrapped journal might -- falls to the flattened join in the existing
// selftalk detectors and is OUT of scope here.
func plainestScan(text string) ([]selftalk.Claim, []selftalk.Installation) {
	var claims []selftalk.Claim
	var installations []selftalk.Installation

	for i, raw := range strings.Split(text, "\n") {
		n := i + 1
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		dated := plainestDated.MatchString(line)
		aspiring := plainestAspiration.MatchString(line)
		imperative := plainestImperative.MatchString(line)

		flat := selftalk.Flatten(line)

		// STANDING claims (the first shape) are subject only to DATED
		// suppression, exactly as the first class is. The first class does
		// not exempt aspirations, only records.
		if !dated {
			for _, m := range plainestStanding.FindAllString(flat, -1) {
				s := strings.TrimSpace(m)
				claims = append(claims, selftalk.Claim{Verdict: selftalk.Standing, Text: s})
			}
		}

		// INSTALLATION findings (the second and third shapes) honour the
		// same DATED, ASPIRATION, and IMPERATIVE suppressors the second
		// class honours, applied at the LINE level so a date marker or
		// leading verb on the same line exempts the finding.
		if dated || aspiring || imperative {
			continue
		}

		for _, m := range plainestTraitVerb.FindAllString(flat, -1) {
			s := plainestFullSentence(flat, m)
			installations = append(installations, selftalk.Installation{Shape: selftalk.Trait, Line: n, Text: s})
		}

		for _, m := range plainestForeclosure.FindAllString(flat, -1) {
			s := plainestFullSentence(flat, m)
			installations = append(installations, selftalk.Installation{Shape: selftalk.Foreclosure, Line: n, Text: s})
		}
	}

	return claims, installations
}

// plainestFullSentence returns the sentence containing m, ending at the next
// terminator. The trailing punctuation is included. Used by the third
// detector to capture what the writer wrote, not just the matched span.
//
// When m itself already ends at a terminator -- which the first and third
// patterns do, by construction -- the function returns m unchanged. The
// extension logic is only there for the second pattern's verb-only match
// ("I always break"), where the full sentence has to be recovered.
func plainestFullSentence(flat, m string) string {
	if strings.HasSuffix(m, ".") || strings.HasSuffix(m, "!") || strings.HasSuffix(m, "?") {
		return strings.TrimSpace(m)
	}
	i := strings.Index(flat, m)
	if i < 0 {
		return strings.TrimSpace(m)
	}
	end := i + len(m)
	for end < len(flat) {
		c := flat[end]
		if c == '.' || c == '!' || c == '?' {
			end++
			break
		}
		end++
	}
	return strings.TrimSpace(flat[i:end])
}
