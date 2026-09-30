//go:build functional

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/redis/go-redis/v9"
)

func TestProfileFirstRead(t *testing.T) {
	st, c := liveStore(t)
	ctx := context.Background()

	// 1. Setup fleet members
	for i := 1; i <= 8; i++ {
		m := fmt.Sprintf("m%d", i)
		if _, err := st.Run(ctx, FleetStep(sprint.FleetReq{Op: "release", Member: m, Who: "tester"})); err != nil {
			t.Fatalf("fleet release %s: %v", m, err)
		}
	}

	// 2. Add 3000 cards (3 streams x 1000)
	t.Log("Adding 3,000 cards...")
	t0 := time.Now()
	var rs []sprint.AddReq
	for _, sn := range []string{"a", "b", "c"} {
		rs = append(rs, sprint.AddReq{Stream: sn, Count: 1000, Who: "tester"})
	}
	if _, err := st.Run(ctx, AddEachStep(rs)); err != nil {
		t.Fatalf("add 3000: %v", err)
	}
	t.Logf("Added 3,000 cards in %s", time.Since(t0))

	// 3. Set machine running
	if _, _, _, err := st.SetMachine(ctx, true); err != nil {
		t.Fatal(err)
	}

	// 4. Now profile `first read` as performed by tick.go:
	pinned, err := st.pin(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Log("Starting first read profile...")
	benchStart := time.Now()
	snap, gen, err := pinned.Fenced(ctx, All, tickExtras, nil)
	totalFenced := time.Since(benchStart)
	if err != nil {
		t.Fatalf("fenced read: %v", err)
	}
	t.Logf(">>> Pinned.Fenced total: %v (gen=%d)", totalFenced, gen)

	// Breakdown of individual operations in loadOnce
	stored := make([]string, len(All))
	for i, name := range All {
		stored[i] = st.Names.Table(name)
	}

	t1 := time.Now()
	shapes, err := st.shapes(ctx, stored)
	tookShapes := time.Since(t1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(">>> 1. Shapes (4 tables pipeline): %v", tookShapes)

	t2 := time.Now()
	cellIDs, err := st.B.CellIDs(ctx, shapes)
	tookCellIDs := time.Since(t2)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(">>> 2. CellIDs (QueueCells pipeline): %v", tookCellIDs)
	for tbl, ids := range cellIDs {
		t.Logf("       Table %s: %d cell IDs", tbl, len(ids))
	}

	// 3. For each table, measure readInto and its chunk breakdown
	totalReadIntoAll := time.Duration(0)
	for i, shape := range shapes {
		tblName := All[i]
		ids := cellIDs[shape.Name]
		t.Logf(">>> 3.%d Table %s (%d member IDs):", i+1, tblName, len(ids))

		tblStart := time.Now()
		spTable := sprint.NewTable(tblName)
		spTable.Epoch, spTable.Revision = shape.Epoch, shape.Revision
		spTable.SetProps(shape.Props)
		for _, r := range shape.Rows {
			spTable.SetRows(append(spTable.Rows(), r.Key))
			if len(r.Texts) > 0 {
				spTable.Texts[r.Key] = r.Texts
			}
		}

		chunkIdx := 0
		for start := 0; start < len(ids); start += ntable.LimitReadSetMembers {
			chunkIdx++
			end := min(start+ntable.LimitReadSetMembers, len(ids))
			chunkIDs := ids[start:end]

			cStart := time.Now()
			// Measure raw ReadSet
			res, err := st.B.ReadSet(ctx, shape.Name, chunkIDs)
			tookReadSet := time.Since(cStart)
			if err != nil {
				t.Fatalf("ReadSet: %v", err)
			}

			putStart := time.Now()
			for _, mem := range res.Members {
				card := &sprint.Card{ID: sprint.CardID(mem.ID), Score: mem.Score, Rev: mem.Revision, Fields: mem.Fields}
				if mem.Placed {
					card.Row, card.Col = mem.Row, mem.Col
				}
				spTable.Put(card)
			}
			tookPut := time.Since(putStart)
			tookChunk := time.Since(cStart)

			t.Logf("       Chunk %d (%d members): total=%v [st.B.ReadSet=%v, Put=%v]",
				chunkIdx, len(chunkIDs), tookChunk, tookReadSet, tookPut)
		}
		tableDuration := time.Since(tblStart)
		totalReadIntoAll += tableDuration
		t.Logf("       Table %s readInto TOTAL: %v", tblName, tableDuration)
	}
	t.Logf(">>> 3. Total readInto across all tables: %v", totalReadIntoAll)

	t4 := time.Now()
	open, err := st.B.OpenNotes(ctx)
	tookOpen := time.Since(t4)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(">>> 4. OpenNotes: %v (%d open)", tookOpen, len(open))

	t5 := time.Now()
	coord, err := st.B.Coordinator(ctx)
	tookCoord := time.Since(t5)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf(">>> 5. Coordinator: %v (%q)", tookCoord, coord)

	t6 := time.Now()
	ex := tickExtras(snap)
	tookExtras := time.Since(t6)
	t.Logf(">>> 6. Extras: %v (%d tables with extras)", tookExtras, len(ex))

	// 7. PIPELINED PROTOTYPE:
	// All chunks across all tables queued on ONE Redis pipeline!
	t.Log(">>> 7. Testing Pipelined ReadSet across all tables and chunks...")
	pipeStart := time.Now()
	type pendingChunk struct {
		tblName  string
		shape    ntable.Table
		chunkIDs []string
		cmd      *redis.Cmd
	}
	var pending []pendingChunk
	pipe := c.Pipeline()
	for i, shape := range shapes {
		tblName := All[i]
		ids := cellIDs[shape.Name]
		for start := 0; start < len(ids); start += ntable.LimitReadSetMembers {
			end := min(start+ntable.LimitReadSetMembers, len(ids))
			chunkIDs := ids[start:end]
			scopeJSON, _ := json.Marshal(map[string]any{"members": chunkIDs})
			cmd := pipe.FCallRO(ctx, "ns_table_read_set", []string{ntable.DefKey(shape.Name)}, shape.Name, string(scopeJSON))
			pending = append(pending, pendingChunk{
				tblName:  tblName,
				shape:    shape,
				chunkIDs: chunkIDs,
				cmd:      cmd,
			})
		}
	}
	tQueue := time.Since(pipeStart)
	execStart := time.Now()
	_, err = pipe.Exec(ctx)
	tExec := time.Since(execStart)
	if err != nil {
		t.Fatalf("pipeline exec: %v", err)
	}

	decodeStart := time.Now()
	totalMembers := 0
	for _, p := range pending {
		reply, err := p.cmd.Slice()
		if err != nil {
			t.Fatalf("cmd.Slice: %v", err)
		}
		rawMembers, ok := reply[4].([]any)
		if ok {
			totalMembers += len(rawMembers)
		}
	}
	tDecode := time.Since(decodeStart)
	totalPipeTime := time.Since(pipeStart)
	t.Logf(">>> PIPELINE RESULT: TOTAL=%v [Queue=%v, Pipe.Exec=%v, Slice/Count=%v] (%d members in %d chunks)",
		totalPipeTime, tQueue, tExec, tDecode, totalMembers, len(pending))

	// 8. CONCURRENT PROTOTYPE:
	// Running the 4 tables concurrently using goroutines!
	t.Log(">>> 8. Testing Concurrent (Per-Table Goroutines) ReadSet...")
	concStart := time.Now()
	var wg sync.WaitGroup
	type tableResult struct {
		tblName string
		dur     time.Duration
		err     error
	}
	resChan := make(chan tableResult, len(shapes))
	for i, shape := range shapes {
		tblName := All[i]
		ids := cellIDs[shape.Name]
		wg.Add(1)
		go func(s ntable.Table, name string, memberIDs []string) {
			defer wg.Done()
			tStart := time.Now()
			for start := 0; start < len(memberIDs); start += ntable.LimitReadSetMembers {
				end := min(start+ntable.LimitReadSetMembers, len(memberIDs))
				chunkIDs := memberIDs[start:end]
				_, err := st.B.ReadSet(ctx, s.Name, chunkIDs)
				if err != nil {
					resChan <- tableResult{tblName: name, err: err}
					return
				}
			}
			resChan <- tableResult{tblName: name, dur: time.Since(tStart)}
		}(shape, tblName, ids)
	}
	wg.Wait()
	close(resChan)
	totalConcTime := time.Since(concStart)
	for r := range resChan {
		if r.err != nil {
			t.Fatalf("concurrent table %s: %v", r.tblName, r.err)
		}
		t.Logf("       Table %s completed in %v", r.tblName, r.dur)
	}
	t.Logf(">>> CONCURRENT (Per-Table) RESULT: TOTAL=%v", totalConcTime)

	// 9. CONCURRENT CHUNKS PROTOTYPE:
	// Running ALL chunks across ALL tables concurrently in separate goroutines!
	t.Log(">>> 9. Testing Concurrent (Per-Chunk Goroutines) ReadSet...")
	chunkStart := time.Now()
	var chunkWg sync.WaitGroup
	type chunkResult struct {
		name string
		idx  int
		dur  time.Duration
		err  error
	}
	chunkChan := make(chan chunkResult, len(pending))
	for i, shape := range shapes {
		tblName := All[i]
		ids := cellIDs[shape.Name]
		cIdx := 0
		for start := 0; start < len(ids); start += ntable.LimitReadSetMembers {
			cIdx++
			end := min(start+ntable.LimitReadSetMembers, len(ids))
			chunkIDs := ids[start:end]
			chunkWg.Add(1)
			go func(s ntable.Table, name string, idx int, cIDs []string) {
				defer chunkWg.Done()
				tStart := time.Now()
				_, err := st.B.ReadSet(ctx, s.Name, cIDs)
				chunkChan <- chunkResult{name: name, idx: idx, dur: time.Since(tStart), err: err}
			}(shape, tblName, cIdx, chunkIDs)
		}
	}
	chunkWg.Wait()
	close(chunkChan)
	totalChunkConcTime := time.Since(chunkStart)
	for r := range chunkChan {
		if r.err != nil {
			t.Fatalf("concurrent chunk %s-%d: %v", r.name, r.idx, r.err)
		}
	}
	t.Logf(">>> CONCURRENT (Per-Chunk) RESULT: TOTAL=%v", totalChunkConcTime)

	// 10. GO DESERIALIZATION COMPARISON (fmt.Sprint vs Fast Type-Switch):
	t.Log(">>> 10. Comparing Go Deserialization: fmt.Sprint vs Fast Type Switch...")
	// Fetch reply of chunk 1 of work table
	chunkIDs := cellIDs[shapes[0].Name][:ntable.LimitReadSetMembers]
	scopeJSON, _ := json.Marshal(map[string]any{"members": chunkIDs})
	rawCmd := c.FCallRO(ctx, "ns_table_read_set", []string{ntable.DefKey(shapes[0].Name)}, shapes[0].Name, string(scopeJSON))
	rawReply, err := rawCmd.Slice()
	if err != nil {
		t.Fatal(err)
	}
	rawMembers := rawReply[4].([]any)

	// Baseline: fmt.Sprint decoding (as in current internal/ntable/store.go)
	const iters = 20
	tBaseStart := time.Now()
	for iter := 0; iter < iters; iter++ {
		members := make([]ntable.ReadSetMember, 0, len(rawMembers))
		for _, rm := range rawMembers {
			item := rm.([]any)
			mRev, _ := strconv.ParseUint(fmt.Sprint(item[1]), 10, 64)
			mScore, _ := strconv.ParseFloat(fmt.Sprint(item[5]), 64)
			fieldsRaw := item[6].([]any)
			fields := make(map[string]string, len(fieldsRaw)/2)
			for i := 0; i < len(fieldsRaw); i += 2 {
				fields[fmt.Sprint(fieldsRaw[i])] = fmt.Sprint(fieldsRaw[i+1])
			}
			members = append(members, ntable.ReadSetMember{
				ID:        fmt.Sprint(item[0]),
				Revision:  mRev,
				Placed:    fmt.Sprint(item[2]) == "1",
				Row:       fmt.Sprint(item[3]),
				Col:       fmt.Sprint(item[4]),
				Score:     mScore,
				ScoreText: fmt.Sprint(item[5]),
				Fields:    fields,
			})
		}
		_ = members
	}
	tBase := time.Since(tBaseStart) / time.Duration(iters)

	// Fast Type-Switch decoder
	fastString := func(v any) string {
		switch s := v.(type) {
		case string:
			return s
		case []byte:
			return string(s)
		case int64:
			return strconv.FormatInt(s, 10)
		default:
			return fmt.Sprint(v)
		}
	}
	fastUint := func(v any) uint64 {
		switch n := v.(type) {
		case int64:
			return uint64(n)
		case string:
			u, _ := strconv.ParseUint(n, 10, 64)
			return u
		default:
			u, _ := strconv.ParseUint(fmt.Sprint(v), 10, 64)
			return u
		}
	}
	fastFloat := func(v any) (float64, string) {
		switch s := v.(type) {
		case string:
			f, _ := strconv.ParseFloat(s, 64)
			return f, s
		case float64:
			return s, strconv.FormatFloat(s, 'f', -1, 64)
		case int64:
			return float64(s), strconv.FormatInt(s, 10)
		default:
			str := fmt.Sprint(v)
			f, _ := strconv.ParseFloat(str, 64)
			return f, str
		}
	}

	tFastStart := time.Now()
	for iter := 0; iter < iters; iter++ {
		members := make([]ntable.ReadSetMember, 0, len(rawMembers))
		for _, rm := range rawMembers {
			item := rm.([]any)
			mRev := fastUint(item[1])
			mScore, scoreText := fastFloat(item[5])
			fieldsRaw := item[6].([]any)
			fields := make(map[string]string, len(fieldsRaw)/2)
			for i := 0; i < len(fieldsRaw); i += 2 {
				fields[fastString(fieldsRaw[i])] = fastString(fieldsRaw[i+1])
			}
			members = append(members, ntable.ReadSetMember{
				ID:        fastString(item[0]),
				Revision:  mRev,
				Placed:    fastString(item[2]) == "1",
				Row:       fastString(item[3]),
				Col:       fastString(item[4]),
				Score:     mScore,
				ScoreText: scoreText,
				Fields:    fields,
			})
		}
		_ = members
	}
	tFast := time.Since(tFastStart) / time.Duration(iters)

	t.Logf(">>> DESERIALIZATION DECODE PER 1024 MEMBERS:")
	t.Logf("       Baseline (fmt.Sprint): %v", tBase)
	t.Logf("       Optimized (Type Switch): %v (%.2fx faster)", tFast, float64(tBase)/float64(tFast))
}
