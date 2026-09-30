package fn

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// waiting_resolve.lua names an entry of a DEPENDS-ON (a bare repo#n) under an owner. That
// owner is never a literal in the function: it comes from the entry's own text, the
// waiting card's repo field, or the sprint's config (cfg:sprint owner), and when none
// carries it the waiter is refused with the reason and left waiting (the generality
// rule: the tools know the concept of an owner, never ours).

const waitingResolveFile = "lua/waiting_resolve.lua"

func waitingResolveSource(t *testing.T) string {
	t.Helper()
	b, err := sources.ReadFile(waitingResolveFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestWaitingResolveHasNoLiteralOwner: the Lua holds no owner string literal, no
// default-owner constant and no account name, anywhere, comments included.
func TestWaitingResolveHasNoLiteralOwner(t *testing.T) {
	t.Parallel()
	src := waitingResolveSource(t)
	for _, bad := range []*regexp.Regexp{
		regexp.MustCompile(`(?i)mas-bandwidth`),
		regexp.MustCompile(`(?i)default\s*owner`),
		regexp.MustCompile(`\bowner\s*(?:,\s*\w+\s*)?=\s*'[^']`),
		regexp.MustCompile(`\bowner\s*(?:,\s*\w+\s*)?=\s*"[^"]`),
	} {
		if m := bad.FindString(src); m != "" {
			t.Errorf("%s holds a literal owner (%q); the owner comes from the entry, the card's repo field or cfg:sprint owner", waitingResolveFile, m)
		}
	}
}

// waitingResolveReply runs the pass over one waiter t1 (DEPENDS-ON dep, repo cardRepo)
// and one landed member m1 (pr #5 under memberRepo) with cfg:sprint owner set to cfg,
// against a stub of the Redis calls the pass makes, and returns its reply joined by
// spaces.
func waitingResolveReply(t *testing.T, cfg, dep, cardRepo, memberRepo string) string {
	t.Helper()
	L := lua.NewState()
	defer L.Close()
	prelude := fmt.Sprintf(`
local cfg, dep, card_repo, member_repo = %q, %q, %q, %q
local captured
redis = {
  register_function = function(name, fn) captured = fn end,
  call = function(cmd, key, ...)
    local f = { ... }
    if cmd == 'HGET' and key == 'cfg:sprint' then
      if cfg == '' then return false end
      return cfg
    elseif cmd == 'ZRANGE' then
      if key == 'ws:order' then return { 's1' } end
      if key == 'wk:waiting' then return { 't1' } end
      if key == 'wk:landed' then return { 'm1' } end
      return {}
    elseif cmd == 'HMGET' then
      if key == 'task:t1' then return { dep, '', '', card_repo } end
      if key == 'task:m1' then return { 'landed', '', '', '#5', '', '', member_repo } end
    end
    return false
  end,
}
NS = {
  card = { epoch = function() return 'e' end, wskey = function(e, s, w) return 'wk:' .. w end },
  task = {
    is_sentinel = function(id) return false end,
    read = function(id) return { stream = 's1' } end,
    move = function(id, to, opts) return nil end,
  },
  stitch = { write = function(id) return nil, 'none' end },
}
function run_pass() return table.concat(captured({}, { 'tester' }), ' ') end
`, cfg, dep, cardRepo, memberRepo)
	if err := L.DoString(prelude); err != nil {
		t.Fatal(err)
	}
	if err := L.DoString(waitingResolveSource(t)); err != nil {
		t.Fatalf("load %s: %v", waitingResolveFile, err)
	}
	if err := L.DoString(`return run_pass()`); err != nil {
		t.Fatalf("pass: %v", err)
	}
	out := L.Get(-1).String()
	return out
}

func TestWaitingResolveOwnerComesFromConfigOrCardNeverADefault(t *testing.T) {
	t.Parallel()

	t.Run("nothing carries the owner: refused with the reason, not moved", func(t *testing.T) {
		t.Parallel()
		out := waitingResolveReply(t, "", "relrepo#5", "relrepo", "relrepo")
		if !strings.Contains(out, "refused s1 t1 no owner for relrepo#5") || !strings.Contains(out, "cfg:sprint owner") {
			t.Errorf("reply %q: want a refusal naming the entry and where an owner comes from", out)
		}
		if strings.Contains(out, "ready s1") {
			t.Errorf("reply %q: a waiter with no owner for its entry was released", out)
		}
		if !strings.Contains(out, "still s1 1") {
			t.Errorf("reply %q: the refused waiter is still waiting", out)
		}
	})
	t.Run("the sprint's config carries it", func(t *testing.T) {
		t.Parallel()
		out := waitingResolveReply(t, "acme", "relrepo#5", "relrepo", "relrepo")
		if !strings.Contains(out, "ready s1 t1") || strings.Contains(out, "refused") {
			t.Errorf("reply %q: want t1 released under the configured owner", out)
		}
	})
	t.Run("the card's repo field carries it", func(t *testing.T) {
		t.Parallel()
		out := waitingResolveReply(t, "", "relrepo#5", "org2/app", "org2/relrepo")
		if !strings.Contains(out, "ready s1 t1") || strings.Contains(out, "refused") {
			t.Errorf("reply %q: want t1 released under the card's owner", out)
		}
	})
	t.Run("the card's owner wins over the config", func(t *testing.T) {
		t.Parallel()
		out := waitingResolveReply(t, "acme", "relrepo#5", "org2/app", "acme/relrepo")
		if strings.Contains(out, "ready s1") {
			t.Errorf("reply %q: the member is acme/relrepo, the card names org2: no release", out)
		}
	})
	t.Run("an entry that names its owner needs none", func(t *testing.T) {
		t.Parallel()
		out := waitingResolveReply(t, "", "org3/relrepo#5", "", "org3/relrepo")
		if !strings.Contains(out, "ready s1 t1") || strings.Contains(out, "refused") {
			t.Errorf("reply %q: want t1 released", out)
		}
	})
}
