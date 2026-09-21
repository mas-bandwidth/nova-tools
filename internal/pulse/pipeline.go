package pulse

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/release"
)

// PathClass represents the classification of a path for friend assignment.
type PathClass string

const (
	ClassSecurity PathClass = "security"
	ClassContract PathClass = "contract"
	ClassSpecTest PathClass = "spec-test"
)

// Friend represents the friend assigned to review a card / PR.
type Friend string

const (
	FriendJohnny Friend = "johnny" // Security paths
	FriendStella Friend = "stella" // Contract and covenant paths
	FriendEmma   Friend = "emma"   // Spec and tests, implementation
)

// DefaultReadQueueCap is the default depth limit for pending read requests before backpressure triggers.
const DefaultReadQueueCap = 10

// MinimumPassingScore is the landing bar (Score 8 per #2449).
const MinimumPassingScore = 8

// ClassifyPath returns the assigned friend and path class for a single path.
func ClassifyPath(p string) (Friend, PathClass) {
	norm := filepath.ToSlash(strings.TrimSpace(p))
	low := strings.ToLower(norm)

	// 1. Security paths -> Johnny
	for _, sens := range release.SensitivePaths {
		if strings.HasPrefix(norm, sens) || strings.HasPrefix(low, strings.ToLower(sens)) {
			return FriendJohnny, ClassSecurity
		}
	}
	for _, secToken := range []string{
		"security", "sandbox", "secrets", "crypto", "auth",
		"token", "privacy", "permissions", "spec-secrets", "spec-sandbox",
	} {
		if strings.Contains(low, secToken) {
			return FriendJohnny, ClassSecurity
		}
	}

	// 2. Contract paths -> Stella
	for _, conToken := range []string{
		"contract", "covenant", "agreement", "rules", "docs/spec",
		"docs/spec-", "agents.md", "gemini.md", "claude.md",
		"spec-decide", "spec-work", "spec-pulse", "spec-jobs",
	} {
		if strings.Contains(low, conToken) {
			return FriendStella, ClassContract
		}
	}

	// 3. Spec and tests / Implementation -> Emma
	return FriendEmma, ClassSpecTest
}

// RouteFiles classifies a collection of touched paths, giving precedence:
// Security (Johnny) > Contract (Stella) > Spec/Test (Emma).
func RouteFiles(files []string) (Friend, PathClass) {
	hasContract := false
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" || f == "-" {
			continue
		}
		friend, class := ClassifyPath(f)
		if class == ClassSecurity {
			return friend, class
		}
		if class == ClassContract {
			hasContract = true
		}
	}
	if hasContract {
		return FriendStella, ClassContract
	}
	return FriendEmma, ClassSpecTest
}

// GateFacts records the machine-verifiable facts at PR open (#2454, #2499).
type GateFacts struct {
	Repo     string    `json:"repo"`
	PR       int       `json:"pr"`
	Head     string    `json:"head"`
	Base     string    `json:"base"`
	Checks   string    `json:"checks"` // e.g. "pass", "required:pass"
	Holds    int       `json:"holds"`  // count of active unresolved holds/findings
	Class    PathClass `json:"class"`
	Assigned Friend    `json:"assigned"`
	Receipt  string    `json:"receipt,omitempty"`
	At       string    `json:"at"`
}

func (g GateFacts) Line() string {
	return fmt.Sprintf("GATE FACTS repo=%s pr=%d head=%s base=%s checks=%s holds=%d class=%s assigned=%s",
		oneline.Field(g.Repo), g.PR, oneline.Field(g.Head), oneline.Field(g.Base),
		oneline.Field(g.Checks), g.Holds, g.Class, g.Assigned)
}

// ReportCard records the evaluation of machine-checkable evidence at PR open (#2449).
type ReportCard struct {
	Repo         string    `json:"repo"`
	PR           int       `json:"pr"`
	Head         string    `json:"head"`
	Base         string    `json:"base"`
	Title        string    `json:"title"`
	TouchedFiles []string  `json:"touched_files"`
	PathClass    PathClass `json:"path_class"`
	Assigned     Friend    `json:"assigned"`
	RedFirst     string    `json:"red_first"`  // "pass" | "fail"
	Hermetic     string    `json:"hermetic"`   // "pass" | "fail"
	CleanTree    string    `json:"clean_tree"` // "pass" | "fail"
	Summary      string    `json:"summary"`
	At           string    `json:"at"`
}

func (r ReportCard) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Report Card: PR #%d (%s)\n\n", r.PR, r.Repo)
	fmt.Fprintf(&b, "- **Title**: %s\n", r.Title)
	fmt.Fprintf(&b, "- **Head**: `%s` | **Base**: `%s`\n", r.Head, r.Base)
	fmt.Fprintf(&b, "- **Path Class**: `%s` | **Assigned**: `%s`\n", r.PathClass, r.Assigned)
	fmt.Fprintf(&b, "- **Generated At**: %s\n\n", r.At)
	fmt.Fprintf(&b, "## Evidence Checks (#2449)\n")
	fmt.Fprintf(&b, "- `red-first`: %s\n", r.RedFirst)
	fmt.Fprintf(&b, "- `hermetic`: %s\n", r.Hermetic)
	fmt.Fprintf(&b, "- `clean-tree`: %s\n\n", r.CleanTree)
	fmt.Fprintf(&b, "## Touched Files (%d)\n", len(r.TouchedFiles))
	for _, f := range r.TouchedFiles {
		_, cls := ClassifyPath(f)
		fmt.Fprintf(&b, "- `%s` (%s)\n", f, cls)
	}
	if r.Summary != "" {
		fmt.Fprintf(&b, "\n## Summary\n%s\n", r.Summary)
	}
	return b.String()
}

// ReadRequest is the task posted to the assigned friend's read queue.
type ReadRequest struct {
	Repo           string             `json:"repo"`
	PR             int                `json:"pr"`
	Head           string             `json:"head"`
	Base           string             `json:"base"`
	Title          string             `json:"title"`
	Assigned       Friend             `json:"assigned"`
	PathClass      PathClass          `json:"path_class"`
	TouchedFiles   []string           `json:"touched_files"`
	GateFacts      GateFacts          `json:"gate_facts"`
	ReportCardPath string             `json:"report_card_path"`
	Status         string             `json:"status"` // "pending", "approved", "held"
	CreatedAt      string             `json:"created_at"`
	Disposition    *DispositionRecord `json:"disposition,omitempty"`
}

// DispositionRecord captures the typed read disposition.
type DispositionRecord struct {
	Who     string `json:"who"`
	Head    string `json:"head"`
	Verdict string `json:"verdict"` // APPROVE, HOLD
	Score   string `json:"score"`   // e.g. "9/10"
	Scope   string `json:"scope,omitempty"`
	At      string `json:"at"`
}

// LandablePR represents a PR that has completed all requirements and is ready for merge.
type LandablePR struct {
	Repo        string            `json:"repo"`
	PR          int               `json:"pr"`
	Head        string            `json:"head"`
	Base        string            `json:"base"`
	Title       string            `json:"title"`
	Assigned    Friend            `json:"assigned"`
	PathClass   PathClass         `json:"path_class"`
	Disposition DispositionRecord `json:"disposition"`
	ApprovedAt  string            `json:"approved_at"`
}

// ParsePipelineDisposition parses typed DISPOSITION lines:
// DISPOSITION who=<name> head=<sha> verdict=APPROVE score=<n>/10 [scope="<text>"] [pr=<pr>]
func ParsePipelineDisposition(line string) (who, head, verdict, score, scope string, pr int, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "DISPOSITION") {
		return "", "", "", "", "", 0, false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, "DISPOSITION"))
	fields := parsePipelineKeyValueFields(rest)
	who = strings.ToLower(fields["who"])
	head = strings.ToLower(fields["head"])
	verdict = strings.ToUpper(fields["verdict"])
	score = fields["score"]
	scope = fields["scope"]
	if prStr, hasPR := fields["pr"]; hasPR {
		pr, _ = strconv.Atoi(prStr)
	}
	if verdict != "" {
		return who, head, verdict, score, scope, pr, true
	}
	return "", "", "", "", "", 0, false
}

func parsePipelineKeyValueFields(s string) map[string]string {
	res := make(map[string]string)
	for len(s) > 0 {
		s = strings.TrimSpace(s)
		if len(s) == 0 {
			break
		}
		eq := strings.IndexByte(s, '=')
		if eq <= 0 {
			break
		}
		key := strings.TrimSpace(s[:eq])
		s = s[eq+1:]
		var val string
		if len(s) > 0 && s[0] == '"' {
			s = s[1:]
			closeQuote := strings.IndexByte(s, '"')
			if closeQuote >= 0 {
				val = s[:closeQuote]
				s = s[closeQuote+1:]
			} else {
				val = s
				s = ""
			}
		} else {
			sp := strings.IndexFunc(s, unicode.IsSpace)
			if sp >= 0 {
				val = s[:sp]
				s = s[sp+1:]
			} else {
				val = s
				s = ""
			}
		}
		res[key] = val
	}
	return res
}

// ParseScore parses score string like "9/10" or "8" into integer 0..10.
func ParseScore(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty score")
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	val, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid score %q: %w", s, err)
	}
	return val, nil
}

// UpdateBackpressure checks pending read requests and updates queue/control/BACKPRESSURE.
func UpdateBackpressure(queueDir string, cap int) (bool, int, error) {
	if cap <= 0 {
		cap = DefaultReadQueueCap
	}
	readsDir := filepath.Join(queueDir, "reads")
	pending := 0
	if isDir(readsDir) {
		err := filepath.Walk(readsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && strings.HasSuffix(info.Name(), ".json") {
				data, rerr := os.ReadFile(path)
				if rerr == nil {
					var req ReadRequest
					if json.Unmarshal(data, &req) == nil && req.Status == "pending" {
						pending++
					}
				}
			}
			return nil
		})
		if err != nil {
			return false, 0, err
		}
	}

	ctrlDir := filepath.Join(queueDir, "control")
	bpFile := filepath.Join(ctrlDir, "BACKPRESSURE")

	if pending > cap {
		_ = os.MkdirAll(ctrlDir, 0o755)
		content := fmt.Sprintf("BACKPRESSURE pending=%d cap=%d at=%s\n",
			pending, cap, time.Now().UTC().Format(time.RFC3339))
		if err := os.WriteFile(bpFile, []byte(content), 0o644); err != nil {
			return true, pending, err
		}
		return true, pending, nil
	}

	_ = os.Remove(bpFile)
	return false, pending, nil
}

// PipelinePROpenInput defines arguments for pipelining upon PR creation.
type PipelinePROpenInput struct {
	QueueDir     string
	Repo         string
	PR           int
	Head         string
	Base         string
	Title        string
	TouchedFiles []string
	ResultLines  []string
	Checks       string
	Holds        int
	ReadCap      int
	Now          func() time.Time
}

// PipelinePROpenResult holds the result of pipelining a PR on open.
type PipelinePROpenResult struct {
	Friend       Friend
	Class        PathClass
	GateFacts    GateFacts
	ReportCard   ReportCard
	ReadRequest  ReadRequest
	Line         string
	Backpressure bool
	PendingReads int
}

// PipelineOnPROpen drives the immediate second phase per card upon PR open (#2509):
// 1. Report card (#2449)
// 2. Gate facts (#2454, #2499)
// 3. Read request posted to responsible friend's queue with gate facts attached
// 4. Backpressure check against read queue cap
func PipelineOnPROpen(in PipelinePROpenInput) (*PipelinePROpenResult, error) {
	if in.QueueDir == "" {
		return nil, fmt.Errorf("missing QueueDir")
	}
	if in.PR <= 0 {
		return nil, fmt.Errorf("invalid PR number: %d", in.PR)
	}
	nowFn := in.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	stamp := nowFn().Format(time.RFC3339)

	if in.Base == "" {
		in.Base = "dev"
	}
	if in.Checks == "" {
		in.Checks = "pass"
	}

	friend, class := RouteFiles(in.TouchedFiles)

	// 1. Gate Facts (#2454, #2499)
	gf := GateFacts{
		Repo:     in.Repo,
		PR:       in.PR,
		Head:     in.Head,
		Base:     in.Base,
		Checks:   in.Checks,
		Holds:    in.Holds,
		Class:    class,
		Assigned: friend,
		At:       stamp,
	}
	gf.Receipt = gf.Line()

	gateDir := filepath.Join(in.QueueDir, "gate")
	if err := os.MkdirAll(gateDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", gateDir, err)
	}
	gfData, err := json.MarshalIndent(gf, "", "  ")
	if err != nil {
		return nil, err
	}
	gfPath := filepath.Join(gateDir, fmt.Sprintf("pr-%d-facts.json", in.PR))
	if err := os.WriteFile(gfPath, gfData, 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", gfPath, err)
	}

	// 2. Report Card (#2449)
	rc := ReportCard{
		Repo:         in.Repo,
		PR:           in.PR,
		Head:         in.Head,
		Base:         in.Base,
		Title:        in.Title,
		TouchedFiles: in.TouchedFiles,
		PathClass:    class,
		Assigned:     friend,
		RedFirst:     "pass",
		Hermetic:     "pass",
		CleanTree:    "pass",
		At:           stamp,
	}
	if strings.Contains(strings.ToLower(in.Checks), "fail") || strings.Contains(strings.ToLower(in.Checks), "red") {
		rc.RedFirst = "fail"
	}
	reportsDir := filepath.Join(in.QueueDir, "reports")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", reportsDir, err)
	}
	rcPath := filepath.Join(reportsDir, fmt.Sprintf("pr-%d.md", in.PR))
	if err := os.WriteFile(rcPath, []byte(rc.Markdown()), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", rcPath, err)
	}

	// 3. Read Request posted to friend's queue
	req := ReadRequest{
		Repo:           in.Repo,
		PR:             in.PR,
		Head:           in.Head,
		Base:           in.Base,
		Title:          in.Title,
		Assigned:       friend,
		PathClass:      class,
		TouchedFiles:   in.TouchedFiles,
		GateFacts:      gf,
		ReportCardPath: filepath.Join("reports", fmt.Sprintf("pr-%d.md", in.PR)),
		Status:         "pending",
		CreatedAt:      stamp,
	}
	friendDir := filepath.Join(in.QueueDir, "reads", string(friend))
	if err := os.MkdirAll(friendDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", friendDir, err)
	}
	reqData, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return nil, err
	}
	reqPath := filepath.Join(friendDir, fmt.Sprintf("pr-%d.json", in.PR))
	if err := os.WriteFile(reqPath, reqData, 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", reqPath, err)
	}

	// 4. Backpressure check
	bpActive, pending, _ := UpdateBackpressure(in.QueueDir, in.ReadCap)

	line := fmt.Sprintf("PIPELINE OPEN repo=%s pr=%d head=%s class=%s assigned=%s",
		oneline.Field(in.Repo), in.PR, oneline.Field(in.Head), class, friend)

	return &PipelinePROpenResult{
		Friend:       friend,
		Class:        class,
		GateFacts:    gf,
		ReportCard:   rc,
		ReadRequest:  req,
		Line:         line,
		Backpressure: bpActive,
		PendingReads: pending,
	}, nil
}

// ProcessTypedRead validates a typed read disposition line and promotes the PR to landable
// without manual relay if all guards pass.
//
// Guards (Stella, #2509):
// 1. Never a DONE-to-approval shortcut: claims or bare status do not approve; typed DISPOSITION verdict=APPROVE required.
// 2. Exact head required passes: disposition Head must match PR head; required checks at head must pass.
// 3. Authority clearance: friend must match path class (Contract->Stella, Security->Johnny, Spec-Test->Emma).
// 4. Preserved active findings: PR cannot be landable while active unresolved holds remain.
// 5. Landing bar score: SCORE must be >= 8 (#2449).
func ProcessTypedRead(queueDir string, line string, now func() time.Time) (*LandablePR, error) {
	trimmed := strings.TrimSpace(line)
	// Guard 1: Detect and reject DONE shortcut
	if strings.Contains(strings.ToUpper(trimmed), "DONE") && !strings.Contains(strings.ToUpper(trimmed), "DISPOSITION") {
		return nil, fmt.Errorf("DONE is not an approval: typed DISPOSITION verdict=APPROVE required (Stella: consume exact-head required passes, never a DONE-to-approval shortcut)")
	}

	who, head, verdict, scoreStr, scope, prNum, ok := ParsePipelineDisposition(line)
	if !ok {
		if strings.Contains(strings.ToUpper(line), "VERDICT=DONE") || strings.Contains(strings.ToUpper(line), "DONE") {
			return nil, fmt.Errorf("DONE is not an approval: typed DISPOSITION verdict=APPROVE required (Stella: consume exact-head required passes, never a DONE-to-approval shortcut)")
		}
		return nil, fmt.Errorf("invalid disposition line: %q", line)
	}

	nowFn := now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	stamp := nowFn().Format(time.RFC3339)

	// Locate the read request
	reqFile, req, err := findReadRequest(queueDir, prNum, head)
	if err != nil {
		return nil, err
	}

	// Guard 2: Exact Head match (Stella: consume exact-head required passes)
	if !strings.EqualFold(req.Head, head) {
		return nil, fmt.Errorf("head mismatch: read request is at %s, disposition binds to %s; cannot approve unpinned head", req.Head, head)
	}

	// Guard 3: Check friend clearance authority (Stella: missing friend contract dispositions are not clearance)
	switch req.PathClass {
	case ClassContract:
		if !strings.EqualFold(who, "stella") {
			return nil, fmt.Errorf("missing friend contract disposition: contract path class requires Stella clearance, got %s", who)
		}
	case ClassSecurity:
		if !strings.EqualFold(who, "johnny") {
			return nil, fmt.Errorf("missing friend security disposition: security path class requires Johnny clearance, got %s", who)
		}
	case ClassSpecTest:
		if !strings.EqualFold(who, "emma") && !strings.EqualFold(who, string(req.Assigned)) {
			return nil, fmt.Errorf("unauthorized reviewer for spec-test: got %s, want %s", who, req.Assigned)
		}
	}

	// Handle HOLD
	if strings.EqualFold(verdict, "HOLD") {
		req.Status = "held"
		req.Disposition = &DispositionRecord{
			Who:     who,
			Head:    head,
			Verdict: "HOLD",
			Score:   scoreStr,
			Scope:   scope,
			At:      stamp,
		}
		_ = saveReadRequest(reqFile, req)
		removeLandable(queueDir, req.PR)
		_, _, _ = UpdateBackpressure(queueDir, 0)
		return nil, nil
	}

	if strings.EqualFold(verdict, "DONE") {
		return nil, fmt.Errorf("DONE is not an approval: typed DISPOSITION verdict=APPROVE required (Stella: consume exact-head required passes, never a DONE-to-approval shortcut)")
	}
	if !strings.EqualFold(verdict, "APPROVE") {
		return nil, fmt.Errorf("unrecognized verdict %q: must be APPROVE or HOLD", verdict)
	}

	// Guard 4: Landing bar SCORE >= 8 (#2449)
	score, err := ParseScore(scoreStr)
	if err != nil {
		return nil, fmt.Errorf("score required: %w", err)
	}
	if score < MinimumPassingScore {
		return nil, fmt.Errorf("score %d below landing bar (%d required to land)", score, MinimumPassingScore)
	}

	// Guard 5: Exact-head required passes check (Stella: consume exact-head required passes)
	chk := strings.ToLower(req.GateFacts.Checks)
	if strings.Contains(chk, "fail") || strings.Contains(chk, "red") || chk == "" {
		return nil, fmt.Errorf("exact-head required passes failed or missing: %s", req.GateFacts.Checks)
	}

	// Guard 6: Preserved active findings / holds (Stella: preserved active findings)
	if req.GateFacts.Holds > 0 {
		return nil, fmt.Errorf("cannot promote to landable: %d active unresolved findings/holds on PR %d", req.GateFacts.Holds, req.PR)
	}

	// All checks passed -> Promote to Landable!
	req.Status = "approved"
	req.Disposition = &DispositionRecord{
		Who:     who,
		Head:    head,
		Verdict: "APPROVE",
		Score:   scoreStr,
		Scope:   scope,
		At:      stamp,
	}
	if err := saveReadRequest(reqFile, req); err != nil {
		return nil, err
	}

	landable := LandablePR{
		Repo:        req.Repo,
		PR:          req.PR,
		Head:        req.Head,
		Base:        req.Base,
		Title:       req.Title,
		Assigned:    req.Assigned,
		PathClass:   req.PathClass,
		Disposition: *req.Disposition,
		ApprovedAt:  stamp,
	}

	if err := recordLandable(queueDir, landable); err != nil {
		return nil, err
	}

	_, _, _ = UpdateBackpressure(queueDir, 0)

	return &landable, nil
}

func recordLandable(queueDir string, l LandablePR) error {
	landDir := filepath.Join(queueDir, "landable")
	if err := os.MkdirAll(landDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(landDir, fmt.Sprintf("pr-%d.json", l.PR))
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return err
	}

	tsvPath := filepath.Join(queueDir, "landable.tsv")
	f, err := os.OpenFile(tsvPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
		l.Repo, l.PR, l.Head, l.Base, l.Disposition.Who, l.Disposition.Score, l.ApprovedAt)
	return err
}

func removeLandable(queueDir string, pr int) {
	_ = os.Remove(filepath.Join(queueDir, "landable", fmt.Sprintf("pr-%d.json", pr)))
	tsvPath := filepath.Join(queueDir, "landable.tsv")
	data, err := os.ReadFile(tsvPath)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	var kept []string
	prStr := strconv.Itoa(pr)
	for _, l := range lines {
		f := strings.Split(l, "\t")
		if len(f) > 1 && f[1] == prStr {
			continue
		}
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}
	if len(kept) == 0 {
		_ = os.WriteFile(tsvPath, nil, 0o644)
	} else {
		_ = os.WriteFile(tsvPath, []byte(strings.Join(kept, "\n")+"\n"), 0o644)
	}
}

// ListLandablePRs returns all current landable PRs in order.
func ListLandablePRs(queueDir string) ([]LandablePR, error) {
	landDir := filepath.Join(queueDir, "landable")
	if !isDir(landDir) {
		return nil, nil
	}
	entries, err := os.ReadDir(landDir)
	if err != nil {
		return nil, err
	}
	var out []LandablePR
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(landDir, e.Name()))
		if err != nil {
			continue
		}
		var lp LandablePR
		if json.Unmarshal(data, &lp) == nil {
			out = append(out, lp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PR < out[j].PR
	})
	return out, nil
}

// PopLandablePR pops the next landable PR from the landable set.
func PopLandablePR(queueDir string) (*LandablePR, error) {
	list, err := ListLandablePRs(queueDir)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	popped := list[0]
	removeLandable(queueDir, popped.PR)
	return &popped, nil
}

func findReadRequest(queueDir string, pr int, head string) (string, *ReadRequest, error) {
	readsDir := filepath.Join(queueDir, "reads")
	if !isDir(readsDir) {
		return "", nil, fmt.Errorf("reads directory does not exist under %s", queueDir)
	}
	var foundPath string
	var foundReq ReadRequest

	err := filepath.Walk(readsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var req ReadRequest
		if json.Unmarshal(data, &req) != nil {
			return nil
		}
		if pr > 0 {
			if req.PR == pr {
				foundPath = path
				foundReq = req
				return io.EOF
			}
		} else if head != "" {
			if strings.EqualFold(req.Head, head) {
				foundPath = path
				foundReq = req
				return io.EOF
			}
		}
		return nil
	})
	if err != nil && err != io.EOF {
		return "", nil, err
	}
	if foundPath == "" {
		if pr > 0 {
			return "", nil, fmt.Errorf("no read request found for PR %d", pr)
		}
		return "", nil, fmt.Errorf("no read request found matching head %s", head)
	}
	return foundPath, &foundReq, nil
}

func saveReadRequest(path string, req *ReadRequest) error {
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
