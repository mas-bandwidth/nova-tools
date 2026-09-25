package fn

import (
	"regexp"
	"strings"
	"testing"
)

// TestTaskCardOneWriter is the Lua half of the task card rule (nova-tools
// #3778; the Go half is internal/ci/taskwriter_class_test.go): TK.move in
// lua/02_card_move.lua (NS.task in later files) is the only writer of a task
// card's pointer and of the sets behind the tables. No other file writes a
// ws:<stream>:<where> set, a friend:<f>:cards:<where> set, a friend-queue
// idx set (sprint:<S>:idx:*) or roster (sprint:<S>:tasks), the sprint
// store's views (s:<S>:idx:task:<state>, s:<S>:open:<f>, s:<S>:ready) or a
// retired s:<S>:task:<id> record (one task store since the 2026-09-25 09:35
// ruling; only task migrate's fold, in 02_card_move.lua, touches one), or a
// pointer
// field of a task:<id> record, directly or through a local bound to such a
// key; the stream registry (ws:names, ws:order) and ws:log receipts are not
// task sets. A second writer fails here, so it cannot land.
func TestTaskCardOneWriter(t *testing.T) {
	t.Parallel()

	source, err := Source()
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.Split(source, "\n-- lua/")
	writes := regexp.MustCompile(`redis\.p?call\('(ZADD|ZREM|ZINCRBY|ZUNIONSTORE|ZINTERSTORE|ZRANGESTORE|ZPOPMIN|ZPOPMAX|` +
		`ZREMRANGEBYSCORE|ZREMRANGEBYRANK|SADD|SREM|SMOVE|DEL|UNLINK|RENAME|HSET|HSETNX|HMSET|HDEL|HINCRBY)',\s*([^,)]+)`)
	// the key expressions that name a task card set
	sets := regexp.MustCompile(`^('ws:'\s*\.\.|W\.key\(|'friend:'\s*\.\..*':cards:|'sprint:'\s*\.\.\s*\w+\s*\.\.\s*':(idx|tasks)|` +
		`'s:'\s*\.\.\s*\w+\s*\.\.\s*':(idx:task:|open:|ready'|task:))`)
	binds := regexp.MustCompile(`local\s+(\w+)\s*=\s*'s(print)?:'\s*\.\.\s*\w+\s*\.\.\s*':(idx:|open:|ready')`)
	tbinds := regexp.MustCompile(`local\s+(\w+)\s*=\s*(\w+\s+and\s+\()?'task:'\s*\.\.`)
	pointer := regexp.MustCompile(`'(where|where_ok|where_at|state|state_at|stream|friend|owner|created_at|queue|xid|lease_until|cancelled)'`)
	checked := 0
	for _, sec := range sections[1:] {
		name := sec[:strings.Index(sec, "\n")]
		if name == "02_card_move.lua" {
			continue
		}
		bound, tasks := map[string]bool{}, map[string]bool{}
		for i, line := range strings.Split(sec, "\n") {
			if strings.HasPrefix(line, "local function ") || strings.HasPrefix(line, "function ") {
				bound, tasks = map[string]bool{}, map[string]bool{}
			}
			if m := binds.FindStringSubmatch(line); m != nil {
				bound[m[1]] = true
			}
			if m := tbinds.FindStringSubmatch(line); m != nil {
				tasks[m[1]] = true
			}
			m := writes.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			checked++
			target := strings.TrimSpace(m[2])
			head := strings.TrimSpace(strings.SplitN(target, "..", 2)[0])
			switch {
			case sets.MatchString(target), bound[head]:
				t.Errorf("lua/%s line %d writes a task card set outside NS.task.move: %s", name, i, strings.TrimSpace(line))
			case (strings.HasPrefix(target, "'task:'") || tasks[target]) && (m[1] == "HSET" || m[1] == "HSETNX" || m[1] == "HMSET" || m[1] == "HDEL") &&
				pointer.MatchString(line[strings.Index(line, m[2])+len(m[2]):]):
				t.Errorf("lua/%s line %d writes a task card pointer field outside NS.task.move: %s", name, i, strings.TrimSpace(line))
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no Redis write at all; the rule is reading the wrong source")
	}
}
