package table

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// LiveWindow is how young the renderer's published file must be for
// `table --check --live` (#2756 6.8, #3253): a renderer stopped for longer is
// a failed check, not a stale pass.
const LiveWindow = 2 * time.Second

// controlPrefix marks a control sprint, hidden from the live table by default.
const controlPrefix = "control-"

// PrepareCheck readies a store for `table --check` (#3253). In one pipeline
// it reads DBSIZE and the sprints set. An empty store is seeded with
// DefectFixture in one MULTI, so the check needs no hand-loaded keyspace. A
// store whose sprints set holds a sprint that is neither a control sprint nor
// one of the fixture's own sprints is the live fleet: PrepareCheck refuses it
// and names that sprint. It reports whether it seeded.
func PrepareCheck(ctx context.Context, client *redis.Client) (seeded bool, err error) {
	var size *redis.IntCmd
	var members *redis.StringSliceCmd
	if _, err := client.Pipelined(ctx, func(p redis.Pipeliner) error {
		size = p.DBSize(ctx)
		members = p.SMembers(ctx, "sprints")
		return nil
	}); err != nil {
		return false, fmt.Errorf("--check: read store: %w", err)
	}
	if size.Val() == 0 {
		fixture := DefectFixture()
		if _, err := client.TxPipelined(ctx, func(p redis.Pipeliner) error {
			for _, cmd := range fixture {
				args := make([]any, len(cmd))
				for i, v := range cmd {
					args[i] = v
				}
				p.Do(ctx, args...)
			}
			return nil
		}); err != nil {
			return false, fmt.Errorf("--check: seed fixture: %w", err)
		}
		return true, nil
	}
	own := fixtureSprints()
	for _, name := range members.Val() {
		if strings.HasPrefix(name, controlPrefix) || own[name] {
			continue
		}
		return false, fmt.Errorf("--check: sprints holds %q, a non-control sprint: this is a live store; point --check at a throwaway server, or use --check --live --out <file>", name)
	}
	return false, nil
}

// fixtureSprints is the set DefectFixture registers, so a re-run of --check
// on a store it seeded is not mistaken for the live fleet.
func fixtureSprints() map[string]bool {
	own := map[string]bool{}
	for _, cmd := range DefectFixture() {
		if len(cmd) > 2 && cmd[0] == "SADD" && cmd[1] == "sprints" {
			for _, name := range cmd[2:] {
				own[name] = true
			}
		}
	}
	return own
}

// CheckLive is `table --check --live` (#2756 6.8, #3253): the renderer's --out
// file must be younger than LiveWindow at now, and every cell it holds must
// equal the same cell of a direct read (one FCALL_RO ns_snapshot, named sprint
// included) taken now. A second-writer key makes the direct read carry an
// error, which fails the check by name. The error names the failing cell; the
// receipt is one line.
func CheckLive(ctx context.Context, client *redis.Client, out, sprint string, now time.Time) (string, error) {
	info, err := os.Stat(out)
	if err != nil {
		return "", fmt.Errorf("--check --live: %w", err)
	}
	age := now.Sub(info.ModTime())
	if age >= LiveWindow {
		return "", fmt.Errorf("--check --live: %s is %s old, want younger than %s: the renderer is not ticking", out, age.Round(time.Millisecond), LiveWindow)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		return "", fmt.Errorf("--check --live: %w", err)
	}
	snap, err := ReadNamed(ctx, client, sprint)
	if err != nil {
		return "", fmt.Errorf("--check --live: %w", err)
	}
	if len(snap.Errors) > 0 {
		return "", fmt.Errorf("--check --live: direct read is RED: %s", strings.Join(snap.Errors, "; "))
	}
	direct := snap.Render()
	if cell := DiffCells(string(body), direct); cell != "" {
		return "", fmt.Errorf("--check --live: %s", cell)
	}
	cells := len(tableCells(direct).order)
	return fmt.Sprintf("CHECK LIVE OK file=%s age=%dms cells=%d", out, age.Milliseconds(), cells), nil
}

// DiffCells compares a published table with a direct render cell by cell and
// names the first cell that differs, or returns "" when every cell is equal.
// A proc age may differ by the live window's seconds, since it counts from
// the tick that rendered it.
func DiffCells(published, direct string) string {
	pub, dir := tableCells(published), tableCells(direct)
	for _, id := range dir.order {
		got, ok := pub.value[id]
		want := dir.value[id]
		if !ok {
			return fmt.Sprintf("cell %s: missing from the file, direct read %q", id, want)
		}
		if got != want && !ageWithinWindow(id, got, want) {
			return fmt.Sprintf("cell %s: file %q, direct read %q", id, got, want)
		}
	}
	for _, id := range pub.order {
		if _, ok := dir.value[id]; !ok {
			return fmt.Sprintf("cell %s: in the file %q, absent from the direct read", id, pub.value[id])
		}
	}
	return ""
}

// cellSet is a rendered table as ordered cell ids and their values.
type cellSet struct {
	order []string
	value map[string]string
}

func (c *cellSet) add(id, v string) {
	if _, dup := c.value[id]; !dup {
		c.order = append(c.order, id)
	}
	c.value[id] = v
}

// tableCells splits a Render() body into named cells: "<row> <column>" for
// bench and friend rows, "pipeline <sprint> <state>" for pipeline counts,
// "proc <name> state|age|why" for process lines and "RED <n>" for errors.
func tableCells(body string) cellSet {
	c := cellSet{value: map[string]string{}}
	var header []string
	red := 0
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		switch {
		case line == "":
		case strings.HasPrefix(line, "RED "):
			red++
			c.add("RED "+strconv.Itoa(red), strings.TrimPrefix(line, "RED "))
		case strings.HasPrefix(line, "pipeline "):
			f := strings.Fields(line)
			if len(f) < 2 {
				c.add("line "+line, line)
				continue
			}
			if rest, ok := strings.CutPrefix(line, "pipeline "+f[1]+" REFUSED "); ok {
				c.add("pipeline "+f[1]+" REFUSED", rest)
				continue
			}
			for _, kv := range f[2:] {
				k, v, _ := strings.Cut(kv, "=")
				c.add("pipeline "+f[1]+" "+k, v)
			}
		case strings.HasPrefix(line, "proc "):
			f := strings.Fields(line)
			if len(f) < 3 {
				c.add("line "+line, line)
				continue
			}
			// procLine: proc <name> <state> age=<n>s[ why=<text with spaces>]
			id := "proc " + f[1]
			c.add(id+" state", f[2])
			if len(f) > 3 {
				c.add(id+" age", strings.TrimPrefix(f[3], "age="))
			}
			if _, why, ok := strings.Cut(line, " why="); ok {
				c.add(id+" why", why)
			}
		case header == nil && strings.HasPrefix(line, "name | "):
			header = strings.Split(line, " | ")
			c.add("header", line)
		default:
			cells := strings.Split(line, " | ")
			for i := 1; i < len(cells); i++ {
				col := strconv.Itoa(i + 1)
				if i < len(header) {
					col = header[i]
				}
				c.add(cells[0]+" "+col, strings.TrimSpace(cells[i]))
			}
		}
	}
	return c
}

// ageWithinWindow lets a proc age cell differ by the live window: the file's
// tick and the direct read are up to LiveWindow apart.
func ageWithinWindow(id, got, want string) bool {
	if !strings.HasPrefix(id, "proc ") || !strings.HasSuffix(id, " age") {
		return false
	}
	g, err1 := strconv.ParseInt(strings.TrimSuffix(got, "s"), 10, 64)
	w, err2 := strconv.ParseInt(strings.TrimSuffix(want, "s"), 10, 64)
	if err1 != nil || err2 != nil || g < 0 || w < 0 {
		return false
	}
	d := w - g
	return d >= 0 && d <= int64(LiveWindow/time.Second)
}
