package friend

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The friend's canonical configuration, applied to the running lanes (the owner, 2026-10-16:
// "nova-config automatically owns lane count; never unhook to resize"; about once a second
// the adapter fetches the friend's canonical nova-config row and applies it to the runtime).
// The source is nova-config's own interface, `nova-config friend show <name> --json`: no
// copy of it is kept anywhere the client could read instead (no flag, no file, no env, no
// default). The loop calls Client.Apply every tick; Apply fetches when ConfigEvery has
// passed and says a change once. What the applied row decides:
//
//   - width: the lanes that take. A round takes a free lane only while her held cards are
//     fewer than the width. Raised, the next round takes into the new lanes; lowered, no
//     lane takes until her held cards are under it, and every held card's lane is still
//     asked (renewed) while friend cards answers, until it finishes: a shrink drains, never
//     drops, returns or re-deals. A blind round (friend cards not answering) asks nothing
//     while her held lanes are more than the width (client.go Round, the bound).
//
// A fetch that fails, or a row that names no width, closes intake (the held lanes are still
// renewed while friend cards answers; a blind round asks nothing) until a fetch answers again; the client never makes up a width. The sprint server
// holds the same bound on its side (its take is held to the row's width, which friend sync
// copies from nova-config). The model is tla/FriendLaneClient.tla (SetWidth, ConfigLost:
// NoLostCard, NoDuplicateHold, DrainNeverTakes, GrowTakes).

// ConfigEvery is how often the loop's Apply fetches the canonical row.
const ConfigEvery = time.Second

// Config is the part of the friend's nova-config row the runtime applies.
type Config struct {
	Width int
}

// Fetch reads the friend's canonical nova-config row: what `nova-config friend show <name>
// --json` printed, or why it could not.
type Fetch func(ctx context.Context, name string) (string, error)

// ConfigArgv is the canonical read of a friend's row, after the nova-config binary.
func ConfigArgv(name string) []string { return []string{"friend", "show", name, "--json"} }

// ParseConfig reads nova-config friend show --json for name: its width (a whole number from
// 1). A refusal, another friend's row or a row with no width is an error: the
// runtime applies the row as nova-config says it, and never a default in its place.
func ParseConfig(name, out string) (Config, error) {
	var a struct {
		Result struct {
			Status string   `json:"status"`
			Why    []string `json:"why"`
		} `json:"result"`
		Items []struct {
			Kind   string            `json:"kind"`
			Fields map[string]string `json:"fields"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &a); err != nil {
		return Config{}, fmt.Errorf("nova-config friend show: the answer is not JSON (%T)", err)
	}
	if a.Result.Status != "ok" {
		return Config{}, fmt.Errorf("nova-config friend show %s: %s: %s", name, cmpOr(a.Result.Status, "no status"), strings.Join(a.Result.Why, "; "))
	}
	for _, it := range a.Items {
		if it.Kind != "friend" || it.Fields["name"] != name {
			continue
		}
		w, err := strconv.Atoi(strings.TrimSpace(it.Fields["width"]))
		if err != nil || w < 1 {
			return Config{}, fmt.Errorf("nova-config friend %s names no width from 1 (width=%q); intake waits for one: nova-config friend set %s --width <n>", name, it.Fields["width"], name)
		}
		return Config{Width: w}, nil
	}
	return Config{}, fmt.Errorf("nova-config friend show %s: the answer holds no row for %s", name, name)
}

func (c Config) String() string {
	return fmt.Sprintf("width=%d", c.Width)
}

// applied is the configuration a client runs by: the row last fetched, or why there is none.
type applied struct {
	cfg  Config
	ok   bool   // the last fetch answered a row: intake is open to cfg.Width
	why  string // the last fetch's failure, "" when ok
	next time.Time
	said string // the last state said, so a change is said once
}

// Change is what one Apply changed: the line the loop prints, once per change.
type Change struct {
	OK     bool   // intake open at Config.Width; false: intake closed, Why says why
	Config Config // the row applied (when OK)
	Why    string
}

// Apply is the one call the loop makes every tick: when ConfigEvery has passed since the
// last fetch, it fetches the canonical row and applies it, and answers the change when what
// the client runs by changed (a new width, a fetch that failed, one that answers
// again), so the caller says it once and asks the lanes on its next round. Nothing to fetch
// (no Fetch) is a closed intake too: a client with no configuration takes nothing.
func (c *Client) Apply(ctx context.Context, now time.Time) (Change, bool) {
	if now.Before(c.conf.next) {
		return Change{}, false
	}
	c.conf.next = now.Add(ConfigEvery)
	var cfg Config
	var err error
	if c.Fetch == nil {
		err = fmt.Errorf("no nova-config read is wired for %s", c.Name)
	} else {
		var out string
		out, err = c.Fetch(ctx, c.Name)
		if err == nil {
			cfg, err = ParseConfig(c.Name, out)
		}
	}
	if err != nil {
		// intake closes; the row last applied is kept only to say what changed, never to take by
		c.conf.ok, c.conf.why = false, err.Error()
	} else {
		c.conf.cfg, c.conf.ok, c.conf.why = cfg, true, ""
	}
	state := "closed " + c.conf.why
	if c.conf.ok {
		state = "open " + c.conf.cfg.String()
	}
	if state == c.conf.said {
		return Change{}, false
	}
	c.conf.said = state
	return Change{OK: c.conf.ok, Config: c.conf.cfg, Why: c.conf.why}, true
}

// Intake is the lanes that take now: the applied row's width, 0 while no row is applied.
func (c *Client) Intake() int {
	if !c.conf.ok {
		return 0
	}
	return c.conf.cfg.Width
}
