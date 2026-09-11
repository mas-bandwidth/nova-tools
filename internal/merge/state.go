/*
Package merge is nova-merge's engine: the lane's state file, its locks, its records, the
one merge predicate and the one pass that applies it. docs/SPEC-MERGE.md is normative for
every rule here and each rule is named by number where it is met.

The shape of the whole thing is one sentence: a lane is an ordered list of entries, a
verdict is a file, and the only thing that ever lands on the base is an object this tool
built, a gate proved by sha, and the remote published under a precondition.
*/
package merge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Version is the state file's shape this binary knows. It is checked FIRST, before any
// other field is read: a state file written by a newer nova-merge holds fields this code
// would mis-read, and refusing by number tells its owner which two binaries disagree.
const Version = 1

// The file names under the lane. Every one of them is under --lane and nothing this tool
// writes goes anywhere else (rule 13).
const (
	StateName    = "state.json"
	StateTmpName = "state.json.tmp" // rule 1: a FIXED name, safe because the lock admits one writer
	LogName      = "log"
	StateLock    = "state.lock"
	RunLock      = "run.lock"
	CheckoutLock = "checkout.lock"
	RepoDir      = "repo"
	ReadsDir     = "reads"
	GatesDir     = "gates"
	OutboxDir    = "outbox"
	SlotsDir     = "slots"
	StopName     = "stop"
)

// The closed set of entry states (the lane section). A pass that cannot place an entry in
// one of these prints RUN STOPPED rather than inventing a fourteenth.
const (
	StateNew            = "NEW"
	StatePending        = "PENDING"
	StateNeedsRead      = "NEEDS-READ"
	StateNeedsGate      = "NEEDS-GATE"
	StateHold           = "HOLD"
	StateRed            = "RED"
	StateWrongBase      = "WRONG-BASE"
	StateFork           = "FORK"
	StateConflicting    = "CONFLICTING"
	StateRemerged       = "REMERGED"
	StateBlocked        = "BLOCKED"
	StateMergeableGreen = "MERGEABLE-GREEN"
	StateUnknown        = "UNKNOWN"
)

// State is <lane>/state.json: the ordered entries, and the FOLD of the record files in
// the lane's branch (rule 22). The lists are the fold and the files are the truth.
type State struct {
	Version    int      `json:"version"`
	Repo       string   `json:"repo"`
	Base       string   `json:"base"`
	LaneBranch string   `json:"lane_branch"`
	PRs        []*Entry `json:"prs"`
	Branches   []*Entry `json:"branches"`
	Gates      []Gate   `json:"gates"`
}

// Entry is one pull request or one branch. The two lists are separate in the file on
// purpose: a list whose items have two shapes is two lists.
type Entry struct {
	PR        int    `json:"pr,omitempty"`
	Branch    string `json:"branch,omitempty"`
	NeedsRead string `json:"needs_read"`
	Reads     []Read `json:"reads"`
	Head      string `json:"head"`
	OID       string `json:"oid"`
	State     string `json:"state"`
	Last      string `json:"last"`
	Detail    string `json:"detail"`
	Green     int    `json:"green,omitempty"`
	Pending   int    `json:"pending,omitempty"`
	Red       int    `json:"red,omitempty"`
}

// Read is one recorded verdict: the fold of one file under <lane>/reads/<entry>/.
// Head is the sha the READER supplied with --head and never one the tool filled in.
type Read struct {
	Who     string `json:"who"`
	Verdict string `json:"verdict"`
	Note    string `json:"note"`
	At      string `json:"at"`
	Head    string `json:"head"`
	File    string `json:"file"`
}

// Gate is one recorded gate verdict: the fold of one file under <lane>/gates/<entry>/.
// All three shas are full and required -- a gate with a truncated sha is a gate that
// might match the wrong commit, and a gate with no Merge is a gate for an object nobody
// can publish (rules 18 and 21).
type Gate struct {
	PR      int    `json:"pr,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Head    string `json:"head"`
	Base    string `json:"base"`
	Merge   string `json:"merge"`
	Verdict string `json:"verdict"`
	Summary string `json:"summary"`
	At      string `json:"at"`
	Run     string `json:"run"`
	File    string `json:"file"`
}

// ID is how an entry is named on an event line and in a record path: the number of a pull
// request, or the name of a branch.
func (e *Entry) ID() string {
	if e.Branch != "" {
		return e.Branch
	}
	return strconv.Itoa(e.PR)
}

// Kind is "pr" or "branch", the first field of ADD OK and STATUS ENTRY.
func (e *Entry) Kind() string {
	if e.Branch != "" {
		return "branch"
	}
	return "pr"
}

// IsPR reports whether this entry is a pull request.
func (e *Entry) IsPR() bool { return e.Branch == "" }

// ID names the entry a gate record belongs to, the same spelling Entry.ID uses.
func (g Gate) ID() string {
	if g.Branch != "" {
		return g.Branch
	}
	return strconv.Itoa(g.PR)
}

// Entries walks the lane in ITS OWN ORDER: the pull requests, then the branches, each in
// the order they were added. The order is the coordinator's and is not shared (rule 22).
func (s *State) Entries() []*Entry {
	out := make([]*Entry, 0, len(s.PRs)+len(s.Branches))
	out = append(out, s.PRs...)
	out = append(out, s.Branches...)
	return out
}

// Find returns the entry with this id, or nil.
func (s *State) Find(id string) *Entry {
	for _, e := range s.Entries() {
		if e.ID() == id {
			return e
		}
	}
	return nil
}

// StatePath, and the rest: every path this tool touches is built from --lane.
func StatePath(lane string) string { return filepath.Join(lane, StateName) }

// Decode reads a state file. VERSION FIRST: a number this binary does not know is a
// refusal naming both numbers before any other field is read, because a field this code
// does not understand is a field it would mis-read rather than ignore. Then the whole is
// decoded STRICTLY -- an unknown field is an error, because a state file whose
// needs_read key was typed needs_reads is a state file whose owner believes a read is
// required and is wrong about it.
func Decode(raw []byte) (*State, error) {
	var probe struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("this lane's state.json does not parse as JSON: %w", err)
	}
	if probe.Version == nil {
		return nil, errors.New("this lane's state.json has no version; a lane is created by nova-merge init, which writes \"version\": 1")
	}
	if *probe.Version != Version {
		return nil, fmt.Errorf("this lane's state.json is version %d and this nova-merge knows version %d; the lane was written by another build", *probe.Version, Version)
	}
	var st State
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&st); err != nil {
		return nil, fmt.Errorf("this lane's state.json does not decode: %w", err)
	}
	if err := st.validate(); err != nil {
		return nil, err
	}
	return &st, nil
}

// validate is the half of the shape the JSON decoder cannot state: the required shas, the
// one-of-two-kinds rule, and the closed verdict vocabularies. A record missing any of
// them does not decode (rules 18, 19 and 21).
func (s *State) validate() error {
	if strings.TrimSpace(s.Repo) == "" {
		return errors.New("this lane's state.json has no repo; a lane is created by nova-merge init --repo <owner>/<name>")
	}
	if strings.TrimSpace(s.Base) == "" {
		return errors.New("this lane's state.json has no base; a lane is created by nova-merge init --base <branch>")
	}
	if strings.TrimSpace(s.LaneBranch) == "" {
		return errors.New("this lane's state.json has no lane_branch; a lane is created by nova-merge init --lane-branch <name>")
	}
	for _, e := range s.PRs {
		if e.PR <= 0 || e.Branch != "" {
			return fmt.Errorf("a pull-request entry carries pr and never branch, got %+v", *e)
		}
		if err := validReads(e.Reads); err != nil {
			return err
		}
	}
	for _, e := range s.Branches {
		if e.Branch == "" || e.PR != 0 {
			return fmt.Errorf("a branch entry carries branch and never pr, got %+v", *e)
		}
		if err := validReads(e.Reads); err != nil {
			return err
		}
	}
	for _, g := range s.Gates {
		if err := ValidGate(g); err != nil {
			return err
		}
	}
	return nil
}

func validReads(reads []Read) error {
	for _, r := range reads {
		if err := ValidRead(r); err != nil {
			return err
		}
	}
	return nil
}

// ValidRead is the shape of one read record, wherever it is read from: the file, or the
// fold in the state. head is required and is the full sha the reader supplied.
func ValidRead(r Read) error {
	if strings.TrimSpace(r.Who) == "" {
		return errors.New("a read record has no who; a verdict is recorded by a line at a keyboard: nova-merge read --who <name>")
	}
	if r.Verdict != "approve" && r.Verdict != "hold" {
		return fmt.Errorf("a read record's verdict is approve or hold, got %q", r.Verdict)
	}
	if !IsSHA(r.Head) {
		return fmt.Errorf("a read record needs head, the full 40-character sha the reader had open, got %q; nova-merge read --head <sha>", r.Head)
	}
	if strings.TrimSpace(r.At) == "" {
		return errors.New("a read record has no at; the newest record per (who, head) decides and an undated one cannot be ordered")
	}
	return nil
}

// ValidGate is the shape of one gate record: three full shas, a colour, and a summary.
// All four are refusals rather than tolerances.
func ValidGate(g Gate) error {
	if (g.PR == 0) == (g.Branch == "") {
		return fmt.Errorf("a gate record carries pr or branch and never both, got pr=%d branch=%q", g.PR, g.Branch)
	}
	if !IsSHA(g.Head) {
		return fmt.Errorf("a gate record needs head, a full 40-character sha, got %q; nova-merge gate --head <sha>", g.Head)
	}
	if !IsSHA(g.Base) {
		return fmt.Errorf("a gate record needs base, the full 40-character sha of the base it was gated against, got %q; nova-merge gate --base-sha <sha>", g.Base)
	}
	if !IsSHA(g.Merge) {
		return fmt.Errorf("a gate record needs merge, the full 40-character sha of the integration commit that was gated, got %q; nova-merge gate --merge <sha>", g.Merge)
	}
	if g.Verdict != "green" && g.Verdict != "red" {
		return fmt.Errorf("a gate record's verdict is green or red, got %q", g.Verdict)
	}
	if strings.TrimSpace(g.Summary) == "" {
		return errors.New("a gate record needs a summary; a gate with no summary is a claim with no evidence behind it")
	}
	if strings.TrimSpace(g.At) == "" {
		return errors.New("a gate record has no at; the newest record for a pair decides and an undated one cannot be ordered")
	}
	return nil
}

// IsSHA is the one spelling of a commit this tool accepts anywhere: forty lower-case
// hexadecimal characters. A truncated sha might match the wrong commit.
func IsSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Short is the twelve characters an event line prints. Twelve rather than git's seven:
// a seven-digit prefix already collides in repositories this size.
func Short(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

// Encode renders the state the way it is stored: indented, one trailing newline, so that
// a person reading the file after a storm reads a file rather than a stream.
func (s *State) Encode() ([]byte, error) {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// Load reads the lane's state. A directory with no state.json is not a lane, and the
// refusal names the one verb that makes one (rule 20).
func Load(lane string) (*State, error) {
	raw, err := readState(StatePath(lane))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotALane
	}
	if err != nil {
		return nil, err
	}
	return Decode(raw)
}

// The replace window: how long a reader waits out a rename that is in flight, and how
// often it looks again. The rename itself is microseconds; 200ms is a machine under a
// load the caller would want to hear about, and 2ms is small enough that a reader in a
// tight loop does not notice the wait at all.
const (
	replaceWindow = 200 * time.Millisecond
	replacePoll   = 2 * time.Millisecond
)

// replaceState renames tmp over path, WAITING OUT A READER'S OPEN -- the other half of
// the same Windows window, and the same bound.
//
// A reader's open is what refuses the replace there: while any handle holds state.json,
// MoveFileEx answers "Access is denied", and the write -- correct, under the lane's lock,
// with its bytes already durable in the temp file -- was LOST. Sixteen of thirty were,
// once two readers were polling the file rather than one. The lock admits one WRITER; a
// reader is not a writer, and a door held shut for the microseconds of an open is not a
// race care can lose. Past the window the refusal is the answer. A temp file that is not
// there is this tool's own bug and returns at once.
//
// On unix replaceRefusal is a compile-time false: rename never fails for a reader there.
func replaceState(tmp, path string) error {
	deadline := time.Now().Add(replaceWindow)
	for {
		err := os.Rename(tmp, path)
		if err == nil || errors.Is(err, fs.ErrNotExist) || !replaceRefusal(err) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(replacePoll)
	}
}

// readState reads the state file, WAITING OUT A REPLACE THAT IS IN FLIGHT.
//
// On unix this is one os.ReadFile and nothing else: replaceRefusal is never true there,
// because a rename is atomic for readers too. On Windows an open during MoveFileEx's
// replace can be refused -- a sharing violation, or a not-found inside the window -- and
// a reader that took that as its answer would report "this is not a lane" about a lane
// that is there, or fail a verb for a write that was landing correctly. It is bounded:
// past the window the refusal IS the answer, because a door that stays shut for 200ms is
// not a rename any more.
//
// Only the OPEN is retried. A file that is there and does not parse is never retried and
// never smoothed over -- Decode's error is the one rule 1 is checked by.
func readState(path string) ([]byte, error) {
	deadline := time.Now().Add(replaceWindow)
	for {
		raw, err := os.ReadFile(path)
		if err == nil || !replaceRefusal(err) || !time.Now().Before(deadline) {
			return raw, err
		}
		time.Sleep(replacePoll)
	}
}

// ErrNotALane is the one error every verb but init turns into the same refusal: exit 2,
// refusing to guess, with the init command in it.
var ErrNotALane = errors.New("this is not a lane")

// NotALaneRefusal is that sentence, in one place so the ten verbs cannot drift.
func NotALaneRefusal(lane string) string {
	return fmt.Sprintf("refusing to guess: this is not a lane; nova-merge init --lane %s --repo <owner>/<name> --base <branch> --lane-branch <name>", lane)
}

// SaveTo writes the state through the fixed temp name and one rename (rule 1). NOTHING
// IS WRITTEN UNLESS BOTH STATES PARSE: the old one parsed on its way in, and the new one
// is decoded back out of the bytes about to be written, so a state this binary could not
// read again never reaches the disk.
//
// The caller holds the lane's state lock. SaveTo does not take it, because the lock's
// scope is one read-modify-write and taking it here would leave the read outside.
func (s *State) SaveTo(lane string) error {
	raw, err := s.Encode()
	if err != nil {
		return err
	}
	if _, err := Decode(raw); err != nil {
		return fmt.Errorf("refusing to write a state this binary could not read back: %w", err)
	}
	tmp := filepath.Join(lane, StateTmpName)
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	if err := replaceState(tmp, StatePath(lane)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Update is the one read-modify-write of the state file, and every writer goes through
// it: take the lane's state lock, read, let the caller change it, write, release. The
// lock's scope is exactly this and never a whole pass (rule 2, lesson 68).
func Update(lane string, wait time.Duration, change func(*State) error) error {
	release, err := Lock(filepath.Join(lane, StateLock), wait)
	if err != nil {
		return err
	}
	defer release()
	st, err := Load(lane)
	if err != nil {
		return err
	}
	if err := change(st); err != nil {
		return err
	}
	return st.SaveTo(lane)
}

// Init creates a lane's state file, once. It is creation-only: a lane whose state.json
// exists is refused, and the refusal leaves the existing state byte-identical (rule 20).
// The checkout of the lane branch and the clone are the caller's, because they run git
// and this file does not.
func Init(lane, repo, base, laneBranch string) error {
	if err := os.MkdirAll(lane, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(StatePath(lane)); err == nil {
		return fmt.Errorf("%s is already a lane; init creates one and never rewrites one, so its repo, base and lane branch are what the first init wrote", StatePath(lane))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	st := &State{
		Version:    Version,
		Repo:       repo,
		Base:       base,
		LaneBranch: laneBranch,
		PRs:        []*Entry{},
		Branches:   []*Entry{},
		Gates:      []Gate{},
	}
	return st.SaveTo(lane)
}

// Appendf writes one line to the lane's log: the stamp first, the text after, append-only,
// never rotated and never filtered. A refusal is logged; a survey logs nothing, which is
// what makes it a survey.
func Appendf(lane string, now time.Time, format string, args ...any) {
	f, err := os.OpenFile(filepath.Join(lane, LogName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", now.UTC().Format(Stamp), fmt.Sprintf(format, args...))
}

// Stamp is the one instant spelling in this tool: an RFC 3339 UTC instant, sortable as a
// string, which is what orders two records by `at`.
const Stamp = "2006-01-02T15:04:05Z"
