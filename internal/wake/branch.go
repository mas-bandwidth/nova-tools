package wake

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// A branch moving -- --ref, the amendment of 2026-09-13, draft 2.
//
// THE FLAG IS --ref AND NOT --branch, and that is a repair of draft 1: --branch
// already means THE BUS CHECKOUT'S BRANCH on the same verb, so
// `watch --refresh --remote origin --branch main --branch o/r:main` had no
// parse and a flag on this tool meant two things at once. One flag, one
// meaning, on every verb.
//
// The state value is the head sha, or `absent` when the branch does not exist;
// any difference is a change -- a push, a force-push, a deletion, a creation --
// and the line says BOTH ENDS.
//
// One tick is one gh api call, repos/<owner>/<repo>/git/ref/heads/<name>, about
// 300 bytes down, against the forge's REST pool. THE DOCUMENTED CONTRACT OF
// THAT ENDPOINT IS ONE EXACT REFERENCE OR A 404, and this spec neither tests
// nor relies on any other: a 404 is absent, a single ref object is its sha, and
// any other shape -- an array among them -- is unreadable, never absent,
// because an answer this source cannot read is not a name that matched none.

// Branches is the --ref source.
type Branches struct {
	Names   []string // <owner/repo>:<name>
	Every_  time.Duration
	Timeout time.Duration
	Prev    func(key string) (string, bool)

	calls
	read, changed, unreadable int
}

func (b *Branches) Name() string         { return "branches" }
func (b *Branches) Every() time.Duration { return b.Every_ }

func (b *Branches) Counts() (read, changed, unreadable, calls int) {
	return b.read, b.changed, b.unreadable, b.Calls()
}

func (b *Branches) Poll(ctx context.Context, now time.Time) (Result, error) {
	res := Result{}
	bad := 0
	var reason string
	for _, name := range b.Names {
		b.read++
		key := "branch:" + name
		head, err := b.one(ctx, name)
		var value string
		if err != nil {
			bad++
			b.unreadable++
			reason = err.Error()
			value = Compose("unreadable", oneLineOf(err.Error()))
		} else {
			value = withWas(b.Prev, key, head)
		}
		if old, had := b.Prev(key); !had || old != value {
			b.changed++
		}
		res.Items = append(res.Items, Item{Kind: KindBranch, Key: key, Value: value})
	}
	if len(b.Names) > 0 && bad == len(b.Names) {
		return res, fmt.Errorf("every --ref is unreadable: %s", oneLineOf(reason))
	}
	return res, nil
}

// one is the single REST read and the three readings it can have.
func (b *Branches) one(ctx context.Context, name string) (string, error) {
	repo, branch, err := SplitRef(name)
	if err != nil {
		return "", err
	}
	owner, rname := ownerRepo(repo)
	raw, err := gh(ctx, b.Timeout, &b.calls, "api",
		fmt.Sprintf("repos/%s/%s/git/ref/heads/%s", owner, rname, branch))
	if err != nil {
		if notFound(err) {
			return "absent", nil
		}
		return "", err
	}
	var one struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if uerr := json.Unmarshal(raw, &one); uerr != nil {
		// An array among them. The defensive branch stays because a forge's
		// answer is gh's to give and not this spec's to promise; what is
		// withdrawn is the claim about WHEN it occurs.
		return "", fmt.Errorf("the forge answered a shape this source cannot read as one reference: %s", oneLineOf(uerr.Error()))
	}
	if one.Object.SHA == "" {
		return "", fmt.Errorf("the forge answered a reference with no object sha")
	}
	return one.Object.SHA, nil
}
