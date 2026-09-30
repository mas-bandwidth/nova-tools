package tset

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A read budget belongs to the entire call, including queries answered before
// an eventual refusal. Nothing accumulated by the caller escapes on refusal.
type readBudget struct {
	fetched, probes, fields, records, rangeIDs int64
	encoded                                    int64
	epoch                                      Decimal
	seenRecords                                map[string]bool
	seenRows                                   map[string]bool
	seenFields                                 map[string]map[string]bool
}

func (b *readBudget) observeRow(table string, epoch Decimal, row string, rank Decimal) error {
	key := table + "\x00" + string(epoch) + "\x00" + row
	if b.seenRows[key] {
		return nil
	}
	if err := b.reserveRaw(32); err != nil {
		return err
	}
	if err := b.chargeProbe(1); err != nil {
		return err
	}
	if err := b.chargeRaw(int64(len(rank))); err != nil {
		return err
	}
	b.seenRows[key] = true
	return nil
}

const (
	readFetchedLimit = 8 << 20
	readReplyLimit   = 8 << 20
	readProbeLimit   = 20000
	readFieldLimit   = 1280000
	readRecordLimit  = 10000
	readRangeLimit   = 20000
)

func (b *readBudget) chargeRaw(n int64) error {
	if n < 0 || n > readFetchedLimit-b.fetched {
		return readBudgetError("fetched_bytes", readFetchedLimit, b.fetched+n)
	}
	b.fetched += n
	return nil
}

// Reserve before a modeled store read, then replace the reservation with the
// actual payload charge. This catches requests whose worst-case reply cannot
// fit even when the in-memory value happens to be small.
func (b *readBudget) reserveRaw(n int64) error {
	if n < 0 || n > readFetchedLimit-b.fetched {
		return readBudgetError("fetched_bytes", readFetchedLimit, b.fetched+n)
	}
	return nil
}

func (b *readBudget) chargeProbe(n int64) error {
	if n < 0 || n > readProbeLimit-b.probes {
		return readBudgetError("cell", readProbeLimit, b.probes+n)
	}
	b.probes += n
	return nil
}

func (b *readBudget) chargeField(n int64) error {
	if n < 0 || n > readFieldLimit-b.fields {
		return readBudgetError("field", readFieldLimit, b.fields+n)
	}
	b.fields += n
	return nil
}

func (b *readBudget) chargeRecord(n int64) error {
	if n < 0 || n > readRecordLimit-b.records {
		return readBudgetError("record", readRecordLimit, b.records+n)
	}
	b.records += n
	return nil
}

func (b *readBudget) chargeRangeID(n int64) error {
	if n < 0 || n > readRangeLimit-b.rangeIDs {
		return readBudgetError("range_id", readRangeLimit, b.rangeIDs+n)
	}
	b.rangeIDs += n
	return nil
}

func readBudgetError(name string, limit, actual int64) error {
	return memRefusal("BUDGET", RefusalDetail{Budget: name, Limit: &limit, Actual: &actual})
}

func readAtQuery(err error, index int, active Decimal) error {
	if r, ok := err.(*Refusal); ok {
		r.Detail.QueryIndex = &index
		if (r.Code == "EPOCHAHEAD" || r.Code == "STALE" || r.Code == "ADVANCE") && r.Detail.ActiveEpoch == "" {
			r.Detail.ActiveEpoch = active
		}
	}
	return err
}

func (b *readBudget) counters() json.RawMessage {
	v, _ := json.Marshal(struct {
		StoreCommands     int64 `json:"store_commands"`
		PlannedCommands   int64 `json:"planned_commands"`
		PlannedArgvBytes  int64 `json:"planned_argv_bytes"`
		FetchedBytes      int64 `json:"fetched_bytes"`
		Field             int64 `json:"field"`
		Cell              int64 `json:"cell"`
		Record            int64 `json:"record"`
		RangeID           int64 `json:"range_id"`
		LogID             int64 `json:"log_id"`
		GeneratedLogBytes int64 `json:"generated_log_bytes"`
	}{0, 0, 0, b.fetched, b.fields, b.probes, b.records, b.rangeIDs, 0, 0})
	return v
}

// Read evaluates every planning query against one locked in-memory snapshot.
// The clock is sampled once, even for a mixed read.
func (m *Mem) Read(ctx context.Context, plan ReadPlan) (ReadReply, error) {
	if err := ctx.Err(); err != nil {
		return ReadReply{}, err
	}
	if _, err := EncodeReadPlan(plan); err != nil {
		return ReadReply{}, err
	}
	if plan.Mode != "" && plan.Mode != "atomic" && plan.Mode != "page" {
		return ReadReply{}, memRefusal("REQUEST", RefusalDetail{})
	}
	if plan.Mode == "page" {
		return ReadReply{}, memRefusal("REQUEST", RefusalDetail{}) // L2 owns page reads.
	}
	for i, q := range plan.Queries {
		if err := validateReadQuery(q); err != nil {
			return ReadReply{}, readAtQuery(err, i, "")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	space := m.spaces[plan.Space]
	if space == nil {
		return ReadReply{}, memRefusal("CONFIG", RefusalDetail{})
	}
	if space.engine != Version {
		return ReadReply{}, memRefusal("ENGINE", RefusalDetail{})
	}
	if err := validateMemConfig(plan.Space, space); err != nil {
		return ReadReply{}, err
	}
	active := space.active
	if !validReadEpoch(plan.Epoch) {
		return ReadReply{}, memRefusal("REQUEST", RefusalDetail{})
	}
	if decimalLess(active, plan.Epoch) {
		return ReadReply{}, memRefusal("EPOCHAHEAD", RefusalDetail{ActiveEpoch: active})
	}
	epoch := space.epochs[plan.Epoch]
	if epoch == nil {
		return ReadReply{}, memRefusal("EPOCHGONE", RefusalDetail{ActiveEpoch: active})
	}
	clock := m.now()
	now := clock.UnixMilli()
	if now < 0 {
		return ReadReply{}, memRefusal("OVERFLOW", RefusalDetail{})
	}
	reply := ReadReply{Status: "read", Epoch: plan.Epoch, ActiveEpoch: active, TimeMS: Decimal(strconv.FormatInt(now, 10)), Answers: make([]ReadAnswer, 0, len(plan.Queries)), Complete: true}
	b := &readBudget{epoch: plan.Epoch, encoded: 512, seenRecords: make(map[string]bool), seenRows: make(map[string]bool), seenFields: make(map[string]map[string]bool)}
	// Engine and epoch are structural probes. TIME is read once but does not
	// consume the cell/key-probe budget. Active definitions use HLEN and
	// HGETALL per named table; retained definitions use one HGETALL plus two
	// snapshot checks. These are structural key probes, not record payloads.
	baseProbes := int64(2)
	seenTables := make(map[string]bool)
	for _, q := range plan.Queries {
		if q.Table != "" && !seenTables[q.Table] {
			seenTables[q.Table] = true
			if plan.Epoch == active {
				baseProbes += 2
			} else {
				baseProbes++
			}
		}
	}
	if plan.Epoch != active {
		baseProbes += 2
	}
	if err := b.chargeProbe(baseProbes); err != nil {
		return ReadReply{}, err
	}
	baseRaw := int64(len("tset/1") + len(active) + len(strconv.FormatInt(clock.Unix(), 10)) + 6)
	if plan.Epoch != active {
		baseRaw += int64(len("tset/1") + len(plan.Epoch))
	}
	for table := range seenTables {
		definition := space.rawDefinition(table)
		if plan.Epoch == active {
			baseRaw += int64(len(strconv.Itoa(len(definition))))
		}
		for field, value := range definition {
			baseRaw += int64(len(field) + len(value))
		}
	}
	if err := b.chargeRaw(baseRaw); err != nil {
		return ReadReply{}, err
	}
	for i, q := range plan.Queries {
		if err := ctx.Err(); err != nil {
			return ReadReply{}, err
		}
		answer, err := m.readOne(plan.Space, space, epoch, q, b)
		if err != nil {
			return ReadReply{}, readAtQuery(err, i, active)
		}
		reply.Answers = append(reply.Answers, answer)
		if err := b.accountEncodedAnswer(answer); err != nil {
			return ReadReply{}, readAtQuery(err, i, active)
		}
		reply.Counters = b.counters()
		encodedBytes, err := readCJSONLength(reply)
		if err != nil {
			return ReadReply{}, readAtQuery(memRefusal("DRIFT", RefusalDetail{}), i, active)
		}
		if encodedBytes > readReplyLimit {
			return ReadReply{}, readAtQuery(readBudgetError("encoded_reply", readReplyLimit, encodedBytes), i, active)
		}
	}
	reply.Counters = b.counters()
	return reply, nil
}

// The Lua reader reserves 512 envelope bytes and accounts each emitted answer
// item. Keep that ledger for the rows count preflight, which must happen before
// materializing an unbounded row list. The final serialized reply is still
// checked exactly by Read.
func (b *readBudget) emitReadItem(value any) error {
	bytes, err := readCJSONLength(value)
	if err != nil {
		return memRefusal("DRIFT", RefusalDetail{})
	}
	b.encoded += bytes + 1
	if b.encoded > readReplyLimit {
		return readBudgetError("encoded_reply", readReplyLimit, b.encoded)
	}
	return nil
}

func (b *readBudget) accountEncodedAnswer(answer ReadAnswer) error {
	switch answer.Kind {
	case "count", "rcount":
		return b.emitReadItem(answer)
	case "done":
		for _, slot := range answer.Done {
			if err := b.emitReadItem(slot); err != nil {
				return err
			}
		}
	}
	return nil
}

// Lua's bundled CJSON leaves HTML characters and U+2028/U+2029 as UTF-8,
// but escapes slash and DEL. Go's JSON encoder does the opposite for those
// strings. Parse only the bounded encoded string tokens to reproduce CJSON's
// byte count; non-string JSON syntax has the same length in both encoders.
func readCJSONLength(value any) (int64, error) {
	wire, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	var total int64
	for i := 0; i < len(wire); {
		if wire[i] != '"' {
			total++
			i++
			continue
		}
		start := i
		i++
		for i < len(wire) {
			if wire[i] == '\\' {
				i += 2
				continue
			}
			if wire[i] == '"' {
				i++
				break
			}
			i++
		}
		if i > len(wire) || wire[i-1] != '"' {
			return 0, errors.New("unterminated JSON string")
		}
		unquoted, err := strconv.Unquote(string(wire[start:i]))
		if err != nil {
			return 0, err
		}
		total += readCJSONStringLength(unquoted)
	}
	return total, nil
}

func readCJSONStringLength(value string) int64 {
	length := int64(2) // surrounding quotes
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\b', '\t', '\n', '\f', '\r', '"', '/', '\\':
			length += 2
		case 0x7f:
			length += 6
		default:
			if value[i] < 0x20 {
				length += 6
			} else {
				length++
			}
		}
	}
	return length
}

// Static shape checks precede all store and epoch observations. Dynamic table,
// row, and record checks remain in readOne under the snapshot lock.
func validateReadQuery(q ReadQuery) error {
	switch q.Kind {
	case "range":
		if q.Limit <= 0 {
			return memRefusal("REQUEST", RefusalDetail{})
		}
		if q.Limit > 2000 {
			return memRefusal("LIMIT", RefusalDetail{})
		}
		if _, ok := parseReadBound(q.Min); !ok {
			return memRefusal("REQUEST", RefusalDetail{})
		}
		if _, ok := parseReadBound(q.Max); !ok {
			return memRefusal("REQUEST", RefusalDetail{})
		}
		if (q.Key == "" && (q.Table == "" || q.Cell == "")) ||
			(q.Key != "" && (q.Table != "" || q.Cell != "" || q.Records || q.Fields != nil)) ||
			(!q.Records && q.Fields != nil) {
			return memRefusal("REQUEST", RefusalDetail{})
		}
	case "count", "rcount":
		if q.Table == "" || len(q.Cells) == 0 {
			return memRefusal("REQUEST", RefusalDetail{})
		}
		if q.Kind == "rcount" {
			if _, ok := parseReadBound(q.Min); !ok {
				return memRefusal("REQUEST", RefusalDetail{})
			}
			if _, ok := parseReadBound(q.Max); !ok {
				return memRefusal("REQUEST", RefusalDetail{})
			}
		} else if q.Min != "" || q.Max != "" {
			return memRefusal("REQUEST", RefusalDetail{})
		}
	case "ids":
		if q.Table == "" || q.IDs == nil {
			return memRefusal("REQUEST", RefusalDetail{})
		}
	case "rows":
		if q.Table == "" {
			return memRefusal("REQUEST", RefusalDetail{})
		}
	case "done":
		if q.Ops == nil {
			return memRefusal("REQUEST", RefusalDetail{})
		}
	default:
		return memRefusal("REQUEST", RefusalDetail{})
	}
	return nil
}

func (m *Mem) readOne(spaceName string, space *memSpace, epoch *memEpoch, q ReadQuery, b *readBudget) (ReadAnswer, error) {
	switch q.Kind {
	case "range":
		return readRange(spaceName, space, epoch, q, b)
	case "count", "rcount":
		return readCounts(space, epoch, q, b)
	case "ids":
		return readIDs(space, epoch, q, b)
	case "rows":
		return readRows(space, epoch, q, b)
	case "done":
		return m.readDoneQuery(space, q, b)
	default:
		return ReadAnswer{}, memRefusal("REQUEST", RefusalDetail{})
	}
}

func readTable(space *memSpace, epoch *memEpoch, name string) (*memTableDef, *memTableEpoch, error) {
	def := space.defs[name]
	if def == nil {
		return nil, nil, memRefusal("NOTABLE", RefusalDetail{Table: name})
	}
	table := epoch.tables[name]
	if table == nil {
		return nil, nil, memRefusal("EPOCHGONE", RefusalDetail{Table: name, ActiveEpoch: space.active})
	}
	return def, table, nil
}

func readCell(tableName string, def *memTableDef, table *memTableEpoch, cell string, b *readBudget) (map[string]string, error) {
	row, col, ok := splitReadCell(cell)
	if !ok {
		return nil, memRefusal("REQUEST", RefusalDetail{Cells: []string{cell}})
	}
	if !def.columns[col] {
		return nil, memRefusal("NOCOL", RefusalDetail{Cells: []string{cell}})
	}
	rank, exists := table.rows[row]
	// The attempted ZSCORE consumes a probe even when Redis finds no row.
	if err := b.observeRow(tableName, b.epoch, row, rank); err != nil {
		return nil, err
	}
	if !exists {
		return nil, memRefusal("NOROW", RefusalDetail{Cells: []string{cell}})
	}
	if !memValidDecimal(rank) || memCompareDecimal(rank, "9007199254740991") > 0 {
		return nil, memRefusal("DRIFT", RefusalDetail{Cells: []string{cell}})
	}
	return table.cells[row][col], nil
}

func splitReadCell(cell string) (string, string, bool) {
	i := strings.LastIndexByte(cell, ':')
	if i <= 0 || i == len(cell)-1 || len(cell[:i]) > 256 || len(cell[i+1:]) > 256 {
		return "", "", false
	}
	return cell[:i], cell[i+1:], true
}

type readBound struct {
	value     float64
	exclusive bool
}

var readNumber = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
var readHexNumber = regexp.MustCompile(`^[+-]?0[xX](?:[0-9a-fA-F]+(?:\.[0-9a-fA-F]*)?|\.[0-9a-fA-F]+)(?:[pP][+-]?[0-9]+)?$`)

func parseReadBound(s string) (readBound, bool) {
	// Redis's range parser treats the exact empty token as inclusive zero and
	// a bare exclusive prefix as exclusive zero. Whitespace-only is invalid.
	if s == "" {
		return readBound{}, true
	}
	if s == "(" {
		return readBound{exclusive: true}, true
	}
	b := readBound{}
	if strings.HasPrefix(s, "(") {
		b.exclusive = true
		s = s[1:]
	}
	// Redis's score-range parser accepts leading ASCII whitespace and C strtod
	// hexadecimal numbers. It does not accept trailing whitespace. This is a
	// different lexical domain from ZADD's finite score input.
	s = strings.TrimLeft(s, " \t\n\r\f\v")
	switch strings.ToLower(s) {
	case "-inf", "-infinity":
		b.value = math.Inf(-1)
		return b, true
	case "+inf", "+infinity", "inf", "infinity":
		b.value = math.Inf(1)
		return b, true
	}
	if !readNumber.MatchString(s) && !readHexNumber.MatchString(s) {
		return readBound{}, false
	}
	if readHexNumber.MatchString(s) && !strings.ContainsAny(s, "pP") {
		s += "p0"
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) || math.IsNaN(v) {
		return readBound{}, false
	}
	b.value = v
	return b, true
}

func inReadBounds(score float64, min, max readBound) bool {
	return (score > min.value || (!min.exclusive && score == min.value)) &&
		(score < max.value || (!max.exclusive && score == max.value))
}

func readScore(s string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, memRefusal("DRIFT", RefusalDetail{})
	}
	return v, nil
}

type readPair struct {
	id, score string
	numeric   float64
}

func readRange(spaceName string, space *memSpace, epoch *memEpoch, q ReadQuery, b *readBudget) (ReadAnswer, error) {
	if q.Limit <= 0 || q.Limit > 2000 {
		return ReadAnswer{}, memRefusal("LIMIT", RefusalDetail{})
	}
	min, minOK := parseReadBound(q.Min)
	max, maxOK := parseReadBound(q.Max)
	if !minOK || !maxOK {
		return ReadAnswer{}, memRefusal("REQUEST", RefusalDetail{})
	}
	var members map[string]string
	var table *memTableEpoch
	if q.Key != "" {
		if q.Table != "" || q.Cell != "" || q.Records || q.Fields != nil || !strings.HasPrefix(q.Key, spaceName) {
			return ReadAnswer{}, memRefusal("REQUEST", RefusalDetail{})
		}
		members = space.zsets[q.Key]
	} else {
		if q.Table == "" || q.Cell == "" {
			return ReadAnswer{}, memRefusal("REQUEST", RefusalDetail{})
		}
		def, t, err := readTable(space, epoch, q.Table)
		if err != nil {
			return ReadAnswer{}, err
		}
		table = t
		members, err = readCell(q.Table, def, t, q.Cell, b)
		if err != nil {
			return ReadAnswer{}, err
		}
	}
	if err := b.reserveRaw(int64(q.Limit+1) * 320); err != nil {
		return ReadAnswer{}, err
	}
	if err := b.chargeProbe(1); err != nil { // one ZRANGE
		return ReadAnswer{}, err
	}
	pairs := make([]readPair, 0, len(members))
	for id, score := range members {
		n, err := readScore(score)
		if err != nil {
			return ReadAnswer{}, err
		}
		if inReadBounds(n, min, max) {
			pairs = append(pairs, readPair{id, score, n})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].numeric != pairs[j].numeric {
			return pairs[i].numeric < pairs[j].numeric
		}
		return pairs[i].id < pairs[j].id
	})
	if q.Desc {
		for i, j := 0, len(pairs)-1; i < j; i, j = i+1, j-1 {
			pairs[i], pairs[j] = pairs[j], pairs[i]
		}
	}
	answer := ReadAnswer{Kind: "range", IDs: []string{}, Scores: []string{}, HasMore: len(pairs) > q.Limit}
	selected := pairs
	if len(selected) > q.Limit {
		selected = selected[:q.Limit]
	}
	if err := b.chargeRangeID(int64(len(selected))); err != nil {
		return ReadAnswer{}, err
	}
	if q.Records {
		answer.Records = []MemberRecord{}
	}
	for _, pair := range selected {
		if len(pair.id) == 0 || len(pair.id) > 256 || !utf8.ValidString(pair.id) {
			return ReadAnswer{}, memRefusal("DRIFT", RefusalDetail{IDs: []string{pair.id}})
		}
		if err := b.chargeRaw(int64(len(pair.id) + len(pair.score))); err != nil {
			return ReadAnswer{}, err
		}
		answer.IDs = append(answer.IDs, pair.id)
		answer.Scores = append(answer.Scores, pair.score)
	}
	if answer.HasMore {
		probe := pairs[q.Limit]
		if len(probe.id) == 0 || len(probe.id) > 256 || !utf8.ValidString(probe.id) {
			return ReadAnswer{}, memRefusal("DRIFT", RefusalDetail{IDs: []string{probe.id}})
		}
		if err := b.chargeRaw(int64(len(probe.id) + len(probe.score))); err != nil {
			return ReadAnswer{}, err
		}
	}
	// Lua emits the range head before projecting any returned record.
	base := ReadAnswer{Kind: "range", IDs: answer.IDs, Scores: answer.Scores, HasMore: answer.HasMore}
	if err := b.emitReadItem(base); err != nil {
		return ReadAnswer{}, err
	}
	if q.Records {
		for _, pair := range selected {
			if err := b.chargeRecord(1); err != nil {
				return ReadAnswer{}, err
			}
			record, err := projectReadRecord(q.Table, table, pair.id, q.Fields, b)
			if err != nil {
				return ReadAnswer{}, err
			}
			if !record.Exists || record.Place == nil {
				return ReadAnswer{}, memRefusal("DRIFT", RefusalDetail{IDs: []string{pair.id}})
			}
			row, col, _ := splitReadCell(q.Cell)
			if record.Place.Row != row || record.Place.Col != col {
				return ReadAnswer{}, memRefusal("DRIFT", RefusalDetail{IDs: []string{pair.id}})
			}
			if err := b.emitReadItem(record); err != nil {
				return ReadAnswer{}, err
			}
			answer.Records = append(answer.Records, record)
		}
	}
	return answer, nil
}

func readCounts(space *memSpace, epoch *memEpoch, q ReadQuery, b *readBudget) (ReadAnswer, error) {
	def, table, err := readTable(space, epoch, q.Table)
	if err != nil {
		return ReadAnswer{}, err
	}
	if len(q.Cells) == 0 {
		return ReadAnswer{}, memRefusal("REQUEST", RefusalDetail{})
	}
	answer := ReadAnswer{Kind: q.Kind, Counts: make([]uint64, 0, len(q.Cells))}
	var min, max readBound
	if q.Kind == "rcount" {
		var ok bool
		min, ok = parseReadBound(q.Min)
		if !ok {
			return ReadAnswer{}, memRefusal("REQUEST", RefusalDetail{})
		}
		max, ok = parseReadBound(q.Max)
		if !ok {
			return ReadAnswer{}, memRefusal("REQUEST", RefusalDetail{})
		}
	} else if q.Min != "" || q.Max != "" {
		return ReadAnswer{}, memRefusal("REQUEST", RefusalDetail{})
	}
	for _, cell := range q.Cells {
		members, err := readCell(q.Table, def, table, cell, b)
		if err != nil {
			return ReadAnswer{}, err
		}
		if err := b.reserveRaw(32); err != nil {
			return ReadAnswer{}, err
		}
		if err := b.chargeProbe(1); err != nil { // ZCARD/ZCOUNT
			return ReadAnswer{}, err
		}
		var n uint64
		if q.Kind == "count" {
			n = uint64(len(members))
		} else {
			for _, score := range members {
				v, err := readScore(score)
				if err != nil {
					return ReadAnswer{}, err
				}
				if inReadBounds(v, min, max) {
					n++
				}
			}
		}
		answer.Counts = append(answer.Counts, n)
		if err := b.chargeRaw(int64(len(strconv.FormatUint(n, 10)))); err != nil {
			return ReadAnswer{}, err
		}
		answer.Sum += n
		if answer.Sum < n || answer.Sum > 9007199254740991 {
			return ReadAnswer{}, memRefusal("OVERFLOW", RefusalDetail{})
		}
	}
	return answer, nil
}

func readIDs(space *memSpace, epoch *memEpoch, q ReadQuery, b *readBudget) (ReadAnswer, error) {
	_, requestedTable, err := readTable(space, epoch, q.Table)
	if err != nil {
		return ReadAnswer{}, err
	}
	answer := ReadAnswer{Kind: "ids", Records: make([]MemberRecord, 0, len(q.IDs))}
	for _, id := range q.IDs {
		if len(id) == 0 || len(id) > 256 {
			return ReadAnswer{}, memRefusal("REQUEST", RefusalDetail{IDs: []string{id}})
		}
		if err := b.chargeRecord(1); err != nil {
			return ReadAnswer{}, err
		}
		// A member hash is keyed by table and stored ID, without an epoch
		// suffix. Its own epoch selects the row and cell used to validate a
		// placed record, even when the read requested another retained epoch.
		table := requestedTable
		if owner, _, found := space.recordTable(q.Table, id); found {
			table = owner
		} else if _, indexed := space.recordEpoch[q.Table][id]; indexed {
			return ReadAnswer{}, memRefusal("DRIFT", RefusalDetail{Table: q.Table, IDs: []string{id}})
		}
		record, err := projectReadRecord(q.Table, table, id, q.Fields, b)
		if err != nil {
			return ReadAnswer{}, err
		}
		if err := b.emitReadItem(record); err != nil {
			return ReadAnswer{}, err
		}
		answer.Records = append(answer.Records, record)
	}
	return answer, nil
}

func projectReadRecord(tableName string, table *memTableEpoch, id string, fields []string, b *readBudget) (MemberRecord, error) {
	answer := MemberRecord{ID: id, Exists: false}
	cacheKey := tableName + "\x00" + id
	record := table.records[id]
	if record == nil {
		if fields == nil { // app_fields HLEN on every projection
			if err := b.reserveRaw(32); err != nil {
				return MemberRecord{}, err
			}
			if err := b.chargeRaw(1); err != nil {
				return MemberRecord{}, err
			}
		}
		if !b.seenRecords[cacheKey] { // S.before HLEN on its first observation
			b.seenRecords[cacheKey] = true
			if err := b.reserveRaw(600); err != nil {
				return MemberRecord{}, err
			}
			if err := b.reserveRaw(32); err != nil {
				return MemberRecord{}, err
			}
			if err := b.chargeRaw(1); err != nil {
				return MemberRecord{}, err
			}
		}
		if b.seenFields[cacheKey] == nil {
			b.seenFields[cacheKey] = make(map[string]bool)
		}
		newFields := 0
		for _, field := range fields {
			if !b.seenFields[cacheKey][field] {
				newFields++
			}
		}
		if int64(newFields)*MaxFieldValueBytes > readFetchedLimit-b.fetched {
			for _, field := range fields {
				if b.seenFields[cacheKey][field] {
					continue
				}
				if err := b.reserveRaw(32); err != nil {
					return MemberRecord{}, err
				}
				if err := b.chargeRaw(1); err != nil {
					return MemberRecord{}, err
				} // HSTRLEN zero
			}
		} else if err := b.reserveRaw(int64(newFields) * MaxFieldValueBytes); err != nil {
			return MemberRecord{}, err
		}
		answer.Fields = make(map[string]FieldValue, len(fields))
		for _, field := range fields {
			if len(field) == 0 || len(field) > 256 {
				return MemberRecord{}, memRefusal("REQUEST", RefusalDetail{IDs: []string{id}})
			}
			if err := b.chargeField(1); err != nil {
				return MemberRecord{}, err
			}
			answer.Fields[field] = FieldValue{Present: false}
			b.seenFields[cacheKey][field] = true
		}
		return answer, nil
	}
	answer.Exists = true
	answer.Epoch = record.epoch
	answer.Revision = record.revision
	if !b.seenRecords[cacheKey] {
		b.seenRecords[cacheKey] = true
		if err := b.reserveRaw(600); err != nil {
			return MemberRecord{}, err
		}
		metaBytes := len(record.epoch) + len(record.revision)
		if record.place != nil {
			metaBytes += len(record.place.row) + 1 + len(record.place.col)
		}
		if err := b.chargeRaw(int64(metaBytes)); err != nil {
			return MemberRecord{}, err
		}
		if err := b.reserveRaw(32); err != nil {
			return MemberRecord{}, err
		}
		storedFields := len(record.fields) + 2
		if record.place != nil {
			storedFields++
		}
		if err := b.chargeRaw(int64(len(strconv.Itoa(storedFields)))); err != nil {
			return MemberRecord{}, err
		}
		if record.place != nil {
			rank, rowExists := table.rows[record.place.row]
			if err := b.observeRow(tableName, record.epoch, record.place.row, rank); err != nil {
				return MemberRecord{}, err
			}
			if !rowExists {
				return MemberRecord{}, memRefusal("DRIFT", RefusalDetail{Table: tableName, IDs: []string{id}, Rows: []string{record.place.row}})
			}
			if err := b.chargeProbe(1); err != nil {
				return MemberRecord{}, err
			}
			if err := b.reserveRaw(32); err != nil {
				return MemberRecord{}, err
			}
			if err := b.chargeRaw(int64(len(record.score))); err != nil {
				return MemberRecord{}, err
			}
		}
	}
	if record.place != nil {
		answer.Place = &CellPlace{Row: record.place.row, Col: record.place.col}
		answer.Score = record.score
		if _, ok := table.rows[record.place.row]; !ok {
			return MemberRecord{}, memRefusal("DRIFT", RefusalDetail{IDs: []string{id}})
		}
		cell := table.cells[record.place.row][record.place.col]
		actual, ok := cell[id]
		if !ok {
			return MemberRecord{}, memRefusal("DRIFT", RefusalDetail{IDs: []string{id}})
		}
		expected, err := readScore(record.score)
		if err != nil {
			return MemberRecord{}, err
		}
		got, err := readScore(actual)
		if err != nil {
			return MemberRecord{}, err
		}
		if expected != got {
			return MemberRecord{}, memRefusal("DRIFT", RefusalDetail{IDs: []string{id}})
		}
	} else if record.score != "" {
		return MemberRecord{}, memRefusal("DRIFT", RefusalDetail{IDs: []string{id}})
	}
	fieldsAll := fields == nil
	if fieldsAll {
		storedFields := len(record.fields) + 2
		if record.place != nil {
			storedFields++
		}
		if err := b.reserveRaw(32); err != nil {
			return MemberRecord{}, err
		}
		if err := b.chargeRaw(int64(len(strconv.Itoa(storedFields)))); err != nil {
			return MemberRecord{}, err
		}
		if err := b.reserveRaw(int64(storedFields * 256)); err != nil {
			return MemberRecord{}, err
		}
		fieldNamesBytes := len("epoch") + len("revision")
		if record.place != nil {
			fieldNamesBytes += len("place:") + len(tableName)
		}
		if err := b.chargeRaw(int64(fieldNamesBytes)); err != nil {
			return MemberRecord{}, err
		}
		fields = make([]string, 0, len(record.fields))
		for field := range record.fields {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			if err := b.chargeRaw(int64(len(field))); err != nil {
				return MemberRecord{}, err
			}
		}
	}
	if len(fields) > 128 || len(record.fields) > 128 {
		return MemberRecord{}, memRefusal("DRIFT", RefusalDetail{IDs: []string{id}})
	}
	if b.seenFields[cacheKey] == nil {
		b.seenFields[cacheKey] = make(map[string]bool)
	}
	missing := make([]string, 0, len(fields))
	for _, field := range fields {
		if !b.seenFields[cacheKey][field] {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		worst := int64(len(missing)) * MaxFieldValueBytes
		if worst <= readFetchedLimit-b.fetched {
			if err := b.reserveRaw(worst); err != nil {
				return MemberRecord{}, err
			}
		} else {
			for _, field := range missing {
				if err := b.reserveRaw(32); err != nil {
					return MemberRecord{}, err
				}
				if err := b.chargeRaw(int64(len(strconv.Itoa(len(record.fields[field]))))); err != nil {
					return MemberRecord{}, err
				}
			}
		}
		var actual int64
		for _, field := range missing {
			actual += int64(len(record.fields[field]))
		}
		if err := b.reserveRaw(actual); err != nil {
			return MemberRecord{}, err
		}
	}
	answer.Fields = make(map[string]FieldValue, len(fields))
	for _, field := range fields {
		if len(field) == 0 || len(field) > 256 {
			return MemberRecord{}, memRefusal("REQUEST", RefusalDetail{IDs: []string{id}})
		}
		if err := b.chargeField(1); err != nil {
			return MemberRecord{}, err
		}
		value, ok := record.fields[field]
		if ok {
			if len(value) > MaxFieldValueBytes {
				return MemberRecord{}, memRefusal("DRIFT", RefusalDetail{IDs: []string{id}})
			}
			if !b.seenFields[cacheKey][field] {
				if err := b.chargeRaw(int64(len(value))); err != nil {
					return MemberRecord{}, err
				}
			}
		}
		b.seenFields[cacheKey][field] = true
		answer.Fields[field] = FieldValue{Present: ok, Value: value}
	}
	return answer, nil
}

func readRows(space *memSpace, epoch *memEpoch, q ReadQuery, b *readBudget) (ReadAnswer, error) {
	_, table, err := readTable(space, epoch, q.Table)
	if err != nil {
		return ReadAnswer{}, err
	}
	// Redis reads ZCARD before making bounded ZRANGE pages. Preflight the
	// total worst-case payload and emitted row objects while the count is
	// cheap, so an oversized row set never becomes an in-memory answer.
	if err := b.reserveRaw(32); err != nil {
		return ReadAnswer{}, err
	}
	if err := b.chargeProbe(1); err != nil {
		return ReadAnswer{}, err
	}
	count := int64(len(table.rows))
	if err := b.chargeRaw(int64(len(strconv.FormatInt(count, 10)))); err != nil {
		return ReadAnswer{}, err
	}
	if count > (readFetchedLimit-b.fetched)/280 {
		return ReadAnswer{}, readBudgetError("fetched_bytes", readFetchedLimit, b.fetched+count*280)
	}
	if count > (readReplyLimit-b.encoded)/280 {
		return ReadAnswer{}, readBudgetError("encoded_reply", readReplyLimit, b.encoded+count*280)
	}
	answer := ReadAnswer{Kind: "rows", Rows: make([]RowRank, 0, len(table.rows))}
	for row, rank := range table.rows {
		if !memValidDecimal(rank) || memCompareDecimal(rank, "9007199254740991") > 0 {
			return ReadAnswer{}, memRefusal("DRIFT", RefusalDetail{Rows: []string{row}})
		}
		answer.Rows = append(answer.Rows, RowRank{Row: row, Rank: rank})
	}
	sort.Slice(answer.Rows, func(i, j int) bool {
		a, c := answer.Rows[i], answer.Rows[j]
		if a.Rank != c.Rank {
			return decimalLess(a.Rank, c.Rank)
		}
		return a.Row < c.Row
	})
	for offset := 0; offset < len(answer.Rows); {
		pageCount := len(answer.Rows) - offset
		if pageCount > 256 {
			pageCount = 256
		}
		if err := b.reserveRaw(int64(pageCount * 280)); err != nil {
			return ReadAnswer{}, err
		}
		if err := b.chargeProbe(1); err != nil {
			return ReadAnswer{}, err
		}
		end := offset + pageCount
		for _, item := range answer.Rows[offset:end] {
			if err := b.chargeRaw(int64(len(item.Row) + len(item.Rank))); err != nil {
				return ReadAnswer{}, err
			}
			if err := b.emitReadItem(item); err != nil {
				return ReadAnswer{}, err
			}
		}
		offset = end
	}
	return answer, nil
}

func validReadEpoch(d Decimal) bool {
	if d == "0" {
		return true
	}
	if len(d) == 0 || d[0] == '0' || len(d) > 20 {
		return false
	}
	for _, r := range d {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(d) < 20 || string(d) <= "18446744073709551615"
}

func decimalLess(a, b Decimal) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}
