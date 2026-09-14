package wake

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// probe -- is a named line here, and is this bench quiet (amended 2026-09-13).
//
// TWO FACES, ONE QUESTION: can work be handed over right now? --line asks it of
// another line; --here asks it of this bench. Neither decides WHAT TO DO: both
// measure and print, and the window decides on the numbers.
//
// CONTACT, NOT PROGRESS, NOT CAPACITY -- AND CONTACT IS NOT ELIGIBILITY. The
// last sign is contact: a receipt proves a harness ran a receipt and nothing
// about a task. contact= is the transport reading and the state word is the
// scheduling reading, and the two are printed side by side and never merged.
//
// A SILENT LINE IS PROBED ONCE, SILENCE IS NEVER A DIAGNOSIS, AND A DECLARED
// REST IS NEVER PROBED AT ALL (rule 17). The tool never writes a cause: it has
// measured a silence and nothing else, so the reason is `unknown` and the words
// "out of credits" and "asleep" appear nowhere in its output.

// The seven state words.
const (
	StatePresent      = "PRESENT"
	StateSilent       = "SILENT"
	StatePinged       = "PINGED"
	StateAnswered     = "ANSWERED"
	StateUnavailable  = "UNAVAILABLE"
	StateResting      = "RESTING"
	StateUnreconciled = "UNRECONCILED"
)

// The two durations Glenn's five-minute availability rule of 2026-09-12 gives
// the family. A duration gets a default here only when a different number per
// window would be a bug, and two windows disagreeing about when a friend is
// silent -- or about how long to wait for the answer before reassigning their
// work -- is exactly that bug.
const (
	DefaultSilentAfter  = 5 * time.Minute
	DefaultAnswerWithin = 2 * time.Minute
)

// Probe is one `probe --line` call.
type Probe struct {
	Bus            string
	Line           string
	As             string
	Remote, Branch string
	Refresh        bool
	SilentAfter    time.Duration
	AnswerWithin   time.Duration
	Interval       time.Duration
	Timeout        time.Duration
	PingDraft      string
	Rest           map[string]Rest
	CorrelateMax   int
	CorrelateBytes int
	StatePath      string
	Clock          Clock
}

// record is `probe:<name>`: the same SEVEN FIELDS in both forms, with only the
// phase word moved -- draft 5's intent had no note id to name and its ping
// dropped the anchor the answer lookup then required on every poll, so each
// form lost exactly what the other needed.
type record struct {
	sign   string
	phase  string // sending | pinged
	stamp  string
	noteID string
	anchor string
	scope  string
	read   string
}

func (r record) compose() string {
	return Compose(r.sign, r.phase, r.stamp, r.noteID, r.anchor, r.scope, r.read)
}

func parseRecord(value string) (record, bool) {
	p := Decompose(value)
	if len(p) < 6 {
		return record{}, false
	}
	p = fields(p, 7)
	return record{sign: p[0], phase: p[1], stamp: p[2], noteID: p[3], anchor: p[4], scope: p[5], read: p[6]}, true
}

// Run is the whole verb. It answers the process exit code: 0 where work can be
// handed over now, 1 where it cannot, 2 where the call could not run.
func (p *Probe) Run(ctx context.Context, st *State, stdout, stderr io.Writer) int {
	if p.SilentAfter <= 0 {
		p.SilentAfter = DefaultSilentAfter
	}
	if p.AnswerWithin <= 0 {
		p.AnswerWithin = DefaultAnswerWithin
	}
	now := p.Clock.Now()
	sign, signStamp, hasSign := p.sign(ctx)
	silent := time.Duration(0)
	contact := "NONE"
	if hasSign {
		if t, err := time.Parse(time.RFC3339, signStamp); err == nil {
			silent = now.Sub(t)
		}
		contact = "FRESH"
		if silent >= p.SilentAfter {
			contact = "STALE"
		}
	}
	head, headAt := Head(ctx, p.Bus, p.Timeout)

	line := probeLine{
		name: p.Line, contact: contact, last: signStamp, silent: silent, commit: sign,
		silentAfter: p.SilentAfter, answerWithin: p.AnswerWithin, headAt: headAt,
	}

	// A REST WINS OVER EVERY READING, and no clock leaves it: a rest ends on the
	// line's own return and the window's hand, never on a timer and never by
	// this tool. contact= is still printed beside it, because a resting line
	// that is still committing is a true thing to see and is still not an
	// assignment.
	if rest, ok := p.Rest[p.Line]; ok {
		line.state, line.rest = StateResting, rest.Note
		fmt.Fprintln(stdout, line.render())
		return 1
	}

	raw, had := st.Get("probe:" + p.Line)
	var rec record
	if had {
		var ok bool
		rec, ok = parseRecord(raw)
		if !ok {
			return refusedProbe(stderr, fmt.Sprintf("probe:%s is not a record this build can read; give this probe a state file of its own", p.Line))
		}
		// --as is required wherever a record stands: a record was made FOR a
		// caller, the correlation read asks whether a note on the lane is
		// addressed to THAT caller, and a record read without that caller is a
		// record read blind.
		if p.As == "" {
			return refusedProbe(stderr, fmt.Sprintf(
				"probe:%s stands in %s and --as is required to read it: the correlation asks whether a note on the lane is addressed to a caller, and this call names none",
				p.Line, p.StatePath))
		}
		// A RECORD IS BOUND TO ITS TRANSPORT, and a mismatch is refused rather
		// than acted on: the one thing this tool cannot do is decide which of
		// two transports a standing ping belonged to.
		if scope := ProbeScope(p.Bus, p.Remote, p.Branch, p.As, p.Line); scope != rec.scope {
			return refusedProbe(stderr, fmt.Sprintf(
				"probe:%s was made on transport %s and this call is %s; a state file carried to another checkout, lane, branch or caller cannot present another transport's ping as this one's. Give this probe a state file of its own",
				p.Line, rec.scope, scope))
		}
	}

	switch {
	case had && rec.phase == "sending":
		return p.reconcile(ctx, st, stdout, stderr, &line, rec, now)
	case had && rec.phase == "pinged":
		return p.correlate(ctx, st, stdout, stderr, &line, rec, now, sign, contact)
	case contact == "FRESH":
		// Contact is fresh and nothing says otherwise; never that the line
		// accepted work.
		line.state = StatePresent
		fmt.Fprintln(stdout, line.render())
		return 0
	case p.PingDraft != "":
		return p.ping(ctx, st, stdout, stderr, &line, sign, head, now)
	default:
		// The number is the report. NO SIGN IS THE OLDEST SIGN THERE IS, and a
		// misspelt name must never read PRESENT and must never be worth an
		// assignment.
		line.state = StateSilent
		fmt.Fprintln(stdout, line.render())
		return 1
	}
}

// correlate is the bounded read and the four states it can reach.
func (p *Probe) correlate(ctx context.Context, st *State, stdout, stderr io.Writer,
	line *probeLine, rec record, now time.Time, sign, contact string) int {
	line.pinged, line.pingedID = rec.stamp, rec.noteID
	read := p.read(ctx, rec)
	for _, note := range read.Notes {
		fmt.Fprintf(stdout, "WAKE NOTE probe %s: %s\n", oneline.Field(p.Line), oneline.Escape(note))
	}
	// A GAP IS NAMED ONCE, on the poll that first records it, and never again
	// while it stands.
	for _, at := range read.NewGaps {
		fmt.Fprintf(stdout, "WAKE NOTE probe %s: %s could not be read (%s); it is a coverage gap and no unavailability is declared\n",
			oneline.Field(p.Line), oneline.Field(at), oneline.Escape(read.Reasons[at]))
	}
	line.correlation, line.remaining, line.gaps = "partial", read.Remaining, strconv.Itoa(read.Gaps)
	if read.Complete {
		line.correlation, line.remaining = "complete", "0"
	}
	if read.Answered {
		line.state, line.correlation, line.remaining = StateAnswered, "complete", "0"
		if read.AnswerVia != "" {
			fmt.Fprintf(stdout, "WAKE NOTE probe %s: an answer names ping %s by its %s\n",
				oneline.Field(p.Line), oneline.Field(rec.noteID), oneline.Escape(read.AnswerVia))
		}
		st.Delete("probe:" + p.Line)
		p.dropArtifact()
		p.save(st, stderr)
		fmt.Fprintln(stdout, line.render())
		return 0
	}
	// The bookmark and the retained gaps are ONE FIELD AND ONE WRITE, and the
	// only write a poll that sends nothing may make: not the phase word, not the
	// sign, the stamp, the note id, the anchor or the scope, and never a record
	// created or cleared.
	if read.Bookmark != "" && read.Bookmark != rec.read {
		rec.read = read.Bookmark
		st.Set("probe:"+p.Line, rec.compose())
		p.save(st, stderr)
	}
	timedOut := false
	if t, err := time.Parse(time.RFC3339, rec.stamp); err == nil {
		timedOut = now.Sub(t) >= p.AnswerWithin
	}
	// ONLY A COMPLETE READ WITH NO ANSWER PAST --answer-within IS A TIMEOUT, and
	// a timeout is the one path to UNAVAILABLE. Nothing incomplete -- a budget
	// that stopped short, a gap, a range that could not be formed, a fetch that
	// did not happen -- is ever evidence AGAINST a friend.
	if read.Complete && timedOut {
		if contact == "FRESH" && sign != "" && sign != rec.sign {
			// The silence this ping measured ended in a sign and not in an
			// answer. The record is RETIRED and the fact survives the clearing.
			fmt.Fprintf(stdout, "WAKE NOTE probe %s: the silence this ping measured ended in a sign and not in an answer; the ping is retired\n",
				oneline.Field(p.Line))
			st.Delete("probe:" + p.Line)
			p.dropArtifact()
			p.save(st, stderr)
			line.state = StatePresent
			fmt.Fprintln(stdout, line.render())
			return 0
		}
		line.state = StateUnavailable
		fmt.Fprintln(stdout, line.render())
		return 1
	}
	if !read.Complete && read.Remaining != "0" {
		fmt.Fprintf(stdout, "WAKE NOTE probe %s: this poll's correlation read stopped short of the lane's tip, %s items remain, %d gaps stand; the correlation is partial and no unavailability is declared\n",
			oneline.Field(p.Line), oneline.Field(read.Remaining), read.Gaps)
	}
	line.state = StatePinged
	fmt.Fprintln(stdout, line.render())
	return 1
}

// read is one poll of the bounded read, over the same ref the reconcile reads:
// the lane's fetched remote ref where this call fetched, the bus checkout's
// head where it did not, and head-at= says which moment was read.
func (p *Probe) read(ctx context.Context, rec record) LaneResult {
	lane := "from-" + p.Line
	var cfg *bus.Config
	if c, err := bus.LoadConfig(p.Bus); err == nil {
		cfg = c
		if part, ok := c.Lookup(p.Line); ok && part.Lane != "" {
			lane = part.Lane
		}
	}
	ref := "HEAD"
	if p.Refresh && p.Remote != "" && p.Branch != "" {
		ref = p.Remote + "/" + p.Branch
	}
	r := &LaneRead{
		Dir: p.Bus, Ref: ref, Lane: lane, Anchor: rec.anchor, Caller: p.As,
		PingID: rec.noteID, MaxItems: p.CorrelateMax, MaxBytes: p.CorrelateBytes,
		Config: cfg, Wall: GitWall,
	}
	return r.Run(ctx, rec.read)
}

// reconcile is what a probe that finds `sending` does before anything else, and
// THE RECONCILE IS A FETCH. With no network there is no answer, and the tool
// says that instead of guessing.
func (p *Probe) reconcile(ctx context.Context, st *State, stdout, stderr io.Writer,
	line *probeLine, rec record, now time.Time) int {
	line.pinged, line.pingedID = rec.stamp, rec.noteID
	if !p.Refresh || p.Remote == "" || p.Branch == "" || !p.fetch(ctx) {
		fmt.Fprintf(stdout, "WAKE NOTE probe %s: a ping was interrupted and this call cannot reach the lane's remote ref; run again with --refresh\n",
			oneline.Field(p.Line))
		line.state, line.reconciled = StateUnreconciled, "false"
		line.correlation, line.remaining, line.gaps = "-", "-", "-"
		fmt.Fprintln(stdout, line.render())
		return 1
	}
	line.reconciled = "true"
	if p.onRemote(ctx, rec.noteID) {
		// The ping reached the bus: the record becomes pinged carrying the same
		// seven fields, and NOTHING IS SENT.
		rec.phase = "pinged"
		st.Set("probe:"+p.Line, rec.compose())
		p.save(st, stderr)
		return p.correlate(ctx, st, stdout, stderr, line, rec, now, "", "")
	}
	fmt.Fprintf(stdout, "WAKE NOTE probe %s: a ping was interrupted and note %s is not on the lane's remote ref; sending the same prepared note again\n",
		oneline.Field(p.Line), oneline.Field(rec.noteID))
	pushed := p.send(ctx, stdout, stderr, rec.noteID, 2)
	if !pushed {
		// A RESEND THAT DOES NOT LAND LEAVES NO PING: the intent is cleared, the
		// artifact is kept, and the next probe offers the same identity again.
		st.Delete("probe:" + p.Line)
		p.save(st, stderr)
		line.state = StateSilent
		fmt.Fprintln(stdout, line.render())
		return 1
	}
	rec.phase, rec.stamp = "pinged", Stamp(now)
	st.Set("probe:"+p.Line, rec.compose())
	p.save(st, stderr)
	line.state, line.pinged = StatePinged, rec.stamp
	line.correlation, line.remaining, line.gaps = "-", "-", "-"
	fmt.Fprintln(stdout, line.render())
	return 1
}

// ping is the one write this verb makes to a bus, and it is the CALLER'S NOTE,
// SENT ONCE: prepared before it is sent so its identity is fixed before
// anything can be interrupted, then offered to the bus until it lands.
func (p *Probe) ping(ctx context.Context, st *State, stdout, stderr io.Writer,
	line *probeLine, sign, head string, now time.Time) int {
	if p.As == "" {
		return refusedProbe(stderr, "--ping-draft without --as: a note is signed by the line that sends it, and this tool composes nothing")
	}
	art, id, to, err := p.prepare(ctx)
	if err != nil {
		// A prepare that fails wrote no bus state and sent nothing.
		return refusedProbe(stderr, oneLineOf(err.Error()))
	}
	// THE PREPARED NOTE'S OWN RECIPIENT IS COMPARED WITH --line BEFORE ANY SEND,
	// rather than sending a person's words to one friend and recording a
	// delivered probe against another.
	if to != "" && !strings.EqualFold(to, p.Line) {
		return refusedProbe(stderr, fmt.Sprintf(
			"the prepared note is addressed To: %s and --line names %s; nothing is sent", to, p.Line))
	}
	if err := p.saveArtifact(art); err != nil {
		return refusedProbe(stderr, oneLineOf(err.Error()))
	}
	rec := record{sign: sign, phase: "sending", stamp: Stamp(now), noteID: id,
		anchor: head, scope: ProbeScope(p.Bus, p.Remote, p.Branch, p.As, p.Line), read: "-"}
	st.Set("probe:"+p.Line, rec.compose())
	p.save(st, stderr)
	line.pingedID = id
	if !p.send(ctx, stdout, stderr, id, 1) {
		// A SEND OK pushed=false is a note in this checkout and nothing more.
		st.Delete("probe:" + p.Line)
		p.save(st, stderr)
		line.state = StateSilent
		fmt.Fprintln(stdout, line.render())
		return 1
	}
	rec.phase, rec.stamp = "pinged", Stamp(p.Clock.Now())
	st.Set("probe:"+p.Line, rec.compose())
	p.save(st, stderr)
	line.state, line.pinged = StatePinged, rec.stamp
	line.correlation, line.remaining, line.gaps = "-", "-", "-"
	fmt.Fprintln(stdout, line.render())
	return 1
}

// prepare runs nova-bus prepare, which is the bus's own operation for exactly
// this class: it uses the existing participant, recipient and draft validation,
// computes the existing deterministic note id, assigns Date once, PERFORMS NO
// NETWORK, NO GIT WRITE AND NO DELIVERY, and prints one artifact.
func (p *Probe) prepare(ctx context.Context) (artifact, id, to string, err error) {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nova-bus", "prepare", "--bus", p.Bus, "--as", p.As, "--file", p.PingDraft)
	raw, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		detail := err.Error()
		if ok := asExit(err, &ee); ok && len(ee.Stderr) > 0 {
			detail = strings.TrimSpace(string(ee.Stderr))
		}
		return "", "", "", fmt.Errorf("nova-bus prepare: %s", oneLineOf(detail))
	}
	var art struct {
		ID   string `json:"id"`
		Note string `json:"note"`
	}
	if uerr := json.Unmarshal(raw, &art); uerr != nil || art.ID == "" {
		return "", "", "", fmt.Errorf("nova-bus prepare printed an artifact this tool cannot read")
	}
	for _, l := range strings.Split(art.Note, "\n") {
		if v, ok := strings.CutPrefix(l, bus.KeyTo+":"); ok {
			to = strings.TrimSpace(v)
			break
		}
	}
	return string(raw), art.ID, to, nil
}

// send is every send this verb makes: nova-bus send --prepared of the ONE saved
// artifact, which is the bus's own retry and confirmation. A resend is the same
// note, which is why it is not a second ping.
func (p *Probe) send(ctx context.Context, stdout, stderr io.Writer, id string, attempt int) bool {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nova-bus", "send", "--bus", p.Bus,
		"--remote", p.Remote, "--branch", p.Branch, "--as", p.As, "--prepared", p.artifactPath())
	raw, err := cmd.Output()
	out := string(raw)
	pushed, commit := false, "-"
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "SEND OK") && !strings.HasPrefix(l, "SEND ALREADY") {
			continue
		}
		for _, tok := range strings.Fields(l) {
			if v, ok := strings.CutPrefix(tok, "pushed="); ok {
				pushed = v == "true"
			}
			if v, ok := strings.CutPrefix(tok, "commit="); ok {
				commit = v
			}
		}
	}
	if err != nil {
		pushed = false
	}
	fmt.Fprintf(stdout, "WAKE PING id=%s to=%s commit=%s pushed=%t attempt=%d\n",
		oneline.Field(id), oneline.Field(p.Line), oneline.Field(commit), pushed, attempt)
	return pushed
}

// fetch is the reconcile's cost: the lane's remote ref as this call has just
// fetched it, through nova-bus wait exactly as watch --refresh does.
func (p *Probe) fetch(ctx context.Context) bool {
	budget := p.Interval
	if budget <= 0 {
		budget = time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout+budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", p.Bus, "fetch", p.Remote, p.Branch)
	return cmd.Run() == nil
}

// onRemote is THE ONE EVIDENCE that a ping reached the bus: a note with the
// PREPARED ID on the lane's remote ref as this call has just fetched it.
// Nothing else counts -- not the checkout's head, not an unpushed commit, not
// another note by the same caller however recent.
func (p *Probe) onRemote(ctx context.Context, id string) bool {
	ctx, cancel := context.WithTimeout(ctx, GitWall)
	defer cancel()
	slug := bus.SlugOfID(id)
	if slug == "" {
		return false
	}
	cmd := exec.CommandContext(ctx, "git", "-C", p.Bus, "grep", "-l", "-F", "Id: "+id,
		p.Remote+"/"+p.Branch, "--", "from-"+slug+"/")
	return cmd.Run() == nil
}

func (p *Probe) artifactPath() string {
	return p.StatePath + ".probe." + p.Line + ".prepared"
}

func (p *Probe) saveArtifact(text string) error {
	path := p.artifactPath()
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".writing")
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (p *Probe) dropArtifact() { _ = os.Remove(p.artifactPath()) }

func (p *Probe) save(st *State, stderr io.Writer) {
	if err := st.Save(p.StatePath); err != nil {
		fmt.Fprintf(stderr, "WAKE POLL state: %s\n", oneline.Err(err))
	}
}

// sign is rule 2's definition, read with git log against the bus checkout,
// read-only, and nothing else is read.
func (p *Probe) sign(ctx context.Context) (sha, stamp string, ok bool) {
	l := &Lines{Bus: p.Bus, Names: []string{p.Line}, After: p.SilentAfter, Timeout: p.Timeout, Start: p.Clock.Now()}
	res, err := l.Poll(ctx, p.Clock.Now())
	if err != nil || len(res.Items) == 0 {
		return "", "", false
	}
	f := fields(Decompose(res.Items[0].Value), 3)
	if f[0] == "" {
		return "", "", false
	}
	return f[0], f[2], true
}

func refusedProbe(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "WAKE REFUSED: %s\n", oneline.Escape(what))
	return 2
}

// probeLine is the one WAKE PROBE line, whose contact= and state word are two
// fields and are never merged.
type probeLine struct {
	name                      string
	state                     string
	contact                   string
	last                      string
	silent                    time.Duration
	commit                    string
	pinged, pingedID          string
	rest                      string
	reconciled                string
	correlation               string
	remaining, gaps           string
	silentAfter, answerWithin time.Duration
	headAt                    string
}

func (l probeLine) render() string {
	dashOr := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	reconciled := l.reconciled
	if reconciled == "" {
		reconciled = "false"
	}
	return fmt.Sprintf("WAKE PROBE name=%s state=%s contact=%s last=%s silent=%s commit=%s pinged=%s pinged-id=%s rest=%s reconciled=%s correlation=%s remaining=%s gaps=%s silent-after=%s answer-within=%s head-at=%s",
		oneline.Field(l.name), oneline.Field(l.state), oneline.Field(l.contact),
		oneline.Field(dashOr(l.last)), oneline.Field(DurShort(l.silent)), oneline.Field(dashOr(l.commit)),
		oneline.Field(dashOr(l.pinged)), oneline.Field(dashOr(l.pingedID)), oneline.Field(dashOr(l.rest)),
		oneline.Field(reconciled), oneline.Field(dashOr(l.correlation)),
		oneline.Field(dashOr(l.remaining)), oneline.Field(dashOr(l.gaps)),
		oneline.Field(DurShort(l.silentAfter)), oneline.Field(DurShort(l.answerWithin)),
		oneline.Field(dashOr(l.headAt)))
}

// DurShort is Dur with the zero tail trimmed, so the family's five minutes
// print as the five minutes they are named as rather than as 5m0s.
func DurShort(d time.Duration) string {
	s := Dur(d)
	if strings.HasSuffix(s, "m0s") && len(s) > len("m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") && len(s) > len("h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
