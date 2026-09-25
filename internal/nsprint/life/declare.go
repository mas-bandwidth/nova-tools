package life

// The friend wake registry (#3048 rev 3): `nova-sprint friend declare --from
// <fleet/group_vars/all.yml>` reads the fleet's `friends:` block at a committed
// revision and writes every friend's wake path in one Redis Function call,
// ns_friend_declare (internal/nsprint/fn/lua/friend_declare.lua). The fleet
// converge is the one writer; every other seat runs --check, which reads only.
//
// Every write carries the fleet source revision (git HEAD of the checkout)
// and the digest of the block at that revision. A checkout whose revision does
// not descend from the stored one is refused `stale`, and the function's
// compare-and-set on the stored revision refuses the loser of two declarers
// that read the same one, so a stale checkout can never overwrite the registry
// or emit a false friend-undeclared receipt.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
	"gopkg.in/yaml.v3"
)

// Registry keys.
const (
	DeclKey     = "friends:decl"
	DeclaredKey = "friends:declared"
	FriendsKey  = "friends" // registered friends (capacity friend / hello)
)

// FunctionDeclare is registered by friend_declare.lua.
const FunctionDeclare = "ns_friend_declare"

// WakePathKey is a friend's declared wake path.
func WakePathKey(f string) string { return "friend:" + f + ":wakepath" }

// The declare refusals, each with its exit code in the verb.
var (
	ErrDeclInvalid  = errors.New("invalid")  // exit 2: a friend misses a field
	ErrDeclDirty    = errors.New("dirty")    // exit 2: the file is not the committed one
	ErrDeclStale    = errors.New("stale")    // exit 3: this checkout does not descend from the registry
	ErrDeclConflict = errors.New("conflict") // exit 3: another declarer wrote first
)

// declFields maps the fleet file's keys to the wakepath hash fields, in the
// order the hash is written and the decl digest is taken.
var declFields = []struct{ yaml, field string }{
	{"wake", "mode"}, {"host", "host"}, {"harness", "harness"},
	{"unit", "unit"}, {"unit_file", "unit_file"},
	{"wake_bus", "bus"}, {"wake_bus_remote", "bus_remote"}, {"wake_bus_branch", "bus_branch"},
	{"wake_on_note", "wake_on_note"},
	{"keeper_unit", "keeper_unit"}, {"keeper_file", "keeper_file"},
	{"keeper_bus", "keeper_bus"}, {"keeper_remote", "keeper_remote"}, {"keeper_branch", "keeper_branch"},
	{"notify", "notify"},
}

// DeclEntry is one friend's declared wake path as the wakepath hash holds it.
type DeclEntry struct {
	Name   string
	Fields map[string]string // wakepath field -> value, non-empty only
}

// Decl is the sha256 of the entry's fields, the wakepath's decl field.
func (e DeclEntry) Decl() string {
	h := sha256.New()
	for _, f := range declFields {
		if v := e.Fields[f.field]; v != "" {
			fmt.Fprintf(h, "%s=%s\n", f.field, v)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Mode is the entry's wake mode.
func (e DeclEntry) Mode() string { return e.Fields["mode"] }

// ParseFriends reads the `friends:` block of a fleet group_vars file and
// validates every entry: `wake: unit` needs host, wake_bus, wake_bus_remote,
// wake_bus_branch and wake_on_note; keeper_unit, when present, needs
// keeper_bus, keeper_remote and keeper_branch; `wake: human` needs notify.
// Anything else is ErrDeclInvalid naming the friend and the field. Keys the
// registry does not hold (machine, slots) are ignored. It also returns the
// block's canonical bytes, whose sha256 is the registry digest.
func ParseFriends(file []byte) ([]DeclEntry, []byte, error) {
	var doc struct {
		Friends yaml.Node `yaml:"friends"`
	}
	if err := yaml.Unmarshal(file, &doc); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrDeclInvalid, err)
	}
	if doc.Friends.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%w: no friends: mapping", ErrDeclInvalid)
	}
	canon, err := yaml.Marshal(&doc.Friends)
	if err != nil {
		return nil, nil, err
	}
	var raw map[string]map[string]any
	if err := doc.Friends.Decode(&raw); err != nil {
		return nil, nil, fmt.Errorf("%w: friends: %v", ErrDeclInvalid, err)
	}
	names := make([]string, 0, len(raw))
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []DeclEntry
	var bad []string
	for _, n := range names {
		e := DeclEntry{Name: n, Fields: map[string]string{}}
		for _, f := range declFields {
			if v, ok := raw[n][f.yaml]; ok && v != nil {
				if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
					e.Fields[f.field] = s
				}
			}
		}
		var need []string
		switch e.Mode() {
		case WakeUnit:
			need = []string{"host", "wake_bus", "wake_bus_remote", "wake_bus_branch", "wake_on_note"}
			if _, ok := raw[n]["keeper_unit"]; ok {
				need = append(need, "keeper_bus", "keeper_remote", "keeper_branch")
			}
		case WakeHuman:
			need = []string{"notify"}
		default:
			bad = append(bad, n+" wake (want unit or human)")
			continue
		}
		for _, k := range need {
			if v, ok := raw[n][k]; !ok || v == nil || strings.TrimSpace(fmt.Sprint(v)) == "" {
				bad = append(bad, n+" "+k)
			}
		}
		out = append(out, e)
	}
	if len(bad) > 0 {
		return nil, nil, fmt.Errorf("%w: missing %s", ErrDeclInvalid, strings.Join(bad, ", "))
	}
	return out, canon, nil
}

// DeclSource is the fleet file at a committed revision.
type DeclSource struct {
	Path     string // as given to --from
	Checkout string // the git top level holding it
	Rev      string // git rev-parse HEAD of the checkout
	Digest   string // sha256 of the friends: block at Rev
	Entries  []DeclEntry
}

// ReadDeclSource reads --from: it must be a committed, clean file inside a git
// checkout. The block is read from `git show <rev>:<path>`; a working-tree
// file whose block differs from the committed one is ErrDeclDirty.
func ReadDeclSource(ctx context.Context, path string) (DeclSource, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return DeclSource{}, err
	}
	dir := filepath.Dir(abs)
	top, err := gitOut(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return DeclSource{}, fmt.Errorf("%w: %s is not in a git checkout: %v", ErrDeclDirty, path, err)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	if real, err := filepath.EvalSymlinks(top); err == nil {
		top = real
	}
	rel, err := filepath.Rel(top, abs)
	if err != nil {
		return DeclSource{}, err
	}
	rev, err := gitOut(ctx, top, "rev-parse", "HEAD")
	if err != nil {
		return DeclSource{}, fmt.Errorf("%w: %s has no commit: %v", ErrDeclDirty, top, err)
	}
	committed, err := gitOut(ctx, top, "show", rev+":"+filepath.ToSlash(rel))
	if err != nil {
		return DeclSource{}, fmt.Errorf("%w: %s is not committed at %s", ErrDeclDirty, rel, short(rev))
	}
	entries, canon, err := ParseFriends([]byte(committed))
	if err != nil {
		return DeclSource{}, err
	}
	work, err := os.ReadFile(abs)
	if err != nil {
		return DeclSource{}, err
	}
	if _, wcanon, err := ParseFriends(work); err != nil || !bytes.Equal(wcanon, canon) {
		return DeclSource{}, fmt.Errorf("%w: the friends: block in %s differs from %s; commit it first", ErrDeclDirty, rel, short(rev))
	}
	sum := sha256.Sum256(canon)
	return DeclSource{Path: path, Checkout: top, Rev: rev, Digest: hex.EncodeToString(sum[:]), Entries: entries}, nil
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// IsAncestor reports whether old is an ancestor of rev in checkout; a rev
// unknown to the checkout is not an ancestor.
func IsAncestor(ctx context.Context, checkout, old, rev string) bool {
	return exec.CommandContext(ctx, "git", "-C", checkout, "merge-base", "--is-ancestor", old, rev).Run() == nil
}

// DeclState is the registry as one pipelined read returns it.
type DeclState struct {
	Rev, Digest string
	Declared    []string          // friends:declared
	Decls       map[string]string // friend -> stored decl, for the source's friends
}

// ReadDeclState is one pipelined read of friends:decl, friends:declared and
// the stored decl of each named friend.
func ReadDeclState(ctx context.Context, c redis.Cmdable, names []string) (DeclState, error) {
	p := c.Pipeline()
	decl := p.HMGet(ctx, DeclKey, "rev", "digest")
	declared := p.SMembers(ctx, DeclaredKey)
	per := make([]*redis.StringCmd, len(names))
	for i, n := range names {
		per[i] = p.HGet(ctx, WakePathKey(n), "decl")
	}
	if _, err := p.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return DeclState{}, err
	}
	s := DeclState{Declared: declared.Val(), Decls: map[string]string{}}
	if v := decl.Val(); len(v) == 2 {
		s.Rev, _ = v[0].(string)
		s.Digest, _ = v[1].(string)
	}
	sort.Strings(s.Declared)
	for i, n := range names {
		s.Decls[n] = per[i].Val()
	}
	return s, nil
}

// DeclareResult is what one declare did.
type DeclareResult struct {
	Unchanged               bool
	N, Unit, Human, Removed int
	Rev, Prev               string
}

// Declare writes src to the registry: one pipelined read, then, unless the
// stored rev and digest are src's (zero writes), the ancestor check and one
// FCALL. ErrDeclStale and ErrDeclConflict write nothing.
func Declare(ctx context.Context, st *store.Store, src DeclSource) (DeclareResult, error) {
	res := DeclareResult{Rev: src.Rev, N: len(src.Entries)}
	for _, e := range src.Entries {
		if e.Mode() == WakeUnit {
			res.Unit++
		} else {
			res.Human++
		}
	}
	cur, err := ReadDeclState(ctx, st.Client(), nil)
	if err != nil {
		return res, err
	}
	res.Prev = cur.Rev
	if cur.Rev == src.Rev && cur.Digest == src.Digest {
		res.Unchanged = true
		return res, nil
	}
	if cur.Rev != "" && !IsAncestor(ctx, src.Checkout, cur.Rev, src.Rev) {
		return res, fmt.Errorf("%w: registry at %s, this checkout at %s", ErrDeclStale, short(cur.Rev), short(src.Rev))
	}
	args := []any{cur.Rev, src.Rev, src.Digest, src.Path, len(src.Entries)}
	for _, e := range src.Entries {
		pairs := []any{}
		for _, f := range declFields {
			if v := e.Fields[f.field]; v != "" {
				pairs = append(pairs, f.field, v)
			}
		}
		pairs = append(pairs, "kind", e.Mode(), "decl", e.Decl())
		args = append(args, e.Name, len(pairs)/2)
		args = append(args, pairs...)
	}
	reply, err := st.Client().FCall(ctx, FunctionDeclare, nil, args...).StringSlice()
	if err != nil {
		return res, fmt.Errorf("friend declare: %w", err)
	}
	if len(reply) > 0 && reply[0] == "CONFLICT" {
		stored := ""
		if len(reply) > 1 {
			stored = reply[1]
		}
		return res, fmt.Errorf("%w: registry moved to %s while this declare read %s", ErrDeclConflict, short(stored), short(cur.Rev))
	}
	if len(reply) != 4 || reply[0] != "OK" {
		return res, fmt.Errorf("friend declare: unexpected reply %q", reply)
	}
	fmt.Sscan(reply[3], &res.Removed)
	return res, nil
}

// DeclCheck is the read-only diff of src against the registry.
type DeclCheck struct {
	Rev, Digest         string // stored
	Add, Remove, Change []string
}

// Clean reports whether the registry already holds src.
func (c DeclCheck) Clean() bool { return len(c.Add)+len(c.Remove)+len(c.Change) == 0 }

// CheckDecl reads the registry in one pipeline and diffs src against it; it
// writes nothing.
func CheckDecl(ctx context.Context, st *store.Store, src DeclSource) (DeclCheck, error) {
	names := make([]string, len(src.Entries))
	for i, e := range src.Entries {
		names[i] = e.Name
	}
	cur, err := ReadDeclState(ctx, st.Client(), names)
	if err != nil {
		return DeclCheck{}, err
	}
	out := DeclCheck{Rev: cur.Rev, Digest: cur.Digest}
	had := map[string]bool{}
	for _, n := range cur.Declared {
		had[n] = true
	}
	want := map[string]bool{}
	for _, e := range src.Entries {
		want[e.Name] = true
		switch {
		case !had[e.Name]:
			out.Add = append(out.Add, e.Name)
		case cur.Decls[e.Name] != e.Decl():
			out.Change = append(out.Change, e.Name)
		}
	}
	for _, n := range cur.Declared {
		if !want[n] {
			out.Remove = append(out.Remove, n)
		}
	}
	return out, nil
}
