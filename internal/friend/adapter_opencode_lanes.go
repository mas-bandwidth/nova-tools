package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// LaneHarness is a Deliverer that can open a session of the friend and
// deliver into a session it names: what a one-shot lane needs, each lane its
// own session in the same harness and directory (docs/SPEC-FRIEND.md,
// one-shot lanes; the owner, 2026-10-04: "so [she] can still be wide, it's
// just 8 [of her]"). OpenCode is one.
type LaneHarness interface {
	Deliverer
	// OpenSession starts a new session of the friend with seed as its first
	// turn, and answers the new session's id.
	OpenSession(ctx context.Context, seed string) (session string, err error)
	// DeliverTo pushes text into session as one turn and blocks until it
	// ends: its exit, and a permission the harness refused without asking,
	// read from the turn's output (empty when none). A rate limit is
	// RateLimited and out of funds OutOfFunds (ProviderLimit); a provider's
	// refusal is ProviderRefused, as Deliver answers it.
	DeliverTo(ctx context.Context, session, text string) (LaneTurn, error)
}

// LaneTurn is how one lane turn ended.
type LaneTurn struct {
	Exit     int
	Rejected string // the line of the output where the harness refused a permission, if any
}

// permissionRejected is a line of a turn's output where a tool call was
// refused for want of a permission: a headless opencode run auto-rejects any
// call that would prompt (measured 2026-10-04: external_directory for a path
// through a symlink to the friend's directory), and the session says so.
var permissionRejected = regexp.MustCompile(`(?i)(\b(permission|external_directory)\b.*\b(reject\w*|denied|refused|blocked)\b|\b(reject\w*|denied|blocked)\b.*\bpermission\b)`)

// PermissionRejection is the first line of out that says a permission was
// refused, one line, at most 200 bytes; empty when none does.
func PermissionRejection(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if permissionRejected.MatchString(line) {
			return oneLine(stripANSI(line), 200)
		}
	}
	return ""
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSI(s string) string { return ansi.ReplaceAllString(s, "") }

// OpenCodeConfig is the project config file opencode reads in its directory.
const OpenCodeConfig = "opencode.json"

// AllowDirs writes into dir's opencode.json that a tool call may touch each
// of paths and everything under it without a prompt (permission
// external_directory, a pattern map of path globs to allow), merged into what
// the file holds, written only when it changes. A headless opencode run
// auto-rejects a call that would prompt, and the turn ends there (measured
// 2026-10-04: a friend's working directory reached through its symlink in the
// home directory). It answers whether it wrote.
func AllowDirs(dir string, paths []string) (bool, error) {
	path := filepath.Join(dir, OpenCodeConfig)
	cfg := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		cfg["$schema"] = "https://opencode.ai/config.json"
	case err != nil:
		return false, err
	default:
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return false, fmt.Errorf("%s is not a JSON object, so the lanes' directories cannot be allowed in it: %v", path, err)
		}
	}
	perm, _ := cfg["permission"].(map[string]any)
	if perm == nil {
		perm = map[string]any{}
	}
	if s, ok := perm["external_directory"].(string); ok && s == "allow" {
		return false, nil // every directory is allowed already
	}
	allowed, _ := perm["external_directory"].(map[string]any)
	if allowed == nil {
		allowed = map[string]any{}
	}
	changed := false
	for _, p := range paths {
		pattern := strings.TrimRight(p, "/") + "/**"
		if allowed[pattern] != "allow" {
			allowed[pattern], changed = "allow", true
		}
	}
	if !changed {
		return false, nil
	}
	perm["external_directory"], cfg["permission"] = allowed, perm
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	return true, atomicfile.WriteFile(path, append(out, '\n'), 0o644)
}

// lanePaths is the friend's directory as every path a tool call may name it
// by: as given, its real path, and each alias (a symlink in the home
// directory), deduplicated.
func (o *OpenCode) lanePaths() []string {
	paths := []string{o.Dir}
	if real, err := filepath.EvalSymlinks(o.Dir); err == nil {
		paths = append(paths, real)
	}
	paths = append(paths, o.Allow...)
	slices.Sort(paths)
	return slices.Compact(paths)
}

// allow is AllowDirs over the friend's paths before a turn; a config that
// cannot be written is said in the record and the turn goes ahead: the run
// may still need no prompt.
func (o *OpenCode) allow() {
	if _, err := AllowDirs(o.Dir, o.lanePaths()); err != nil && o.Out != nil {
		fmt.Fprintf(o.Out, "opencode: the friend's directories are not allowed in %s: %v; a tool call there may be auto-rejected\n", filepath.Join(o.Dir, OpenCodeConfig), err)
	}
}

// opening serialises the lanes' session opens: each finds its new session by
// the listing before and after its first turn.
var opening sync.Mutex

// OpenSession runs the seed as the first turn of a new session in Dir
// (`opencode run --dir <dir> <seed>`, no --session) and answers the session
// that appeared in the listing of Dir, the newest the listing before it did
// not hold. The lanes' directories are allowed in the project config first.
func (o *OpenCode) OpenSession(ctx context.Context, seed string) (string, error) {
	o.allow()
	opening.Lock()
	defer opening.Unlock()
	before, err := o.sessions(ctx)
	if err != nil {
		return "", err
	}
	out, exit, err := o.Run(ctx, o.Dir, o.program(), []string{"run", "--dir", o.Dir, seed}, "")
	if o.Out != nil && out != "" {
		fmt.Fprintln(o.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	if exit != 0 && err == nil {
		if limit := ProviderLimit("(new)", out); limit != nil {
			return "", limit // a rate limit or out of funds: the lanes' governor answers it, not the open's retry alone
		}
	}
	if exit, err = refused("(new)", out, exit, err); err != nil {
		return "", err
	}
	if exit != 0 {
		return "", fmt.Errorf("opencode run (a new session in %s) exited %d", o.Dir, exit)
	}
	after, err := o.sessions(ctx)
	if err != nil {
		return "", err
	}
	best := session{}
	for _, s := range after {
		if s.Directory == o.Dir && !slices.ContainsFunc(before, func(b session) bool { return b.ID == s.ID }) && s.Updated > best.Updated {
			best = s
		}
	}
	if best.ID == "" {
		return "", fmt.Errorf("opencode run started no new session in %s that the listing shows", o.Dir)
	}
	return best.ID, nil
}

func (o *OpenCode) sessions(ctx context.Context) ([]session, error) {
	listing, exit, err := o.Run(ctx, o.Dir, o.program(), []string{"session", "list", "--format", "json"}, "")
	if err != nil {
		return nil, fmt.Errorf("opencode session list: %w", err)
	}
	if exit != 0 {
		return nil, fmt.Errorf("opencode session list exited %d", exit)
	}
	var rows []session
	if err := json.Unmarshal([]byte(listing), &rows); err != nil {
		return nil, fmt.Errorf("opencode session list: not a JSON list: %v", err)
	}
	return rows, nil
}

// DeliverTo is one card's turn in a lane's session: `opencode run --session
// <id> --dir <dir> <text>`, its output read for a refused permission, and its
// tail for a rate limit or out of funds (ProviderLimit, whatever the exit:
// the lanes heed it only when the card has no RESULT.md) before a provider's
// refusal of the session. With a record (Out), the turn is priced from
// opencode's own session record, the session's running cost and tokens before
// the turn and after it (SessionSpend), and said in one OPENCODE RUN line
// with the provider's rate limit when the output carries one: an API friend
// has no window to read, only the provider's refusal (docs/SPEC-FRIEND.md,
// the headless Claude lane and the OpenCode lane).
func (o *OpenCode) DeliverTo(ctx context.Context, id, text string) (LaneTurn, error) {
	o.allow()
	var before SessionSpend
	var berr error
	price := o.prices()
	if price {
		before, berr = o.spend(ctx, id)
	}
	out, exit, err := o.Run(ctx, o.Dir, o.program(), []string{"run", "--session", id, "--dir", o.Dir, text}, "")
	if o.Out != nil && out != "" {
		fmt.Fprintln(o.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	if price {
		limit := "-"
		if m := providerErrorType.FindStringSubmatch(out); m != nil && m[1] == "rate_limit_error" {
			limit = m[1]
		}
		after, aerr := o.spend(ctx, id)
		if aerr == nil && berr == nil {
			fmt.Fprintf(o.Out, "OPENCODE RUN session=%s exit=%d %s limit=%s\n", id, exit, after.Minus(before), limit)
		} else {
			fmt.Fprintf(o.Out, "OPENCODE RUN session=%s exit=%d cost_usd=- limit=%s unpriced=%q\n", id, exit, limit, oneLine(errors.Join(berr, aerr).Error(), 200))
		}
	}
	if err == nil {
		if limit := ProviderLimit(id, out); limit != nil {
			return LaneTurn{Exit: exit, Rejected: PermissionRejection(out)}, limit
		}
	}
	exit, err = refused(id, out, exit, err)
	return LaneTurn{Exit: exit, Rejected: PermissionRejection(out)}, err
}

// SessionSpend is an opencode session's running cost and tokens, as its own
// record says them (`opencode export <id>`, the session's info).
type SessionSpend struct {
	Cost   json.Number `json:"cost"`
	Tokens struct {
		Input     int64 `json:"input"`
		Output    int64 `json:"output"`
		Reasoning int64 `json:"reasoning"`
		Cache     struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
}

// Minus is the turn between two reads of the session, said as the record's
// words: cost_usd= tokens_in= tokens_out= reasoning= cache_read= cache_write=.
func (s SessionSpend) Minus(before SessionSpend) string {
	cost := "-"
	if a, ok := new(big.Rat).SetString(string(s.Cost)); ok {
		b, ok := new(big.Rat).SetString(string(before.Cost))
		if !ok {
			b = new(big.Rat)
		}
		cost = strings.TrimRight(strings.TrimRight(a.Sub(a, b).FloatString(8), "0"), ".")
	}
	return fmt.Sprintf("cost_usd=%s tokens_in=%d tokens_out=%d reasoning=%d cache_read=%d cache_write=%d", cost,
		s.Tokens.Input-before.Tokens.Input, s.Tokens.Output-before.Tokens.Output, s.Tokens.Reasoning-before.Tokens.Reasoning,
		s.Tokens.Cache.Read-before.Tokens.Cache.Read, s.Tokens.Cache.Write-before.Tokens.Cache.Write)
}

// ReadSessionSpend reads the session's info from an export: only the
// leading "info" object, so an export cut short after it (measured
// 2026-10-04: a 680 KB export read through a pipe ended mid-string) still
// prices the turn.
func ReadSessionSpend(export string) (SessionSpend, error) {
	dec := json.NewDecoder(strings.NewReader(export))
	dec.UseNumber()
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return SessionSpend{}, fmt.Errorf("opencode export: not a JSON object")
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return SessionSpend{}, fmt.Errorf("opencode export: %v", err)
		}
		if key == "info" {
			var s SessionSpend
			if err := dec.Decode(&s); err != nil {
				return SessionSpend{}, fmt.Errorf("opencode export: the session's info: %v", err)
			}
			return s, nil
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return SessionSpend{}, fmt.Errorf("opencode export: %v", err)
		}
	}
	return SessionSpend{}, fmt.Errorf("opencode export: no session info")
}

// prices says a turn is read from opencode's own session record. A stand-in
// program (the lane-wall test runs this binary as the harness) is not
// opencode: an export through it is not a session record, and is not run.
func (o *OpenCode) prices() bool {
	if o.Out == nil {
		return false
	}
	p := o.Program
	return p == "" || p == "opencode" || strings.HasSuffix(p, "/opencode")
}

func (o *OpenCode) spend(ctx context.Context, id string) (SessionSpend, error) {
	out, exit, err := o.Run(ctx, o.Dir, o.program(), []string{"export", id}, "")
	if err != nil {
		return SessionSpend{}, fmt.Errorf("opencode export %s: %w", id, err)
	}
	if exit != 0 {
		return SessionSpend{}, fmt.Errorf("opencode export %s exited %d", id, exit)
	}
	return ReadSessionSpend(out)
}
