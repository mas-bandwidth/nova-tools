package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
)

const (
	maxCodexHistoryBytes = 1 << 20
	maxCodexHistory      = 4096
	maxCodexRecovery     = 128
)

// codexBacklog covers the Redis-clock 24h range through captured tail within a
// fixed budget. An answer to a later message never hides an earlier one.
type codexRecovery struct {
	messages        []bus.Message
	oldest          string
	coverageUnknown bool
	challengeIDs    map[string]bool
	challengeEpoch  int64
}

func codexBacklog(s *session, tail string, since time.Time) (codexRecovery, error) {
	ctx, cancel := call()
	defer cancel()
	states, now, err := s.hooks.Read(ctx, s.as)
	if err != nil {
		return codexRecovery{}, err
	}
	window := now.Add(-24 * time.Hour)
	if since.After(now) {
		return codexRecovery{}, errors.New("codex cutover timestamp is after Redis TIME; use the actual fixed cutover time, not a future boundary")
	}
	if tail == "0-0" {
		return codexRecovery{oldest: "none", coverageUnknown: true}, nil
	}
	start := window
	if since.After(start) {
		start = since
	}
	floor := bus.IDAt(start)
	oldest, err := s.bus.Range(ctx, bus.StreamOf(s.as), "-", tail, 1)
	if err != nil {
		return codexRecovery{}, err
	}
	plan := codexRecovery{}
	if len(oldest) > 0 {
		plan.oldest = oldest[0].Entry
		// Stream creation and trimming have no distinguishing marker in Store.
		// State the uncertainty rather than claim unseen history was covered.
		entryMS, _, _ := strings.Cut(plan.oldest, "-")
		millis, parseErr := strconv.ParseInt(entryMS, 10, 64)
		if parseErr != nil {
			return codexRecovery{}, errors.New("recipient stream has malformed oldest entry ID")
		}
		plan.coverageUnknown = millis >= start.UnixMilli()
	}
	entries, err := s.bus.Range(ctx, bus.StreamOf(s.as), floor, tail, maxCodexHistory+1)
	if err != nil {
		return codexRecovery{}, err
	}
	if len(entries) > maxCodexHistory {
		return codexRecovery{}, fmt.Errorf("native startup recovery incomplete: 24h recipient history exceeds %d entries through %s; explicit backlog reconciliation is required before cutover", maxCodexHistory, tail)
	}
	// Matching requires the actual bus message. Raw nonce is never reconstructed.
	challenge, err := s.hooks.C.HMGet(ctx, hook.ChallengeKey(s.as), "sum", "epoch").Result()
	if err != nil {
		return codexRecovery{}, err
	}
	activeSum, activeEpoch := "", ""
	if len(challenge) == 2 {
		activeSum, _ = challenge[0].(string)
		activeEpoch, _ = challenge[1].(string)
	}
	if activeSum != "" {
		epoch, err := strconv.ParseInt(activeEpoch, 10, 64)
		if err != nil || epoch != states[0].Epoch || epoch <= 0 {
			return codexRecovery{}, errors.New("active hook challenge has malformed or mismatched epoch")
		}
	}
	isCurrentChallenge := func(m bus.Message) bool {
		return activeSum != "" && strings.HasPrefix(m.Subject, "HOOK CHALLENGE nonce=") && hook.Sum(strings.TrimPrefix(m.Subject, "HOOK CHALLENGE nonce=")) == activeSum
	}
	// The explicit ordinary-history boundary never authorizes dropping a current
	// server challenge. Recover only its actual hash-matching event before it.
	if activeSum != "" && since.After(window) {
		found := false
		for _, entry := range entries {
			if isCurrentChallenge(entry.Message()) {
				found = true
			}
		}
		if !found {
			earlier, err := s.bus.Range(ctx, bus.StreamOf(s.as), bus.IDAt(window), "("+floor, maxCodexHistory-len(entries)+1)
			if err != nil {
				return codexRecovery{}, err
			}
			if len(earlier)+len(entries) > maxCodexHistory {
				return codexRecovery{}, errors.New("native startup recovery incomplete: current challenge search exceeds history budget")
			}
			rawBytes := 0
			selected := make([]bus.Entry, 0)
			for _, entry := range earlier {
				rawBytes += codexMessageBytes(entry.Message())
				if isCurrentChallenge(entry.Message()) {
					selected = append(selected, entry)
				}
			}
			for _, entry := range entries {
				rawBytes += codexMessageBytes(entry.Message())
			}
			if rawBytes > maxCodexHistoryBytes {
				return codexRecovery{}, errors.New("native startup recovery incomplete: current challenge search exceeds byte budget")
			}
			entries = append(selected, entries...)
		}
	}
	observed := func(m bus.Message) bool { return m.From != s.as || strings.HasPrefix(m.Subject, "HOOK CHALLENGE ") }
	senderNames := make([]string, 0)
	seenSenders := map[string]bool{}
	for _, entry := range entries {
		m := entry.Message()
		if canonical(m, s.as) && !seenSenders[m.From] {
			if why := bus.CheckName(m.From); why != "" {
				return codexRecovery{}, errors.New(why)
			}
			seenSenders[m.From] = true
			senderNames = append(senderNames, m.From)
		}
	}
	serverRole := map[string]bool{}
	if len(senderNames) > 0 {
		statuses, _, err := s.hooks.Read(ctx, senderNames...)
		if err != nil {
			return codexRecovery{}, err
		}
		for _, status := range statuses {
			serverRole[status.Name] = status.Role == hook.Coordinator || status.Role == hook.Comms
		}
	}
	obsoleteProtocol := func(m bus.Message) bool {
		return !isCurrentChallenge(m) && serverRole[m.From] && canonical(m, s.as)
	}
	ids := make([]string, 0, len(entries))
	historyBytes := 0
	for _, entry := range entries {
		m := entry.Message()
		historyBytes += codexMessageBytes(m)
		if historyBytes > maxCodexHistoryBytes {
			return codexRecovery{}, errors.New("native startup recovery incomplete: scanned history exceeds byte budget")
		}
		if _, err := codexdesktop.SavedPath("/validated", m.ID); err != nil {
			return codexRecovery{}, err
		}
		if observed(m) && !isCurrentChallenge(m) && !obsoleteProtocol(m) {
			ids = append(ids, m.ID)
		}
	}
	answered := map[string]bool{}
	if len(ids) > 0 {
		values, err := s.hooks.C.HMGet(ctx, bus.AnswersOf(s.as), ids...).Result()
		if err != nil {
			return codexRecovery{}, err
		}
		if len(values) != len(ids) {
			return codexRecovery{}, errors.New("bus answer lookup returned incomplete fields")
		}
		for i, value := range values {
			if value == nil {
				continue
			}
			raw, ok := value.(string)
			if !ok {
				return codexRecovery{}, errors.New("bus answer wants a timestamp")
			}
			stamp, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || stamp <= 0 {
				return codexRecovery{}, errors.New("bus answer wants a valid store timestamp")
			}
			answered[ids[i]] = true
		}
	}
	matched := activeSum == ""
	out := make([]bus.Message, 0)
	bytes := 0
	for _, entry := range entries {
		m := entry.Message()
		if isCurrentChallenge(m) {
			matched = true
			if plan.challengeIDs == nil {
				plan.challengeIDs = map[string]bool{}
			}
			plan.challengeIDs[m.ID] = true
			plan.challengeEpoch = states[0].Epoch
		} else {
			if !observed(m) || obsoleteProtocol(m) {
				continue
			}
			if answered[m.ID] {
				continue
			}
		}
		if _, err := codexdesktop.SavedPath("/validated", m.ID); err != nil {
			return codexRecovery{}, fmt.Errorf("startup recovery event ID is malformed: %w", err)
		}
		bytes += codexMessageBytes(m)
		if len(out) >= maxCodexRecovery || bytes > maxCodexEvent-4096 {
			return codexRecovery{}, errors.New("native startup recovery incomplete: ordered batch exceeds message or byte budget; explicit backlog reconciliation is required")
		}
		out = append(out, m)
	}
	if !matched {
		later, err := s.bus.Range(ctx, bus.StreamOf(s.as), "("+tail, "+", maxCodexRecovery+1)
		if err != nil {
			return codexRecovery{}, err
		}
		for _, entry := range later {
			m := entry.Message()
			if isCurrentChallenge(m) {
				matched = true
				break
			}
		}
	}
	if !matched {
		return codexRecovery{}, errors.New("native startup recovery incomplete: active challenge has no matching actual bus message through captured tail; wait for the server to publish its next challenge")
	}
	encoded, err := json.Marshal(out)
	if err != nil || len(encoded) > maxCodexEvent-4096 {
		return codexRecovery{}, errors.New("native startup recovery incomplete: encoded batch exceeds byte budget")
	}
	plan.messages = out
	return plan, nil
}

func (p *codexRecovery) afterEpoch(epoch int64) {
	if len(p.challengeIDs) == 0 || p.challengeEpoch == epoch {
		return
	}
	kept := p.messages[:0]
	for _, m := range p.messages {
		if !p.challengeIDs[m.ID] {
			kept = append(kept, m)
		}
	}
	p.messages = kept
	p.challengeIDs = nil
}

// canonical reports whether m has the server's whole challenge shape.
func canonical(m bus.Message, recipient string) bool {
	_, ok := notice.Canonical(m, recipient)
	return ok
}
