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
	// Proof is the session's proof as the server has it from her daemon: pending
	// while the push is unproven (ProofAge since the daemon started waiting), sent
	// once the server took one on her beat (ProofAge the proof's age), none before.
	Proof    string `json:"proof"`     // pending, sent, none
	ProofAge string `json:"proof_age"` // e.g. "4s" or "-"
}

// HarnessFacts carries facts about the harness, deliveries and breaks: every
// delivery count is of the --since window (docs/SPEC-FRIEND.md "Check").
type HarnessFacts struct {
	Friend         string `json:"friend"`
	Harness        string `json:"harness"`
	Route          string `json:"route"` // push, mailbox, queue or passive
	Last           string `json:"last"`  // RFC3339 or "-"
	LastExit       string `json:"last_exit"`
	FailedOfLast20 int    `json:"failed_of_last20"`
	Deferred       int    `json:"deferred"`
	Delivered      int    `json:"delivered"`    // deliveries in the window (JSON only)
	Failed         int    `json:"failed"`       // of those, the ones that failed (JSON only)
	Silent         int    `json:"silent"`       // turns the turn-progress watchdog ended with no session write (JSON only)
	Broken         string `json:"broken"`       // RFC3339 or "-"
	Reason         string `json:"reason"`       // one line or "-"
	SessionLive    string `json:"session_live"` // the conversation a mailbox harness delivers into, or "-"
	Queued         string `json:"queued"`       // the harness's own queue not yet taken (codex), or "-"
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

// LogFacts is what the daemon's log says inside a window.
type LogFacts struct {
	Last           string // RFC3339 of the newest delivery in the window, or "-"
	LastExit       string // its exit, or "-"
	FailedOfLast20 int    // failures among the newest 20 deliveries in the window
	Deferred       int    // deferrals in the window
	Delivered      int    // deliveries in the window
	Failed         int    // of those, the ones that exited non-zero
	Silent         int    // turns the turn-progress watchdog ended with no session write (SessionDeafMark)
}

// ParseLog reads the daemon's log lines for deliveries and deferrals at or
// after from; a line with no RFC3339 stamp is outside every window
// (docs/SPEC-FRIEND.md "Check": the facts).
func ParseLog(lines []string, from time.Time) LogFacts {
	lf := LogFacts{Last: "-", LastExit: "-"}
	type turn struct {
		at   time.Time
		exit int
	}
	var turns []turn
	for _, raw := range lines {
		line := strings.TrimPrefix(strings.TrimSpace(raw), "RUN ")
		var at time.Time
		exit, hasExit := 0, false
		for _, f := range strings.Fields(line) {
			if t, err := time.Parse(time.RFC3339, f); err == nil && at.IsZero() {
				at = t
			}
			if val, ok := strings.CutPrefix(f, "exit="); ok {
				if n, err := strconv.Atoi(val); err == nil {
					exit, hasExit = n, true
				}
			}
		}
		if at.IsZero() || at.Before(from) {
			continue
		}
		if strings.Contains(line, SessionDeafMark) {
			// a turn the turn-progress watchdog ended: the session was taken
			// and wrote nothing, so it is not a failed delivery. The deaf rule
			// reads it (factsVerdict; docs/SPEC-FRIEND.md, Check: the verdicts).
			lf.Silent++
			continue
		}
		if strings.Contains(line, "deferred=") || strings.Contains(line, "deferred:") || strings.Contains(line, " deferred ") {
			lf.Deferred++
		}
		if hasExit {
			turns = append(turns, turn{at: at, exit: exit})
		}
	}
	lf.Delivered = len(turns)
	for _, t := range turns {
		if t.exit != 0 {
			lf.Failed++
		}
	}
	if len(turns) > 0 {
		latest := turns[len(turns)-1]
		lf.Last = latest.at.UTC().Format(time.RFC3339)
		lf.LastExit = strconv.Itoa(latest.exit)
		for _, t := range turns[max(0, len(turns)-20):] {
			if t.exit != 0 {
				lf.FailedOfLast20++
			}
		}
	}
	return lf
}

// DefaultReadWork reads directory entries of inbox/ and outbox/ under dir
// (docs/SPEC-FRIEND.md "Check": the facts, work).
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

// DecideVerdict is the pure function deciding a friend verdict from facts; every
// count and age is of the window (docs/SPEC-FRIEND.md "Check": the verdicts).
// The facts decide first, in this order:
//  1. broken when the session is marked broken, or deliveries in the window
//     are all failures (delivered > 0 and failed == delivered)
//  2. deaf when a delivery in the window succeeded, or a turn ran with no
//     session write past its wall cap (the turn-progress watchdog), and neither
//     a session pong nor a real message came back in the window
//  3. silent when no delivery was due in the window and nothing came back
//  4. down by presence
//  5. else ok
//
// Then --shown: when it says up or working and the facts verdict is not ok,
// the verdict stays the facts' and the why leads with "untrue:", the claim and
// the facts' reason; when the facts verdict is ok but the friend is asleep or
// its agent is not loaded, the verdict is untrue.
func DecideVerdict(df DaemonFacts, hf HarnessFacts, bf BusFacts, wf WorkFacts, shown *ShownEntry, window time.Duration) VerdictFacts {
	vf := VerdictFacts{Friend: df.Friend, Shown: "-"}
	claimsUp := false
	if shown != nil {
		vf.Shown = fmt.Sprintf("%s/%d", shown.State, shown.Working)
		claimsUp = shown.State == "up" || shown.Working > 0
	}
	vf.Verdict, vf.Why = factsVerdict(df, hf, bf, wf, window)
	switch {
	case !claimsUp:
	case vf.Verdict != VerdictOK:
		vf.Why = fmt.Sprintf("untrue: shown %s, %s", vf.Shown, vf.Why)
	case df.Presence == "asleep":
		vf.Verdict, vf.Why = VerdictUntrue, fmt.Sprintf("shown %s, facts say asleep", vf.Shown)
	case df.Agent != "loaded":
		vf.Verdict, vf.Why = VerdictUntrue, fmt.Sprintf("shown %s, facts say agent %s", vf.Shown, df.Agent)
	}
	return vf
}

// factsVerdict is DecideVerdict's rules 1 to 5, before --shown (docs/SPEC-FRIEND.md "Check": the verdicts).
func factsVerdict(df DaemonFacts, hf HarnessFacts, bf BusFacts, wf WorkFacts, window time.Duration) (verdict, why string) {
	cameBack := bf.RealSince > 0 || pongWithin(df.PongAge, window)
	switch {
	case hf.Broken != "-":
		if hf.Reason != "-" && hf.Reason != "" {
			return VerdictBroken, "session broken: " + hf.Reason
		}
		return VerdictBroken, "session broken since " + hf.Broken
	case hf.Delivered > 0 && hf.Failed == hf.Delivered:
		return VerdictBroken, fmt.Sprintf("every delivery in the window failed (%d of %d)", hf.Failed, hf.Delivered)
	case hf.Silent > 0 && !cameBack:
		return VerdictDeaf, "a turn ran with no session write past its cap and no session pong or real message came back in the window"
	case hf.Delivered > hf.Failed && !cameBack:
		return VerdictDeaf, "deliveries succeed but no session pong or real message came back in the window"
	case df.Status == "stale":
		return VerdictDown, "stale daemon status"
	case hf.Delivered == 0 && hf.Deferred == 0 && wf.Inbox == 0 && !cameBack && df.Presence != PresenceDown:
		return VerdictSilent, "no delivery was due and nothing came back in the window"
	case df.Presence == PresenceDown || df.Agent == "none" || (df.Agent == "not-loaded" && df.Status == "none"):
		return VerdictDown, "down by presence"
	}
	return VerdictOK, "live"
}

// pongWithin reports whether a session pong aged ageStr ("-" is none) is
// inside the window (docs/SPEC-FRIEND.md "Check": the verdicts, deaf and silent).
func pongWithin(ageStr string, window time.Duration) bool {
	d, err := time.ParseDuration(ageStr)
	return err == nil && d <= window
}

// CheckFriend gathers the facts through the seams over the window and decides
// the verdict for one friend (docs/SPEC-FRIEND.md "Check").
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
		Proof:      "none",
		ProofAge:   "-",
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
		if now.Sub(st.At) < DaemonStale {
			df.Status = "ok"
		} else {
			df.Status = "stale"
		}
		df.Connection = dash(st.Connection)
		df.Challenge = dash(st.Challenge)
		if (harness == "" || harness == "unknown") && st.Harness != "" {
			harness = st.Harness
		}
		switch {
		case st.Push == PushUnproven:
			df.Proof = "pending"
			if !st.PushSince.IsZero() {
				df.ProofAge = AgeString(now.Sub(st.PushSince))
			}
		case !st.ProofSent.IsZero():
			df.Proof, df.ProofAge = "sent", AgeString(now.Sub(st.ProofSent))
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
		if pr.Presence == PresenceUp && df.Status == "ok" {
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
	route := "push" // dsh included: each delivery is a headless turn into her session, never a deferral
	if harness == "claude" || slices.Contains(RefusedHarnesses, harness) {
		route = "passive"
	} else if harness == "antigravity" {
		route = "mailbox" // delivered into the conversation's mailbox at once, never deferred for a turn
	} else if harness == "codex" {
		route = "queue" // the open chat's queue: a turn under way is never steered from outside (Codex)
	}

	hf := HarnessFacts{
		Friend:      friendName,
		Harness:     harness,
		Route:       route,
		Last:        "-",
		LastExit:    "-",
		Broken:      "-",
		Reason:      "-",
		SessionLive: dash(st.SessionLive),
		Queued:      "-",
	}
	if st.QueueKnown {
		hf.Queued = strconv.Itoa(st.Queued)
	}

	if seams.ReadLog != nil {
		lines, _ := seams.ReadLog(friendName)
		lf := ParseLog(lines, now.Add(-since))
		hf.Last, hf.LastExit, hf.FailedOfLast20, hf.Deferred = lf.Last, lf.LastExit, lf.FailedOfLast20, lf.Deferred
		hf.Delivered, hf.Failed, hf.Silent = lf.Delivered, lf.Failed, lf.Silent
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
	vf := DecideVerdict(df, hf, bf, wf, shown, since)

	return FriendCheck{
		Friend:  friendName,
		Daemon:  df,
		Harness: hf,
		Bus:     bf,
		Work:    wf,
		Verdict: vf,
	}
}

// ComputeSummary aggregates verdicts across friends (docs/SPEC-FRIEND.md "Check": the summary).
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

// IsRealMessage reports whether a message subject is a real turn message (not ping, pong, daemon-pong or keepalive)
// (docs/SPEC-FRIEND.md "Check": the facts, bus).
func IsRealMessage(subject string) bool {
	s := strings.TrimSpace(strings.ToLower(subject))
	return s != "ping" && s != "pong" && s != "daemon-pong" && s != "keepalive"
}

// BusLogEntries reads the bus store log for real messages from friend since since
// (docs/SPEC-FRIEND.md "Check": the facts, bus).
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
	proof, age := df.Proof, df.ProofAge
	if proof == "" {
		proof, age = "none", "-"
	}
	return fmt.Sprintf("CHECK DAEMON friend=%s agent=%s pid=%s status=%s connection=%s challenge=%s pong_age=%s presence=%s seen_age=%s proof=%s proof_age=%s",
		df.Friend, df.Agent, df.PID, df.Status, df.Connection, df.Challenge, df.PongAge, df.Presence, df.SeenAge, proof, dash(age))
}

// Line renders the CHECK HARNESS line.
func (hf HarnessFacts) Line() string {
	return fmt.Sprintf("CHECK HARNESS friend=%s harness=%s route=%s last=%s last_exit=%s failed_of_last20=%d deferred=%d broken=%s reason=%s session_live=%s queued=%s",
		hf.Friend, hf.Harness, hf.Route, hf.Last, hf.LastExit, hf.FailedOfLast20, hf.Deferred, hf.Broken, QuoteWhy(hf.Reason), dash(hf.SessionLive), dash(hf.Queued))
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
