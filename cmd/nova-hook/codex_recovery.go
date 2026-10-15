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
	cutover         time.Time
	archived        int
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
	trimHistory := func(in []bus.Entry) []bus.Entry {
		if len(in) <= maxCodexHistory {
			return in
		}
		trimmed := make([]bus.Entry, 0, maxCodexHistory)
		drop := len(in) - maxCodexHistory
		for _, entry := range in {
			if drop > 0 && !isCurrentChallenge(entry.Message()) {
				drop--
				plan.archived++
				continue
			}
			trimmed = append(trimmed, entry)
		}
		plan.cutover = now.UTC().Truncate(time.Second)
		return trimmed
	}
	var entries []bus.Entry
	curFrom := floor
	lastCursor := ""
	for {
		page, err := s.bus.Range(ctx, bus.StreamOf(s.as), curFrom, tail, maxCodexHistory+1)
		if err != nil {
			return codexRecovery{}, err
		}
		entries = trimHistory(append(entries, page...))
		if len(page) <= maxCodexHistory {
			break
		}
		nextCursor := page[len(page)-1].Entry
		if nextCursor == "" || nextCursor == lastCursor || nextCursor == tail {
			break
		}
		lastCursor = nextCursor
		curFrom = "(" + nextCursor
	}
	// The explicit ordinary-history boundary never authorizes dropping a current
	// server challenge. Recover only its actual hash-matching event before floor.
	if activeSum != "" {
		found := false
		for _, entry := range entries {
			if isCurrentChallenge(entry.Message()) {
				found = true
				break
			}
		}
		if !found {
			searchFrom := bus.IDAt(window)
			if !since.After(window) {
				searchFrom = "-"
			}
			searchTo := "(" + floor
			selected := make([]bus.Entry, 0)
			lastEarly := ""
			for {
				earlier, err := s.bus.Range(ctx, bus.StreamOf(s.as), searchFrom, searchTo, maxCodexHistory+1)
				if err != nil {
					return codexRecovery{}, err
				}
				for _, entry := range earlier {
					if isCurrentChallenge(entry.Message()) {
						selected = append(selected, entry)
					}
				}
				if len(earlier) <= maxCodexHistory {
					break
				}
				nextCursor := earlier[len(earlier)-1].Entry
				if nextCursor == "" || nextCursor == lastEarly {
					break
				}
				lastEarly = nextCursor
				searchFrom = "(" + nextCursor
			}
			if len(selected) > 0 {
				entries = trimHistory(append(selected, entries...))
			}
		}
	}
	totalHistoryBytes := 0
	for _, entry := range entries {
		totalHistoryBytes += codexMessageBytes(entry.Message())
	}
	if totalHistoryBytes > maxCodexHistoryBytes {
		trimmed := make([]bus.Entry, 0, len(entries))
		for _, entry := range entries {
			m := entry.Message()
			if totalHistoryBytes > maxCodexHistoryBytes && !isCurrentChallenge(m) {
				totalHistoryBytes -= codexMessageBytes(m)
				plan.archived++
				continue
			}
			trimmed = append(trimmed, entry)
		}
		entries = trimmed
		plan.cutover = now.UTC().Truncate(time.Second)
	}
	observed := func(m bus.Message) bool { return m.From != s.as || strings.HasPrefix(m.Subject, "HOOK CHALLENGE ") }
	senderNames := make([]string, 0)
	seenSenders := map[string]bool{}
	serverRole := map[string]bool{hook.ServeFrom: true}
	for _, entry := range entries {
		m := entry.Message()
		if canonical(m, s.as) && !serverRole[m.From] && !seenSenders[m.From] {
			if why := bus.CheckName(m.From); why != "" {
				return codexRecovery{}, errors.New(why)
			}
			seenSenders[m.From] = true
			senderNames = append(senderNames, m.From)
		}
	}
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
	for _, entry := range entries {
		m := entry.Message()
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
		if !isCurrentChallenge(m) && codexMessageBytes(m) > maxCodexEvent-4096 {
			plan.archived++
			plan.cutover = now.UTC().Truncate(time.Second)
			continue
		}
		out = append(out, m)
	}
	// Cut over and archive the oldest ordinary messages when the recovery batch
	// exceeds message or byte budgets, keeping any current challenge.
	dropOldestOrdinary := func() bool {
		for i, m := range out {
			if !isCurrentChallenge(m) {
				out = append(out[:i], out[i+1:]...)
				plan.archived++
				plan.cutover = now.UTC().Truncate(time.Second)
				return true
			}
		}
		return false
	}
	for len(out) > maxCodexRecovery {
		if !dropOldestOrdinary() {
			break
		}
	}
	for {
		bytes := 0
		for _, m := range out {
			bytes += codexMessageBytes(m)
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			return codexRecovery{}, err
		}
		if bytes <= maxCodexEvent-4096 && len(encoded) <= maxCodexEvent-4096 {
			break
		}
		if !dropOldestOrdinary() {
			break
		}
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
