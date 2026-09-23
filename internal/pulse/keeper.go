package pulse

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Essential 10 of nova-tools #2459: Autonomy that survives the night.
// `fleet keeper` runs on the hour:
// 1. Folds results from finished cards/jobs.
// 2. Checks merge bases against the target base branch.
// 3. Checks the status of durable loops (dealer, backpressure, harvest, sprint table).
// 4. Records decisions as one receipt per hour under queue/wake/.

// DefaultDurableLoops are the four durable loops the keeper monitors (issue #2459).
var DefaultDurableLoops = []string{
	"dealer",
	"backpressure",
	"harvest",
	"sprint table",
}

// LoopStatus describes the state of one monitored durable loop.
type LoopStatus struct {
	Name    string `json:"name"`
	Alive   bool   `json:"alive"`
	PID     int    `json:"pid,omitempty"`
	Command string `json:"command,omitempty"`
	Details string `json:"details,omitempty"`
}

// MergeBaseStatus describes the relation between HEAD and the target base branch.
type MergeBaseStatus struct {
	Repo         string `json:"repo"`
	Base         string `json:"base"`
	Head         string `json:"head"`
	HeadSHA      string `json:"head_sha"`
	BaseSHA      string `json:"base_sha"`
	MergeBaseSHA string `json:"merge_base_sha"`
	Status       string `json:"status"` // "aligned", "ahead", "behind", "diverged", "unknown"
	Decision     string `json:"decision"`
}

// FoldResult is one card's folded result.
type FoldResult struct {
	Card    string `json:"card"`
	Verdict string `json:"verdict"` // "clean", "defect", "skip", "failed"
	Details string `json:"details,omitempty"`
}

// FoldSummary summarizes the card results folded during this cycle.
type FoldSummary struct {
	Total   int          `json:"total"`
	Clean   int          `json:"clean"`
	Defect  int          `json:"defect"`
	Failed  int          `json:"failed"`
	Skipped int          `json:"skipped"`
	Results []FoldResult `json:"results,omitempty"`
}

// KeeperReceipt is the complete record of one keeper wake on the hour.
type KeeperReceipt struct {
	Hour      string          `json:"hour"`
	At        string          `json:"at"`
	Host      string          `json:"host"`
	Verdict   string          `json:"verdict"`
	Folds     FoldSummary     `json:"folds"`
	MergeBase MergeBaseStatus `json:"merge_base"`
	Loops     []LoopStatus    `json:"loops"`
	Decisions []string        `json:"decisions"`
}

// FleetKeeperInput is everything `fleet keeper` needs.
type FleetKeeperInput struct {
	Queue       string
	Repo        string
	Base        string
	Roots       string
	Receipt     string
	Strict      bool
	RestartDead bool
	Loops       []string
	Stdout      io.Writer
	Stderr      io.Writer
	Now         func() time.Time

	// Seams for testability and injection.
	LoopChecker func(loops []string) []LoopStatus
	BaseChecker func(repo, base string) (MergeBaseStatus, error)
	Folder      func(queue, roots string) (FoldSummary, error)
	Restarter   func(loopName string) error
}

// FleetKeeper runs the hourly wake cycle and writes the decision receipt under queue/wake/.
func FleetKeeper(in FleetKeeperInput) int {
	if strings.TrimSpace(in.Queue) == "" {
		return refusal(in.Stderr, "FLEET KEEPER", fmt.Errorf(
			"--queue is required; it wants the queue directory holding wake/ and the state files; refusing to guess"))
	}

	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	now := in.Now().UTC()

	loops := in.Loops
	if len(loops) == 0 {
		loops = DefaultDurableLoops
	}

	// 1. Fold results.
	folder := in.Folder
	if folder == nil {
		folder = defaultResultFolder
	}
	folds, foldErr := folder(in.Queue, in.Roots)

	// 2. Check merge bases.
	baseChecker := in.BaseChecker
	if baseChecker == nil {
		baseChecker = defaultBaseChecker
	}
	baseStatus, baseErr := baseChecker(in.Repo, in.Base)

	// 3. Check status of durable loops.
	loopChecker := in.LoopChecker
	if loopChecker == nil {
		loopChecker = defaultLoopChecker
	}
	loopStatuses := loopChecker(loops)

	// 4. Formulate decisions.
	var decisions []string
	if foldErr != nil {
		decisions = append(decisions, fmt.Sprintf("FOLD FAILED: %v", foldErr))
	} else {
		decisions = append(decisions, fmt.Sprintf("FOLD: %d results folded (%d clean, %d defect, %d failed, %d skipped)",
			folds.Total, folds.Clean, folds.Defect, folds.Failed, folds.Skipped))
	}

	if baseErr != nil {
		decisions = append(decisions, fmt.Sprintf("BASE FAILED: %v", baseErr))
		if baseStatus.Status == "" || baseStatus.Status == "unknown" {
			baseStatus.Status = "error"
		}
	} else {
		decisions = append(decisions, fmt.Sprintf("BASE: %s against %s (merge-base=%s head=%s base=%s)",
			baseStatus.Status, baseStatus.Base, baseStatus.MergeBaseSHA, baseStatus.HeadSHA, baseStatus.BaseSHA))
	}

	var deadLoops []string
	for _, l := range loopStatuses {
		if l.Alive {
			decisions = append(decisions, fmt.Sprintf("LOOP %s: alive (pid %d)", l.Name, l.PID))
		} else {
			deadLoops = append(deadLoops, l.Name)
			decision := fmt.Sprintf("LOOP %s: DEAD -> restart required", l.Name)
			if in.RestartDead && in.Restarter != nil {
				if err := in.Restarter(l.Name); err != nil {
					decision += fmt.Sprintf(" (restart failed: %v)", err)
				} else {
					decision += " (restart succeeded)"
				}
			}
			decisions = append(decisions, decision)
		}
	}

	verdict := "OK"
	if len(deadLoops) > 0 {
		verdict = "DEGRADED"
	}
	if foldErr != nil || baseErr != nil || baseStatus.Status == "diverged" {
		verdict = "ATTENTION_NEEDED"
	}
	decisions = append(decisions, fmt.Sprintf("VERDICT: %s", verdict))

	// Determine host name.
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}

	hourStamp := now.Truncate(time.Hour).Format(time.RFC3339)
	receipt := KeeperReceipt{
		Hour:      hourStamp,
		At:        now.Format(time.RFC3339),
		Host:      host,
		Verdict:   verdict,
		Folds:     folds,
		MergeBase: baseStatus,
		Loops:     loopStatuses,
		Decisions: decisions,
	}

	// 5. Write receipt under queue/wake/.
	receiptDir := filepath.Join(in.Queue, "wake")
	if err := os.MkdirAll(receiptDir, 0o755); err != nil {
		return refusal(in.Stderr, "FLEET KEEPER", fmt.Errorf("cannot create wake directory %s: %w", receiptDir, err))
	}

	receiptPath := in.Receipt
	if strings.TrimSpace(receiptPath) == "" {
		fileStamp := now.Format("2006-01-02T15Z")
		receiptPath = filepath.Join(receiptDir, fileStamp+".receipt")
	}

	formatted := FormatKeeperReceipt(&receipt)
	if err := os.WriteFile(receiptPath, []byte(formatted), 0o644); err != nil {
		return refusal(in.Stderr, "FLEET KEEPER", fmt.Errorf("cannot write receipt %s: %w", receiptPath, err))
	}

	// 6. Print one status line to stdout.
	loopParts := make([]string, len(loopStatuses))
	for i, l := range loopStatuses {
		st := "down"
		if l.Alive {
			st = "up"
		}
		loopParts[i] = fmt.Sprintf("%s:%s", strings.ReplaceAll(l.Name, " ", "-"), st)
	}

	outLine := fmt.Sprintf("KEEPER WAKE hour=%s folded=%d base=%s loops=%s receipt=%s",
		oneline.Field(receipt.Hour),
		folds.Total,
		oneline.Field(baseStatus.Status),
		strings.Join(loopParts, ","),
		oneline.Field(receiptPath),
	)
	if len(deadLoops) > 0 {
		outLine += fmt.Sprintf(" dead=%s", strings.Join(deadLoops, ","))
	}
	fmt.Fprintln(in.Stdout, outLine)

	if foldErr != nil && in.Stderr != nil {
		fmt.Fprintf(in.Stderr, "nova-pulse fleet keeper: folder error: %v\n", foldErr)
	}
	if baseErr != nil && in.Stderr != nil {
		fmt.Fprintf(in.Stderr, "nova-pulse fleet keeper: base checker error: %v\n", baseErr)
	}

	if foldErr != nil || baseErr != nil {
		return 1
	}
	if in.Strict && (len(deadLoops) > 0 || baseStatus.Status == "diverged") {
		return 1
	}
	return 0
}

// FormatKeeperReceipt formats a KeeperReceipt into a readable structured text document.
func FormatKeeperReceipt(r *KeeperReceipt) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# KEEPER WAKE RECEIPT: %s\n", r.Hour)
	fmt.Fprintf(&b, "hour: %s\n", r.Hour)
	fmt.Fprintf(&b, "at: %s\n", r.At)
	fmt.Fprintf(&b, "host: %s\n", r.Host)
	fmt.Fprintf(&b, "verdict: %s\n\n", r.Verdict)

	fmt.Fprintf(&b, "## Results Folded\n")
	fmt.Fprintf(&b, "total: %d\n", r.Folds.Total)
	fmt.Fprintf(&b, "clean: %d\n", r.Folds.Clean)
	fmt.Fprintf(&b, "defect: %d\n", r.Folds.Defect)
	fmt.Fprintf(&b, "failed: %d\n", r.Folds.Failed)
	fmt.Fprintf(&b, "skipped: %d\n", r.Folds.Skipped)
	for _, res := range r.Folds.Results {
		fmt.Fprintf(&b, "- card=%s verdict=%s\n", res.Card, res.Verdict)
	}
	fmt.Fprintln(&b)

	fmt.Fprintf(&b, "## Merge Bases\n")
	fmt.Fprintf(&b, "repo: %s\n", r.MergeBase.Repo)
	fmt.Fprintf(&b, "base: %s\n", r.MergeBase.Base)
	fmt.Fprintf(&b, "head_sha: %s\n", r.MergeBase.HeadSHA)
	fmt.Fprintf(&b, "base_sha: %s\n", r.MergeBase.BaseSHA)
	fmt.Fprintf(&b, "merge_base_sha: %s\n", r.MergeBase.MergeBaseSHA)
	fmt.Fprintf(&b, "status: %s\n", r.MergeBase.Status)
	fmt.Fprintf(&b, "decision: %s\n\n", r.MergeBase.Decision)

	fmt.Fprintf(&b, "## Durable Loops\n")
	aliveCount := 0
	for _, l := range r.Loops {
		if l.Alive {
			aliveCount++
		}
	}
	fmt.Fprintf(&b, "loops_total: %d\n", len(r.Loops))
	fmt.Fprintf(&b, "loops_alive: %d\n", aliveCount)
	fmt.Fprintf(&b, "loops_dead: %d\n", len(r.Loops)-aliveCount)
	for _, l := range r.Loops {
		st := "dead"
		if l.Alive {
			st = "alive"
		}
		fmt.Fprintf(&b, "- loop=%s status=%s pid=%d\n", l.Name, st, l.PID)
	}
	fmt.Fprintln(&b)

	fmt.Fprintf(&b, "## Decisions\n")
	for _, d := range r.Decisions {
		fmt.Fprintf(&b, "- %s\n", d)
	}
	return b.String()
}

// ParseKeeperReceipt parses a formatted KeeperReceipt back into a KeeperReceipt struct.
func ParseKeeperReceipt(data []byte) (*KeeperReceipt, error) {
	r := &KeeperReceipt{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	section := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "# ") {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimPrefix(line, "## ")
			continue
		}

		switch section {
		case "":
			if k, v, ok := strings.Cut(line, ": "); ok {
				switch k {
				case "hour":
					r.Hour = v
				case "at":
					r.At = v
				case "host":
					r.Host = v
				case "verdict":
					r.Verdict = v
				}
			}
		case "Results Folded":
			if strings.HasPrefix(line, "- card=") {
				fields := strings.Fields(line[2:])
				res := FoldResult{}
				for _, f := range fields {
					k, v, ok := strings.Cut(f, "=")
					if ok {
						switch k {
						case "card":
							res.Card = v
						case "verdict":
							res.Verdict = v
						}
					}
				}
				r.Folds.Results = append(r.Folds.Results, res)
			} else if k, v, ok := strings.Cut(line, ": "); ok {
				n, _ := strconv.Atoi(v)
				switch k {
				case "total":
					r.Folds.Total = n
				case "clean":
					r.Folds.Clean = n
				case "defect":
					r.Folds.Defect = n
				case "failed":
					r.Folds.Failed = n
				case "skipped":
					r.Folds.Skipped = n
				}
			}
		case "Merge Bases":
			if k, v, ok := strings.Cut(line, ": "); ok {
				switch k {
				case "repo":
					r.MergeBase.Repo = v
				case "base":
					r.MergeBase.Base = v
				case "head_sha":
					r.MergeBase.HeadSHA = v
				case "base_sha":
					r.MergeBase.BaseSHA = v
				case "merge_base_sha":
					r.MergeBase.MergeBaseSHA = v
				case "status":
					r.MergeBase.Status = v
				case "decision":
					r.MergeBase.Decision = v
				}
			}
		case "Durable Loops":
			if strings.HasPrefix(line, "- loop=") {
				fields := strings.Fields(line[2:])
				st := LoopStatus{}
				for _, f := range fields {
					k, v, ok := strings.Cut(f, "=")
					if ok {
						switch k {
						case "loop":
							st.Name = v
						case "status":
							st.Alive = (v == "alive")
						case "pid":
							st.PID, _ = strconv.Atoi(v)
						}
					}
				}
				// Support multi-word names like "sprint table" if formatted with spaces or dashes.
				if strings.Contains(line, "loop=sprint table") {
					st.Name = "sprint table"
				}
				r.Loops = append(r.Loops, st)
			}
		case "Decisions":
			if strings.HasPrefix(line, "- ") {
				r.Decisions = append(r.Decisions, strings.TrimPrefix(line, "- "))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

// defaultLoopChecker checks whether durable loops are active in the host's process table.
func defaultLoopChecker(loops []string) []LoopStatus {
	procs := readHostProcesses()
	statuses := make([]LoopStatus, len(loops))

	for i, loop := range loops {
		normalized := strings.ToLower(strings.TrimSpace(loop))
		var matchedPID int
		var matchedCmd string
		alive := false

		for _, p := range procs {
			cmdLower := strings.ToLower(p.command)
			match := false
			switch normalized {
			case "dealer":
				match = strings.Contains(cmdLower, "dealer") ||
					strings.Contains(cmdLower, "pulse launch") ||
					strings.Contains(cmdLower, "pulse fill") ||
					strings.Contains(cmdLower, "fill-loop")
			case "backpressure":
				match = strings.Contains(cmdLower, "backpressure") ||
					strings.Contains(cmdLower, "swarm pull") ||
					strings.Contains(cmdLower, "pull-loop")
			case "harvest":
				match = strings.Contains(cmdLower, "harvest") ||
					strings.Contains(cmdLower, "harvest-loop")
			case "sprint table", "sprint-table", "table":
				match = strings.Contains(cmdLower, "sprint table") ||
					strings.Contains(cmdLower, "sprint-table") ||
					strings.Contains(cmdLower, "sprint_table") ||
					strings.Contains(cmdLower, "table-loop")
			default:
				match = strings.Contains(cmdLower, normalized)
			}

			if match {
				alive = true
				matchedPID = p.pid
				matchedCmd = p.command
				break
			}
		}

		if alive {
			statuses[i] = LoopStatus{
				Name:    loop,
				Alive:   true,
				PID:     matchedPID,
				Command: matchedCmd,
				Details: "running",
			}
		} else {
			statuses[i] = LoopStatus{
				Name:    loop,
				Alive:   false,
				PID:     0,
				Details: "not running",
			}
		}
	}
	return statuses
}

type keeperProcess struct {
	pid     int
	command string
}

func readHostProcesses() []keeperProcess {
	if runtime.GOOS == "windows" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return nil
	}
	myPID := os.Getpid()
	var list []keeperProcess
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		pid, perr := strconv.Atoi(fields[0])
		if perr != nil || pid == myPID {
			continue
		}
		cmd := strings.Join(fields[1:], " ")
		// Exclude ps command itself.
		if strings.HasPrefix(cmd, "ps -axo") {
			continue
		}
		list = append(list, keeperProcess{pid: pid, command: cmd})
	}
	return list
}

// defaultBaseChecker compares the git merge-base of HEAD against the target base.
func defaultBaseChecker(repo, base string) (MergeBaseStatus, error) {
	if strings.TrimSpace(repo) == "" {
		repo = "."
	}
	if strings.TrimSpace(base) == "" {
		base = "origin/dev"
	}

	st := MergeBaseStatus{
		Repo:   repo,
		Base:   base,
		Status: "unknown",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	headOut, err := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		st.Status = "error"
		st.Decision = fmt.Sprintf("ERROR: git rev-parse HEAD failed: %v", err)
		return st, err
	}
	st.HeadSHA = strings.TrimSpace(string(headOut))

	baseOut, err := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", base).Output()
	if err != nil {
		// If base was e.g. "origin/dev" and remote ref is missing, try local branch.
		trimmed := strings.TrimPrefix(base, "origin/")
		if baseOut2, err2 := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", trimmed).Output(); err2 == nil {
			st.BaseSHA = strings.TrimSpace(string(baseOut2))
			base = trimmed
			st.Base = trimmed
		} else {
			st.Status = "error"
			st.Decision = fmt.Sprintf("ERROR: base ref %q not found: %v", base, err)
			return st, err
		}
	} else {
		st.BaseSHA = strings.TrimSpace(string(baseOut))
	}

	mbOut, err := exec.CommandContext(ctx, "git", "-C", repo, "merge-base", base, "HEAD").Output()
	if err != nil {
		st.Status = "error"
		st.Decision = fmt.Sprintf("ERROR: git merge-base failed: %v", err)
		return st, err
	}
	st.MergeBaseSHA = strings.TrimSpace(string(mbOut))

	switch {
	case st.MergeBaseSHA == st.BaseSHA && st.MergeBaseSHA == st.HeadSHA:
		st.Status = "aligned"
		st.Decision = "OK: head is aligned with base"
	case st.MergeBaseSHA == st.BaseSHA && st.MergeBaseSHA != st.HeadSHA:
		st.Status = "ahead"
		st.Decision = "OK: head is ahead of base"
	case st.MergeBaseSHA == st.HeadSHA && st.MergeBaseSHA != st.BaseSHA:
		st.Status = "behind"
		st.Decision = "WARN: head is behind base; rebase or fast-forward recommended"
	default:
		st.Status = "diverged"
		st.Decision = "WARN: head and base have diverged; rebase required"
	}

	return st, nil
}

// defaultResultFolder folds finished card results from queue/launched into queue/done or queue/failed.
func defaultResultFolder(queue, roots string) (FoldSummary, error) {
	summary := FoldSummary{}
	launchedDir := filepath.Join(queue, "launched")
	doneDir := filepath.Join(queue, "done")
	failedDir := filepath.Join(queue, "failed")

	entries, err := os.ReadDir(launchedDir)
	if err != nil {
		if os.IsNotExist(err) {
			// If launched directory doesn't exist, count any existing cards in done/failed.
			if dEntries, errD := os.ReadDir(doneDir); errD == nil {
				for _, de := range dEntries {
					if !de.IsDir() && strings.HasSuffix(de.Name(), ".md") {
						summary.Total++
						summary.Clean++
					}
				}
			}
			if fEntries, errF := os.ReadDir(failedDir); errF == nil {
				for _, fe := range fEntries {
					if !fe.IsDir() && strings.HasSuffix(fe.Name(), ".md") {
						summary.Total++
						summary.Failed++
					}
				}
			}
			return summary, nil
		}
		return summary, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		cardName := entry.Name()
		cardPath := filepath.Join(launchedDir, cardName)

		// Look for RESULT.md or result file.
		cardBase := strings.TrimSuffix(cardName, ".md")
		resultFile := filepath.Join(queue, "results", cardName, "RESULT.md")
		if _, err := os.Stat(resultFile); err != nil {
			resultFile = filepath.Join(queue, "results", cardBase, "RESULT.md")
			if _, err := os.Stat(resultFile); err != nil {
				resultFile = filepath.Join(launchedDir, cardBase+".result")
			}
		}

		raw, err := os.ReadFile(resultFile)
		if err != nil {
			// Card is still active in launched/ without a completed result.
			// Do not infer verdict from card text or move the card.
			continue
		}

		verdict := "failed"
		text := strings.ToLower(string(raw))
		if strings.Contains(text, "clean") {
			verdict = "clean"
			summary.Clean++
		} else if strings.Contains(text, "defect") {
			verdict = "defect"
			summary.Defect++
		} else if strings.Contains(text, "skip") {
			verdict = "skip"
			summary.Skipped++
		} else if strings.Contains(text, "result") && strings.Contains(text, "pass") {
			verdict = "clean"
			summary.Clean++
		} else {
			summary.Failed++
		}

		summary.Total++
		summary.Results = append(summary.Results, FoldResult{
			Card:    cardName,
			Verdict: verdict,
		})

		// Move to done or failed.
		_ = os.MkdirAll(doneDir, 0o755)
		_ = os.MkdirAll(failedDir, 0o755)
		targetDir := doneDir
		if verdict == "failed" {
			targetDir = failedDir
		}
		if err := os.Rename(cardPath, filepath.Join(targetDir, cardName)); err != nil {
			return summary, fmt.Errorf("failed to move folded card %s: %w", cardName, err)
		}
	}

	return summary, nil
}
