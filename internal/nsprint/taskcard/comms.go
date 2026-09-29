package taskcard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// TellRequest is the input to Tell (#4351 Item B).
type TellRequest struct {
	CopyID string    // copy id, e.g. "task-123~1"
	Text   string    // message text to append to inbox stream
	By     string    // optional sender/actor
	At     time.Time // timestamp; time.Now() when zero
}

// TellResult is the receipt of a successful Tell.
type TellResult struct {
	CopyID    string
	Text      string
	Streams   []string // streams written to: "copy:<id>:tell" and "copy:<id>:inbox"
	MessageID string
	LastTold  int64
}

// Tell appends a message to the running copy's inbox stream (copy:<id>:tell and copy:<id>:inbox)
// in Redis so iteration is a direct message, not a redeal (#4351 Item B).
// It updates the copy's metadata last_told timestamp and last_told_text on copy:<id>.
func Tell(ctx context.Context, c redis.Cmdable, req TellRequest) (*TellResult, error) {
	if req.CopyID == "" {
		return nil, &Refused{Why: "tell: --id is required"}
	}
	if req.Text == "" {
		return nil, &Refused{Why: "tell: --text is required"}
	}
	rec, err := c.HGetAll(ctx, Key(req.CopyID)).Result()
	if err != nil {
		return nil, err
	}
	cpRec, _ := c.HGetAll(ctx, "copy:"+req.CopyID).Result()
	if len(rec) == 0 && len(cpRec) == 0 {
		return nil, &Refused{Why: fmt.Sprintf("no copy task:%s", req.CopyID)}
	}
	where := rec["where"]
	if where == "" && cpRec != nil {
		where = cpRec["outcome"]
	}
	if where == "" {
		return nil, &Refused{Why: fmt.Sprintf("copy %s has no where status", req.CopyID)}
	}
	if where != "working" {
		return nil, &Refused{Why: fmt.Sprintf("copy %s is %s, not running", req.CopyID, where)}
	}

	at := req.At
	if at.IsZero() {
		at = time.Now()
	}
	ms := at.UnixMilli()
	msStr := strconv.FormatInt(ms, 10)

	s1 := fmt.Sprintf("copy:%s:tell", req.CopyID)
	s2 := fmt.Sprintf("copy:%s:inbox", req.CopyID)

	values := map[string]interface{}{
		"text": req.Text,
		"from": req.By,
		"at":   ms,
	}

	pipe := c.Pipeline()
	cmd1 := pipe.XAdd(ctx, &redis.XAddArgs{Stream: s1, Values: values})
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: s2, Values: values})
	pipe.HSet(ctx, "copy:"+req.CopyID,
		"last_told", msStr,
		"last_told_text", req.Text,
	)

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("tell: %w", err)
	}

	return &TellResult{
		CopyID:    req.CopyID,
		Text:      req.Text,
		Streams:   []string{s1, s2},
		MessageID: cmd1.Val(),
		LastTold:  ms,
	}, nil
}

// CardReport holds the details printed by `card report --id <copy>` (#4351 Item C).
type CardReport struct {
	CopyID       string
	PrimaryID    string
	Consumer     string
	State        string
	Model        string
	Child        string
	Branch       string
	Head         string
	PRNumber     string
	PRURL        string
	PRTitle      string
	CIStatus     string
	CIFailing    []string
	ReadScores   []string
	ResultMD     string
	HasResultMD  bool
	LastTold     string
	LastToldText string
	WhereAt      string
}

// Render formats the report as a clean summary screen extending `read brief`.
func (r *CardReport) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "== CARD REPORT: %s\n", r.CopyID)
	if r.PrimaryID != "" {
		fmt.Fprintf(&b, "PRIMARY:   %s\n", r.PrimaryID)
	}
	if r.Consumer != "" {
		fmt.Fprintf(&b, "CONSUMER:  %s\n", r.Consumer)
	}
	if r.State != "" {
		fmt.Fprintf(&b, "STATE:     %s\n", r.State)
	}
	if r.Model != "" {
		fmt.Fprintf(&b, "MODEL:     %s\n", r.Model)
	}
	if r.Child != "" {
		fmt.Fprintf(&b, "CHILD:     %s\n", r.Child)
	}
	if r.Branch != "" {
		fmt.Fprintf(&b, "BRANCH:    %s\n", r.Branch)
	}
	if r.Head != "" {
		fmt.Fprintf(&b, "HEAD:      %s\n", r.Head)
	}
	if r.LastTold != "" {
		toldLine := r.LastTold
		if r.LastToldText != "" {
			toldLine += fmt.Sprintf(" (%q)", r.LastToldText)
		}
		fmt.Fprintf(&b, "LAST-TOLD: %s\n", toldLine)
	}

	b.WriteString("== PR\n")
	if r.PRNumber != "" {
		titleSuffix := ""
		if r.PRTitle != "" {
			titleSuffix = fmt.Sprintf(" (%s)", r.PRTitle)
		}
		prURL := r.PRURL
		if prURL == "" {
			prURL = "#" + r.PRNumber
		} else {
			prURL = fmt.Sprintf("#%s: %s", r.PRNumber, prURL)
		}
		fmt.Fprintf(&b, "%s%s\n", prURL, titleSuffix)
	} else {
		b.WriteString("none\n")
	}

	b.WriteString("== CI\n")
	if r.CIStatus != "" {
		ciLine := fmt.Sprintf("STATUS: %s", r.CIStatus)
		if len(r.CIFailing) > 0 {
			ciLine += fmt.Sprintf(" (FAILING: %s)", strings.Join(r.CIFailing, ", "))
		}
		fmt.Fprintf(&b, "%s\n", ciLine)
	} else {
		b.WriteString("STATUS: -\n")
	}

	b.WriteString("== READ SCORES\n")
	if len(r.ReadScores) > 0 {
		for _, s := range r.ReadScores {
			fmt.Fprintf(&b, "%s\n", s)
		}
	} else {
		b.WriteString("none\n")
	}

	b.WriteString("== RESULT.md\n")
	if r.ResultMD != "" {
		b.WriteString(r.ResultMD)
		if !strings.HasSuffix(r.ResultMD, "\n") {
			b.WriteByte('\n')
		}
	} else {
		b.WriteString("(no RESULT.md)\n")
	}

	return b.String()
}

// Report collects report data for copyID from Redis and on-disk files (#4351 Item C).
func Report(ctx context.Context, c redis.Cmdable, copyID string) (*CardReport, error) {
	if copyID == "" {
		return nil, &Refused{Why: "report: --id is required"}
	}
	rec, err := c.HGetAll(ctx, Key(copyID)).Result()
	if err != nil {
		return nil, err
	}
	cpRec, _ := c.HGetAll(ctx, "copy:"+copyID).Result()
	if len(rec) == 0 && len(cpRec) == 0 {
		return nil, &Refused{Why: fmt.Sprintf("no copy task:%s", copyID)}
	}

	rep := &CardReport{
		CopyID:       copyID,
		PrimaryID:    valOr(rec["primary"], cpRec["primary"]),
		Consumer:     valOr(rec["consumer"], cpRec["consumer"]),
		State:        valOr(rec["where"], cpRec["outcome"]),
		Model:        valOr(rec["model"], cpRec["model"]),
		Child:        valOr(valOr(rec["child"], cpRec["child"]), cpRec["pid"]),
		Branch:       valOr(rec["branch"], cpRec["branch"]),
		Head:         valOr(valOr(rec["commit"], rec["head"]), valOr(cpRec["commit"], cpRec["head"])),
		PRNumber:     valOr(rec["pr"], cpRec["pr"]),
		LastTold:     valOr(cpRec["last_told"], rec["last_told"]),
		LastToldText: cpRec["last_told_text"],
	}
	repo := valOr(rec["repo"], cpRec["repo"])

	// Check primary if PR or head or repo was not set on copy
	if rep.PrimaryID != "" && (rep.PRNumber == "" || rep.Head == "" || repo == "") {
		primRec, _ := c.HGetAll(ctx, Key(rep.PrimaryID)).Result()
		if rep.PRNumber == "" {
			rep.PRNumber = primRec["pr"]
		}
		if rep.Head == "" {
			rep.Head = primRec["commit"]
			if rep.Head == "" {
				rep.Head = primRec["head"]
			}
		}
		if repo == "" {
			repo = primRec["repo"]
		}
	}

	// Format PR info
	if rep.PRNumber != "" {
		prNum := strings.TrimPrefix(rep.PRNumber, "#")
		if strings.Contains(prNum, "#") {
			parts := strings.Split(prNum, "#")
			if len(parts) >= 2 {
				repo = parts[0]
				prNum = parts[1]
			}
		}
		rep.PRNumber = prNum
		if repo != "" {
			rep.PRURL = fmt.Sprintf("https://github.com/%s/pull/%s", repo, prNum)
		}

		// Read pr:<repo>:<n>
		shortRepo := repo
		if idx := strings.LastIndex(shortRepo, "/"); idx >= 0 {
			shortRepo = shortRepo[idx+1:]
		}
		prHash, _ := c.HGetAll(ctx, "pr:"+shortRepo+":"+prNum).Result()
		if len(prHash) == 0 && repo != "" {
			prHash, _ = c.HGetAll(ctx, "pr:"+repo+":"+prNum).Result()
		}
		if len(prHash) > 0 {
			rep.PRTitle = prHash["pr_title"]
			if reads := prHash["reads"]; reads != "" {
				for _, line := range strings.Split(reads, "\n") {
					if line = strings.TrimSpace(line); line != "" {
						rep.ReadScores = append(rep.ReadScores, line)
					}
				}
			}
		}
	}

	// Read CI status
	head := rep.Head
	if head != "" {
		ciHash, _ := c.HGetAll(ctx, "ci:"+head).Result()
		if len(ciHash) == 0 && repo != "" {
			shortRepo := repo
			if idx := strings.LastIndex(shortRepo, "/"); idx >= 0 {
				shortRepo = shortRepo[idx+1:]
			}
			ciHash, _ = c.HGetAll(ctx, "ci:"+shortRepo+":"+head).Result()
		}
		if len(ciHash) > 0 {
			rep.CIStatus = ciHash["verdict"]
			if rep.CIStatus == "" {
				rep.CIStatus = ciHash["status"]
			}
			if failing := ciHash["failing"]; failing != "" {
				rep.CIFailing = strings.Split(failing, ",")
			}
		}
	}
	if rep.CIStatus == "" && rec["ci"] != "" {
		rep.CIStatus = rec["ci"]
	}

	// Check copy's own score if read copy
	if score := rec["score"]; score != "" {
		finding := rec["finding"]
		sc := fmt.Sprintf("SCORE %s", score)
		if finding != "" {
			sc += fmt.Sprintf(" finding=%s", finding)
		}
		rep.ReadScores = append(rep.ReadScores, sc)
	}

	// Load RESULT.md
	if resMD := rec["result_md"]; resMD != "" {
		rep.ResultMD = resMD
		rep.HasResultMD = true
	} else if res := rec["result"]; res != "" {
		rep.ResultMD = res
		rep.HasResultMD = true
	} else if res := cpRec["result"]; res != "" {
		rep.ResultMD = res
		rep.HasResultMD = true
	} else {
		// Try reading from on-disk directory
		wtDir := valOr(rec["worktree"], cpRec["worktree"])
		dirs := []string{wtDir, rec["results"], rec["jobdir"], rec["results_dir"]}
		for _, dir := range dirs {
			if dir == "" {
				continue
			}
			candidates := []string{
				filepath.Join(dir, "RESULT.md"),
				filepath.Join(dir, "out", "RESULT.md"),
			}
			for _, path := range candidates {
				if content, err := os.ReadFile(path); err == nil && len(content) > 0 {
					rep.ResultMD = string(content)
					rep.HasResultMD = true
					break
				}
			}
			if rep.HasResultMD {
				break
			}
		}
	}
	if !rep.HasResultMD && (rec["line1"] != "" || rec["line2"] != "") {
		var lines []string
		if rec["line1"] != "" {
			lines = append(lines, rec["line1"])
		}
		if rec["line2"] != "" {
			lines = append(lines, rec["line2"])
		}
		rep.ResultMD = strings.Join(lines, "\n")
		rep.HasResultMD = true
	}

	return rep, nil
}

// ListCopiesOptions specifies filter options for ListCopies (#4351 Item E).
type ListCopiesOptions struct {
	Consumer string    // e.g. "friend:worker"
	Live     bool      // true = only working set
	Epoch    uint64    // sprint epoch
	Now      time.Time // current time
}

// CopyListing is one copy's summary in `card ls`.
type CopyListing struct {
	ID       string
	Primary  string
	State    string
	Model    string
	Child    string
	Age      string
	LastBeat string
	PR       string
	CIStatus string
}

// Line formats the copy listing as a single line for card ls.
func (c CopyListing) Line() string {
	return fmt.Sprintf("COPY %s primary=%s state=%s model=%s child=%s age=%s beat=%s pr=%s ci=%s",
		c.ID, c.Primary, c.State, c.Model, c.Child, c.Age, c.LastBeat, c.PR, c.CIStatus)
}

// ListCopies lists a friend's copies with model, child PID, age, last beat, PR, and CI (#4351 Item E).
func ListCopies(ctx context.Context, c redis.Cmdable, opts ListCopiesOptions) ([]CopyListing, error) {
	if opts.Consumer == "" {
		return nil, &Refused{Why: "ls: --as is required"}
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	sets := []string{"working"}
	if !opts.Live {
		sets = append(sets, "ready", "ok", "fail")
	}

	epoch := opts.Epoch
	if epoch == 0 {
		epoch, _ = ws.Epoch(ctx, c)
	}

	pipe := c.Pipeline()
	var cmds []*redis.StringSliceCmd
	for _, s := range sets {
		key := ws.ConsumerKeyAt(epoch, opts.Consumer, s)
		cmds = append(cmds, pipe.ZRange(ctx, key, 0, -1))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("card ls: read sets: %w", err)
	}

	type copyRef struct {
		id    string
		state string
	}
	var refs []copyRef
	for i, cmd := range cmds {
		st := sets[i]
		for _, id := range cmd.Val() {
			refs = append(refs, copyRef{id: id, state: st})
		}
	}

	if len(refs) == 0 {
		return nil, nil
	}

	pipe = c.Pipeline()
	var taskCmds []*redis.MapStringStringCmd
	var copyCmds []*redis.MapStringStringCmd
	for _, r := range refs {
		taskCmds = append(taskCmds, pipe.HGetAll(ctx, Key(r.id)))
		copyCmds = append(copyCmds, pipe.HGetAll(ctx, "copy:"+r.id))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("card ls: read copy tasks: %w", err)
	}

	out := make([]CopyListing, len(refs))
	for i, r := range refs {
		rec := taskCmds[i].Val()
		cpRec := copyCmds[i].Val()
		item := CopyListing{
			ID:       r.id,
			Primary:  valOr(rec["primary"], cpRec["primary"]),
			State:    r.state,
			Model:    valOr(rec["model"], valOr(cpRec["model"], "-")),
			Child:    valOr(valOr(rec["child"], cpRec["child"]), valOr(cpRec["pid"], "-")),
			PR:       valOr(rec["pr"], valOr(cpRec["pr"], "-")),
			CIStatus: valOr(rec["ci"], valOr(cpRec["ci"], "-")),
		}
		if item.PR != "-" && !strings.HasPrefix(item.PR, "#") {
			item.PR = "#" + item.PR
		}

		var tStart int64
		for _, key := range []string{"leased_at", "where_at", "launched_at", "created_at"} {
			vStr := rec[key]
			if vStr == "" {
				vStr = cpRec[key]
			}
			if v, err := strconv.ParseInt(vStr, 10, 64); err == nil && v > 0 {
				tStart = v
				break
			}
		}
		if tStart > 0 {
			ageDur := now.Sub(time.UnixMilli(tStart))
			item.Age = formatCompactDuration(ageDur)
		} else {
			item.Age = "-"
		}

		bStr := valOr(rec["beat_at"], cpRec["beat_at"])
		if bStr != "" {
			if bMs, err := strconv.ParseInt(bStr, 10, 64); err == nil && bMs > 0 {
				beatDur := now.Sub(time.UnixMilli(bMs))
				item.LastBeat = formatCompactDuration(beatDur)
			} else {
				item.LastBeat = "-"
			}
		} else {
			item.LastBeat = "-"
		}

		out[i] = item
	}

	return out, nil
}

func valOr(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func formatCompactDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
