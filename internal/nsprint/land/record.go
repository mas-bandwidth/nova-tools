package land

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ID is a PR named as <repo>#<n>.
type ID struct {
	Repo string
	N    int
}

func (id ID) String() string { return id.Repo + "#" + strconv.Itoa(id.N) }

// ParseID reads <repo>#<n>. A bare number is refused: a PR number without
// its repo names two PRs.
func ParseID(s string) (ID, error) {
	repo, num, ok := strings.Cut(s, "#")
	if !ok || repo == "" || strings.ContainsAny(repo, ": \t") {
		return ID{}, fmt.Errorf("want <repo>#<n>, got %q", s)
	}
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return ID{}, fmt.Errorf("want <repo>#<n> with a positive number, got %q", s)
	}
	return ID{Repo: repo, N: n}, nil
}

// parentID resolves stack_parent against the child's repo. "" or "none" is
// no parent.
func parentID(child ID, v string) (ID, bool, error) {
	if v == "" || v == "none" {
		return ID{}, false, nil
	}
	if strings.HasPrefix(v, "#") {
		v = child.Repo + v
	} else if !strings.Contains(v, "#") {
		v = child.Repo + "#" + v
	}
	id, err := ParseID(v)
	return id, err == nil, err
}

// Hold is one record of s:<S>:hold:<repo>:<n>.
type Hold struct {
	ID          string `json:"-"`
	Holder      string `json:"holder"`
	Head        string `json:"head"`
	Kind        string `json:"kind"`
	Reason      string `json:"reason"`
	URL         string `json:"url"`
	At          string `json:"at"`
	ReleasedBy  string `json:"released_by"`
	ReleaseKind string `json:"release_kind"`
	ReleaseURL  string `json:"release_url"`
	ReleasedAt  string `json:"released_at"`
}

func (h Hold) Open() bool { return h.ReleasedBy == "" }

func parseHolds(m map[string]string) ([]Hold, error) {
	out := make([]Hold, 0, len(m))
	for id, v := range m {
		var h Hold
		if err := json.Unmarshal([]byte(v), &h); err != nil {
			return nil, fmt.Errorf("hold %s: %w", id, err)
		}
		if h.Holder == "" || h.Head == "" {
			return nil, fmt.Errorf("hold %s: holder and head are required", id)
		}
		h.ID = id
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Read is one typed disposition from s:<S>:disp:<repo>:<n>, or one
// s:<S>:read:<unit>:<who> record. CarriedFrom is the head the line was
// typed at when `read carry` (nova-tools #3630) moved it to Head across an
// identical-diff head move; "" for a line typed at Head itself. A carried
// read counts as a read: the lander compares Head, never CarriedFrom, and
// `why` prints carried_from=<h8> on it (#3612). A read rubric SCORE line is
// recorded with Verdict APPROVE, so it counts like one (#3612).
type Read struct {
	Friend      string
	Head        string
	Verdict     string
	Score       int
	HasScore    bool
	CarriedFrom string
}

func parseReads(m map[string]string) []Read {
	out := make([]Read, 0, len(m))
	for field, v := range m {
		friend, head, ok := strings.Cut(field, "@")
		if !ok {
			continue
		}
		parts := strings.Fields(v)
		r := Read{Friend: friend, Head: head}
		if len(parts) > 0 {
			r.Verdict = parts[0]
		}
		if len(parts) > 1 {
			if n, err := strconv.Atoi(parts[1]); err == nil {
				r.Score, r.HasScore = n, true
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Friend != out[j].Friend {
			return out[i].Friend < out[j].Friend
		}
		return out[i].Head < out[j].Head
	})
	return out
}

// FriendState is friend:<f>:state; Known is false when the hash is absent.
type FriendState struct {
	State string
	Since time.Time
	Known bool
}

func parseFriend(m map[string]string) FriendState {
	if len(m) == 0 || m["state"] == "" {
		return FriendState{}
	}
	t, _ := parseTime(m["since"])
	return FriendState{State: m["state"], Since: t, Known: true}
}

// Absent is the set of states that let a reader release a carried hold
// (v6 3.5 (2)(c): down or out-of-credits; away is a declared absence).
func (f FriendState) Absent() bool {
	switch f.State {
	case "down", "out-of-credits", "away":
		return true
	}
	return false
}

// parseTime reads unix seconds or RFC 3339.
func parseTime(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(n, 0), true
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// Age prints a duration the way the table does: minutes under two hours,
// hours after.
func Age(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	m := int(d / time.Minute)
	if m < 120 {
		return strconv.Itoa(m) + "m"
	}
	return strconv.Itoa(m/60) + "h"
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	if sha == "" {
		return "MISSING"
	}
	return sha
}
