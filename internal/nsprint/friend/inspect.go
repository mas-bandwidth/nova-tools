// Package friend inspect implements the joy lens on friends (nova-tools #4356 Item E):
// friend ls and friend show, reading friend presence, desired slots, working
// copies with age, and recent receipts in a single pipelined round-trip.
package friend

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/beat"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// WorkingCopySummary is one working copy held by a friend.
type WorkingCopySummary struct {
	ID       string
	Primary  string
	Stream   string
	Leg      string // "work", "read", "fix", etc.
	Attempt  int
	Title    string
	Model    string
	LeasedAt time.Time
	Age      time.Duration
}

// ReceiptSummary is one completed task or review receipt.
type ReceiptSummary struct {
	ID      string
	Primary string
	Stream  string
	Leg     string // "task" (work/fix) or "review" (read)
	Outcome string // "OK" or "FAIL"
	Why     string // verdict, score, or refusal reason
	PR      string
	EndedAt time.Time
	Age     time.Duration
}

// FriendSummary is one friend's status as read in one pipelined round trip.
type FriendSummary struct {
	Name        string
	Status      string // "up", "down", "out-of-credits", "away", "paused", "offline-model", "wake-missed"
	Harness     string
	Host        string
	Session     string
	Models      string
	SlotsTotal  int
	SlotsFree   int
	SlotsUsed   int
	Working     []WorkingCopySummary
	LastBeatAt  time.Time
	BeatAge     time.Duration
	HasBeat     bool
	Credits     string
	Quota       string
	State       string
	StateReason string
	ResetUntil  time.Time
	Load        string
}

// CreditsQuotaString returns a concise summary of credits and quota where known.
func (s FriendSummary) CreditsQuotaString() string {
	var parts []string
	if s.Credits != "" {
		parts = append(parts, "credits="+s.Credits)
	}
	if s.Quota != "" {
		parts = append(parts, "quota="+s.Quota)
	}
	if !s.ResetUntil.IsZero() {
		now := time.Now()
		if s.ResetUntil.After(now) {
			rem := s.ResetUntil.Sub(now).Truncate(time.Second)
			parts = append(parts, fmt.Sprintf("resets in %s (%s)", rem, s.ResetUntil.UTC().Format("15:04Z")))
		} else {
			parts = append(parts, "reset passed")
		}
	} else if s.Status == StateOutOfCredits {
		parts = append(parts, "out-of-credits")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

// WorkingCopiesString returns a concise one-line summary of working copies with age.
func (s FriendSummary) WorkingCopiesString() string {
	if len(s.Working) == 0 {
		return "-"
	}
	items := make([]string, 0, len(s.Working))
	for _, w := range s.Working {
		label := w.ID
		if len(label) > 30 {
			label = label[:27] + "..."
		}
		items = append(items, fmt.Sprintf("%s (%s)", label, formatDuration(w.Age)))
	}
	if len(items) > 3 {
		return strings.Join(items[:2], ", ") + fmt.Sprintf(", +%d more", len(items)-2)
	}
	return strings.Join(items, ", ")
}

// LastBeatString returns a formatted age for the last beat, e.g. "4s ago".
func (s FriendSummary) LastBeatString() string {
	if !s.HasBeat {
		return "-"
	}
	return formatDuration(s.BeatAge) + " ago"
}

// SlotsString formats slots as free/used (total).
func (s FriendSummary) SlotsString() string {
	return fmt.Sprintf("%d/%d", s.SlotsFree, s.SlotsUsed)
}

// FriendDetail is one friend's detailed view for friend show.
type FriendDetail struct {
	FriendSummary
	Machine  string
	Paused   bool
	Tiers    string
	Receipts []ReceiptSummary
}

type friendCmds struct {
	beat     *redis.MapStringStringCmd
	desired  *redis.MapStringStringCmd
	state    *redis.MapStringStringCmd
	down     *redis.IntCmd
	downVal  *redis.StringCmd
	workingZ *redis.ZSliceCmd
}

// List reads all friends (or the named friends when names is not empty) from
// Redis in a single pipelined round-trip.
func List(ctx context.Context, c redis.Cmdable, now time.Time, names ...string) ([]FriendSummary, error) {
	if c == nil {
		return nil, errors.New("friend ls: nil redis client")
	}
	if len(names) == 0 {
		var err error
		names, err = c.SMembers(ctx, "friends").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("friend ls: smembers friends: %w", err)
		}
		sort.Strings(names)
	}
	if len(names) == 0 {
		return nil, nil
	}

	epoch, _ := ws.Epoch(ctx, c)

	pipe := c.Pipeline()
	cmds := make([]friendCmds, len(names))
	for i, f := range names {
		cmds[i] = friendCmds{
			beat:     pipe.HGetAll(ctx, beat.Key(f)),
			desired:  pipe.HGetAll(ctx, "friend:"+f+":desired"),
			state:    pipe.HGetAll(ctx, StateKey(f)),
			down:     pipe.Exists(ctx, "friend:"+f+":down"),
			downVal:  pipe.Get(ctx, "friend:"+f+":down"),
			workingZ: pipe.ZRevRangeWithScores(ctx, ws.ConsumerKeyAt(epoch, "friend:"+f, "working"), 0, -1),
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("friend ls: read pipeline: %w", err)
	}

	out := make([]FriendSummary, len(names))
	for i, f := range names {
		cmd := cmds[i]
		bMap := cmd.beat.Val()
		dMap := cmd.desired.Val()
		sMap := cmd.state.Val()

		summary := FriendSummary{
			Name:    f,
			Harness: bMap["harness"],
			Host:    bMap["host"],
			Session: bMap["session"],
			Models:  bMap["models"],
			Credits: bMap["credits"],
			Quota:   bMap["quota"],
			Load:    bMap["load1"],
		}

		atStr := bMap["at"]
		if beatTime, ok := beat.At(atStr); ok {
			summary.HasBeat = true
			summary.LastBeatAt = beatTime
			d := now.Sub(beatTime)
			if d < 0 {
				d = 0
			}
			summary.BeatAge = d
		}
		isUp := summary.HasBeat && beat.Up(atStr, now)

		slotsTotal, _ := strconv.Atoi(dMap["slots"])
		summary.SlotsTotal = slotsTotal
		isPaused := dMap["paused"] == "1"

		stateVal := sMap["state"]
		summary.State = stateVal
		summary.StateReason = sMap["reason"]
		if untilMS, err := strconv.ParseInt(sMap["until"], 10, 64); err == nil && untilMS > 0 {
			summary.ResetUntil = time.UnixMilli(untilMS)
		}

		isDownKey := cmd.down.Val() > 0
		downReason := cmd.downVal.Val()
		if isDownKey && summary.StateReason == "" && downReason != "" {
			summary.StateReason = downReason
		}

		// Calculate status: one beat per friend so down: beat cannot disagree with a running row.
		switch {
		case !isUp:
			summary.Status = StateDown
			if stateVal == StateOutOfCredits || strings.HasPrefix(downReason, "out-of-credits") {
				summary.Status = StateOutOfCredits
			} else if stateVal == StateAway || strings.HasPrefix(downReason, "away") {
				summary.Status = StateAway
			} else if stateVal == StateOfflineModel {
				summary.Status = StateOfflineModel
			} else if stateVal == StateWakeMissed {
				summary.Status = StateWakeMissed
			}
		case isDownKey:
			summary.Status = StateDown
			if strings.HasPrefix(downReason, "out-of-credits") || stateVal == StateOutOfCredits {
				summary.Status = StateOutOfCredits
			} else if strings.HasPrefix(downReason, "away") || stateVal == StateAway {
				summary.Status = StateAway
			}
		case stateVal == StateOutOfCredits:
			summary.Status = StateOutOfCredits
		case stateVal == StateAway:
			summary.Status = StateAway
		case stateVal == StateOfflineModel:
			summary.Status = StateOfflineModel
		case stateVal == StateWakeMissed:
			summary.Status = StateWakeMissed
		case stateVal == StateDown:
			summary.Status = StateDown
		case isPaused:
			summary.Status = "paused"
		default:
			summary.Status = StateUp
		}

		// Working copies from the ZSET
		wzs := cmd.workingZ.Val()
		summary.SlotsUsed = len(wzs)
		summary.SlotsFree = summary.SlotsTotal - summary.SlotsUsed
		if summary.SlotsFree < 0 {
			summary.SlotsFree = 0
		}

		for _, z := range wzs {
			cid := fmt.Sprint(z.Member)
			prim := taskcard.PrimaryOf(cid)
			if prim == "" {
				prim = cid
			}
			leasedAt := time.UnixMilli(int64(z.Score))
			age := now.Sub(leasedAt)
			if age < 0 {
				age = 0
			}
			summary.Working = append(summary.Working, WorkingCopySummary{
				ID:       cid,
				Primary:  prim,
				Attempt:  parseAttempt(cid),
				LeasedAt: leasedAt,
				Age:      age,
			})
		}

		out[i] = summary
	}

	return out, nil
}

// Show reads the detailed status of a specific friend, including working copies
// and recent receipts.
func Show(ctx context.Context, c redis.Cmdable, friend string, now time.Time) (*FriendDetail, error) {
	if c == nil {
		return nil, errors.New("friend show: nil redis client")
	}
	friend = strings.TrimSpace(friend)
	if friend == "" {
		return nil, errors.New("friend show: friend name is required")
	}

	registered, err := c.SIsMember(ctx, "friends", friend).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("friend show %s: check registered: %w", friend, err)
	}
	if !registered {
		return nil, fmt.Errorf("unregistered friend %q", friend)
	}

	epoch, _ := ws.Epoch(ctx, c)

	// Pipeline 1: Friend metadata and set ranges
	pipe := c.Pipeline()
	beatCmd := pipe.HGetAll(ctx, beat.Key(friend))
	desiredCmd := pipe.HGetAll(ctx, "friend:"+friend+":desired")
	stateCmd := pipe.HGetAll(ctx, StateKey(friend))
	downCmd := pipe.Exists(ctx, "friend:"+friend+":down")
	downValCmd := pipe.Get(ctx, "friend:"+friend+":down")
	workingCmd := pipe.ZRevRangeWithScores(ctx, ws.ConsumerKeyAt(epoch, "friend:"+friend, "working"), 0, -1)
	okCmd := pipe.ZRevRangeWithScores(ctx, ws.ConsumerKeyAt(epoch, "friend:"+friend, "ok"), 0, 19)
	failCmd := pipe.ZRevRangeWithScores(ctx, ws.ConsumerKeyAt(epoch, "friend:"+friend, "fail"), 0, 19)

	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("friend show %s: read pipeline: %w", friend, err)
	}

	bMap := beatCmd.Val()
	dMap := desiredCmd.Val()
	sMap := stateCmd.Val()

	summary := FriendSummary{
		Name:    friend,
		Harness: bMap["harness"],
		Host:    bMap["host"],
		Session: bMap["session"],
		Models:  bMap["models"],
		Credits: bMap["credits"],
		Quota:   bMap["quota"],
		Load:    bMap["load1"],
	}

	atStr := bMap["at"]
	if beatTime, ok := beat.At(atStr); ok {
		summary.HasBeat = true
		summary.LastBeatAt = beatTime
		d := now.Sub(beatTime)
		if d < 0 {
			d = 0
		}
		summary.BeatAge = d
	}
	isUp := summary.HasBeat && beat.Up(atStr, now)

	slotsTotal, _ := strconv.Atoi(dMap["slots"])
	summary.SlotsTotal = slotsTotal
	isPaused := dMap["paused"] == "1"

	stateVal := sMap["state"]
	summary.State = stateVal
	summary.StateReason = sMap["reason"]
	if untilMS, err := strconv.ParseInt(sMap["until"], 10, 64); err == nil && untilMS > 0 {
		summary.ResetUntil = time.UnixMilli(untilMS)
	}

	isDownKey := downCmd.Val() > 0
	downReason := downValCmd.Val()
	if isDownKey && summary.StateReason == "" && downReason != "" {
		summary.StateReason = downReason
	}

	switch {
	case !isUp:
		summary.Status = StateDown
		if stateVal == StateOutOfCredits || strings.HasPrefix(downReason, "out-of-credits") {
			summary.Status = StateOutOfCredits
		} else if stateVal == StateAway || strings.HasPrefix(downReason, "away") {
			summary.Status = StateAway
		} else if stateVal == StateOfflineModel {
			summary.Status = StateOfflineModel
		} else if stateVal == StateWakeMissed {
			summary.Status = StateWakeMissed
		}
	case isDownKey:
		summary.Status = StateDown
		if strings.HasPrefix(downReason, "out-of-credits") || stateVal == StateOutOfCredits {
			summary.Status = StateOutOfCredits
		} else if strings.HasPrefix(downReason, "away") || stateVal == StateAway {
			summary.Status = StateAway
		}
	case stateVal == StateOutOfCredits:
		summary.Status = StateOutOfCredits
	case stateVal == StateAway:
		summary.Status = StateAway
	case stateVal == StateOfflineModel:
		summary.Status = StateOfflineModel
	case stateVal == StateWakeMissed:
		summary.Status = StateWakeMissed
	case stateVal == StateDown:
		summary.Status = StateDown
	case isPaused:
		summary.Status = "paused"
	default:
		summary.Status = StateUp
	}

	detail := &FriendDetail{
		FriendSummary: summary,
		Machine:       dMap["machine"],
		Paused:        isPaused,
		Tiers:         dMap["tiers"],
	}

	wzs := workingCmd.Val()
	detail.SlotsUsed = len(wzs)
	detail.SlotsFree = detail.SlotsTotal - detail.SlotsUsed
	if detail.SlotsFree < 0 {
		detail.SlotsFree = 0
	}

	okzs := okCmd.Val()
	failzs := failCmd.Val()

	// Collect all task IDs to inspect records in Pipeline 2
	type taskTarget struct {
		id      string
		isWork  bool
		outcome string
		timeMS  int64
	}
	var targets []taskTarget
	for _, z := range wzs {
		targets = append(targets, taskTarget{id: fmt.Sprint(z.Member), isWork: true, timeMS: int64(z.Score)})
	}
	for _, z := range okzs {
		targets = append(targets, taskTarget{id: fmt.Sprint(z.Member), isWork: false, outcome: "OK", timeMS: int64(z.Score)})
	}
	for _, z := range failzs {
		targets = append(targets, taskTarget{id: fmt.Sprint(z.Member), isWork: false, outcome: "FAIL", timeMS: int64(z.Score)})
	}

	if len(targets) > 0 {
		pipe2 := c.Pipeline()
		recCmds := make([]*redis.MapStringStringCmd, len(targets))
		for i, t := range targets {
			recCmds[i] = pipe2.HGetAll(ctx, "task:"+t.id)
		}
		if _, err := pipe2.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("friend show %s: read tasks pipeline: %w", friend, err)
		}

		for i, t := range targets {
			m := recCmds[i].Val()
			prim := m["primary"]
			if prim == "" {
				prim = taskcard.PrimaryOf(t.id)
				if prim == "" {
					prim = t.id
				}
			}
			stream := m["stream"]
			leg := m["leg"]
			if leg == "" {
				leg = m["kind"]
			}
			title := m["title"]
			model := m["model"]

			if t.isWork {
				leasedAt := time.UnixMilli(t.timeMS)
				if lms, err := strconv.ParseInt(m["leased_at"], 10, 64); err == nil && lms > 0 {
					leasedAt = time.UnixMilli(lms)
				}
				age := now.Sub(leasedAt)
				if age < 0 {
					age = 0
				}
				detail.Working = append(detail.Working, WorkingCopySummary{
					ID:       t.id,
					Primary:  prim,
					Stream:   stream,
					Leg:      leg,
					Attempt:  parseAttempt(t.id),
					Title:    title,
					Model:    model,
					LeasedAt: leasedAt,
					Age:      age,
				})
			} else {
				endedAt := time.UnixMilli(t.timeMS)
				if ems, err := strconv.ParseInt(m["ended_at"], 10, 64); err == nil && ems > 0 {
					endedAt = time.UnixMilli(ems)
				}
				age := now.Sub(endedAt)
				if age < 0 {
					age = 0
				}
				legType := "task"
				if leg == "read" {
					legType = "review"
				}
				why := m["why"]
				if why == "" && m["outcome"] != "" {
					why = m["outcome"]
				}
				detail.Receipts = append(detail.Receipts, ReceiptSummary{
					ID:      t.id,
					Primary: prim,
					Stream:  stream,
					Leg:     legType,
					Outcome: t.outcome,
					Why:     why,
					PR:      m["pr"],
					EndedAt: endedAt,
					Age:     age,
				})
			}
		}
	}

	// Sort receipts newest first, bounded to 20
	sort.Slice(detail.Receipts, func(i, j int) bool {
		return detail.Receipts[i].EndedAt.After(detail.Receipts[j].EndedAt)
	})
	if len(detail.Receipts) > 20 {
		detail.Receipts = detail.Receipts[:20]
	}

	return detail, nil
}

// FormatList formats a list of friend summaries into a clean tabular string.
func FormatList(summaries []FriendSummary, now time.Time) string {
	if len(summaries) == 0 {
		return "FRIENDS n=0 up=0 down=0 working=0\n"
	}

	var b strings.Builder
	// Table Header
	fmt.Fprintf(&b, "%-12s | %-13s | %-12s | %-18s | %-11s | %-24s | %-10s | %s\n",
		"FRIEND", "STATUS", "HARNESS", "MODELS", "SLOTS (F/U)", "WORKING COPIES", "LAST BEAT", "CREDITS/QUOTA")
	b.WriteString("-------------+---------------+--------------+--------------------+-------------+--------------------------+------------+--------------\n")

	upCount := 0
	downCount := 0
	totalWorking := 0

	for _, s := range summaries {
		if s.Status == StateUp {
			upCount++
		} else {
			downCount++
		}
		totalWorking += s.SlotsUsed

		harness := s.Harness
		if harness == "" {
			harness = "-"
		}
		models := s.Models
		if models == "" {
			models = "-"
		}

		fmt.Fprintf(&b, "%-12s | %-13s | %-12s | %-18s | %-11s | %-24s | %-10s | %s\n",
			s.Name,
			s.Status,
			harness,
			models,
			s.SlotsString(),
			s.WorkingCopiesString(),
			s.LastBeatString(),
			s.CreditsQuotaString(),
		)
	}

	b.WriteString("-------------+---------------+--------------+--------------------+-------------+--------------------------+------------+--------------\n")
	fmt.Fprintf(&b, "FRIENDS n=%d up=%d down=%d working=%d\n", len(summaries), upCount, downCount, totalWorking)
	return b.String()
}

// FormatShow formats a FriendDetail into a human-readable detailed view.
func FormatShow(d *FriendDetail, now time.Time) string {
	if d == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "FRIEND %s\n", d.Name)
	fmt.Fprintf(&b, "Status:       %s\n", d.Status)
	fmt.Fprintf(&b, "Harness:      %s\n", orDash(d.Harness))
	fmt.Fprintf(&b, "Host:         %s\n", orDash(d.Host))
	fmt.Fprintf(&b, "Machine:      %s\n", orDash(d.Machine))
	fmt.Fprintf(&b, "Session:      %s\n", orDash(d.Session))
	fmt.Fprintf(&b, "Models:       %s\n", orDash(d.Models))
	fmt.Fprintf(&b, "Slots:        %d desired (%d free, %d used)\n", d.SlotsTotal, d.SlotsFree, d.SlotsUsed)
	fmt.Fprintf(&b, "Paused:       %t\n", d.Paused)
	if d.Tiers != "" {
		fmt.Fprintf(&b, "Tiers:        %s\n", d.Tiers)
	}
	if d.HasBeat {
		fmt.Fprintf(&b, "Last beat:    %s (%s)\n", d.LastBeatString(), d.LastBeatAt.UTC().Format(time.RFC3339))
	} else {
		fmt.Fprintf(&b, "Last beat:    -\n")
	}
	if d.Load != "" {
		fmt.Fprintf(&b, "Load:         %s\n", d.Load)
	}
	if d.State != "" {
		stLine := d.State
		if d.StateReason != "" {
			stLine += " (" + d.StateReason + ")"
		}
		fmt.Fprintf(&b, "State:        %s\n", stLine)
	}
	fmt.Fprintf(&b, "Credits:      %s\n", d.CreditsQuotaString())

	// Working copies
	b.WriteString("\n")
	if len(d.Working) == 0 {
		b.WriteString("WORKING COPIES: none\n")
	} else {
		fmt.Fprintf(&b, "WORKING COPIES (%d):\n", len(d.Working))
		for _, w := range d.Working {
			line := fmt.Sprintf("  %-32s stream=%-10s attempt=%-2d age=%-8s leg=%-6s",
				w.ID, orDash(w.Stream), w.Attempt, formatDuration(w.Age), orDash(w.Leg))
			if w.Model != "" {
				line += " model=" + w.Model
			}
			if w.Title != "" {
				line += " title=" + strconv.Quote(w.Title)
			}
			b.WriteString(line + "\n")
		}
	}

	// Recent receipts
	b.WriteString("\n")
	if len(d.Receipts) == 0 {
		b.WriteString("RECENT RECEIPTS: none\n")
	} else {
		fmt.Fprintf(&b, "RECENT RECEIPTS (%d):\n", len(d.Receipts))
		for _, r := range d.Receipts {
			line := fmt.Sprintf("  %-32s stream=%-10s leg=%-7s outcome=%-4s age=%-8s",
				r.ID, orDash(r.Stream), r.Leg, r.Outcome, formatDuration(r.Age))
			if r.Why != "" {
				line += " why=" + strconv.Quote(r.Why)
			}
			if r.PR != "" {
				line += " pr=" + r.PR
			}
			b.WriteString(line + "\n")
		}
	}

	return b.String()
}

func parseAttempt(id string) int {
	if idx := strings.LastIndex(id, "~"); idx >= 0 && idx+1 < len(id) {
		if n, err := strconv.Atoi(id[idx+1:]); err == nil && n > 0 {
			return n
		}
	}
	return 1
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
}

func orDash(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	return s
}
