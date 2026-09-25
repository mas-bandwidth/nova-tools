package fn

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// historyStream names the three history streams of nova-tools #3878 on an
// XADD line: sprint:<S>:moves, land:<repo>:events and cap:log.
var historyStream = map[string]*regexp.Regexp{
	"sprint:*:moves": regexp.MustCompile(`'sprint:' \.\. \w+ \.\. ':moves'|"sprint:" ?\+ ?\w+ ?\+ ?":moves"|MovesKey\(`),
	"land:*:events":  regexp.MustCompile(`'land:' \.\. \w+ \.\. ':events'|EventsStream\(`),
	"cap:log":        regexp.MustCompile(`['"]cap:log['"]`),
}

var xadd = regexp.MustCompile(`XADD|XAdd`)
var maxlen = regexp.MustCompile(`MAXLEN|MaxLen|MINID|MinID`)

// TestHistoryStreamsAreNeverTrimmed is nova-tools #3878's DONE-WHEN for the
// history streams: no Lua file and no Go XADD under cmd/ or internal/ passes
// MAXLEN (or MINID) on sprint:*:moves, land:*:events or cap:log, so a card's
// history cannot disappear. An XADD call is read with the two lines after it,
// where a wrapped call carries its trim. The scan must find XADDs on all
// three streams, or it proves nothing.
func TestHistoryStreamsAreNeverTrimmed(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	found := map[string]int{}
	var bad []string
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if strings.HasSuffix(name, "_test.go") || !(strings.HasSuffix(name, ".go") || strings.HasSuffix(name, ".lua")) {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			lines := strings.Split(string(src), "\n")
			for i, line := range lines {
				if !xadd.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "--") || strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				end := i + 3
				if end > len(lines) {
					end = len(lines)
				}
				call := strings.Join(lines[i:end], "\n")
				for stream, re := range historyStream {
					if !re.MatchString(call) {
						continue
					}
					found[stream]++
					if maxlen.MatchString(call) {
						rel, _ := filepath.Rel(root, path)
						bad = append(bad, rel+":"+strconv.Itoa(i+1)+" trims "+stream)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for stream := range historyStream {
		if found[stream] == 0 {
			t.Errorf("found no XADD on %s; the scan's pattern no longer matches the writers", stream)
		}
	}
	for _, b := range bad {
		t.Errorf("%s: history streams are never trimmed (#3878)", b)
	}
}
