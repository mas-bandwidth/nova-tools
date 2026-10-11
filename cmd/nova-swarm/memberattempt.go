package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// THE ATTEMPT DECISION (docs/SPEC-SPRINT.md section 2; pkg/decide, attempt.go). A work
// member whose environment holds JEV_API_KEY (the loop row's nova-secrets keys) asks, when
// any take ends, how it ended (whether the class routes the finish is the server's to say,
// by the card's bars): the member's own process asks it, in the end's long work beside the
// push, and never hands the key to native or the child (nativeChildEnv removes it). The
// finish carries the decision.

// attemptWait bounds the backend's answer, as the decide read's does (decideWait).
const attemptWait = decideWait

// attemptDecider is the member's attempt decision over b, stamped by now: the decision's
// card line and its record line, or the backend's failure (the finish then goes by its
// reason line alone).
func attemptDecider(b decide.Backend, now func() time.Time) func(member.Packet, string, string) (string, []byte, error) {
	return func(p member.Packet, result, reason string) (string, []byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), attemptWait)
		defer cancel()
		d, err := decide.AttemptDecision(ctx, b, p.Primary, p.Attempt, p.Brief, result, reason, now())
		if err != nil {
			return "", nil, err
		}
		dec, err := decide.AttemptDecided(d)
		if err != nil {
			return "", nil, err
		}
		raw, err := json.Marshal(d)
		return dec.String(), raw, err
	}
}

// workAttempt is a work member's attempt decider: Jev over its real transport with the key
// JEV_API_KEY holds; nil for a reader, and with no key (the finish goes by its reason line).
func workAttempt(reader bool, getenv func(string) string) func(member.Packet, string, string) (string, []byte, error) {
	key := getenv(decide.JevSecret)
	if reader || key == "" {
		return nil
	}
	return attemptDecider(decide.JevHTTP(key, decide.JevTimeout), time.Now)
}
