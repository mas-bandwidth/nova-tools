package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
// refusal of the session.
func (o *OpenCode) DeliverTo(ctx context.Context, id, text string) (LaneTurn, error) {
	o.allow()
	out, exit, err := o.Run(ctx, o.Dir, o.program(), []string{"run", "--session", id, "--dir", o.Dir, text}, "")
	if o.Out != nil && out != "" {
		fmt.Fprintln(o.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	if err == nil {
		if limit := ProviderLimit(id, out); limit != nil {
			return LaneTurn{Exit: exit, Rejected: PermissionRejection(out)}, limit
		}
	}
	exit, err = refused(id, out, exit, err)
	return LaneTurn{Exit: exit, Rejected: PermissionRejection(out)}, err
}
