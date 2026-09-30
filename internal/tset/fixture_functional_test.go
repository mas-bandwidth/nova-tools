//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// tsetFixture owns one throwaway Redis process. Definitions and epoch markers
// are initialized directly; AddRow queues public rows steps. Other functional
// fixtures still seed raw member/cell or deliberately corrupt row keys.
type tsetFixture struct {
	Client         *redis.Client
	Space          string
	Epoch          string
	profile        fn.TSetProfile
	loaded         bool
	active         bool
	t              *testing.T
	tables         []string
	seededEpoch    string
	pendingRows    []fixturePendingRow
	paddingRows    int64
	nextPaddingRow int64
}

type fixturePendingRow struct {
	table, row, epoch string
	rank              int64
}

func newTSetFixture(t *testing.T) *tsetFixture {
	t.Helper()
	return newTSetFixtureProfile(t, fn.TSetStandalone)
}

func newComposedTSetFixture(t *testing.T) *tsetFixture {
	t.Helper()
	// Resolve the real embedded source before starting a Redis process. Only
	// the exact missing Layer 2 fragment may defer composed-only witnesses.
	requireTSetLogFragment(t)
	return newTSetFixtureProfile(t, fn.TSetComposed)
}

func newTSetFixtureProfile(t *testing.T, profile fn.TSetProfile) *tsetFixture {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	fx := &tsetFixture{Client: client, Space: "l1:", Epoch: "0", profile: profile, t: t, tables: []string{}}
	fx.seedEpoch(t)
	return fx
}

// newFixtureRedis owns a separate public tset connection to the same private
// server. The raw client remains available for setup and key-level assertions.
func newFixtureRedis(t *testing.T, client *redis.Client) *RedisStore {
	t.Helper()
	store, err := NewRedis(client.Options().Addr, "", "")
	if err != nil {
		t.Fatalf("create fixture tset store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close fixture tset store: %v", err)
		}
	})
	return store
}

func (fx *tsetFixture) seedEpoch(t *testing.T) {
	t.Helper()
	// A read fixture can construct snapshots for successive epochs before its
	// final Activate call. Commit queued rows in the old epoch before its raw
	// marker transition; the row mutations themselves remain public steps.
	if fx.seededEpoch != "" && fx.seededEpoch != fx.Epoch && len(fx.pendingRows) != 0 {
		fx.loadTSet(t)
		fx.flushPendingRows(t)
	}
	ctx := context.Background()
	pipe := fx.Client.Pipeline()
	catalog, err := json.Marshal(fx.tables)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{"engine": Version, "n": fx.Epoch, "tables": string(catalog)}
	pipe.HSet(ctx, fx.Space+"sprint:epoch", fields)
	pipe.HSet(ctx, fx.Space+"sprint:epoch@"+fx.Epoch, fields)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("seed tset epoch: %v", err)
	}
	fx.seededEpoch = fx.Epoch
}

// Define installs both the current definition and the epoch-zero snapshot.
// The fixture's owner must finish all definitions before Activate.
func (fx *tsetFixture) Define(t *testing.T, table string, columns ...string) {
	t.Helper()
	fx.mustInitialize(t)
	if fx.loaded {
		t.Fatal("fixture definition after tset library load")
	}
	if table == "" || len(columns) == 0 {
		t.Fatal("fixture definition needs a table and columns")
	}
	for _, old := range fx.tables {
		if old == table {
			t.Fatalf("fixture table %q already defined", table)
		}
	}
	if len(fx.tables) >= 4 {
		t.Fatal("fixture exceeds four table definitions")
	}
	fields := map[string]any{
		"engine":        Version,
		"order":         strings.Join(columns, ","),
		"member_prefix": fx.Space + "member:" + table + ":",
		"epoch_key":     fx.Space + "sprint:epoch",
		"epoch_field":   "n",
	}
	for _, column := range columns {
		if column == "" {
			t.Fatal("fixture column is empty")
		}
		fields["col:"+column] = "set"
	}
	ctx := context.Background()
	base := fx.Space + "table:" + table
	pipe := fx.Client.Pipeline()
	pipe.HSet(ctx, base, fields)
	pipe.HSet(ctx, base+":definition", fields)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("seed tset definition %q: %v", table, err)
	}
	fx.tables = append(fx.tables, table)
	fx.seedEpoch(t)
}

func (fx *tsetFixture) AddRow(t *testing.T, table, row string, rank int64) {
	t.Helper()
	fx.mustInitialize(t)
	if table == "" || row == "" || rank < 0 || rank > 9007199254740991 {
		t.Fatalf("invalid fixture row %q/%q rank %d", table, row, rank)
	}
	defined := false
	for _, name := range fx.tables {
		if name == table {
			defined = true
			break
		}
	}
	if !defined {
		t.Fatalf("fixture row %q/%q has no definition", table, row)
	}
	if fx.Epoch != fx.seededEpoch {
		t.Fatalf("fixture row %q/%q added before seeding epoch %q", table, row, fx.Epoch)
	}
	fx.pendingRows = append(fx.pendingRows, fixturePendingRow{table: table, row: row, epoch: fx.Epoch, rank: rank})
}

func (fx *tsetFixture) mustInitialize(t *testing.T) {
	t.Helper()
	if fx.active {
		t.Fatal("fixture setup after tset runtime activation")
	}
}

func (fx *tsetFixture) Activate(t *testing.T) {
	t.Helper()
	if fx.active {
		return
	}
	fx.loadTSet(t)
	fx.flushPendingRows(t)
	fx.active = true
}

func (fx *tsetFixture) loadTSet(t *testing.T) {
	t.Helper()
	if fx.loaded {
		return
	}
	if err := fn.LoadTSet(context.Background(), fx.Client, fx.profile); err != nil {
		t.Fatal(err)
	}
	fx.loaded = true
}

// flushPendingRows commits queued initial rows through exact encoded public
// rows steps. Public add assigns only max-rank+1; temporary rows bridge small
// requested rank gaps and are subsequently removed by public steps. In a
// composed profile these temporary mutations are real log/history events.
func (fx *tsetFixture) flushPendingRows(t *testing.T) {
	t.Helper()
	if len(fx.pendingRows) == 0 {
		return
	}
	if !fx.loaded {
		t.Fatal("fixture row flush before tset library load")
	}
	const maxPaddingRows int64 = 1024
	const rowsPerStep = 100
	ctx := context.Background()
	used := make(map[string]map[string]bool)
	for _, item := range fx.pendingRows {
		if item.epoch != fx.seededEpoch {
			t.Fatalf("fixture row %q/%q belongs to epoch %q, active setup epoch %q", item.table, item.row, item.epoch, fx.seededEpoch)
		}
		if used[item.table] == nil {
			used[item.table] = make(map[string]bool)
		}
		if used[item.table][item.row] {
			t.Fatalf("duplicate fixture row %q/%q", item.table, item.row)
		}
		used[item.table][item.row] = true
	}
	for table := range used {
		key := fixtureRowsKey(fx.Space, table, fx.seededEpoch)
		count, err := fx.Client.ZCard(ctx, key).Result()
		if err != nil || count != 0 {
			t.Fatalf("fixture public rows require empty %q before setup: count=%d err=%v", key, count, err)
		}
	}
	type plannedRow struct {
		fixturePendingRow
		padding []string
	}
	planned := make([]plannedRow, 0, len(fx.pendingRows))
	nextRank := make(map[string]int64)
	paddingCount := fx.paddingRows
	paddingName := fx.nextPaddingRow
	for _, item := range fx.pendingRows {
		want := nextRank[item.table]
		if item.rank < want {
			t.Fatalf("fixture row %q/%q rank %d is below next public rank %d", item.table, item.row, item.rank, want)
		}
		gap := item.rank - want
		if gap > maxPaddingRows-paddingCount {
			t.Fatalf("fixture row %q/%q rank gap %d exceeds public padding cap %d", item.table, item.row, gap, maxPaddingRows)
		}
		entry := plannedRow{fixturePendingRow: item}
		for i := int64(0); i < gap; i++ {
			for {
				candidate := fmt.Sprintf("__tset_fixture_pad_%04d", paddingName)
				paddingName++
				if !used[item.table][candidate] {
					used[item.table][candidate] = true
					entry.padding = append(entry.padding, candidate)
					break
				}
			}
		}
		paddingCount += gap
		nextRank[item.table] = item.rank + 1
		planned = append(planned, entry)
	}
	store := newFixtureRedis(t, fx.Client)
	addPadding := make(map[string][]string)
	for _, item := range planned {
		addPadding[item.table] = append(addPadding[item.table], item.padding...)
		add := append(append([]string{}, item.padding...), item.row)
		for len(add) != 0 {
			count := len(add)
			if count > rowsPerStep {
				count = rowsPerStep
			}
			fx.publicRowsStep(t, store, item.epoch, item.table, add[:count], nil)
			add = add[count:]
		}
	}
	for _, table := range fx.tables {
		padding := addPadding[table]
		for len(padding) != 0 {
			count := len(padding)
			if count > rowsPerStep {
				count = rowsPerStep
			}
			fx.publicRowsStep(t, store, fx.seededEpoch, table, nil, padding[:count])
			padding = padding[count:]
		}
	}
	for _, item := range fx.pendingRows {
		key := fixtureRowsKey(fx.Space, item.table, item.epoch)
		score, err := fx.Client.ZScore(ctx, key, item.row).Result()
		if err != nil || score != float64(item.rank) {
			t.Fatalf("fixture row %q/%q rank=%g err=%v, want %d", item.table, item.row, score, err, item.rank)
		}
	}
	for table, names := range addPadding {
		key := fixtureRowsKey(fx.Space, table, fx.seededEpoch)
		for _, name := range names {
			if score, err := fx.Client.ZScore(ctx, key, name).Result(); !errors.Is(err, redis.Nil) {
				t.Fatalf("temporary fixture row %q/%q survived cleanup: score=%g err=%v", table, name, score, err)
			}
		}
	}
	fx.paddingRows = paddingCount
	fx.nextPaddingRow = paddingName
	fx.pendingRows = nil
}

func (fx *tsetFixture) publicRowsStep(t *testing.T, store *RedisStore, epoch, table string, add, del []string) {
	t.Helper()
	ctx := context.Background()
	var before int64
	if fx.profile == fn.TSetComposed {
		var err error
		before, err = fx.Client.XLen(ctx, fixtureLogKey(fx.Space, epoch)).Result()
		if err != nil {
			t.Fatalf("fixture row log before %q: %v", table, err)
		}
	}
	reply, err := store.Step(ctx, Step{Epoch: Decimal(epoch), Space: fx.Space,
		Entries: []Entry{{Kind: "rows", Table: table, Add: add, Del: del}}})
	if err != nil || reply.Status != "ok" || reply.Replay {
		t.Fatalf("fixture public rows %q add=%v del=%v: reply=%+v err=%v", table, add, del, reply, err)
	}
	if fx.profile == fn.TSetComposed {
		after, err := fx.Client.XLen(ctx, fixtureLogKey(fx.Space, epoch)).Result()
		if err != nil || reply.Lines != 1 || reply.FirstSeq == "0" || reply.FirstSeq != reply.LastSeq || after != before+1 {
			t.Fatalf("fixture composed rows %q lacks one replayable row line: reply=%+v log=%d..%d err=%v", table, reply, before, after, err)
		}
	} else if reply.Lines != 0 {
		t.Fatalf("fixture standalone rows %q unexpectedly emitted %d lines", table, reply.Lines)
	}
}

// ActivateWithLua appends one test-owned callback to the isolated library.
// It is intentionally test-only and must be called during initialization.
func (fx *tsetFixture) ActivateWithLua(t *testing.T, extraSource string) {
	t.Helper()
	fx.mustInitialize(t)
	if fx.loaded {
		t.Fatal("fixture test callback must be loaded before any public row step")
	}
	name := tsetTestProbeName(t, extraSource)
	source := tsetTestSourceWithProbe(t, fx.profile, extraSource, name)
	if err := fx.Client.FunctionLoad(context.Background(), source).Err(); err != nil {
		t.Fatalf("load tset with test callback: %v", err)
	}
	tsetRequireProbeLoaded(t, fx.Client, name)
	fx.loaded = true
	fx.flushPendingRows(t)
	fx.active = true
}

// These helpers exist only in functional tests. They give one explicitly named
// probe the native load-time registration API while its callback continues to
// use the profile's late-bound runtime Redis methods. Production TSetSource
// never includes this second registration scope.
func tsetTestSourceWithProbe(t *testing.T, profile fn.TSetProfile, extraSource, name string) string {
	t.Helper()
	source, err := fn.TSetSource(profile)
	if err != nil {
		t.Fatal(err)
	}
	if name == "" || tsetTestProbeName(t, extraSource) != name {
		t.Fatalf("test callback %q is missing its explicit registration", name)
	}
	return source + "\ndo\nlocal redis = {\n" +
		"  call = redis.call, pcall = redis.pcall, sha1hex = redis.sha1hex,\n" +
		"  acl_check_cmd = redis.acl_check_cmd, register_function = native_redis.register_function,\n" +
		"}\n" + extraSource + "\nend\n"
}

func tsetTestProbeName(t *testing.T, extraSource string) string {
	t.Helper()
	const marker = "redis.register_function('"
	start := strings.Index(extraSource, marker)
	if start < 0 || strings.TrimSpace(extraSource) == "" {
		t.Fatal("test callback source has no literal registration")
	}
	start += len(marker)
	end := strings.IndexByte(extraSource[start:], '\'')
	if end <= 0 || strings.Contains(extraSource[start+end:], marker) {
		t.Fatal("test callback source must have exactly one literal registration")
	}
	return extraSource[start : start+end]
}

func tsetRequireProbeLoaded(t *testing.T, client *redis.Client, name string) {
	t.Helper()
	libraries, err := client.FunctionList(context.Background(), redis.FunctionListQuery{LibraryNamePattern: fn.Library}).Result()
	if err != nil {
		t.Fatalf("list test callback %q: %v", name, err)
	}
	for _, library := range libraries {
		if library.Name != fn.Library {
			continue
		}
		for _, function := range library.Functions {
			if function.Name == name {
				return
			}
		}
	}
	t.Fatalf("test callback %q was filtered from installed library", name)
}

// Reset opens a fresh declared initialization interval on this test's own
// process. It clears the library as well as data, so setup cannot act through
// an already installed runtime write surface.
func (fx *tsetFixture) Reset(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := fx.Client.Do(ctx, "FUNCTION", "FLUSH").Err(); err != nil {
		t.Fatalf("reset tset function library: %v", err)
	}
	if err := fx.Client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("reset tset data: %v", err)
	}
	fx.active = false
	fx.loaded = false
	fx.Epoch = "0"
	fx.tables = []string{}
	fx.seededEpoch = ""
	fx.pendingRows = nil
	fx.paddingRows = 0
	fx.nextPaddingRow = 0
	fx.seedEpoch(t)
}

// Step uses the public writer and retains the raw Redis result/error for
// refusal and unknown-outcome probes. It activates the profile after setup.
func (fx *tsetFixture) Step(rawJSON string) (any, error) {
	fx.Activate(fx.t)
	return fx.Client.FCall(context.Background(), "ns_tset_step", []string{}, Version, rawJSON).Result()
}

func fixtureTablePrefix(space, table, epoch string) string {
	base := space + "table:" + table
	if epoch == "0" {
		return base
	}
	return base + ":" + epoch
}

func fixtureDefinitionKey(space, table, epoch string) string {
	return fixtureTablePrefix(space, table, epoch) + ":definition"
}

func fixtureRowsKey(space, table, epoch string) string {
	return fixtureTablePrefix(space, table, epoch) + ":rows"
}

func fixtureCellKey(space, table, epoch, row, column string) string {
	return fixtureTablePrefix(space, table, epoch) + ":cell:" + row + ":" + column
}

func fixtureRecordKey(space, table, id string) string {
	return fmt.Sprintf("%smember:%s:%s", space, table, id)
}

func fixtureLogKey(space, epoch string) string { return space + "sprint:log@" + epoch }

func fixtureHistoryKey(space, epoch, about string) string {
	return space + "sprint:cl:" + about + "@" + epoch
}

func fixtureDoneKey(space, epoch string) string { return space + "sprint:done@" + epoch }

// SemanticSnapshot reads the model's observable table state directly from
// Redis. It is separate from the whole-key TYPE/DUMP image used on refusals:
// successful parity checks should compare rows, cells, records and receipts,
// while a refusal image must also catch unexpected keys and value types.
func (fx *tsetFixture) SemanticSnapshot(t *testing.T) MemSnapshot {
	t.Helper()
	ctx := context.Background()
	keys, err := fx.Client.Keys(ctx, fx.Space+"*").Result()
	if err != nil {
		t.Fatal(err)
	}
	activeHash, err := fx.Client.HGetAll(ctx, fx.Space+"sprint:epoch").Result()
	if err != nil {
		t.Fatalf("read active epoch: %v", err)
	}
	if activeHash["n"] == "" {
		t.Fatalf("invalid active epoch marker: %v", activeHash)
	}
	out := MemSnapshot{
		ActiveEpoch: Decimal(activeHash["n"]),
		Engine:      activeHash["engine"],
		Definitions: make(map[string]TableDefinition),
		Epochs:      make(map[Decimal]MemEpochSnapshot),
		Receipts:    make(map[Decimal]map[string]MemReceiptSnapshot),
		ZSets:       make(map[string]map[string]string),
	}
	known := map[string]bool{fx.Space + "sprint:epoch": true}
	for _, key := range keys {
		if strings.HasPrefix(key, fx.Space+"sprint:epoch@") {
			epoch := Decimal(strings.TrimPrefix(key, fx.Space+"sprint:epoch@"))
			marker, err := fx.Client.HGetAll(ctx, key).Result()
			if err != nil || marker["engine"] != Version || marker["n"] != string(epoch) {
				t.Fatalf("invalid epoch marker %q: %v err=%v", key, marker, err)
			}
			out.Epochs[epoch] = MemEpochSnapshot{Tables: make(map[string]MemTableSnapshot)}
			known[key] = true
		}
	}
	if len(out.Epochs) == 0 {
		t.Fatal("tset store has no epoch snapshot marker")
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, fx.Space+"table:") {
			continue
		}
		name := strings.TrimPrefix(key, fx.Space+"table:")
		if strings.Contains(name, ":") || name == "" {
			continue
		}
		h, err := fx.Client.HGetAll(ctx, key).Result()
		if err != nil {
			t.Fatalf("definition %q: %v", key, err)
		}
		columns := strings.Split(h["order"], ",")
		if len(columns) == 1 && columns[0] == "" {
			t.Fatalf("definition %q lacks order", key)
		}
		if h["engine"] != Version || h["member_prefix"] == "" || h["epoch_key"] != fx.Space+"sprint:epoch" ||
			h["epoch_field"] != "n" {
			t.Fatalf("invalid tset definition %q: %v", key, h)
		}
		for _, column := range columns {
			if h["col:"+column] != "set" {
				t.Fatalf("invalid set column %q in %q", column, key)
			}
		}
		sort.Strings(columns)
		out.Definitions[name] = TableDefinition{Columns: columns,
			MemberPrefix: h["member_prefix"], EpochKey: h["epoch_key"], EpochField: h["epoch_field"]}
		known[key] = true
	}
	var catalog []string
	if err := json.Unmarshal([]byte(activeHash["tables"]), &catalog); err != nil {
		t.Fatalf("invalid active table catalog %q: %v", activeHash["tables"], err)
	}
	if len(catalog) != len(out.Definitions) {
		t.Fatalf("active catalog %v differs from definitions %v", catalog, out.Definitions)
	}
	seenTables := make(map[string]bool, len(catalog))
	for _, table := range catalog {
		if _, ok := out.Definitions[table]; !ok || seenTables[table] {
			t.Fatalf("active catalog has missing or duplicate definition %q", table)
		}
		seenTables[table] = true
	}
	for epoch, es := range out.Epochs {
		for table, def := range out.Definitions {
			snapshotKey := fixtureDefinitionKey(fx.Space, table, string(epoch))
			known[snapshotKey] = true
			snapshot, err := fx.Client.HGetAll(ctx, snapshotKey).Result()
			if err != nil {
				t.Fatalf("snapshot definition %q: %v", snapshotKey, err)
			}
			snapshotColumns := strings.Split(snapshot["order"], ",")
			sort.Strings(snapshotColumns)
			if snapshot["engine"] != Version || snapshot["member_prefix"] != def.MemberPrefix ||
				snapshot["epoch_key"] != def.EpochKey || snapshot["epoch_field"] != def.EpochField ||
				strings.Join(snapshotColumns, ",") != strings.Join(def.Columns, ",") {
				t.Fatalf("snapshot definition %q diverges from live definition: %v", snapshotKey, snapshot)
			}
			for _, column := range def.Columns {
				if snapshot["col:"+column] != "set" {
					t.Fatalf("snapshot definition %q lacks set column %q", snapshotKey, column)
				}
			}
			rowsKey := fixtureRowsKey(fx.Space, table, string(epoch))
			known[rowsKey] = true
			rows := make(map[string]Decimal)
			rowScores, err := fx.Client.ZRangeWithScores(ctx, rowsKey, 0, -1).Result()
			if err != nil {
				t.Fatalf("rows %q: %v", rowsKey, err)
			}
			for _, z := range rowScores {
				row := fmt.Sprint(z.Member)
				if z.Score < 0 || z.Score > 9007199254740991 || float64(int64(z.Score)) != z.Score {
					t.Fatalf("row %q has invalid rank %g", row, z.Score)
				}
				rows[row] = Decimal(strconv.FormatInt(int64(z.Score), 10))
			}
			ts := MemTableSnapshot{Rows: rows,
				Cells:   make(map[string]map[string]map[string]string),
				Records: make(map[string]MemRecord)}
			for row := range rows {
				for _, column := range def.Columns {
					cellKey := fixtureCellKey(fx.Space, table, string(epoch), row, column)
					known[cellKey] = true
					members, err := fx.Client.ZRangeWithScores(ctx, cellKey, 0, -1).Result()
					if err != nil {
						t.Fatalf("cell %q: %v", cellKey, err)
					}
					if len(members) == 0 {
						continue
					}
					if ts.Cells[row] == nil {
						ts.Cells[row] = make(map[string]map[string]string)
					}
					cell := make(map[string]string, len(members))
					for _, member := range members {
						cell[fmt.Sprint(member.Member)] = memScoreText(member.Score)
					}
					ts.Cells[row][column] = cell
				}
			}
			es.Tables[table] = ts
		}
		out.Epochs[epoch] = es
	}
	for table, def := range out.Definitions {
		for _, key := range keys {
			if !strings.HasPrefix(key, def.MemberPrefix) {
				continue
			}
			known[key] = true
			id := strings.TrimPrefix(key, def.MemberPrefix)
			h, err := fx.Client.HGetAll(ctx, key).Result()
			if err != nil {
				t.Fatalf("record %q: %v", key, err)
			}
			epoch := Decimal(h["epoch"])
			es, ok := out.Epochs[epoch]
			if !ok {
				t.Fatalf("record %q has unknown epoch %q", key, epoch)
			}
			ts := es.Tables[table]
			record := MemRecord{Epoch: epoch, Revision: Decimal(h["revision"]), Fields: make(map[string]string)}
			if place := h["place:"+table]; place != "" {
				sep := strings.LastIndex(place, ":")
				if sep < 0 {
					t.Fatalf("record %q has malformed place %q", key, place)
				}
				record.Row, record.Column = place[:sep], place[sep+1:]
				record.Score = ts.Cells[record.Row][record.Column][id]
			}
			for field, value := range h {
				if field != "epoch" && field != "revision" && field != "place:"+table {
					record.Fields[field] = value
				}
			}
			ts.Records[id] = record
			es.Tables[table] = ts
			out.Epochs[epoch] = es
		}
	}
	for epoch := range out.Epochs {
		key := fixtureDoneKey(fx.Space, string(epoch))
		known[key] = true
		values, err := fx.Client.HGetAll(ctx, key).Result()
		if err != nil {
			t.Fatalf("receipts %q: %v", key, err)
		}
		if len(values) == 0 {
			continue
		}
		byOp := make(map[string]MemReceiptSnapshot, len(values))
		for op, raw := range values {
			var receipt memReceipt
			if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
				t.Fatalf("receipt %q/%q: %v", key, op, err)
			}
			byOp[op] = MemReceiptSnapshot{IntentDigest: receipt.IntentDigest,
				Status: receipt.Status, EpochBefore: receipt.EpochBefore,
				EpochAfter: receipt.EpochAfter, FirstSeq: receipt.FirstSeq,
				LastSeq: receipt.LastSeq, Changed: receipt.Changed,
				Result: receipt.Result}
		}
		out.Receipts[epoch] = byOp
	}
	for _, key := range keys {
		if known[key] || strings.HasPrefix(key, fx.Space+"sprint:log@") ||
			strings.HasPrefix(key, fx.Space+"sprint:cl:") {
			continue
		}
		kind, err := fx.Client.Type(ctx, key).Result()
		if err != nil {
			t.Fatalf("TYPE %q: %v", key, err)
		}
		if kind != "zset" {
			t.Fatalf("unexpected %s key %q in tset semantic snapshot", kind, key)
		}
		members, err := fx.Client.ZRangeWithScores(ctx, key, 0, -1).Result()
		if err != nil {
			t.Fatalf("ZRange %q: %v", key, err)
		}
		set := make(map[string]string, len(members))
		for _, member := range members {
			set[fmt.Sprint(member.Member)] = memScoreText(member.Score)
		}
		out.ZSets[key] = set
	}
	return out
}
