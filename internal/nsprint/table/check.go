package table

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
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

// CellCheck is one cell's verification against the Redis sets.
type CellCheck struct {
	Cell  string // "ws:<stream>:<state>" or "<consumer>:cards:<set>"
	Table int64  // count rendered by the table
	Sets  int64  // count recounted from the Redis set
	Drift bool   // Table != Sets
}

// Line returns the printed line formatted as "cell, table, sets, ok|DRIFT".
func (c CellCheck) Line() string {
	verdict := "ok"
	if c.Drift {
		verdict = "DRIFT"
	}
	return fmt.Sprintf("%s, %d, %d, %s", c.Cell, c.Table, c.Sets, verdict)
}

// ParseCheckLine parses a line produced by CellCheck.Line() ("cell, table, sets, ok|DRIFT").
// It parses from the right so cells whose stream names contain commas (e.g. "ws:fleet, ci:ready")
// are handled correctly.
func ParseCheckLine(line string) (CellCheck, error) {
	idx3 := strings.LastIndex(line, ", ")
	if idx3 == -1 {
		return CellCheck{}, fmt.Errorf("malformed check line: missing verdict delimiter: %q", line)
	}
	verdict := line[idx3+2:]
	if verdict != "ok" && verdict != "DRIFT" {
		return CellCheck{}, fmt.Errorf("malformed check line: invalid verdict %q", verdict)
	}

	rest := line[:idx3]
	idx2 := strings.LastIndex(rest, ", ")
	if idx2 == -1 {
		return CellCheck{}, fmt.Errorf("malformed check line: missing sets delimiter: %q", line)
	}
	setsStr := rest[idx2+2:]
	setsVal, err := strconv.ParseInt(setsStr, 10, 64)
	if err != nil {
		return CellCheck{}, fmt.Errorf("malformed check line: invalid sets count %q: %w", setsStr, err)
	}

	rest = rest[:idx2]
	idx1 := strings.LastIndex(rest, ", ")
	if idx1 == -1 {
		return CellCheck{}, fmt.Errorf("malformed check line: missing table delimiter: %q", line)
	}
	tableStr := rest[idx1+2:]
	tableVal, err := strconv.ParseInt(tableStr, 10, 64)
	if err != nil {
		return CellCheck{}, fmt.Errorf("malformed check line: invalid table count %q: %w", tableStr, err)
	}

	cell := rest[:idx1]
	return CellCheck{
		Cell:  cell,
		Table: tableVal,
		Sets:  setsVal,
		Drift: verdict == "DRIFT",
	}, nil
}

// CheckResult holds the recount of every cell and whether drift was found.
type CheckResult struct {
	Sprint     string
	Epoch      uint64
	Cells      []CellCheck
	DriftCount int
}

// HasDrift reports whether any cell differs between the table and the sets.
func (r *CheckResult) HasDrift() bool {
	return r != nil && r.DriftCount > 0
}

// Lines returns the printed lines for every cell in order.
func (r *CheckResult) Lines() []string {
	if r == nil {
		return nil
	}
	lines := make([]string, len(r.Cells))
	for i, c := range r.Cells {
		lines[i] = c.Line()
	}
	return lines
}

func streamTableVal(r StreamRow, state string) int64 {
	switch state {
	case ws.Waiting:
		return r.Waiting
	case ws.Ready:
		return r.Ready
	case ws.Working:
		return r.Working
	case ws.Review:
		return r.Review
	case ws.Merging:
		return r.Merging
	case ws.Landed:
		return r.Landed
	}
	return 0
}

// CheckSnapshot recounts every cell of snap from the underlying Redis sets in
// one pipeline (SCARD/ZCARD per state). It returns the cells and whether any cell
// drifted (#4341).
func CheckSnapshot(ctx context.Context, client redis.UniversalClient, snap *SprintSnapshot) (*CheckResult, error) {
	if client == nil {
		return nil, errors.New("table check: client is required")
	}
	if snap == nil {
		return nil, errors.New("table check: snapshot is required")
	}

	pipe := client.Pipeline()

	type streamRecount struct {
		stream string
		row    StreamRow
		cmds   [ws.CountsCells]*ws.CardCountCmd
	}
	streamCmds := make([]streamRecount, len(snap.Streams))
	for i, s := range snap.Streams {
		streamCmds[i] = streamRecount{
			stream: s.Name,
			row:    s,
		}
		for j, state := range ws.Stream {
			streamCmds[i].cmds[j] = ws.QueueCardCount(ctx, pipe, snap.Epoch, s.Name, state)
		}
	}

	type consumerRecount struct {
		consumer string
		row      ConsumerRow
		cmds     [4]*redis.IntCmd // ready, working, ok, fail
	}
	consumerCmds := make([]consumerRecount, len(snap.Consumers))
	for i, c := range snap.Consumers {
		consumerCmds[i] = consumerRecount{
			consumer: c.ID(),
			row:      c,
		}
		for j, set := range ConsumerSets {
			consumerCmds[i].cmds[j] = pipe.ZCard(ctx, ws.ConsumerKeyAt(snap.Epoch, c.ID(), set))
		}
	}

	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("table check: recount sets pipeline: %w", err)
	}

	res := &CheckResult{
		Sprint: snap.Counts.Sprint,
		Epoch:  snap.Epoch,
	}

	addCheck := func(cell string, tableVal, setsVal int64) {
		drift := tableVal != setsVal
		if drift {
			res.DriftCount++
		}
		res.Cells = append(res.Cells, CellCheck{
			Cell:  cell,
			Table: tableVal,
			Sets:  setsVal,
			Drift: drift,
		})
	}

	var setsTotal [ws.CountsCells]int64
	for _, sc := range streamCmds {
		for j, state := range ws.Stream {
			setsVal, err := sc.cmds[j].Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return nil, fmt.Errorf("table check: set %s:%s count: %w", sc.stream, state, err)
			}
			setsTotal[j] += setsVal
			tableVal := streamTableVal(sc.row, state)
			cellName := ws.KeyAt(snap.Epoch, sc.stream, state)
			addCheck(cellName, tableVal, setsVal)
		}
	}

	if len(streamCmds) > 0 {
		for j, state := range ws.Stream {
			tableVal := snap.Counts.Total.Cell(state)
			cellName := "stream:total:" + state
			addCheck(cellName, tableVal, setsTotal[j])
		}
	}

	var consTotalReady, consTotalWorking, consTotalOK, consTotalFail int64
	var setsTotalReady, setsTotalWorking, setsTotalOK, setsTotalFail int64

	for _, cc := range consumerCmds {
		rVal := cc.cmds[0].Val()
		wVal := cc.cmds[1].Val()
		oVal := cc.cmds[2].Val()
		fVal := cc.cmds[3].Val()
		dVal := oVal + fVal

		setsTotalReady += rVal
		setsTotalWorking += wVal
		setsTotalOK += oVal
		setsTotalFail += fVal

		consTotalReady += cc.row.Ready
		consTotalWorking += cc.row.Working
		consTotalOK += cc.row.OK
		consTotalFail += cc.row.Fail

		addCheck(ws.ConsumerKeyAt(snap.Epoch, cc.consumer, "ready"), cc.row.Ready, rVal)
		addCheck(ws.ConsumerKeyAt(snap.Epoch, cc.consumer, "working"), cc.row.Working, wVal)
		addCheck(ws.ConsumerKeyAt(snap.Epoch, cc.consumer, "done"), cc.row.Done(), dVal)
		addCheck(ws.ConsumerKeyAt(snap.Epoch, cc.consumer, "ok"), cc.row.OK, oVal)
		addCheck(ws.ConsumerKeyAt(snap.Epoch, cc.consumer, "fail"), cc.row.Fail, fVal)
	}

	if len(consumerCmds) > 0 {
		addCheck("worker:total:ready", consTotalReady, setsTotalReady)
		addCheck("worker:total:working", consTotalWorking, setsTotalWorking)
		addCheck("worker:total:done", consTotalOK+consTotalFail, setsTotalOK+setsTotalFail)
		addCheck("worker:total:ok", consTotalOK, setsTotalOK)
		addCheck("worker:total:fail", consTotalFail, setsTotalFail)
	}

	return res, nil
}

// CheckSets renders the sprint table and recounts every cell from the underlying
// Redis sets in one pipeline (SCARD/ZCARD per state). It returns the cells and
// whether any cell drifted (#4341).
func CheckSets(ctx context.Context, client redis.UniversalClient, sprint string, now time.Time) (*CheckResult, error) {
	if client == nil {
		return nil, errors.New("table check: client is required")
	}
	r := NewSprintReader(client, SprintConfig{Sprint: sprint})
	snap, err := r.Read(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("table check: read table: %w", err)
	}
	res, err := CheckSnapshot(ctx, client, snap)
	if err != nil {
		return nil, err
	}
	if sprint != "" {
		res.Sprint = sprint
	}
	return res, nil
}
