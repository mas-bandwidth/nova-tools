// Package reader is the one reader-choice function shared by ok-to-friend,
// pr-to-read, hold-to-fix, redistribute and rebalance (nova-tools #3100,
// spec 5.5, controls 44 and 61).
//
// Choose never writes Redis and never creates a task. It names at most one
// may-hold reader with free width, or it says NO-READER. A friend who already
// holds this read is not given another task; that line says DEDUP.
package reader

import (
	"fmt"
	"strings"
)

// Login is one binding in friends:login. A login bound to exactly one friend
// names that friend as the author. A login bound to more than one friend names
// nobody: a shared login is not a guess, and an unknown author is not excluded.
type Login struct {
	Login  string
	Friend string
}

// Friend is one candidate reader. Load is that friend's open+working count.
// Free width is Desired minus Starting minus Living, the same accounting as
// task width. Only a friend with free width greater than zero can be chosen.
type Friend struct {
	Name     string
	State    string
	MayHold  bool
	Desired  int
	Starting int
	Living   int
	Load     int
}

// Task is a read the friend already holds. Dedup matches on repo, PR, the
// full head and the friend, and only when State is closed or working.
type Task struct {
	ID     string
	Friend string
	Repo   string
	PR     int
	Head   string
	State  string
}

// Line is a typed disposition already recorded for a friend at a head.
// Dedup matches on repo, PR, the full head and the friend. ID is printed
// when set; otherwise the line uses disp:<friend>@<head>.
type Line struct {
	ID     string
	Friend string
	Repo   string
	PR     int
	Head   string
}

// Request is one choice: who may read this repo's PR at this full head.
type Request struct {
	Repo        string
	PR          int
	Head        string
	AuthorLogin string
	Logins      []Login
	Friends     []Friend
	Tasks       []Task
	Lines       []Line
}

// Choice is the decision. Friend is empty when no new task may be created.
// Lines are printed in order: a DEDUP line for each otherwise-eligible friend
// who already holds the read, then NO-READER <pr> when Friend is empty.
type Choice struct {
	Friend string
	Lines  []string
}

// Assign reports whether the caller should create one new read for Friend.
func (c Choice) Assign() bool { return c.Friend != "" }

// Choose excludes the author and any friend who is down, out of credits, or
// away. It also skips a friend with no may-hold permission or no free width.
// Among whoever remains it returns the least-loaded reader, ties kept in
// Friends order. A friend who already has a closed or working task, or a typed
// line, for this repo, PR and full head gets no new task.
func Choose(req Request) (Choice, error) {
	if err := validate(req); err != nil {
		return Choice{}, err
	}
	author := resolveAuthor(req.AuthorLogin, req.Logins)
	head := norm(req.Head)
	repo := norm(req.Repo)

	var lines []string
	type cand struct {
		name string
		load int
	}
	var cands []cand
	for _, f := range req.Friends {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			continue
		}
		if author != "" && norm(name) == author {
			continue
		}
		if !available(f.State) || !f.MayHold || freeWidth(f) <= 0 {
			continue
		}
		if id, reason, blocked := blocked(req, name, repo, head); blocked {
			lines = append(lines, fmt.Sprintf("DEDUP %s: %s", id, reason))
			continue
		}
		cands = append(cands, cand{name: name, load: f.Load})
	}
	if len(cands) == 0 {
		lines = append(lines, fmt.Sprintf("NO-READER %d", req.PR))
		return Choice{Lines: lines}, nil
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c.load < best.load {
			best = c
		}
	}
	return Choice{Friend: best.name, Lines: lines}, nil
}

func validate(req Request) error {
	if strings.TrimSpace(req.Repo) == "" {
		return fmt.Errorf("reader: repo is required")
	}
	if req.PR <= 0 {
		return fmt.Errorf("reader: pr must be positive")
	}
	if !fullHead(req.Head) {
		return fmt.Errorf("reader: head must be the full sha, not a prefix")
	}
	return nil
}

func fullHead(h string) bool {
	h = strings.TrimSpace(h)
	if len(h) != 40 && len(h) != 64 {
		return false
	}
	for _, c := range h {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

func resolveAuthor(login string, bindings []Login) string {
	login = norm(login)
	if login == "" {
		return ""
	}
	author := ""
	for _, b := range bindings {
		if norm(b.Login) != login {
			continue
		}
		friend := norm(b.Friend)
		if friend == "" {
			continue
		}
		if author == "" {
			author = friend
			continue
		}
		if author != friend {
			return ""
		}
	}
	return author
}

// available is the friend who can take a read. underfull and idle still can.
// down, out-of-credits, away, and any state not named here, cannot.
func available(state string) bool {
	switch norm(state) {
	case "up", "underfull", "idle":
		return true
	default:
		return false
	}
}

func freeWidth(f Friend) int {
	return f.Desired - f.Starting - f.Living
}

// blocked reports the first reason this friend must not receive a new task
// for the request's identity. A working task outranks a closed read, which
// outranks a typed line. Anything short of the full head does not match.
func blocked(req Request, friend, repo, head string) (string, string, bool) {
	closedID := ""
	for _, t := range req.Tasks {
		if norm(t.Friend) != norm(friend) || norm(t.Repo) != repo || t.PR != req.PR {
			continue
		}
		if norm(t.Head) != head {
			continue
		}
		switch norm(t.State) {
		case "working":
			return taskID(t, friend, head), "working task at head", true
		case "closed":
			if closedID == "" {
				closedID = taskID(t, friend, head)
			}
		}
	}
	if closedID != "" {
		return closedID, "closed same-head read", true
	}
	for _, ln := range req.Lines {
		if norm(ln.Friend) != norm(friend) || norm(ln.Repo) != repo || ln.PR != req.PR {
			continue
		}
		if norm(ln.Head) != head {
			continue
		}
		id := strings.TrimSpace(ln.ID)
		if id == "" {
			id = "disp:" + norm(friend) + "@" + head
		}
		return id, "typed line at head", true
	}
	return "", "", false
}

func taskID(t Task, friend, head string) string {
	if id := strings.TrimSpace(t.ID); id != "" {
		return id
	}
	return fmt.Sprintf("read-%d-%s-%s", t.PR, head[:12], norm(friend))
}

func norm(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
