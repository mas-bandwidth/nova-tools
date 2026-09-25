package ci

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestTaskCardsHaveOneWriter is the Go half of the task card rule (nova-tools
// #3778; rowan-new specs/ws-index.md, "Tasks are cards"): the one writer of a
// task's pointer and of the sets behind the tables is the Lua move in
// internal/nsprint/fn/lua/02_card_move.lua (the Lua half of this rule is
// internal/nsprint/fn/taskwriter_test.go). No non-test Go file under cmd/ or
// internal/ writes a ws:<stream>:<where> set, a friend:<f>:cards:<where>
// set, a friend-queue idx set, a task:<id> record or the retired sprint store
// (s:<S>:task:<id>, s:<S>:open:<f>, s:<S>:ready; one task store, 2026-09-25
// 09:35 ET) with a direct Redis call;
// a verb FCALLs ns_tcard_* (internal/nsprint/taskcard) instead. Fixtures that
// seed a throwaway store (a *fixture*.go file, internal/nsprint/ws/wstest)
// are the only exceptions.
func TestTaskCardsHaveOneWriter(t *testing.T) {
	t.Parallel()
	tree := repoTree(t)
	write := regexp.MustCompile(`\.(ZAdd|ZAddNX|ZAddXX|ZAddArgs|ZIncrBy|ZRem|ZRemRangeByScore|ZRemRangeByRank|ZUnionStore|ZInterStore|` +
		`SAdd|SRem|SMove|HSet|HSetNX|HMSet|HDel|HIncrBy|Del|Unlink|Rename|RenameNX)\(ctx, ([^,)]+)`)
	raw := regexp.MustCompile(`"(ZADD|ZREM|SADD|SREM|SMOVE|HSET|HDEL|DEL|RENAME|ZUNIONSTORE)", "(ws:|task:|friend:[^"]*:cards:|sprint:[^"]*:idx:)`)
	key := regexp.MustCompile(`^("ws:"\s*\+|"task:"\s*\+|"friend:"\s*\+.*":cards:"|".*:idx:"|"s:"\s*\+.*":(task|open|ready)|` +
		`(ws|taskcard|stream)\.(Key|WSKey|StreamKey|FriendKey)\(|(table\.)?(StreamKey|FriendKey|FriendCardsKey|WSKey)\()`)
	// Inside the two packages that name the keys, a bare Key( is theirs.
	bareKey := regexp.MustCompile(`^Key\(`)
	owns := func(rel string) bool {
		return strings.HasPrefix(rel, "internal/nsprint/ws/") || strings.HasPrefix(rel, "internal/nsprint/taskcard/")
	}
	exempt := func(rel string) bool {
		return strings.HasPrefix(rel, "internal/nsprint/ws/wstest/") || strings.Contains(rel[strings.LastIndex(rel, "/")+1:], "fixture")
	}
	checked := 0
	var bad []string
	for _, dir := range []string{"cmd", "internal"} {
		for _, src := range tree.GoFilesUnder(false, dir) {
			if exempt(src.Rel) {
				continue
			}
			for i, line := range strings.Split(string(src.Src), "\n") {
				checked++
				if m := write.FindStringSubmatch(line); m != nil {
					k := strings.TrimSpace(m[2])
					if k == `"ws:names"` || k == `"ws:order"` || k == `"ws:checkpoint"` {
						continue
					}
					if key.MatchString(k) || (owns(src.Rel) && bareKey.MatchString(k)) {
						bad = append(bad, src.Rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
					}
				}
				if raw.MatchString(line) {
					bad = append(bad, src.Rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("read no Go source; the rule is reading the wrong tree")
	}
	for _, b := range bad {
		t.Errorf("a second writer of the task card sets (the one writer is ns_tcard_move, internal/nsprint/taskcard): %s", b)
	}
}
