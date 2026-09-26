package friend_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/redis/go-redis/v9"
)

const (
	fsSprint = "control-3101"
	fsRepo   = "nova-tools"
)

func fsMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func fsQueueOf(client *redis.Client, id string, names ...string) string {
	for _, name := range names {
		if _, err := client.ZScore(context.Background(), "s:"+fsSprint+":open:"+name, id).Result(); err == nil {
			return name
		}
	}
	return ""
}

// TestControl43OneWriter: each friend key has one writer. friend:<f>:state
// is written only in friend.lua (ns_friend_state and its helpers),
// friend:<f>:wakemode only in friend.lua (ns_friend_wakemode) and
// friend:<f>:events only in presence.lua (ns_friend_event); no Go source
// writes any of the three; every XADD to the events stream is capped.
func TestControl43OneWriter(t *testing.T) {
	t.Parallel()

	source, err := fn.Source()
	fsMust(t, err)
	sections := strings.Split(source, "\n-- lua/")
	t.Run("lua", func(t *testing.T) {
		writes := regexp.MustCompile(`redis\.call\('(HSET|HSETNX|HDEL|DEL|UNLINK|HINCRBY|HINCRBYFLOAT|EXPIRE|PEXPIRE|SET|RENAME|HMSET|XADD|XTRIM|XDEL)',\s*([^,)]+)`)
		owner := map[string]string{"state": "friend.lua", "wakemode": "friend.lua", "events": "presence.lua"}
		binds := regexp.MustCompile(`local\s+(\w+)\s*=\s*(?:'friend:'\s*\.\.\s*\w+\s*\.\.\s*':(state|wakemode|events)'|(fs_key)\()`)
		checked := map[string]int{}
		for _, sec := range sections[1:] {
			name := sec[:strings.Index(sec, "\n")]
			// A local bound to a key is tracked within its top-level function.
			vars := map[string]string{}
			for i, line := range strings.Split(sec, "\n") {
				if strings.HasPrefix(line, "local function ") || strings.HasPrefix(line, "function ") {
					vars = map[string]string{}
				}
				if m := binds.FindStringSubmatch(line); m != nil {
					if m[3] != "" {
						vars[m[1]] = "state"
					} else {
						vars[m[1]] = m[2]
					}
				}
				m := writes.FindStringSubmatch(line)
				if m == nil {
					continue
				}
				target := strings.TrimSpace(m[2])
				key := vars[target]
				for k := range owner {
					if strings.Contains(target, "'friend:'") && strings.Contains(target, "':"+k+"'") {
						key = k
					}
				}
				if key == "" {
					continue
				}
				checked[key]++
				if name != owner[key] {
					t.Errorf("lua/%s line %d writes friend:<f>:%s (%s) outside %s", name, i, key, strings.TrimSpace(line), owner[key])
				}
			}
		}
		for k := range owner {
			if checked[k] == 0 {
				t.Errorf("found no write to friend:<f>:%s at all; the rule is reading the wrong source", k)
			}
		}
	})
	t.Run("go_sources", func(t *testing.T) {
		root := fsRepoRoot(t)
		call := regexp.MustCompile(`\.(HSet|HSetNX|HDel|Del|Unlink|Set|Expire|PExpire|XAdd)\(`)
		marker := regexp.MustCompile(`(^|\W)(friend\.)?(StateKey|EventsKey|WakeModeKey)\(|:(state|events|wakemode)"`)
		scanned := 0
		for _, dir := range []string{"internal/nsprint", "cmd/nova-sprint"} {
			err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return err
				}
				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				scanned++
				text := string(body)
				for _, loc := range call.FindAllStringIndex(text, -1) {
					args := fsCallArgs(text[loc[1]:])
					if marker.MatchString(args) {
						rel, _ := filepath.Rel(root, path)
						t.Errorf("%s writes a one-writer friend key from Go: %s%s)", rel, text[loc[0]:loc[1]], args)
					}
				}
				return nil
			})
			fsMust(t, err)
		}
		if scanned == 0 {
			t.Fatal("scanned no Go source; the rule is reading the wrong tree")
		}
	})
	t.Run("events_maxlen", func(t *testing.T) {
		xadd := regexp.MustCompile(`redis\.call\('XADD',\s*'friend:'\s*\.\.\s*\w+\s*\.\.\s*':events'(.*)`)
		found := 0
		for _, sec := range sections[1:] {
			name := sec[:strings.Index(sec, "\n")]
			for i, line := range strings.Split(sec, "\n") {
				m := xadd.FindStringSubmatch(line)
				if m == nil {
					continue
				}
				found++
				if !strings.Contains(m[1], "'MAXLEN', '~', 100000") {
					t.Errorf("lua/%s line %d appends to friend:<f>:events without MAXLEN ~ 100000: %s", name, i, strings.TrimSpace(line))
				}
			}
		}
		if found == 0 {
			t.Fatal("found no XADD to friend:<f>:events; the rule is reading the wrong source")
		}
	})
}

// fsCallArgs is the argument text of a call whose "(" was just consumed, up
// to its matching ")".
func fsCallArgs(rest string) string {
	depth := 1
	for i, r := range rest {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return rest[:i]
			}
		}
	}
	return rest
}

// fsRepoRoot is the module root (the directory holding go.mod).
func fsRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	fsMust(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

func fsCapLog(t *testing.T, client *redis.Client) []redis.XMessage {
	t.Helper()
	msgs, err := client.XRange(context.Background(), "cap:log", "-", "+").Result()
	fsMust(t, err)
	return msgs
}
