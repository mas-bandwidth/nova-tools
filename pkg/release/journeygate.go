package release

// THE RECOVERY JOURNEYS, IN FRONT OF THE TAG.
//
// A review of the release lane (item 5): the chaos suite (pkg/friend/chaos_functional_test.go)
// turns every check the landed code cannot yet meet into a named skip, so that a
// red-by-design test does not block the merge queue -- and `go test` calls a run
// whose subtests all skipped green. A green run was being read as proof that a
// friend whose harness closed, whose session went silent, who hit a usage limit or
// whose bus credential was revoked is found, has his cards dealt elsewhere and
// comes back. It proved none of that.
//
// So a release that PROMISES those journeys is cut only on evidence: a `go test
// -json` run of each promised journey, under a header that binds it to the revision
// being tagged, to the build installed where it ran, and to the function and schema
// versions it ran against. Each promised journey is read off that run one by one --
// never off the parent's verdict -- and anything but a pass is incomplete: owed
// (the suite's own "OWED <card>: ..." skip), skipped, failed, or not run at all.
// One skip is not a broken promise: a journey that names a platform as optional
// may say PLATFORM UNAVAILABLE for it, and the line says so rather than counting it.
//
// The way past is the dogfood gate's: --no-journey-gate --reason <why>, and every
// incomplete journey is then written into the CHANGELOG section under the reason,
// because a release that shipped a promise it had not kept says so where a person
// reads what shipped.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Journey is one promised recovery journey: a test that has to have run and passed
// at the release revision.
type Journey struct {
	// Package is the test's package, relative to the module root, such as
	// pkg/friend. A checkout without it does not ship the capability, and
	// does not promise its journeys.
	Package string
	// Test is the test's name as the source spells it, Parent/subtest, with the
	// spaces `go test` turns into underscores.
	Test string
	// Optional names the platforms whose absence is a skip rather than a broken
	// promise: a journey that needs a real harness on a windows bench may say the
	// bench did not answer.
	Optional []string
}

// PromisedJourneys are the recovery journeys a release that ships the friend
// package promises: each way a friend fails, detected within its bound, his cards
// dealt elsewhere, recovered (docs/SPEC-FRIEND.md, "Chaos").
var PromisedJourneys = []Journey{
	{Package: "pkg/friend", Test: "TestEveryFriendFailureShowsWithinItsBound/harness closed: down within 1 minute"},
	{Package: "pkg/friend", Test: "TestEveryFriendFailureShowsWithinItsBound/session silent: down within 15 minutes of bus silence"},
	{Package: "pkg/friend", Test: "TestEveryFriendFailureShowsWithinItsBound/usage limit: down until the reset, woken after"},
	{Package: "pkg/friend", Test: "TestEveryFriendFailureShowsWithinItsBound/bus credential revoked: an alarm on the first failed send"},
	{Package: "pkg/friend", Test: "TestEveryFriendFailureShowsWithinItsBound/hold: no card left on him, his cards dealt elsewhere"},
}

// EvidenceKind is what the evidence header's "evidence" field says, so a file of
// some other JSON is refused rather than read as an empty run.
const EvidenceKind = "release-journeys"

// PlatformUnavailable is how a journey's skip says its platform was not there to
// run on: `t.Skip(release.PlatformUnavailable + "windows: no windows bench answered")`.
// It counts only for a platform the journey names as Optional.
const PlatformUnavailable = "PLATFORM UNAVAILABLE "

// owedMark is how the chaos suite names a part the landed code cannot meet yet
// (owed.check in pkg/friend/chaos_functional_test.go).
const owedMark = "OWED "

// EvidenceHeader is the evidence file's first line. Every field is required: a
// journey that passed is proof about one revision, one installed build and one
// set of function and schema versions, and evidence that cannot say which is
// proof about nothing in particular.
type EvidenceHeader struct {
	Evidence  string             `json:"evidence"`
	Revision  string             `json:"revision"`
	Functions string             `json:"functions"`
	Schema    string             `json:"schema"`
	Installed []InstalledReceipt `json:"installed"`
}

// InstalledReceipt is one machine the journeys ran against and the build it had
// installed, at the revision that build was made from.
type InstalledReceipt struct {
	Machine  string `json:"machine"`
	Build    string `json:"build"`
	Revision string `json:"revision"`
}

// JourneyWaiveFlag is the way past the gate, spelled once.
const JourneyWaiveFlag = "--no-journey-gate"

// JourneyRemedy is what the refusal tells somebody to do.
const JourneyRemedy = "run each promised journey at the release revision and pass --journeys <file>, or " + JourneyWaiveFlag + " " + DogfoodReasonFlag + " <why>"

// JourneysProvenPrefix and JourneysIncompletePrefix are how the CHANGELOG section
// names what the gate found.
const (
	JourneysProvenPrefix     = "Recovery journeys proven at "
	JourneysIncompletePrefix = "Recovery journeys incomplete, gate waived: "
)

// JourneyNote is the gate said where a person meets it.
var JourneyNote = "cut runs the journey gate once the head is known: a checkout that ships pkg/friend PROMISES its recovery journeys, and the cut refuses unless --journeys names a `go test -json` run of each, " +
	"under a first line {\"evidence\":\"" + EvidenceKind + "\",\"revision\":<the sha being tagged>,\"functions\":<v>,\"schema\":<v>,\"installed\":[{\"machine\",\"build\",\"revision\"}]}. " +
	"Each journey is read on its own and only a pass proves it: owed, skipped, failed and not-run are incomplete, and a green parent over skipped subtests proves nothing. " +
	"A skip saying `" + PlatformUnavailable + "<platform>` is named, and is not incomplete only for a platform the journey names as optional. " +
	"The way past is " + JourneyWaiveFlag + " " + DogfoodReasonFlag + " <why>, and every incomplete journey is then written into the CHANGELOG section."

// errJourney says the gate refused and the refusal is already printed.
var errJourney = errors.New("journey-gate")

// journey states, as the lines carry them.
const (
	journeyProven      = "proven"
	journeyOwed        = "owed"
	journeySkipped     = "skipped"
	journeyFailed      = "failed"
	journeyNotRun      = "not-run"
	journeyUnavailable = "platform-unavailable"
)

// JourneyResult is what the evidence says about one promised journey.
type JourneyResult struct {
	Journey Journey
	State   string
	Detail  string
}

// incomplete is whether the result leaves the promise unkept.
func (r JourneyResult) incomplete() bool {
	return r.State != journeyProven && r.State != journeyUnavailable
}

// goTestName is the name `go test` reports a test under.
func goTestName(test string) string { return strings.ReplaceAll(test, " ", "_") }

// promisedJourneys is the promise a release from checkout makes: the injected
// list when a test gave one, else every PromisedJourneys entry whose package the
// checkout ships.
func promisedJourneys(deps Deps, checkout string) []Journey {
	if deps.Journeys != nil {
		return deps.Journeys
	}
	var out []Journey
	for _, j := range PromisedJourneys {
		if checkout != "" && exists(filepath.Join(checkout, filepath.FromSlash(j.Package))) {
			out = append(out, j)
		}
	}
	return out
}

// testEvent is the part of a `go test -json` line the gate reads.
type testEvent struct {
	Action  string
	Package string
	Test    string
	Output  string
}

// readJourneyEvidence reads an evidence file: the header, then `go test -json`.
// A line that is not JSON refuses: a broken record is not an absent one, and
// reading past it would answer a greener truth than the one on disk.
func readJourneyEvidence(path string) (EvidenceHeader, []testEvent, error) {
	var h EvidenceHeader
	f, err := os.Open(path)
	if err != nil {
		return h, nil, refuse("name the evidence file with --journeys <file>", "cannot read the journey evidence: %s", err)
	}
	// ignored: the evidence file was opened only for reading; the scanner's error is the one returned
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var events []testEvent
	n := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		n++
		if line == "" {
			continue
		}
		if n == 1 {
			if err := json.Unmarshal([]byte(line), &h); err != nil || h.Evidence != EvidenceKind {
				return h, nil, refuse("start the file with the evidence header",
					"%s line 1 is not a {\"evidence\":%q} header", path, EvidenceKind)
			}
			continue
		}
		var e testEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return h, nil, refuse("rerun the journeys with go test -json and keep its output whole",
				"%s line %d is not a go test -json event: %.80q", path, n, line)
		}
		events = append(events, e)
	}
	if err := sc.Err(); err != nil {
		return h, nil, refuse("name a readable evidence file", "cannot read %s: %s", path, err)
	}
	if n == 0 {
		return h, nil, refuse("start the file with the evidence header", "%s is empty", path)
	}
	return h, events, nil
}

// bindEvidence holds the header to the revision being released.
func bindEvidence(h EvidenceHeader, revision string) error {
	const remedy = "run the journeys against a build of the revision being tagged and record it in the header"
	switch {
	case h.Revision != revision:
		return refuse(remedy, "the journey evidence is for revision %s and the release is %s", field(h.Revision), revision)
	case strings.TrimSpace(h.Functions) == "":
		return refuse(remedy, "the journey evidence names no function version")
	case strings.TrimSpace(h.Schema) == "":
		return refuse(remedy, "the journey evidence names no schema version")
	case len(h.Installed) == 0:
		return refuse(remedy, "the journey evidence names no installed build")
	}
	for _, r := range h.Installed {
		if r.Revision != revision || strings.TrimSpace(r.Machine) == "" || strings.TrimSpace(r.Build) == "" {
			return refuse(remedy, "the installed build on %s is %s at revision %s, not the release revision %s",
				field(r.Machine), field(r.Build), field(r.Revision), revision)
		}
	}
	return nil
}

// judgeJourneys reads each promised journey off the events, on its own: a parent
// that passed over skipped subtests proves nothing about them.
func judgeJourneys(promised []Journey, events []testEvent) []JourneyResult {
	type seen struct {
		action string
		output []string
	}
	runs := map[string]*seen{}
	key := func(pkg, test string) string { return pkg + "\x00" + test }
	for _, e := range events {
		if e.Test == "" {
			continue
		}
		k := key(e.Package, e.Test)
		s := runs[k]
		if s == nil {
			s = &seen{}
			runs[k] = s
		}
		switch e.Action {
		case "output":
			s.output = append(s.output, strings.TrimRight(e.Output, "\n"))
		case "pass", "fail", "skip":
			s.action = e.Action
		}
	}
	var out []JourneyResult
	for _, j := range promised {
		var s *seen
		for k, v := range runs {
			pkg, test, _ := strings.Cut(k, "\x00")
			if test == goTestName(j.Test) && (pkg == j.Package || strings.HasSuffix(pkg, "/"+j.Package)) {
				s = v
				break
			}
		}
		r := JourneyResult{Journey: j}
		switch {
		case s == nil || s.action == "":
			r.State, r.Detail = journeyNotRun, "no run of it in the evidence"
		case s.action == "pass":
			r.State = journeyProven
		case s.action == "fail":
			r.State, r.Detail = journeyFailed, lastWords(s.output)
		default:
			r.State, r.Detail = skipState(j, s.output)
		}
		out = append(out, r)
	}
	return out
}

// skipState tells an owed part, an optional platform's absence and any other
// skip apart.
func skipState(j Journey, output []string) (string, string) {
	var owed []string
	for _, line := range output {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, owedMark) {
			owed = append(owed, line)
		}
	}
	if len(owed) > 0 {
		return journeyOwed, strings.Join(owed, "; ")
	}
	for _, line := range output {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, PlatformUnavailable); i >= 0 {
			platform, _, _ := strings.Cut(line[i+len(PlatformUnavailable):], ":")
			platform = strings.TrimSpace(platform)
			if slices.Contains(j.Optional, platform) {
				return journeyUnavailable, line[i:]
			}
			return journeySkipped, "skipped for platform " + platform + ", which this journey does not name as optional"
		}
	}
	return journeySkipped, lastWords(output)
}

// lastWords is the last non-empty output line, the one a skip or a failure says.
func lastWords(output []string) string {
	for i := len(output) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(output[i]); s != "" && !strings.HasPrefix(s, "---") && !strings.HasPrefix(s, "=== ") {
			return s
		}
	}
	return "-"
}

// JourneyRecord is what the gate leaves for the CHANGELOG section: the bound
// evidence when it passed, the incomplete journeys when it was waived.
type JourneyRecord struct {
	State      string
	Revision   string
	Header     EvidenceHeader
	Proven     int
	Reason     string
	Incomplete []JourneyResult
}

// journeyCheck is the gate as `cut` runs it, once the revision is known. It
// answers the record and an error, which is an ordinary refusal or errJourney
// for one whose lines are already written.
func journeyCheck(o options, deps Deps, checkout, revision string, out, errs io.Writer) (JourneyRecord, error) {
	promised := promisedJourneys(deps, checkout)
	if len(promised) == 0 {
		return JourneyRecord{State: "none-promised"}, nil
	}
	if o.noJourneys && strings.TrimSpace(o.reason) == "" {
		return JourneyRecord{}, refuse("say why: "+JourneyWaiveFlag+" "+DogfoodReasonFlag+" <why>",
			"%s ships promised journeys unproven and no reason was given", JourneyWaiveFlag)
	}
	var results []JourneyResult
	rec := JourneyRecord{Revision: revision}
	if o.journeys == "" {
		for _, j := range promised {
			results = append(results, JourneyResult{Journey: j, State: journeyNotRun, Detail: "no --journeys evidence"})
		}
	} else {
		h, events, err := readJourneyEvidence(o.journeys)
		if err != nil {
			return JourneyRecord{}, err
		}
		// A WAIVER IS FOR AN UNKEPT PROMISE, NOT FOR EVIDENCE ABOUT SOMETHING
		// ELSE: evidence from another revision refuses either way.
		if err := bindEvidence(h, revision); err != nil {
			return JourneyRecord{}, err
		}
		rec.Header = h
		results = judgeJourneys(promised, events)
	}
	for _, r := range results {
		fmt.Fprintf(out, "RELEASE CUT JOURNEY state=%s name=%s detail=%s\n", r.State, field(goTestName(r.Journey.Test)), field(r.Detail))
		if r.incomplete() {
			rec.Incomplete = append(rec.Incomplete, r)
		} else if r.State == journeyProven {
			rec.Proven++
		}
	}
	if o.noJourneys {
		rec.State, rec.Reason = "waived", strings.TrimSpace(o.reason)
		fmt.Fprintf(out, "RELEASE CUT JOURNEYS WAIVED incomplete=%d reason=%s\n", len(rec.Incomplete), field(rec.Reason))
		return rec, nil
	}
	if o.journeys == "" {
		fmt.Fprintf(errs, "RELEASE CUT REFUSED reason=journey-evidence promised=%d remedy=%q\n", len(promised), JourneyRemedy)
		return JourneyRecord{}, errJourney
	}
	if len(rec.Incomplete) > 0 {
		fmt.Fprintf(errs, "RELEASE CUT REFUSED reason=journey-gate incomplete=%d remedy=%q\n", len(rec.Incomplete), JourneyRemedy)
		return JourneyRecord{}, errJourney
	}
	rec.State = "ok"
	fmt.Fprintf(out, "RELEASE CUT JOURNEYS proven=%d promised=%d revision=%s functions=%s schema=%s installed=%d\n",
		rec.Proven, len(promised), revision, field(rec.Header.Functions), field(rec.Header.Schema), len(rec.Header.Installed))
	return rec, nil
}

// section is the record as the CHANGELOG carries it, and "" when nothing was
// promised.
func (r JourneyRecord) section() string {
	var b strings.Builder
	switch r.State {
	case "ok":
		var on []string
		for _, i := range r.Header.Installed {
			on = append(on, i.Machine+" "+i.Build)
		}
		fmt.Fprintf(&b, "%s%s: %d (functions %s, schema %s, installed %s).\n\n",
			JourneysProvenPrefix, r.Revision, r.Proven, r.Header.Functions, r.Header.Schema, strings.Join(on, ", "))
	case "waived":
		fmt.Fprintf(&b, "%s%s\n\n", JourneysIncompletePrefix, r.Reason)
		for _, i := range r.Incomplete {
			fmt.Fprintf(&b, "- %s: %s (%s)\n", i.Journey.Test, i.State, i.Detail)
		}
		if len(r.Incomplete) > 0 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
