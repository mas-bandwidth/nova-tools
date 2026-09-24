package land

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

var (
	// orderPrefixes captures ordering phrases
	orderWords = map[string]bool{
		"after":   true,
		"lands":   true,
		"land":    true,
		"order":   true,
		"blocked": true,
		"on":      true,
		"behind":  true,
		"wait":    true,
		"for":     true,
		"parent":  true,
		"stack":   true,
		"depends": true,
		"needs":   true,
	}

	ciWords = map[string]bool{
		"ci":       true,
		"actions":  true,
		"action":   true,
		"failed":   true,
		"failing":  true,
		"failure":  true,
		"red":      true,
		"check":    true,
		"checks":   true,
		"build":    true,
		"test":     true,
		"tests":    true,
		"pipeline": true,
		"job":      true,
		"run":      true,
		"flake":    true,
		"flaky":    true,
		"status":   true,
		"on":       true,
		"in":       true,
		"at":       true,
		"for":      true,
		"the":      true,
		"macos":    true,
		"linux":    true,
		"windows":  true,
		"darwin":   true,
		"ubuntu":   true,
		"runner":   true,
		"runners":  true,
		"shard":    true,
		"shards":   true,
	}

	unitOrPRRefRe = regexp.MustCompile(`([a-zA-Z0-9_\.\-]+#\d+|#\d+|gh/[a-zA-Z0-9_\.\-\/]+|[a-zA-Z0-9_\.\-]+/[a-zA-Z0-9_\.\-]+#[0-9]+|[a-zA-Z0-9_\-]+:[a-zA-Z0-9_\-]+)`)
)

// ClassifyHoldReason analyzes a hold reason to determine if it is an order note,
// a CI note, or a substantive hold (§3.4, L13).
func ClassifyHoldReason(reason string) (isNote bool, noteKind string, stackParent string) {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return false, "", ""
	}

	lower := strings.ToLower(trimmed)

	// Clean punctuation
	cleaned := strings.Map(func(r rune) rune {
		if r == ':' || r == ',' || r == ';' || r == '.' || r == '!' || r == '(' || r == ')' {
			return ' '
		}
		return r
	}, lower)

	tokens := strings.Fields(cleaned)
	if len(tokens) == 0 {
		return false, "", ""
	}

	// 1. Check if reason is purely CI state
	isCIOnly := true
	for _, tok := range tokens {
		if !ciWords[tok] {
			isCIOnly = false
			break
		}
	}
	if isCIOnly {
		return true, "ci", ""
	}

	// 2. Check if reason is order-only
	// Find unit or PR ref in original string
	matches := unitOrPRRefRe.FindAllString(trimmed, -1)
	if len(matches) > 0 {
		// Remove matches from the cleaned token list and see if remaining tokens are all ordering words
		rem := trimmed
		for _, m := range matches {
			rem = strings.Replace(rem, m, " ", 1)
		}
		remCleaned := strings.Map(func(r rune) rune {
			if r == ':' || r == ',' || r == ';' || r == '.' || r == '!' || r == '(' || r == ')' || r == '#' {
				return ' '
			}
			return r
		}, strings.ToLower(rem))
		remTokens := strings.Fields(remCleaned)

		isOrderOnly := true
		for _, tok := range remTokens {
			if !orderWords[tok] {
				isOrderOnly = false
				break
			}
		}
		if isOrderOnly {
			return true, "order", matches[0]
		}
	}

	// Substantive hold
	return false, "", ""
}

// RecordHoldOrNote records a hold or note according to §3.4 and L13.
// If reason is classified as order or CI, it writes a NOTE and updates stack_parent,
// keeping holds_open unchanged. Otherwise it writes a HOLD and increments holds_open.
func RecordHoldOrNote(ctx context.Context, c *redis.Client, sprint, unit, holder, head, kind, reason, url, files, doneWhen, origin string) (seq int64, isHold bool, err error) {
	isNote, noteKind, stackParent := ClassifyHoldReason(reason)

	if isNote {
		// Update stack_parent if this was an order note
		if noteKind == "order" && stackParent != "" {
			resolvedParent := stackParent
			// If stackParent is #<n> or <repo>#<n>, check s:<S>:prunit:<repo>:<n>
			if strings.HasPrefix(stackParent, "#") {
				repo := c.HGet(ctx, UnitKey(sprint, unit), "repo").Val()
				prStr := strings.TrimPrefix(stackParent, "#")
				if prNum, err := strconv.Atoi(prStr); err == nil && repo != "" {
					u := c.Get(ctx, PRUnitKey(sprint, repo, prNum)).Val()
					if u != "" {
						resolvedParent = u
					}
				}
			} else if strings.Contains(stackParent, "#") {
				parts := strings.SplitN(stackParent, "#", 2)
				if prNum, err := strconv.Atoi(parts[1]); err == nil {
					u := c.Get(ctx, PRUnitKey(sprint, parts[0], prNum)).Val()
					if u != "" {
						resolvedParent = u
					}
				}
			}
			_ = c.HSet(ctx, UnitKey(sprint, unit), "stack_parent", resolvedParent).Err()
		}

		// Record as a note via ns_read with verdict="NOTE"
		seq, err = CallRead(ctx, c, sprint, unit, holder, head, "NOTE", "", noteKind, files, doneWhen)
		return seq, false, err
	}

	// Substantive hold: call ns_hold
	seq, _, err = CallHold(ctx, c, sprint, unit, holder, head, kind, reason, url, files, doneWhen, origin)
	return seq, true, err
}

// SupersedeHoldsOnApprove checks open holds by `who` on `unit`.
// If `who` posts a newer APPROVE at `newHead`, any older HOLD by `who` at `olderHead` (where olderHead != newHead)
// is released with release_kind="superseded" (§3.4, L29).
// An APPROVE at an older head never releases a newer HOLD.
func SupersedeHoldsOnApprove(ctx context.Context, c *redis.Client, sprint, unit, who, newHead string, isNewer func(h1, h2 string) bool) ([]string, error) {
	hkey := HoldKey(sprint, unit, who)
	held, err := c.HGetAll(ctx, hkey).Result()
	if err != nil || len(held) == 0 {
		return nil, nil
	}

	// If already released, nothing to do
	if held["released_by"] != "" {
		return nil, nil
	}

	holdHead := held["head"]
	// If the hold was placed at a different head and newHead is newer:
	if holdHead != "" && holdHead != newHead {
		if isNewer != nil && !isNewer(newHead, holdHead) {
			// newHead is not newer than holdHead; do not release
			return nil, nil
		}
		// Supersede!
		_, err := CallRelease(ctx, c, sprint, unit, who, who, "superseded", "superseded at "+newHead, "")
		if err != nil {
			return nil, err
		}
		return []string{who}, nil
	}

	return nil, nil
}

// ParseInboundObjection inspects a webhook event (§3.4, L29b).
// If issue_comment / pull_request_review begins with HOLD or BLOCKED,
// or pull_request body starts with HOLD or BLOCKED: returns isObjection=true.
func ParseInboundObjection(eventType, login, body string) (isObjection bool, reason string) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return false, ""
	}

	switch eventType {
	case "issue_comment", "pull_request_review":
		fields := strings.Fields(trimmed)
		if len(fields) > 0 {
			first := strings.ToUpper(strings.Trim(fields[0], ":*#_"))
			if first == "HOLD" || first == "BLOCKED" {
				return true, trimmed
			}
		}
	case "pull_request":
		lines := strings.Split(trimmed, "\n")
		if len(lines) > 0 {
			firstLine := strings.ToUpper(strings.TrimSpace(lines[0]))
			if strings.HasPrefix(firstLine, "HOLD") || strings.HasPrefix(firstLine, "BLOCKED") {
				return true, trimmed
			}
		}
	}
	return false, ""
}

// RecordInboundObjection records an inbound objection as a hold keyed login:<login> (§3.4, L29b).
func RecordInboundObjection(ctx context.Context, c *redis.Client, sprint, unit, head, login, body string) (seq int64, isHold bool, err error) {
	holder := "login:" + login
	return RecordHoldOrNote(ctx, c, sprint, unit, holder, head, "objection", body, "", "", "", "inbound")
}

// ReleaseInboundObjection releases an inbound objection from login:<login> (§3.4, L29b).
// Released either by the same login or by a friend via --releases.
func ReleaseInboundObjection(ctx context.Context, c *redis.Client, sprint, unit, login, releasedBy, reason string) (int64, error) {
	holder := "login:" + login
	return CallRelease(ctx, c, sprint, unit, holder, releasedBy, "inbound-release", reason, "")
}
