// Package adoption implements the evidence-based adoption ledger for tools and verbs
// across the fleet.
//
// LENS (Glenn Fiedler, 2026-09-20; issue #2068, Sprint Row 14):
// "Everything that you have done as coordinator in the past few days, is something
// that Stella needs to be able to do easily."
//
// THE CORE INVARIANT: "INSTALLED IS NOT ADOPTED".
// A binary installed on disk (DISK_SHA) does not mean the running daemon or service
// has adopted it. The running daemon may still be executing an older binary in memory
// (RUNNING_SHA), or may not be running at all. Calling a row 90% or adopted when the
// fleet is still executing the old binary led to regressions twice (#1885, #1925).
//
// ADOPTION IS ONLY VERIFIED WHEN:
// The running daemon reports its live commit SHA via probe or heartbeat matching
// the disk commit SHA (RUNNING_SHA == DISK_SHA).
//
// STAGES COMPUTED FROM EVIDENCE, NEVER TYPED:
//   0%  StageNone:      No PR open yet. Next act: open PR.
//  25%  StagePROpen:    PR open. Next act: green CI and clean hygiene.
//  50%  StageGreen:     CI green and clean. Next act: required readers approve live head.
//  75%  StageApproved:  Every required read approves live head. Next act: land on dev.
//  90%  StageInstalled: Merged to dev AND installed on disk (DISK_SHA) on every UP bench.
// 100%  StageInUse:     IN USE: running daemon reports live SHA via probe/heartbeat
//                       matching DISK_SHA (RUNNING_SHA == DISK_SHA), a named receipt
//                       from a real run on the fleet, and no operational HOLD.
package adoption

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Invariant banner: installed is not adopted.
const InvariantInstalledNotAdopted = "installed is not adopted"

// Stage represents the computed adoption percentage stage.
type Stage int

const (
	StageNone       Stage = 0   // Not started / no PR
	StagePROpen     Stage = 25  // PR open
	StageGreen      Stage = 50  // Green and clean
	StageApproved   Stage = 75  // Every required read approves the live head
	StageInstalled  Stage = 90  // On dev and installed on disk (DISK_SHA)
	StageInUse      Stage = 100 // Adopted and in use (RUNNING_SHA == DISK_SHA, receipt, no holds)
)

// Percent returns the stage percentage as an integer.
func (s Stage) Percent() int { return int(s) }

// String returns the stage percentage formatted with %.
func (s Stage) String() string {
	return fmt.Sprintf("%d%%", int(s))
}

// EvidenceKind describes the channel that reported the running daemon's commit SHA.
type EvidenceKind string

const (
	// EvidenceProbe indicates live commit SHA reported via active probe query.
	EvidenceProbe EvidenceKind = "probe"
	// EvidenceHeartbeat indicates live commit SHA reported via running daemon heartbeat.
	EvidenceHeartbeat EvidenceKind = "heartbeat"
)

// DefaultHeartbeatStaleBound is the maximum duration before a heartbeat is considered stale.
const DefaultHeartbeatStaleBound = 5 * time.Minute

// Typed sentinel errors for adoption verification.
var (
	ErrInstalledNotAdopted   = errors.New("installed is not adopted: running daemon SHA does not match disk SHA")
	ErrDaemonNotRunning      = errors.New("running daemon not reporting live SHA: probe/heartbeat missing")
	ErrMissingDiskSHA        = errors.New("missing DISK_SHA on bench")
	ErrTargetMismatch        = errors.New("disk SHA does not match target SHA")
	ErrHeartbeatStale        = errors.New("heartbeat is stale")
	ErrOperationalHold       = errors.New("adoption held: operational hold active")
	ErrMissingReceipt        = errors.New("adoption unproven: missing execution receipt")
	ErrUnrecognizedEvidence  = errors.New("unrecognized evidence kind (must be probe or heartbeat)")
	ErrMissingBench          = errors.New("missing bench name in daemon evidence")
)

// DaemonEvidence is the live evidence reported by or collected from a bench daemon.
type DaemonEvidence struct {
	Bench      string       // Host machine / bench name (e.g., "batman", "superman")
	Daemon     string       // Daemon, service, or tool name (e.g., "nova-swarm", "nova-pulse")
	DiskSHA    string       // DISK_SHA: commit SHA of the binary installed on disk
	RunningSHA string       // RUNNING_SHA: live commit SHA reported by running process
	Kind       EvidenceKind // EvidenceProbe ("probe") or EvidenceHeartbeat ("heartbeat")
	ReportedAt time.Time    // Timestamp when the evidence was collected
	Hold       string       // Operational hold reason if any, or ""
	Receipt    string       // Named receipt from a real run on the fleet, or ""
}

// IsStale reports whether the evidence is a heartbeat older than the given bound.
func (e DaemonEvidence) IsStale(now time.Time, bound time.Duration) bool {
	if e.Kind != EvidenceHeartbeat || e.ReportedAt.IsZero() || bound <= 0 {
		return false
	}
	return now.Sub(e.ReportedAt) > bound
}

// VerifyDaemonAdoption verifies whether a daemon's live evidence confirms adoption.
//
// Under the core invariant 'installed is not adopted':
// - Evidence must come from a recognized live channel ("probe" or "heartbeat").
// - The installed binary must have a non-empty DISK_SHA.
// - If targetSHA is specified, DISK_SHA must match targetSHA.
// - The daemon MUST report a non-empty RUNNING_SHA.
// - RUNNING_SHA MUST match DISK_SHA exactly.
//
// Any mismatch between DISK_SHA and RUNNING_SHA returns ErrInstalledNotAdopted.
func VerifyDaemonAdoption(evidence DaemonEvidence, targetSHA string) error {
	if evidence.Bench == "" {
		return ErrMissingBench
	}
	if evidence.Kind != EvidenceProbe && evidence.Kind != EvidenceHeartbeat {
		return fmt.Errorf("%w: %q", ErrUnrecognizedEvidence, evidence.Kind)
	}
	if evidence.DiskSHA == "" {
		return ErrMissingDiskSHA
	}
	if targetSHA != "" && !shaMatches(evidence.DiskSHA, targetSHA) {
		return fmt.Errorf("%w: disk %s != target %s", ErrTargetMismatch, ShortSHA(evidence.DiskSHA), ShortSHA(targetSHA))
	}
	if evidence.RunningSHA == "" {
		return fmt.Errorf("%w on %s", ErrDaemonNotRunning, evidence.Bench)
	}
	if !shaMatches(evidence.RunningSHA, evidence.DiskSHA) {
		return fmt.Errorf("%w on %s: running=%s disk=%s",
			ErrInstalledNotAdopted, evidence.Bench, ShortSHA(evidence.RunningSHA), ShortSHA(evidence.DiskSHA))
	}
	return nil
}

// IsAdopted reports whether the daemon evidence verifies adoption against targetSHA.
func (e DaemonEvidence) IsAdopted(targetSHA string) bool {
	return VerifyDaemonAdoption(e, targetSHA) == nil
}

// Record holds the adoption facts for one tool or verb.
type Record struct {
	Tool      string                    // e.g. "nova-pulse"
	Verb      string                    // e.g. "adoption", or "-" if bare tool
	PR        int                       // PR number carrying the verb
	Issues    []int                     // Associated issues (e.g. [#2068])
	Owner     string                    // Owner responsible (e.g. "Emma", "Rowan", "Stella")
	Readers   []string                  // Required readers for approvals
	LiveHead  string                    // Live head commit SHA of the PR
	TargetSHA string                    // Expected commit SHA on dev
	PROpen    bool                      // PR is open
	Green     bool                      // CI checks are green & clean
	Approvals map[string]string         // reader -> approved commit SHA
	OnDev     bool                      // Merged to dev
	Fleet     map[string]DaemonEvidence // bench -> live daemon evidence
	Receipts  []string                  // Real run receipts on the fleet
	Holds     map[string]string         // friend -> hold reason
}

// Key returns the canonical identifier for the tool/verb pair.
func (r *Record) Key() string {
	tool := strings.TrimSpace(r.Tool)
	verb := strings.TrimSpace(r.Verb)
	if verb == "" || verb == "-" {
		return tool
	}
	return tool + " " + verb
}

// ApprovedByReports reports whether the given reader approved the live head.
func (r *Record) ApprovedBy(reader string) bool {
	if r.Approvals == nil || r.LiveHead == "" {
		return false
	}
	approvedHead, ok := r.Approvals[reader]
	if !ok || approvedHead == "" {
		return false
	}
	return shaMatches(approvedHead, r.LiveHead)
}

// UnapprovedReaders returns the list of required readers who have not approved LiveHead.
func (r *Record) UnapprovedReaders() []string {
	var missing []string
	for _, reader := range r.Readers {
		if !r.ApprovedBy(reader) {
			missing = append(missing, reader)
		}
	}
	sort.Strings(missing)
	return missing
}

// AllReadersApproved reports whether every reader in Readers has approved LiveHead.
func (r *Record) AllReadersApproved() bool {
	return len(r.UnapprovedReaders()) == 0
}

// ComputeStage computes the adoption stage, status description, single next act,
// and who owes that act based strictly on verifiable evidence.
func (r *Record) ComputeStage(upBenches []string) (stage Stage, status string, nextAct string, owedBy string) {
	// Stage 0: No PR open
	if !r.PROpen {
		return StageNone, "no PR open", "open PR", r.Owner
	}

	// Stage 25: PR open, but not yet green
	if !r.Green {
		return StagePROpen, "checks pending or failing", "green CI and pass hygiene", r.Owner
	}

	// Stage 50: Green and clean, awaiting required reviews on live head
	if len(r.Readers) > 0 && !r.AllReadersApproved() {
		unapproved := r.UnapprovedReaders()
		return StageGreen, "awaiting review approvals",
			fmt.Sprintf("review and approve live head %s", ShortSHA(r.LiveHead)),
			strings.Join(unapproved, ", ")
	}

	// Stage 75: Approved, awaiting landing on dev
	if !r.OnDev {
		return StageApproved, "approved on live head", "land on dev via merge queue", "coordinator"
	}

	// Landed on dev: check fleet benches
	benchesToCheck := upBenches
	if len(benchesToCheck) == 0 {
		// If no specific UP list provided, evaluate all benches in Fleet
		for b := range r.Fleet {
			benchesToCheck = append(benchesToCheck, b)
		}
		sort.Strings(benchesToCheck)
	}

	// Check installation on disk (DISK_SHA) across UP benches
	target := r.TargetSHA
	if target == "" {
		target = r.LiveHead
	}

	for _, b := range benchesToCheck {
		ev, ok := r.Fleet[b]
		if !ok || ev.DiskSHA == "" || (target != "" && !shaMatches(ev.DiskSHA, target)) {
			return StageApproved, fmt.Sprintf("landed on dev, installing on %s", b),
				fmt.Sprintf("install build %s on %s", ShortSHA(target), b), "operator"
		}
	}

	// Check running daemons across UP benches:
	// "INSTALLED IS NOT ADOPTED"
	// Only verify adoption when running daemon reports its live commit SHA via probe/heartbeat matching DISK_SHA.
	for _, b := range benchesToCheck {
		ev := r.Fleet[b]
		if err := VerifyDaemonAdoption(ev, target); err != nil {
			// Installed on disk, but daemon is not running or running an older SHA!
			return StageInstalled, fmt.Sprintf("installed on disk; unadopted on %s (%v)", b, err),
				fmt.Sprintf("restart %s on %s to adopt new build (installed is not adopted)", r.Key(), b),
				"operator"
		}
	}

	// All UP benches have verified running daemons (RUNNING_SHA == DISK_SHA).
	// Now check requirements for Stage 100 (In Use):
	// 1. No operational hold from a friend
	if len(r.Holds) > 0 {
		var holders []string
		for h := range r.Holds {
			holders = append(holders, h)
		}
		sort.Strings(holders)
		holder := holders[0]
		return StageInstalled, fmt.Sprintf("operational hold from %s", holder),
			fmt.Sprintf("resolve hold from %s: %s", holder, r.Holds[holder]),
			r.Owner
	}

	// 2. Named receipt from a real run on the fleet
	if len(r.Receipts) == 0 {
		return StageInstalled, "daemons verified live; awaiting real execution receipt",
			fmt.Sprintf("run %s on fleet and post receipt", r.Key()),
			r.Owner
	}

	// Stage 100: Verified live, in use, receipts recorded, no holds.
	return StageInUse, "adopted and in use", "none (adopted)", "-"
}

// Ledger is the collection of adoption records.
type Ledger struct {
	Records []Record
}

// NewLedger creates an empty adoption ledger.
func NewLedger() *Ledger {
	return &Ledger{Records: []Record{}}
}

// AddRecord appends a record to the ledger.
func (l *Ledger) AddRecord(rec Record) {
	l.Records = append(l.Records, rec)
}

// FindRecord finds a record by tool and verb.
func (l *Ledger) FindRecord(tool, verb string) *Record {
	targetKey := (&Record{Tool: tool, Verb: verb}).Key()
	for i := range l.Records {
		if l.Records[i].Key() == targetKey {
			return &l.Records[i]
		}
	}
	return nil
}

// Summary aggregates counts across the ledger.
type Summary struct {
	TotalRows        int
	NotStartedCount  int
	PROpenCount      int
	GreenCount       int
	ApprovedCount    int
	InstalledCount   int // Stage 90: installed on disk, but not adopted
	InUseCount       int // Stage 100: verified adopted in use
	UnadoptedDaemons int // Benches where disk has updated but running daemon has not
}

// Summarize computes aggregate adoption stats for the ledger.
func (l *Ledger) Summarize(upBenches []string) Summary {
	s := Summary{TotalRows: len(l.Records)}
	for _, r := range l.Records {
		st, _, _, _ := r.ComputeStage(upBenches)
		switch st {
		case StageNone:
			s.NotStartedCount++
		case StagePROpen:
			s.PROpenCount++
		case StageGreen:
			s.GreenCount++
		case StageApproved:
			s.ApprovedCount++
		case StageInstalled:
			s.InstalledCount++
		case StageInUse:
			s.InUseCount++
		}

		for _, b := range upBenches {
			if ev, ok := r.Fleet[b]; ok {
				if ev.DiskSHA != "" && (ev.RunningSHA == "" || !shaMatches(ev.RunningSHA, ev.DiskSHA)) {
					s.UnadoptedDaemons++
					break
				}
			}
		}
	}
	return s
}

// RenderTable writes a human-readable table of adoption rows and the single next act per row.
func (l *Ledger) RenderTable(w io.Writer, upBenches []string) error {
	fmt.Fprintf(w, "%-24s %-6s %-12s %-12s %-32s %-16s\n",
		"TOOL/VERB", "STAGE", "DISK_SHA", "RUNNING_SHA", "NEXT ACT", "OWED BY")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 108))

	for _, r := range l.Records {
		stage, _, nextAct, owedBy := r.ComputeStage(upBenches)

		diskSHA := "-"
		runningSHA := "-"
		if len(r.Fleet) > 0 {
			for _, b := range upBenches {
				if ev, ok := r.Fleet[b]; ok {
					if ev.DiskSHA != "" {
						diskSHA = ShortSHA(ev.DiskSHA)
					}
					if ev.RunningSHA != "" {
						runningSHA = ShortSHA(ev.RunningSHA)
					}
					break
				}
			}
			if diskSHA == "-" {
				for _, ev := range r.Fleet {
					if ev.DiskSHA != "" {
						diskSHA = ShortSHA(ev.DiskSHA)
						break
					}
				}
			}
			if runningSHA == "-" {
				for _, ev := range r.Fleet {
					if ev.RunningSHA != "" {
						runningSHA = ShortSHA(ev.RunningSHA)
						break
					}
				}
			}
		}

		fmt.Fprintf(w, "%-24s %-6s %-12s %-12s %-32s %-16s\n",
			oneline.Cap(r.Key(), 24),
			stage.String(),
			diskSHA,
			runningSHA,
			oneline.Cap(nextAct, 32),
			oneline.Cap(owedBy, 16))
	}
	return nil
}

// TSVHeader is the header line for serializing adoption rows.
const TSVHeader = "# tool\tverb\tpr\tstage\tdisk_sha\trunning_sha\tstatus\tnext_act\towed_by"

// RenderTSV writes the ledger rows to TSV.
func (l *Ledger) RenderTSV(w io.Writer, upBenches []string) error {
	if _, err := fmt.Fprintln(w, TSVHeader); err != nil {
		return err
	}
	for _, r := range l.Records {
		stage, status, nextAct, owedBy := r.ComputeStage(upBenches)
		diskSHA := "-"
		runningSHA := "-"
		for _, b := range upBenches {
			if ev, ok := r.Fleet[b]; ok {
				if ev.DiskSHA != "" {
					diskSHA = ev.DiskSHA
				}
				if ev.RunningSHA != "" {
					runningSHA = ev.RunningSHA
				}
				break
			}
		}
		_, err := fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n",
			r.Tool, r.Verb, r.PR, stage.Percent(), diskSHA, runningSHA,
			oneline.Field(status), oneline.Field(nextAct), oneline.Field(owedBy))
		if err != nil {
			return err
		}
	}
	return nil
}

// ParsedTSVRow represents a parsed line from an adoption TSV.
type ParsedTSVRow struct {
	Tool       string
	Verb       string
	PR         int
	Stage      int
	DiskSHA    string
	RunningSHA string
	Status     string
	NextAct    string
	OwedBy     string
}

// LoadTSV parses an adoption ledger from a TSV reader.
func LoadTSV(r io.Reader) ([]ParsedTSVRow, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 1024*1024)
	if !sc.Scan() {
		return nil, errors.New("empty TSV input")
	}
	if sc.Text() != TSVHeader {
		return nil, fmt.Errorf("invalid header, want %q, got %q", TSVHeader, sc.Text())
	}

	var rows []ParsedTSVRow
	line := 1
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Split(text, "\t")
		if len(fields) != 9 {
			return nil, fmt.Errorf("line %d: %d fields, want 9", line, len(fields))
		}
		pr, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid PR number %q", line, fields[2])
		}
		st, err := strconv.Atoi(fields[3])
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid stage %q", line, fields[3])
		}
		rows = append(rows, ParsedTSVRow{
			Tool:       fields[0],
			Verb:       fields[1],
			PR:         pr,
			Stage:      st,
			DiskSHA:    fields[4],
			RunningSHA: fields[5],
			Status:     fields[6],
			NextAct:    fields[7],
			OwedBy:     fields[8],
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

// ShortSHA returns an 8-character prefix of a commit SHA, or the string itself if shorter.
func ShortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// shaMatches compares two SHAs, allowing prefix matches (min 7 hex chars) or exact equality.
func shaMatches(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	if len(a) >= 7 && len(b) >= 7 {
		minLen := len(a)
		if len(b) < minLen {
			minLen = len(b)
		}
		return a[:minLen] == b[:minLen]
	}
	return false
}
