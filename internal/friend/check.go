package friend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// Verdict constants for the friend health check.
const (
	VerdictOK     = "ok"
	VerdictBroken = "broken"
	VerdictDeaf   = "deaf"
	VerdictSilent = "silent"
	VerdictDown   = "down"
	VerdictUntrue = "untrue"
)

// DaemonFacts carries facts about the launchd agent and daemon.
type DaemonFacts struct {
	Friend     string `json:"friend"`
	Agent      string `json:"agent"`      // loaded, not-loaded, none
	PID        string `json:"pid"`        // numeric string or "-"
	Status     string `json:"status"`     // ok, stale, none
	Connection string `json:"connection"` // string or "-"
	Challenge  string `json:"challenge"`  // string or "-"
	PongAge    string `json:"pong_age"`   // e.g. "4s" or "-"
	Presence   string `json:"presence"`   // up, asleep, down
	SeenAge    string `json:"seen_age"`   // e.g. "4s" or "-"
}

// HarnessFacts carries facts about the harness, deliveries and breaks.
type HarnessFacts struct {
	Friend         string `json:"friend"`
	Harness        string `json:"harness"`
	Route          string `json:"route"` // push, defer, passive
	Last           string `json:"last"`  // RFC3339 or "-"
	LastExit       string `json:"last_exit"`
	FailedOfLast20 int    `json:"failed_of_last20"`
	Deferred       int    `json:"deferred"`
	Broken         string `json:"broken"` // RFC3339 or "-"
	Reason         string `json:"reason"` // one line or "-"
}

// BusFacts carries facts about real messages on the bus.
type BusFacts struct {
	Friend    string `json:"friend"`
	RealSince int    `json:"real_since"`
	LastReal  string `json:"last_real"` // RFC3339 or "-"
}

// WorkFacts carries facts about inbox and outbox directories.
type WorkFacts struct {
	Friend       string `json:"friend"`
	Inbox        int    `json:"inbox"`
	Outbox       int    `json:"outbox"`
	NewestOutbox string `json:"newest_outbox"` // name or "-"
	NewestAt     string `json:"newest_at"`     // RFC3339 or "-"
}

// VerdictFacts carries the health check verdict and explanation.
type VerdictFacts struct {
	Friend  string `json:"friend"`
	Verdict string `json:"verdict"` // ok, broken, deaf, silent, down, untrue
	Shown   string `json:"shown"`   // state/working (e.g. "up/8") or "-"
	Why     string `json:"why"`
}

// FriendCheck is the full fact sheet for one friend.
type FriendCheck struct {
	Friend  string       `json:"friend"`
	Daemon  DaemonFacts  `json:"daemon"`
	Harness HarnessFacts `json:"harness"`
	Bus     BusFacts     `json:"bus"`
	Work    WorkFacts    `json:"work"`
	Verdict VerdictFacts `json:"verdict"`
}

// CheckSummary is the counts across all checked friends.
type CheckSummary struct {
	Friends int `json:"friends"`
	OK      int `json:"ok"`
	Broken  int `json:"broken"`
	Deaf    int `json:"deaf"`
	Silent  int `json:"silent"`
	Down    int `json:"down"`
	Untrue  int `json:"untrue"`
}

// CheckReport is the top-level report for JSON serialization.
type CheckReport struct {
	Friends []FriendCheck `json:"friends"`
	Summary CheckSummary  `json:"summary"`
}

// ShownEntry is the shown status of a friend (passed via --shown).
type ShownEntry struct {
	State   string `json:"state"`
	Working int    `json:"working"`
}

// CheckSeams provides external dependencies for friend check facts.
type CheckSeams struct {
	Now          func() time.Time
	Home         string
	Launchctl    Launchctl
	ReadStatus   func(friend string) (Status, bool, error)
	ReadPresence func(friend string) (PresenceStatus, bool, error)
	ReadPong     func(friend string) (Pong, bool, error)
	ReadLog      func(friend string) ([]string, error)
	BusLog       func(ctx context.Context, friend string, since time.Time) (realCount int, lastReal time.Time, err error)
	ReadWork     func(friend string, dir string) (inboxCount, outboxCount int, newestName string, newestAt time.Time, err error)
	ListFriends  func() ([]string, error)
	HarnessDir   func(friend string) (harness, dir string, err error)
}

// AgeString returns a formatted age string (e.g. "5s", "1m2s").
func AgeString(d time.Duration) string {
	if d < 0 {
		return "0s"
	}
	return d.Round(time.Second).String()
}

// QuoteWhy quotes a reason string if it contains whitespace or quotes.
func QuoteWhy(s string) string {
	if s == "" || s == "-" {
		return "-"
	}
	if strings.ContainsAny(s, " \t\n\"") {
		return strconv.Quote(s)
	}
	return s
}

// ParseLog parses deliver / runner log lines for turns, exits and deferrals.
func ParseLog(lines []string) (last string, lastExit string, failedOfLast20 int, deferred int) {
	last = "-"
	lastExit = "-"
	type turn struct {
		at   time.Time
		exit int
	}
	var turns []turn
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "RUN ") {
			line = strings.TrimPrefix(line, "RUN ")
		}
		if strings.Contains(line, "deferred=") || strings.Contains(line, "deferred:") || strings.Contains(line, " deferred ") {
			deferred++
		}
		if strings.Contains(line, "exit=") {
			fields := strings.Fields(line)
			var at time.Time
			var exit int
			var hasExit bool
			for _, f := range fields {
				if t, err := time.Parse(time.RFC3339, f); err == nil && at.IsZero() {
					at = t
				}
				if val, ok := strings.CutPrefix(f, "exit="); ok {
					if n, err := strconv.Atoi(val); err == nil {
						exit = n
						hasExit = true
					}
				}
			}
			if hasExit {
				turns = append(turns, turn{at: at, exit: exit})
			}
		}
	}
	if len(turns) > 0 {
		latest := turns[len(turns)-1]
		if !latest.at.IsZero() {
			last = latest.at.UTC().Format(time.RFC3339)
		}
		lastExit = strconv.Itoa(latest.exit)
		startIdx := max(0, len(turns)-20)
		for _, t := range turns[startIdx:] {
			if t.exit != 0 {
				failedOfLast20++
			}
		}
	}
	return last, lastExit, failedOfLast20, deferred
}

// DefaultReadWork reads directory entries of inbox/ and outbox/ under dir.
func DefaultReadWork(dir string) (inboxCount, outboxCount int, newestName string, newestAt time.Time, err error) {
	newestName = "-"
	if dir == "" {
		return 0, 0, "-", time.Time{}, nil
	}
	inboxDir := filepath.Join(dir, "inbox")
	if entries, err := os.ReadDir(inboxDir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") || e.Name() == "QUEUE.json" {
				continue
			}
			inboxCount++
		}
	}
	outboxDir := filepath.Join(dir, "outbox")
	if entries, err := os.ReadDir(outboxDir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			outboxCount++
			if info, err := e.Info(); err == nil {
				if newestAt.IsZero() || info.ModTime().After(newestAt) {
					newestAt = info.ModTime()
					newestName = e.Name()
				}
			}
		}
	}
	return inboxCount, outboxCount, newestName, newestAt, nil
}

// DecideVerdict is the pure function deciding a friend verdict from facts.
// Precedence:
// 1. broken when session is marked broken or every delivery in window failed
// 2. deaf when deliveries succeed but no session pong or real message came back
// 3. silent when no delivery was due and nothing came back
// 4. down by presence
// 5. untrue when --shown says up or working and facts say otherwise
// 6. else ok
func DecideVerdict(df DaemonFacts, hf HarnessFacts, bf BusFacts, wf WorkFacts, shown *ShownEntry, now time.Time) VerdictFacts {
	shownStr := "-"
	if shown != nil {
		shownStr = fmt.Sprintf("%s/%d", shown.State, shown.Working)
	}
	vf := VerdictFacts{
		Friend: df.Friend,
		Shown:  shownStr,
	}

	// 1. Broken when the session is marked broken or every delivery in window failed
	if hf.Broken != "-" || (hf.FailedOfLast20 > 0 && hf.LastExit != "0" && hf.LastExit != "-") {
		vf.Verdict = VerdictBroken
		if shown != nil && (shown.State == "up" || shown.Working > 0) {
			vf.Why = fmt.Sprintf("shown %s/working %d, no turn answered since %s: broken", shown.State, shown.Working, hf.Broken)
		} else if hf.Reason != "-" && hf.Reason != "" {
			vf.Why = "session broken: " + hf.Reason
		} else {
			vf.Why = "every delivery in window failed"
		}
		return vf
	}

	// 2. Deaf when deliveries succeed but no session pong or real message came back in window
	if hf.LastExit == "0" && bf.RealSince == 0 && (df.PongAge == "-" || isStaleAge(df.PongAge)) {
		vf.Verdict = VerdictDeaf
		vf.Why = "deliveries succeed but no session pong or real message came back"
		return vf
	}

	// 3. Silent when no delivery was due and nothing came back
	noDeliveryDue := (hf.Last == "-" || hf.LastExit == "-") && hf.Deferred == 0 && wf.Inbox == 0
	nothingCameBack := bf.RealSince == 0 && (df.PongAge == "-" || isStaleAge(df.PongAge))
	if noDeliveryDue && nothingCameBack && df.Presence != "down" {
		if shown != nil && (shown.State == "up" || shown.Working > 0) {
			vf.Verdict = VerdictUntrue
			vf.Why = fmt.Sprintf("shown %s/working %d, no delivery was due and nothing came back", shown.State, shown.Working)
		} else {
			vf.Verdict = VerdictSilent
			vf.Why = "no delivery was due and nothing came back"
		}
		return vf
	}

	// 4. Down by presence
	if df.Presence == "down" || df.Agent == "none" || (df.Agent == "not-loaded" && df.Status == "none") {
		if shown != nil && (shown.State == "up" || shown.Working > 0) {
			vf.Verdict = VerdictUntrue
			vf.Why = fmt.Sprintf("shown %s/working %d, facts say down by presence", shown.State, shown.Working)
		} else {
			vf.Verdict = VerdictDown
			vf.Why = "down by presence"
		}
		return vf
	}

	// 5. Untrue when --shown says up or working and facts say otherwise
	if shown != nil && (shown.State == "up" || shown.Working > 0) {
		if df.Presence == "asleep" {
			vf.Verdict = VerdictUntrue
			vf.Why = fmt.Sprintf("shown %s/working %d, facts say asleep", shown.State, shown.Working)
			return vf
		}
		if df.Agent != "loaded" {
			vf.Verdict = VerdictUntrue
			vf.Why = fmt.Sprintf("shown %s/working %d, facts say agent not loaded", shown.State, shown.Working)
			return vf
		}
	}

	// 6. Else ok
	vf.Verdict = VerdictOK
	vf.Why = "live"
	return vf
}

func isStaleAge(ageStr string) bool {
	if ageStr == "" || ageStr == "-" {
		return true
	}
	d, err := time.ParseDuration(ageStr)
	if err != nil {
		return true
	}
	return d > SessionQuiet
}

// CheckFriend gathers facts and decides the verdict for one friend.
func CheckFriend(ctx context.Context, friendName string, seams CheckSeams, since time.Duration, shown *ShownEntry) FriendCheck {
	now := seams.Now().UTC()
	harness, dir, _ := seams.HarnessDir(friendName)
	if harness == "" {
		harness = "unknown"
	}

	// Daemon facts
	df := DaemonFacts{
		Friend:     friendName,
		Agent:      "none",
		PID:        "-",
		Status:     "none",
		Connection: "-",
		Challenge:  "-",
		PongAge:    "-",
		Presence:   "down",
		SeenAge:    "-",
	}

	if seams.Launchctl != nil {
		out, _ := seams.Launchctl(ctx, "list")
		foundInLaunchctl := false
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && fields[2] == "com.nova.friend-"+friendName {
				foundInLaunchctl = true
				df.Agent = "loaded"
				if fields[0] != "-" && fields[0] != "0" {
					df.PID = fields[0]
				}
				break
			}
		}
		if !foundInLaunchctl {
			plistPath := filepath.Join(seams.Home, "Library", "LaunchAgents", "com.nova.friend-"+friendName+".plist")
			if _, err := os.Stat(plistPath); err == nil {
				df.Agent = "not-loaded"
			}
		}
	} else {
		plistPath := filepath.Join(seams.Home, "Library", "LaunchAgents", "com.nova.friend-"+friendName+".plist")
		if _, err := os.Stat(plistPath); err == nil {
			df.Agent = "not-loaded"
		}
	}

	st, stFound, _ := seams.ReadStatus(friendName)
	if stFound {
		if now.Sub(st.At) <= DaemonStale {
			df.Status = "ok"
		} else {
			df.Status = "stale"
		}
		df.Connection = dash(st.Connection)
		df.Challenge = dash(st.Challenge)
		if (harness == "" || harness == "unknown") && st.Harness != "" {
			harness = st.Harness
		}
	}

	pong, pongFound, _ := seams.ReadPong(friendName)
	if pongFound && !pong.At.IsZero() {
		df.PongAge = AgeString(now.Sub(pong.At))
	} else if !st.LastPong.IsZero() {
		df.PongAge = AgeString(now.Sub(st.LastPong))
	}

	pr, prFound, _ := seams.ReadPresence(friendName)
	if prFound {
		if pr.Presence == PresenceUp {
			df.Presence = PresenceUp
			if !pr.LastHeard.IsZero() {
				df.SeenAge = AgeString(now.Sub(pr.LastHeard))
			} else if pongFound && !pong.At.IsZero() {
				df.SeenAge = AgeString(now.Sub(pong.At))
			} else if !st.LastPong.IsZero() {
				df.SeenAge = AgeString(now.Sub(st.LastPong))
			}
		} else {
			if df.Status == "ok" {
				df.Presence = "asleep"
				if !st.LastDaemonPong.IsZero() {
					df.SeenAge = AgeString(now.Sub(st.LastDaemonPong))
				} else {
					df.SeenAge = AgeString(now.Sub(st.At))
				}
			} else {
				df.Presence = PresenceDown
				if !pr.LastHeard.IsZero() {
					df.SeenAge = AgeString(now.Sub(pr.LastHeard))
				}
			}
		}
	} else {
		if df.Status == "ok" {
			if !st.LastPong.IsZero() && now.Sub(st.LastPong) < AnswerBound {
				df.Presence = PresenceUp
				df.SeenAge = AgeString(now.Sub(st.LastPong))
			} else {
				df.Presence = "asleep"
				if !st.LastDaemonPong.IsZero() {
					df.SeenAge = AgeString(now.Sub(st.LastDaemonPong))
				} else {
					df.SeenAge = AgeString(now.Sub(st.At))
				}
			}
		} else {
			df.Presence = PresenceDown
		}
	}

	// Harness facts
	route := "push"
	if harness == "claude" || slices.Contains(RefusedHarnesses, harness) {
		route = "passive"
	} else if harness == "dsh" {
		route = "defer"
	}

	hf := HarnessFacts{
		Friend:   friendName,
		Harness:  harness,
		Route:    route,
		Last:     "-",
		LastExit: "-",
		Broken:   "-",
		Reason:   "-",
	}

	if seams.ReadLog != nil {
		lines, _ := seams.ReadLog(friendName)
		hf.Last, hf.LastExit, hf.FailedOfLast20, hf.Deferred = ParseLog(lines)
	}

	if st.Session == SessionBroken {
		if !st.BrokenAt.IsZero() {
			hf.Broken = st.BrokenAt.UTC().Format(time.RFC3339)
		} else {
			hf.Broken = st.At.UTC().Format(time.RFC3339)
		}
		hf.Reason = dash(st.SessionReason)
	}

	// Bus facts
	bf := BusFacts{
		Friend:    friendName,
		RealSince: 0,
		LastReal:  "-",
	}
	if seams.BusLog != nil {
		count, lastReal, _ := seams.BusLog(ctx, friendName, now.Add(-since))
		bf.RealSince = count
		if !lastReal.IsZero() {
			bf.LastReal = lastReal.UTC().Format(time.RFC3339)
		}
	}

	// Work facts
	wf := WorkFacts{
		Friend:       friendName,
		Inbox:        0,
		Outbox:       0,
		NewestOutbox: "-",
		NewestAt:     "-",
	}
	if seams.ReadWork != nil {
		inbox, outbox, newestName, newestAt, _ := seams.ReadWork(friendName, dir)
		wf.Inbox = inbox
		wf.Outbox = outbox
		wf.NewestOutbox = newestName
		if !newestAt.IsZero() {
			wf.NewestAt = newestAt.UTC().Format(time.RFC3339)
		}
	}

	// Verdict
	vf := DecideVerdict(df, hf, bf, wf, shown, now)

	return FriendCheck{
		Friend:  friendName,
		Daemon:  df,
		Harness: hf,
		Bus:     bf,
		Work:    wf,
		Verdict: vf,
	}
}

// ComputeSummary aggregates verdicts across friends.
func ComputeSummary(checks []FriendCheck) CheckSummary {
	var s CheckSummary
	s.Friends = len(checks)
	for _, c := range checks {
		switch c.Verdict.Verdict {
		case VerdictOK:
			s.OK++
		case VerdictBroken:
			s.Broken++
		case VerdictDeaf:
			s.Deaf++
		case VerdictSilent:
			s.Silent++
		case VerdictDown:
			s.Down++
		case VerdictUntrue:
			s.Untrue++
		}
	}
	return s
}

// IsRealMessage reports whether a message subject is a real turn message (not ping, pong, daemon-pong or keepalive).
func IsRealMessage(subject string) bool {
	s := strings.TrimSpace(strings.ToLower(subject))
	return s != "ping" && s != "pong" && s != "daemon-pong" && s != "keepalive"
}

// BusLogEntries reads the bus store log for real messages from friend since since.
func BusLogEntries(ctx context.Context, st bus.Store, friend string, since time.Time) (realCount int, lastReal time.Time, err error) {
	if st == nil {
		return 0, time.Time{}, nil
	}
	entries, err := st.Range(ctx, bus.LogKey, "-", "+", 0)
	if err != nil {
		return 0, time.Time{}, err
	}
	for _, e := range entries {
		from := e.Fields["from"]
		if from != friend {
			continue
		}
		subject := e.Fields["subject"]
		if !IsRealMessage(subject) {
			continue
		}
		atStr := e.Fields["at"]
		t, terr := time.Parse(time.RFC3339, atStr)
		if terr != nil {
			continue
		}
		if !t.Before(since) {
			realCount++
		}
		if t.After(lastReal) {
			lastReal = t
		}
	}
	return realCount, lastReal, nil
}

// Line renders the CHECK DAEMON line.
func (df DaemonFacts) Line() string {
	return fmt.Sprintf("CHECK DAEMON friend=%s agent=%s pid=%s status=%s connection=%s challenge=%s pong_age=%s presence=%s seen_age=%s",
		df.Friend, df.Agent, df.PID, df.Status, df.Connection, df.Challenge, df.PongAge, df.Presence, df.SeenAge)
}

// Line renders the CHECK HARNESS line.
func (hf HarnessFacts) Line() string {
	return fmt.Sprintf("CHECK HARNESS friend=%s harness=%s route=%s last=%s last_exit=%s failed_of_last20=%d deferred=%d broken=%s reason=%s",
		hf.Friend, hf.Harness, hf.Route, hf.Last, hf.LastExit, hf.FailedOfLast20, hf.Deferred, hf.Broken, QuoteWhy(hf.Reason))
}

// Line renders the CHECK BUS line.
func (bf BusFacts) Line() string {
	return fmt.Sprintf("CHECK BUS friend=%s real_since=%d last_real=%s",
		bf.Friend, bf.RealSince, bf.LastReal)
}

// Line renders the CHECK WORK line.
func (wf WorkFacts) Line() string {
	return fmt.Sprintf("CHECK WORK friend=%s inbox=%d outbox=%d newest_outbox=%s newest_at=%s",
		wf.Friend, wf.Inbox, wf.Outbox, wf.NewestOutbox, wf.NewestAt)
}

// Line renders the CHECK VERDICT line.
func (vf VerdictFacts) Line() string {
	return fmt.Sprintf("CHECK VERDICT friend=%s verdict=%s shown=%s why=%s",
		vf.Friend, vf.Verdict, vf.Shown, QuoteWhy(vf.Why))
}

// Lines renders the five CHECK lines for one friend.
func (fc FriendCheck) Lines() []string {
	return []string{
		fc.Daemon.Line(),
		fc.Harness.Line(),
		fc.Bus.Line(),
		fc.Work.Line(),
		fc.Verdict.Line(),
	}
}

// Line renders the CHECK OK summary line.
func (s CheckSummary) Line() string {
	return fmt.Sprintf("CHECK OK friends=%d ok=%d broken=%d deaf=%d silent=%d down=%d untrue=%d",
		s.Friends, s.OK, s.Broken, s.Deaf, s.Silent, s.Down, s.Untrue)
}

// Lines renders all output lines for the full report.
func (cr CheckReport) Lines() []string {
	var lines []string
	for _, fc := range cr.Friends {
		lines = append(lines, fc.Lines()...)
	}
	lines = append(lines, cr.Summary.Line())
	return lines
}

// ParseShown parses JSON representing shown state.
func ParseShown(data []byte) (map[string]ShownEntry, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	var m map[string]ShownEntry
	if err := json.Unmarshal(data, &m); err == nil && len(m) > 0 {
		return m, nil
	}
	var list []struct {
		Friend  string `json:"friend"`
		State   string `json:"state"`
		Working int    `json:"working"`
	}
	if err := json.Unmarshal(data, &list); err == nil && len(list) > 0 {
		res := make(map[string]ShownEntry, len(list))
		for _, item := range list {
			res[item.Friend] = ShownEntry{State: item.State, Working: item.Working}
		}
		return res, nil
	}
	var single struct {
		Friend  string `json:"friend"`
		State   string `json:"state"`
		Working int    `json:"working"`
	}
	if err := json.Unmarshal(data, &single); err == nil && (single.State != "" || single.Friend != "") {
		key := single.Friend
		if key == "" {
			key = "*"
		}
		return map[string]ShownEntry{key: {State: single.State, Working: single.Working}}, nil
	}
	return nil, fmt.Errorf("invalid shown JSON format")
}

// LookupShown returns the ShownEntry for friend, checking friend name then wildcard.
func LookupShown(shown map[string]ShownEntry, friend string) *ShownEntry {
	if shown == nil {
		return nil
	}
	if entry, ok := shown[friend]; ok {
		return &entry
	}
	if entry, ok := shown["*"]; ok {
		return &entry
	}
	return nil
}
