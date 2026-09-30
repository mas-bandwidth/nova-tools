package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
	"github.com/redis/go-redis/v9"
)

// The L1 limits are the original 1.8 rows, qualified by section 10 of
// L1-CONTRACT. A row is only judged when its complete, exact workload ran.
// In particular, a smaller chunk is not evidence for a 2,000-member row.
type tsetLimit struct {
	id, operation string
	local         time.Duration
	large         time.Duration
	note          string
}

var tsetLimits = []tsetLimit{
	{"L1-1", "fill across 100 streams", 2 * time.Second, 20 * time.Second, "50/500 steps, then last <= 1.5x first"},
	{"L1-2", "create 2,000 in one cell", 25 * time.Millisecond, 25 * time.Millisecond, "original 40ms; composed gate <=25ms"},
	{"L1-3", "move 2,000 with revisions", 25 * time.Millisecond, 25 * time.Millisecond, "original 40ms; composed gate <=25ms"},
	{"L1-4", "move one", 2 * time.Millisecond, 2 * time.Millisecond, ""},
	{"L1-5", "remove 2,000", 25 * time.Millisecond, 25 * time.Millisecond, "original 40ms; composed gate <=25ms"},
	{"L1-6", "set three fields on 2,000", 25 * time.Millisecond, 25 * time.Millisecond, "original 40ms; composed gate <=25ms"},
	{"L1-7", "guard 4,000 and change one", 30 * time.Millisecond, 30 * time.Millisecond, ""},
	{"L1-8", "four-table deal-shaped step", 5 * time.Millisecond, 5 * time.Millisecond, ""},
	{"L1-9", "last guard fails; whole-store DUMP equal", 30 * time.Millisecond, 30 * time.Millisecond, "must refuse without a write"},
	{"L1-10", "2,000 creates with 1KiB words; 8KiB refusal", 60 * time.Millisecond, 60 * time.Millisecond, "split bounded entries; refusal <=2ms and no write"},
	{"L1-11", "single move at 1,000 work rows and 500x10 fleet", 2 * time.Millisecond, 2 * time.Millisecond, "singleton commands include fixed overhead; no four-command bound"},
	{"L1-12", "2,000 moves at 10 versus 1,000 rows", 0, 0, "time ratio <=1.2"},
	{"L1-13", "125 beats/s during 1,000 single moves", 2 * time.Millisecond, 2 * time.Millisecond, "beat wall p99 <=2ms and zero unrelated refusals"},
	{"L1-14", "same op replay and receipt size", time.Millisecond, time.Millisecond, "no writes; receipt <=32KiB"},
	{"L1-15", "first 16 records of a cell", 2 * time.Millisecond, 2 * time.Millisecond, ""},
	{"L1-16", "rcount over six cells", time.Millisecond, time.Millisecond, ""},
	{"L1-17", "2,000 and 10,000 records by ID; 10,001 refusal", 0, 0, "25ms / 100ms / BUDGET with no partial answer"},
	{"L1-18", "counts of all 5,600 cells", 10 * time.Millisecond, 10 * time.Millisecond, ""},
	{"L1-19", "add then delete 100 empty rows", 10 * time.Millisecond, 10 * time.Millisecond, ""},
	{"L1-20", "advance and restore 604 rows", 20 * time.Millisecond, 20 * time.Millisecond, ""},
	{"L1-21", "memory per card", 0, 0, "record+cell <=400B; composed lifecycle <=5,000B/primary unresolved"},
}

// No L1 connection is made by this file without an explicit address and an
// assertion that the target is the caller's disposable benchmark container.
// A fresh dev- namespace prevents accidental reuse of an earlier size run.
type tsetSizeConfig struct {
	redisAddr string
	proxyAddr string
	space     string
	owned     bool
	profile   string
}

func (c tsetSizeConfig) validate() error {
	if !c.owned {
		return errors.New("L1 size run wants --tset-owned-container for a disposable benchmark store")
	}
	if c.redisAddr == "" {
		return errors.New("L1 size run wants explicit --tset-redis host:port")
	}
	for _, addr := range []string{c.redisAddr, c.proxyAddr} {
		if addr == "" {
			continue
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil || host == "" || port == "" {
			return fmt.Errorf("L1 size run wants host:port address, got %q", addr)
		}
	}
	if c.proxyAddr != "" && c.proxyAddr == c.redisAddr {
		return errors.New("L1 delay proxy address must differ from direct Redis address")
	}
	if !strings.HasPrefix(c.space, "dev-") || !strings.HasSuffix(c.space, ":") || len(c.space) > 128 {
		return errors.New("L1 size run wants a fresh dev- space ending in ':'")
	}
	return nil
}

// tsetSizeSample is collected only from an actual call. Store is matched
// SLOWLOG FCALL time; Wall includes the client round trip. The two are never
// substituted for each other. Every counter comes from that call's reply.
type tsetSizeSample struct {
	store      time.Duration
	wall       time.Duration
	beatWall   time.Duration
	commands   int64
	probes     int64
	fields     int64
	records    int64
	fetched    int64
	argvBytes  int64
	logBytes   int64
	replyBytes int64
	members    int64
	guards     int64
	changed    int64
	counters   string
	costOK     bool
}

type tsetSizeResult struct {
	row, scenario   string
	cards           int
	limit           time.Duration
	refusalWall     time.Duration
	beatWalls       []time.Duration
	beatRate        float64
	beatErrors      int
	memoryPerCard   float64
	memorySamples   int
	samples         []tsetSizeSample
	complete        bool
	correct         bool
	requireCounters bool
	why             string
}

func (r tsetSizeResult) verdict() string {
	if r.row == "L1-21" {
		if !r.complete || r.memorySamples != 100 {
			return "NOT MEASURED"
		}
		if !r.correct {
			return "FAIL"
		}
		if r.memoryPerCard > 400 {
			return "OVER"
		}
		return "INSIDE"
	}
	if !r.correct && len(r.samples) > 0 {
		return "FAIL"
	}
	if !r.complete || len(r.samples) == 0 {
		return "NOT MEASURED"
	}
	for _, s := range r.samples {
		if s.store <= 0 || s.wall <= 0 {
			return "NOT MEASURED"
		}
		if r.requireCounters && !s.costOK {
			return "NOT MEASURED"
		}
	}
	if r.row == "L1-1" {
		want := r.cards / tsetSizeChunk
		if len(r.samples) != want {
			return "NOT MEASURED"
		}
		var total time.Duration
		for _, s := range r.samples {
			total += s.store
		}
		if total > r.limit || r.samples[len(r.samples)-1].store > r.samples[0].store*3/2 {
			return "OVER"
		}
		return "INSIDE"
	}
	if r.row == "L1-17" {
		if len(r.samples) != 2 {
			return "NOT MEASURED"
		}
		if r.samples[0].store > 25*time.Millisecond || r.samples[1].store > 100*time.Millisecond {
			return "OVER"
		}
		return "INSIDE"
	}
	if r.row == "L1-10" {
		if len(r.samples) != 1 || r.refusalWall <= 0 {
			return "NOT MEASURED"
		}
		if r.samples[0].store > 60*time.Millisecond || r.refusalWall > 2*time.Millisecond {
			return "OVER"
		}
		return "INSIDE"
	}
	if r.row == "L1-12" {
		if len(r.samples) != 2 {
			return "NOT MEASURED"
		}
		small, large := r.samples[0].store, r.samples[1].store
		if large > small*6/5 {
			return "OVER"
		}
		return "INSIDE"
	}
	if r.row == "L1-13" {
		if len(r.samples) != 1_000 || len(r.beatWalls) == 0 || r.beatRate < 125 {
			return "NOT MEASURED"
		}
		if r.beatErrors != 0 {
			return "FAIL"
		}
		if tsetQuantile(r.beatWalls, .99) > 2*time.Millisecond {
			return "OVER"
		}
		return "INSIDE"
	}
	if r.row == "L1-19" {
		if len(r.samples) != 2 {
			return "NOT MEASURED"
		}
		if r.samples[0].store+r.samples[1].store > r.limit {
			return "OVER"
		}
		return "INSIDE"
	}
	if r.limit == 0 {
		return "NOT MEASURED"
	}
	if r.limit > 0 {
		for _, s := range r.samples {
			if s.store > r.limit {
				return "OVER"
			}
		}
	}
	return "INSIDE"
}

func tsetQuantile(durations []time.Duration, p float64) time.Duration {
	if len(durations) == 0 || p < 0 || p > 1 || math.IsNaN(p) {
		return 0
	}
	copyOf := append([]time.Duration(nil), durations...)
	sort.Slice(copyOf, func(i, j int) bool { return copyOf[i] < copyOf[j] })
	index := int(math.Ceil(p*float64(len(copyOf)))) - 1
	if index < 0 {
		index = 0
	}
	return copyOf[index]
}

func tsetSummary(samples []tsetSizeSample, selectDuration func(tsetSizeSample) time.Duration) string {
	if len(samples) == 0 {
		return "-"
	}
	values := make([]time.Duration, len(samples))
	for i, sample := range samples {
		values[i] = selectDuration(sample)
		if values[i] <= 0 {
			return "unmatched"
		}
	}
	return fmt.Sprintf("p50=%s p99=%s max=%s", tsetQuantile(values, .50), tsetQuantile(values, .99), tsetQuantile(values, 1))
}

func tsetReport(w io.Writer, profile string, results []tsetSizeResult) error {
	if w == nil {
		return errors.New("nil report writer")
	}
	if profile == "" {
		profile = "composed"
	}
	header := fmt.Sprintf("L1 %s profile; durations and work triples are p50/p99/max. Store time is matched SLOWLOG FCALL, separate from client wall.\n\n| row | cards | route | store SLOWLOG | client wall | store commands | cell/field probes | fetched/argv/log/reply bytes | candidates/guards | limit | verdict | detail |\n|---|---:|---|---|---|---|---|---|---:|---|---|---|\n", profile)
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	for _, result := range results {
		store := tsetSummary(result.samples, func(s tsetSizeSample) time.Duration { return s.store })
		wall := tsetSummary(result.samples, func(s tsetSizeSample) time.Duration { return s.wall })
		limit := "see row"
		if result.limit > 0 {
			limit = result.limit.String()
		}
		cost := tsetCostSummary(result.samples)
		detail := strings.ReplaceAll(strings.ReplaceAll(result.why, "|", "/"), "\n", " ")
		if _, err := fmt.Fprintf(w, "| %s | %d | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			result.row, result.cards, result.scenario, store, wall, cost.commands, cost.probes, cost.bytes, cost.members, limit, result.verdict(), detail); err != nil {
			return err
		}
	}
	return nil
}

type tsetCostColumns struct{ commands, probes, bytes, members string }

func tsetMetricSummary(samples []tsetSizeSample, selectMetric func(tsetSizeSample) int64) string {
	values := make([]int64, len(samples))
	for i, sample := range samples {
		values[i] = selectMetric(sample)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	index := func(p float64) int { return max(0, int(math.Ceil(p*float64(len(values))))-1) }
	return fmt.Sprintf("%d/%d/%d", values[index(.50)], values[index(.99)], values[len(values)-1])
}

func tsetCostSummary(samples []tsetSizeSample) tsetCostColumns {
	missing := tsetCostColumns{"-", "-", "-", "-"}
	if len(samples) == 0 {
		return missing
	}
	var members, guards int64
	for _, sample := range samples {
		if !sample.costOK {
			return missing
		}
		members += sample.members
		guards += sample.guards
	}
	return tsetCostColumns{
		commands: tsetMetricSummary(samples, func(s tsetSizeSample) int64 { return s.commands }),
		probes: "cell " + tsetMetricSummary(samples, func(s tsetSizeSample) int64 { return s.probes }) +
			"; field " + tsetMetricSummary(samples, func(s tsetSizeSample) int64 { return s.fields }),
		bytes: "fetch " + tsetMetricSummary(samples, func(s tsetSizeSample) int64 { return s.fetched }) +
			"; argv " + tsetMetricSummary(samples, func(s tsetSizeSample) int64 { return s.argvBytes }) +
			"; log " + tsetMetricSummary(samples, func(s tsetSizeSample) int64 { return s.logBytes }) +
			"; reply " + tsetMetricSummary(samples, func(s tsetSizeSample) int64 { return s.replyBytes }),
		members: fmt.Sprintf("%d/%d", members, guards),
	}
}

func tsetDecodeCounters(raw []byte, sample *tsetSizeSample) error {
	sample.costOK = false
	var values map[string]int64
	if err := json.Unmarshal(raw, &values); err != nil {
		return fmt.Errorf("decode reply counters: %w", err)
	}
	for _, key := range []string{"store_commands", "planned_commands", "planned_argv_bytes", "fetched_bytes", "field", "cell", "record", "range_id", "log_id", "generated_log_bytes"} {
		value, ok := values[key]
		if !ok || value < 0 {
			return fmt.Errorf("reply counters missing nonnegative %s", key)
		}
	}
	sample.commands = values["store_commands"]
	sample.probes = values["cell"]
	sample.fields = values["field"]
	sample.records = values["record"]
	sample.fetched = values["fetched_bytes"]
	sample.argvBytes = values["planned_argv_bytes"]
	sample.logBytes = values["generated_log_bytes"]
	sample.costOK = true
	return nil
}

// tsetPendingRows makes every required size and route visible before any run.
// A proxy run has an independent verdict; its ~128ms injected round trip makes
// the local store-time thresholds inapplicable to the client-wall column.
func tsetPendingRows(includeProxy bool) []tsetSizeResult {
	routes := []string{"local"}
	if includeProxy {
		routes = append(routes, "~128ms-per-trip-proxy")
	}
	var rows []tsetSizeResult
	for _, cards := range []int{100_000, 1_000_000} {
		for _, route := range routes {
			for _, limit := range tsetLimits {
				bound := limit.local
				if cards == 1_000_000 {
					bound = limit.large
				}
				rows = append(rows, tsetSizeResult{row: limit.id, cards: cards, scenario: route, limit: bound, why: strings.TrimSpace(limit.note)})
			}
		}
	}
	return rows
}

const tsetSizeChunk = 2_000

// tsetFillEntries keeps candidate physical members within the admission cap.
// The two-row case at 100,000 cards still runs as one atomic Step.
func tsetFillEntries(cards, first, count int) ([]tset.Entry, error) {
	if cards != 100_000 && cards != 1_000_000 {
		return nil, fmt.Errorf("size run wants 100,000 or 1,000,000 cards")
	}
	if first < 0 || count < 1 || count > tsetSizeChunk || first+count > cards {
		return nil, fmt.Errorf("fill step is outside its bounded card range")
	}
	perRow := cards / 100
	var entries []tset.Entry
	for at := first; at < first+count; {
		row := at / perRow
		end := min(first+count, (row+1)*perRow)
		e := tset.Entry{Kind: "create", Table: "work", To: fmt.Sprintf("stream-%03d:waiting", row),
			Set: map[string]string{"brief": "size", "needs": ""}}
		for id := at; id < end; id++ {
			member := fmt.Sprintf("card-%07d", id)
			e.IDs = append(e.IDs, member)
			e.About = append(e.About, member)
			e.Scores = append(e.Scores, strconv.Itoa(id%perRow+1))
		}
		entries = append(entries, e)
		at = end
	}
	return entries, nil
}

// tsetSizeSampler reads a matched FCALL SLOWLOG record through the direct
// observer, while Store may use either direct Redis or Emma's delay proxy.
// The owned container must already have SLOWLOG threshold zero and enough
// capacity. The sampler never changes server configuration.
type tsetSizeSampler struct {
	store      tset.Store
	observer   *redis.Client
	clientName string
}

func (s tsetSizeSampler) step(ctx context.Context, input tset.Step) (tset.Reply, tsetSizeSample, error) {
	if s.store == nil || s.observer == nil || s.clientName == "" {
		return tset.Reply{}, tsetSizeSample{}, errors.New("size sampler needs store, direct observer and unique client name")
	}
	before, err := s.observer.SlowLogGet(ctx, 1).Result()
	if err != nil {
		return tset.Reply{}, tsetSizeSample{}, err
	}
	var lastID int64 = -1
	if len(before) != 0 {
		lastID = before[0].ID
	}
	start := time.Now()
	reply, callErr := s.store.Step(ctx, input)
	wall := time.Since(start)
	logs, readErr := s.observer.SlowLogGet(ctx, 64).Result()
	sample := tsetSizeSample{wall: wall, counters: string(reply.Counters), members: int64(tsetCandidates(input)), guards: int64(reply.Guarded), changed: int64(reply.Changed)}
	if encoded, marshalErr := json.Marshal(reply); marshalErr == nil {
		sample.replyBytes = int64(len(encoded))
	}
	if callErr == nil && !reply.Replay {
		if err := tsetDecodeCounters(reply.Counters, &sample); err != nil {
			return reply, sample, err
		}
	}
	if readErr != nil {
		return reply, sample, fmt.Errorf("read matched SLOWLOG: %w", readErr)
	}
	for _, record := range logs {
		if record.ID <= lastID || record.ClientName != s.clientName || len(record.Args) < 2 ||
			!strings.EqualFold(record.Args[0], "FCALL") || record.Args[1] != "ns_tset_step" {
			continue
		}
		if sample.store != 0 {
			return reply, sample, errors.New("multiple matching FCALLs; SLOWLOG measurement ambiguous")
		}
		sample.store = record.Duration
	}
	if sample.store <= 0 {
		return reply, sample, errors.New("no matched FCALL SLOWLOG record; row remains unmeasured")
	}
	return reply, sample, callErr
}

func (s tsetSizeSampler) read(ctx context.Context, input tset.ReadPlan) (tset.ReadReply, tsetSizeSample, error) {
	if s.store == nil || s.observer == nil || s.clientName == "" {
		return tset.ReadReply{}, tsetSizeSample{}, errors.New("size sampler needs store, direct observer and unique client name")
	}
	before, err := s.observer.SlowLogGet(ctx, 1).Result()
	if err != nil {
		return tset.ReadReply{}, tsetSizeSample{}, err
	}
	var lastID int64 = -1
	if len(before) != 0 {
		lastID = before[0].ID
	}
	start := time.Now()
	reply, callErr := s.store.Read(ctx, input)
	wall := time.Since(start)
	logs, readErr := s.observer.SlowLogGet(ctx, 64).Result()
	sample := tsetSizeSample{wall: wall, counters: string(reply.Counters)}
	if encoded, marshalErr := json.Marshal(reply); marshalErr == nil {
		sample.replyBytes = int64(len(encoded))
	}
	if callErr == nil {
		if err := tsetDecodeCounters(reply.Counters, &sample); err != nil {
			return reply, sample, err
		}
	}
	if readErr != nil {
		return reply, sample, fmt.Errorf("read matched SLOWLOG: %w", readErr)
	}
	for _, record := range logs {
		if record.ID <= lastID || record.ClientName != s.clientName || len(record.Args) < 2 ||
			!strings.EqualFold(record.Args[0], "FCALL_RO") || record.Args[1] != "ns_tset_read" {
			continue
		}
		if sample.store != 0 {
			return reply, sample, errors.New("multiple matching FCALL_ROs; SLOWLOG measurement ambiguous")
		}
		sample.store = record.Duration
	}
	if sample.store <= 0 {
		return reply, sample, errors.New("no matched FCALL_RO SLOWLOG record; row remains unmeasured")
	}
	return reply, sample, callErr
}

func tsetCandidates(step tset.Step) int {
	count := 0
	for _, entry := range step.Entries {
		switch entry.Kind {
		case "create", "move", "remove":
			count += len(entry.IDs)
		}
	}
	return count
}

func tsetCardEntries(cards int, kind string, first, count int, fromCol, toCol string, fields map[string]string, rev tset.Decimal) ([]tset.Entry, error) {
	if cards != 100_000 && cards != 1_000_000 || first < 0 || count < 1 || first+count > cards {
		return nil, errors.New("card range is outside a supported size")
	}
	if kind != "move" && kind != "guard" && kind != "remove" {
		return nil, errors.New("card entry kind is not a member operation")
	}
	perRow := cards / 100
	var entries []tset.Entry
	for at := first; at < first+count; {
		row := at / perRow
		end := min(first+count, (row+1)*perRow, at+tsetSizeChunk)
		e := tset.Entry{Kind: kind, Table: "work", From: fmt.Sprintf("stream-%03d:%s", row, fromCol), Set: fields}
		if toCol != "" {
			e.To = fmt.Sprintf("stream-%03d:%s", row, toCol)
		}
		for id := at; id < end; id++ {
			member := fmt.Sprintf("card-%07d", id)
			e.IDs = append(e.IDs, member)
			e.Revs = append(e.Revs, rev)
			if kind != "guard" {
				e.About = append(e.About, member)
			}
		}
		entries = append(entries, e)
		at = end
	}
	return entries, nil
}

func tsetOneStep(ctx context.Context, sampler tsetSizeSampler, space string, cards int, route, row string,
	entries []tset.Entry, changed int) tsetSizeResult {
	result := tsetSizeResult{row: row, cards: cards, scenario: route, correct: true, requireCounters: true}
	for _, spec := range tsetLimits {
		if spec.id == row {
			result.limit = spec.local
			if cards == 1_000_000 {
				result.limit = spec.large
			}
			break
		}
	}
	if tsetCandidates(tset.Step{Entries: entries}) > tsetSizeChunk {
		result.why = "more than 2,000 candidate physical members"
		return result
	}
	reply, sample, err := sampler.step(ctx, tset.Step{Epoch: "0", Space: space, Entries: entries})
	result.samples = []tsetSizeSample{sample}
	if err != nil {
		result.why = err.Error()
		return result
	}
	result.complete = true
	if reply.Status != "ok" || reply.Changed != changed {
		result.correct = false
		result.why = fmt.Sprintf("status=%q changed=%d, want %d", reply.Status, reply.Changed, changed)
	}
	return result
}

func tsetSimpleCases(ctx context.Context, sampler tsetSizeSampler, space string, cards int, route string) []tsetSizeResult {
	results := make([]tsetSizeResult, 0, 6)
	extra := make([]string, tsetSizeChunk)
	scores := make([]string, tsetSizeChunk)
	revs := make([]tset.Decimal, tsetSizeChunk)
	for i := range extra {
		extra[i] = fmt.Sprintf("extra-%04d", i)
		scores[i] = strconv.Itoa(i + 1)
		revs[i] = "1"
	}
	create := tset.Entry{Kind: "create", Table: "work", To: "stream-000:waiting", IDs: extra, Scores: scores, About: extra}
	r2 := tsetOneStep(ctx, sampler, space, cards, route, "L1-2", []tset.Entry{create}, tsetSizeChunk)
	results = append(results, r2)
	if r2.verdict() == "FAIL" || r2.verdict() == "NOT MEASURED" {
		return results
	}
	move := tset.Entry{Kind: "move", Table: "work", From: "stream-000:waiting", To: "stream-000:ready", IDs: extra, Revs: revs, About: extra}
	r3 := tsetOneStep(ctx, sampler, space, cards, route, "L1-3", []tset.Entry{move}, tsetSizeChunk)
	results = append(results, r3)
	if r3.verdict() == "FAIL" || r3.verdict() == "NOT MEASURED" {
		return results
	}
	one := tset.Entry{Kind: "move", Table: "work", From: "stream-000:ready", To: "stream-000:working",
		IDs: extra[:1], Revs: []tset.Decimal{"2"}, About: extra[:1]}
	r4 := tsetOneStep(ctx, sampler, space, cards, route, "L1-4", []tset.Entry{one}, 1)
	results = append(results, r4)
	if r4.verdict() == "FAIL" || r4.verdict() == "NOT MEASURED" {
		return results
	}
	revs2 := make([]tset.Decimal, len(extra)-1)
	for i := range revs2 {
		revs2[i] = "2"
	}
	remove := []tset.Entry{
		{Kind: "remove", Table: "work", From: "stream-000:working", IDs: extra[:1], Revs: []tset.Decimal{"3"}, About: extra[:1]},
		{Kind: "remove", Table: "work", From: "stream-000:ready", IDs: extra[1:], Revs: revs2, About: extra[1:]},
	}
	r5 := tsetOneStep(ctx, sampler, space, cards, route, "L1-5", remove, tsetSizeChunk)
	results = append(results, r5)
	if r5.verdict() == "FAIL" || r5.verdict() == "NOT MEASURED" {
		return results
	}
	fields, err := tsetCardEntries(cards, "move", 0, tsetSizeChunk, "waiting", "", map[string]string{"brief": "updated", "needs": "review", "tag": "sized"}, "1")
	if err != nil {
		return results
	}
	r6 := tsetOneStep(ctx, sampler, space, cards, route, "L1-6", fields, tsetSizeChunk)
	results = append(results, r6)
	if r6.verdict() == "FAIL" || r6.verdict() == "NOT MEASURED" {
		return results
	}
	guards, err := tsetCardEntries(cards, "guard", 2_000, 4_000, "waiting", "", nil, "1")
	if err != nil {
		return results
	}
	change, err := tsetCardEntries(cards, "move", 6_000, 1, "waiting", "ready", nil, "1")
	if err != nil {
		return results
	}
	r7 := tsetOneStep(ctx, sampler, space, cards, route, "L1-7", append(guards, change...), 1)
	results = append(results, r7)
	return results
}

func tsetReadCases(ctx context.Context, sampler tsetSizeSampler, space string, cards int, route string) []tsetSizeResult {
	rows := make([]tsetSizeResult, 0, 4)
	read := func(row string, query tset.ReadQuery, expectedRecords, expectedCounts, expectedSum int) tsetSizeResult {
		result := tsetSizeResult{row: row, cards: cards, scenario: route, correct: true, requireCounters: true}
		for _, spec := range tsetLimits {
			if spec.id == row {
				result.limit = spec.local
				break
			}
		}
		reply, sample, err := sampler.read(ctx, tset.ReadPlan{Epoch: "0", Space: space, Queries: []tset.ReadQuery{query}})
		result.samples = []tsetSizeSample{sample}
		if err != nil {
			result.why = err.Error()
			return result
		}
		result.complete = true
		if reply.Status != "read" || !reply.Complete || len(reply.Answers) != 1 ||
			(expectedRecords >= 0 && len(reply.Answers[0].Records) != expectedRecords) ||
			(expectedCounts >= 0 && len(reply.Answers[0].Counts) != expectedCounts) ||
			(expectedSum >= 0 && reply.Answers[0].Sum != uint64(expectedSum)) {
			result.correct = false
			result.why = fmt.Sprintf("read shape status=%q complete=%t answers=%d", reply.Status, reply.Complete, len(reply.Answers))
		}
		if result.correct && row == "L1-15" {
			for i, record := range reply.Answers[0].Records {
				if record.ID != fmt.Sprintf("card-%07d", i) || !record.Exists || record.Place == nil ||
					record.Place.Row != "stream-000" || record.Place.Col != "waiting" {
					result.correct = false
					result.why = fmt.Sprintf("first-16 record %d not in expected order/place", i)
					break
				}
			}
		}
		return result
	}
	rows = append(rows, read("L1-15", tset.ReadQuery{Kind: "range", Table: "work", Cell: "stream-000:waiting",
		Min: "-inf", Max: "+inf", Limit: 16, Records: true}, 16, -1, -1))
	cells := make([]string, 0, 6)
	for _, col := range []string{"waiting", "ready", "working", "review", "merging", "done"} {
		cells = append(cells, "stream-000:"+col)
	}
	rows = append(rows, read("L1-16", tset.ReadQuery{Kind: "rcount", Table: "work", Cells: cells,
		Min: "-inf", Max: "+inf"}, -1, 6, cards/100))
	r17 := tsetSizeResult{row: "L1-17", cards: cards, scenario: route, correct: true, requireCounters: true}
	for _, count := range []int{2_000, 10_000} {
		ids := make([]string, count)
		for i := range ids {
			ids[i] = fmt.Sprintf("card-%07d", i)
		}
		reply, sample, err := sampler.read(ctx, tset.ReadPlan{Epoch: "0", Space: space,
			Queries: []tset.ReadQuery{{Kind: "ids", Table: "work", IDs: ids}}})
		r17.samples = append(r17.samples, sample)
		if err != nil {
			r17.why = err.Error()
			rows = append(rows, r17)
			return rows
		}
		validRecords := reply.Status == "read" && reply.Complete && len(reply.Answers) == 1 && len(reply.Answers[0].Records) == count
		if validRecords {
			for i, record := range reply.Answers[0].Records {
				if record.ID != ids[i] || !record.Exists {
					validRecords = false
					break
				}
			}
		}
		if !validRecords {
			r17.correct = false
			r17.why = fmt.Sprintf("ids %d: incomplete or wrong cardinality", count)
			rows = append(rows, r17)
			return rows
		}
	}
	tooMany := make([]string, 10_001)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("card-%07d", i)
	}
	_, err := sampler.store.Read(ctx, tset.ReadPlan{Epoch: "0", Space: space,
		Queries: []tset.ReadQuery{{Kind: "ids", Table: "work", IDs: tooMany}}})
	var refusal *tset.Refusal
	if !errors.As(err, &refusal) || refusal.Code != "LIMIT" {
		r17.correct = false
		r17.why = fmt.Sprintf("10,001 IDs: want LIMIT, got %v", err)
	}
	r17.complete = true
	rows = append(rows, r17)
	workCells := make([]string, 0, 600)
	for row := 0; row < 100; row++ {
		for _, col := range []string{"waiting", "ready", "working", "review", "merging", "done"} {
			workCells = append(workCells, fmt.Sprintf("stream-%03d:%s", row, col))
		}
	}
	fleetCells := make([]string, 0, 5_000)
	for row := 0; row < 500; row++ {
		for col := 0; col < 10; col++ {
			fleetCells = append(fleetCells, fmt.Sprintf("fleet-%03d:col%d", row, col))
		}
	}
	r18 := tsetSizeResult{row: "L1-18", cards: cards, scenario: route, limit: 10 * time.Millisecond, correct: true, requireCounters: true}
	reply, sample, err := sampler.read(ctx, tset.ReadPlan{Epoch: "0", Space: space, Queries: []tset.ReadQuery{
		{Kind: "count", Table: "work", Cells: workCells}, {Kind: "count", Table: "fleet", Cells: fleetCells},
	}})
	r18.samples = []tsetSizeSample{sample}
	if err != nil {
		r18.why = err.Error()
	} else {
		r18.complete = true
		if reply.Status != "read" || !reply.Complete || len(reply.Answers) != 2 ||
			len(reply.Answers[0].Counts) != 600 || len(reply.Answers[1].Counts) != 5_000 ||
			reply.Answers[0].Sum != uint64(cards+2_000) || reply.Answers[1].Sum != 516 {
			r18.correct = false
			r18.why = "5,600-cell count did not return every count"
		}
	}
	rows = append(rows, r18)
	return rows
}

func tsetTopologyCases(ctx context.Context, sampler tsetSizeSampler, space string, cards int, route string) []tsetSizeResult {
	added := make([]string, 100)
	for i := range added {
		added[i] = fmt.Sprintf("empty-%03d", i)
	}
	add := tsetOneStep(ctx, sampler, space, cards, route, "L1-19", []tset.Entry{{Kind: "rows", Table: "work", Add: added}}, 0)
	del := tsetOneStep(ctx, sampler, space, cards, route, "L1-19", []tset.Entry{{Kind: "rows", Table: "work", Del: added}}, 0)
	add.samples = append(add.samples, del.samples...)
	add.complete = add.complete && del.complete
	add.correct = add.correct && del.correct
	if del.why != "" {
		if add.why != "" {
			add.why += "; " + del.why
		} else {
			add.why = del.why
		}
	}
	return []tsetSizeResult{add}
}

func tsetAdvanceCase(ctx context.Context, sampler tsetSizeSampler, space string, cards int, route string) tsetSizeResult {
	result := tsetSizeResult{row: "L1-20", cards: cards, scenario: route, limit: 20 * time.Millisecond, correct: true, requireCounters: true}
	work := make([]string, 100)
	fleet := make([]string, 500)
	readers := make([]string, 4)
	for i := range work {
		work[i] = fmt.Sprintf("stream-%03d", i)
	}
	for i := range fleet {
		fleet[i] = fmt.Sprintf("fleet-%03d", i)
	}
	for i := range readers {
		readers[i] = fmt.Sprintf("reader-%d", i)
	}
	op, intent := "size-advance-0-to-1", "size-run-advance-v1"
	reply, sample, err := sampler.step(ctx, tset.Step{Epoch: "0", Space: space, Op: &op, Intent: &intent,
		Entries: []tset.Entry{
			{Kind: "advance", AdvanceFrom: "0"},
			{Kind: "rows", Table: "work", Add: work},
			{Kind: "rows", Table: "fleet", Add: fleet},
			{Kind: "rows", Table: "readers", Add: readers},
		}})
	result.samples = []tsetSizeSample{sample}
	if err != nil {
		result.why = err.Error()
		return result
	}
	result.complete = true
	if reply.Status != "ok" || reply.EpochAfter != "1" || reply.Changed != 0 {
		result.correct = false
		result.why = fmt.Sprintf("advance status=%q epoch_after=%q changed=%d", reply.Status, reply.EpochAfter, reply.Changed)
	}
	return result
}

func tsetReplayCase(ctx context.Context, sampler tsetSizeSampler, space string, cards int, route string) tsetSizeResult {
	result := tsetSizeResult{row: "L1-14", cards: cards, scenario: route, limit: time.Millisecond, correct: true}
	entries, err := tsetCardEntries(cards, "move", 7_000, 1, "waiting", "ready", nil, "1")
	if err != nil {
		result.why = err.Error()
		return result
	}
	op, intent := "size-replay-card-7000", "size-replay-v1-card-7000-to-ready"
	input := tset.Step{Epoch: "0", Space: space, Op: &op, Intent: &intent, Entries: entries}
	first, _, err := sampler.step(ctx, input)
	if err != nil {
		result.why = err.Error()
		return result
	}
	if first.Status != "ok" || first.Changed != 1 || first.Replay {
		result.correct = false
		result.complete = true
		result.why = "first receipt-bearing step did not change one card"
		return result
	}
	doneKey, logKey := space+"sprint:done@0", space+"sprint:log@0"
	beforeDone, err := sampler.observer.Dump(ctx, doneKey).Result()
	if err != nil {
		result.why = fmt.Sprintf("snapshot done key: %v", err)
		return result
	}
	beforeLines, err := sampler.observer.XLen(ctx, logKey).Result()
	if err != nil {
		result.why = fmt.Sprintf("read log length: %v", err)
		return result
	}
	replayed, sample, err := sampler.step(ctx, input)
	result.samples = []tsetSizeSample{sample}
	if err != nil {
		result.why = err.Error()
		return result
	}
	usage, err := sampler.observer.MemoryUsage(ctx, doneKey).Result()
	if err != nil {
		result.why = fmt.Sprintf("measure done key: %v", err)
		return result
	}
	afterDone, err := sampler.observer.Dump(ctx, doneKey).Result()
	if err != nil {
		result.why = fmt.Sprintf("resnapshot done key: %v", err)
		return result
	}
	afterLines, err := sampler.observer.XLen(ctx, logKey).Result()
	if err != nil {
		result.why = fmt.Sprintf("reread log length: %v", err)
		return result
	}
	result.complete = true
	if !replayed.Replay || replayed.Status != "ok" || replayed.FirstSeq != first.FirstSeq || replayed.LastSeq != first.LastSeq ||
		replayed.Changed != first.Changed || usage > 32*1024 || !bytes.Equal([]byte(beforeDone), []byte(afterDone)) || beforeLines != afterLines {
		result.correct = false
	}
	result.why = fmt.Sprintf("replay=%t seq=%q..%q done_key_bytes=%d log_lines=%d/%d",
		replayed.Replay, replayed.FirstSeq, replayed.LastSeq, usage, beforeLines, afterLines)
	return result
}

func tsetDealCase(ctx context.Context, sampler tsetSizeSampler, space string, cards int, route string) tsetSizeResult {
	entries := make([]tset.Entry, 0, 29)
	for i := 0; i < 16; i++ {
		member := fmt.Sprintf("deal-fleet-%02d", i)
		entries = append(entries, tset.Entry{Kind: "create", Table: "fleet", To: fmt.Sprintf("fleet-%03d:col1", i),
			IDs: []string{member}, Scores: []string{"1"}, About: []string{member}})
	}
	work, err := tsetCardEntries(cards, "move", 8_000, 16, "waiting", "ready", nil, "1")
	if err != nil {
		return tsetSizeResult{row: "L1-8", cards: cards, scenario: route, why: err.Error()}
	}
	entries = append(entries, work...)
	for i := 0; i < 8; i++ {
		entries = append(entries, tset.Entry{Kind: "count", Table: "work", Cells: []string{fmt.Sprintf("stream-%03d:ready", i)}, CountMax: []uint64{10_000}})
	}
	entries = append(entries, tset.Entry{Kind: "move", Table: "merge", From: "ctl:queued", IDs: []string{"merge-control"},
		Revs: []tset.Decimal{"1"}, Set: map[string]string{"state": "deal"}, About: []string{"merge-control"}})
	for i := 0; i < 2; i++ {
		entries = append(entries, tset.Entry{Kind: "guard", Table: "readers", From: fmt.Sprintf("reader-%d:asked", i),
			IDs: []string{fmt.Sprintf("reader-member-%d", i)}, Revs: []tset.Decimal{"1"}})
	}
	return tsetOneStep(ctx, sampler, space, cards, route, "L1-8", entries, 33)
}

// tsetNamespaceDigest compares every owned key's Redis DUMP bytes. SCAN may
// repeat keys, so the key set is deduplicated and sorted before hashing. The
// namespace is quiescent during L1-9; this comparison runs outside its timed
// FCALL and is deliberately not hidden in the 30ms refusal limit.
func tsetNamespaceDigest(ctx context.Context, client *redis.Client, space string) ([32]byte, error) {
	var zero [32]byte
	keys := make(map[string]bool)
	var cursor uint64
	for {
		page, next, err := client.Scan(ctx, cursor, space+"*", 1_000).Result()
		if err != nil {
			return zero, err
		}
		for _, key := range page {
			keys[key] = true
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	h := sha256.New()
	var length [8]byte
	for first := 0; first < len(ordered); first += 1_000 {
		end := min(first+1_000, len(ordered))
		pipe := client.Pipeline()
		commands := make([]*redis.StringCmd, 0, end-first)
		for _, key := range ordered[first:end] {
			commands = append(commands, pipe.Dump(ctx, key))
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return zero, err
		}
		for i, key := range ordered[first:end] {
			dump, err := commands[i].Result()
			if err != nil {
				return zero, fmt.Errorf("DUMP %q: %w", key, err)
			}
			binary.BigEndian.PutUint64(length[:], uint64(len(key)))
			h.Write(length[:])
			h.Write([]byte(key))
			binary.BigEndian.PutUint64(length[:], uint64(len(dump)))
			h.Write(length[:])
			h.Write([]byte(dump))
		}
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest, nil
}

func tsetRefusalCase(ctx context.Context, sampler tsetSizeSampler, space string, cards int, route string) tsetSizeResult {
	result := tsetSizeResult{row: "L1-9", cards: cards, scenario: route, limit: 30 * time.Millisecond, correct: true}
	before, err := tsetNamespaceDigest(ctx, sampler.observer, space)
	if err != nil {
		result.why = fmt.Sprintf("before DUMP digest: %v", err)
		return result
	}
	ids, scores := make([]string, 1_997), make([]string, 1_997)
	for i := range ids {
		ids[i] = fmt.Sprintf("refused-%04d", i)
		scores[i] = strconv.Itoa(i + 1)
	}
	entries := []tset.Entry{
		{Kind: "create", Table: "work", To: "stream-000:waiting", IDs: ids, Scores: scores, About: ids},
		{Kind: "guard", Table: "fleet", From: "fleet-000:col0", IDs: []string{"fleet-member-000"}, Revs: []tset.Decimal{"1"}},
		{Kind: "guard", Table: "merge", From: "ctl:queued", IDs: []string{"merge-control"}, Revs: []tset.Decimal{"2"}},
		{Kind: "guard", Table: "readers", From: "reader-0:asked", IDs: []string{"reader-member-0"}, Revs: []tset.Decimal{"999"}},
	}
	_, sample, callErr := sampler.step(ctx, tset.Step{Epoch: "0", Space: space, Entries: entries})
	result.samples = []tsetSizeSample{sample}
	after, digestErr := tsetNamespaceDigest(ctx, sampler.observer, space)
	if digestErr != nil {
		result.why = fmt.Sprintf("after DUMP digest: %v", digestErr)
		return result
	}
	if sample.store <= 0 {
		result.why = fmt.Sprintf("refusal SLOWLOG unavailable: %v", callErr)
		return result
	}
	var refusal *tset.Refusal
	result.complete = true
	if !errors.As(callErr, &refusal) || refusal.Code != "REVISION" || before != after {
		result.correct = false
		result.why = fmt.Sprintf("want REVISION and equal whole-namespace DUMPs; got err=%v equal=%t", callErr, before == after)
	}
	return result
}

func tsetWordEntries(width int, prefix string) []tset.Entry {
	entries := make([]tset.Entry, 0, 4)
	for first := 0; first < tsetSizeChunk; first += 500 {
		e := tset.Entry{Kind: "create", Table: "work", To: "stream-002:waiting"}
		for id := first; id < first+500; id++ {
			member := fmt.Sprintf("%s-%04d", prefix, id)
			value := fmt.Sprintf("%08d", id) + strings.Repeat("w", width-8)
			e.IDs = append(e.IDs, member)
			e.Scores = append(e.Scores, strconv.Itoa(id+1))
			e.About = append(e.About, member)
			e.Each = append(e.Each, map[string]string{"brief": value})
		}
		entries = append(entries, e)
	}
	return entries
}

func tsetByteCase(ctx context.Context, sampler tsetSizeSampler, space string, cards int, route string) tsetSizeResult {
	result := tsetOneStep(ctx, sampler, space, cards, route, "L1-10", tsetWordEntries(1_024, "word"), tsetSizeChunk)
	if !result.complete || !result.correct {
		return result
	}
	before, err := tsetNamespaceDigest(ctx, sampler.observer, space)
	if err != nil {
		result.complete = false
		result.why = fmt.Sprintf("before 8KiB refusal DUMP digest: %v", err)
		return result
	}
	start := time.Now()
	_, callErr := sampler.store.Step(ctx, tset.Step{Epoch: "0", Space: space, Entries: tsetWordEntries(8_192, "fat")})
	result.refusalWall = time.Since(start)
	after, err := tsetNamespaceDigest(ctx, sampler.observer, space)
	if err != nil {
		result.complete = false
		result.why = fmt.Sprintf("after 8KiB refusal DUMP digest: %v", err)
		return result
	}
	var refusal *tset.Refusal
	if !errors.As(callErr, &refusal) || refusal.Code != "LIMIT" || before != after {
		result.correct = false
		result.why = fmt.Sprintf("8KiB branch wants LIMIT and equal whole-namespace DUMPs; got err=%v equal=%t", callErr, before == after)
		return result
	}
	result.why = fmt.Sprintf("1KiB split into four 500-member entries; 8KiB refusal wall=%s; whole-namespace DUMP equal", result.refusalWall)
	return result
}

func tsetWideRowsCase(ctx context.Context, sampler tsetSizeSampler, space string, cards int, route string) tsetSizeResult {
	result := tsetSizeResult{row: "L1-11", cards: cards, scenario: route, limit: 2 * time.Millisecond, correct: true, requireCounters: true}
	for first := 100; first < 1_000; first += 100 {
		rows := make([]string, 100)
		for i := range rows {
			rows[i] = fmt.Sprintf("empty-wide-%04d", first+i)
		}
		reply, err := sampler.store.Step(ctx, tset.Step{Epoch: "0", Space: space,
			Entries: []tset.Entry{{Kind: "rows", Table: "work", Add: rows}}})
		if err != nil || reply.Status != "ok" {
			result.why = fmt.Sprintf("prepare 1,000 work rows: status=%q err=%v", reply.Status, err)
			return result
		}
	}
	work, err := tsetCardEntries(cards, "move", 9_000, 1, "waiting", "ready", nil, "1")
	if err != nil {
		result.why = err.Error()
		return result
	}
	first := tsetOneStep(ctx, sampler, space, cards, route, "L1-11", work, 1)
	result.samples = append(result.samples, first.samples...)
	if !first.complete || !first.correct {
		result.why = first.why
		return result
	}
	fleet := []tset.Entry{{Kind: "move", Table: "fleet", From: "fleet-000:col0", To: "fleet-000:col2",
		IDs: []string{"fleet-member-000"}, Revs: []tset.Decimal{"1"}, About: []string{"fleet-member-000"}}}
	second := tsetOneStep(ctx, sampler, space, cards, route, "L1-11", fleet, 1)
	result.samples = append(result.samples, second.samples...)
	result.complete = second.complete
	result.correct = first.correct && second.correct
	if second.why != "" {
		result.why = second.why
	} else {
		result.why = "work=1,000 rows; fleet=500 rows x 10 columns; singleton commands include fixed cost"
	}
	return result
}

func tsetRowScaleCase(ctx context.Context, sampler tsetSizeSampler, direct *redis.Client,
	space string, cards int, route string) tsetSizeResult {
	result := tsetSizeResult{row: "L1-12", cards: cards, scenario: route, correct: true, requireCounters: true}
	fixtureSpace := space + "row-scale:"
	if err := tsetBootstrap(ctx, direct, sampler.store, fixtureSpace, 10); err != nil {
		result.why = fmt.Sprintf("10-row fixture: %v", err)
		return result
	}
	ids, scores := make([]string, tsetSizeChunk), make([]string, tsetSizeChunk)
	for i := range ids {
		ids[i] = fmt.Sprintf("scale-%04d", i)
		scores[i] = strconv.Itoa(i + 1)
	}
	create := tset.Step{Epoch: "0", Space: fixtureSpace, Entries: []tset.Entry{{Kind: "create", Table: "work",
		To: "stream-000:waiting", IDs: ids, Scores: scores, About: ids}}}
	created, err := sampler.store.Step(ctx, create)
	if err != nil || created.Status != "ok" || created.Changed != tsetSizeChunk {
		result.why = fmt.Sprintf("seed 2,000 scale members: status=%q changed=%d err=%v", created.Status, created.Changed, err)
		return result
	}
	revs := make([]tset.Decimal, tsetSizeChunk)
	for i := range revs {
		revs[i] = "1"
	}
	move := tset.Entry{Kind: "move", Table: "work", From: "stream-000:waiting", To: "stream-000:ready", IDs: ids, Revs: revs, About: ids}
	first, sample, err := sampler.step(ctx, tset.Step{Epoch: "0", Space: fixtureSpace, Entries: []tset.Entry{move}})
	result.samples = append(result.samples, sample)
	if err != nil || first.Status != "ok" || first.Changed != tsetSizeChunk {
		result.why = fmt.Sprintf("10-row move: status=%q changed=%d err=%v", first.Status, first.Changed, err)
		return result
	}
	for i := range revs {
		revs[i] = "2"
	}
	reset := tset.Entry{Kind: "move", Table: "work", From: "stream-000:ready", To: "stream-000:waiting", IDs: ids, Revs: revs, About: ids}
	resetReply, err := sampler.store.Step(ctx, tset.Step{Epoch: "0", Space: fixtureSpace, Entries: []tset.Entry{reset}})
	if err != nil || resetReply.Status != "ok" || resetReply.Changed != tsetSizeChunk {
		result.why = fmt.Sprintf("reset scale members: status=%q changed=%d err=%v", resetReply.Status, resetReply.Changed, err)
		return result
	}
	for firstRow := 10; firstRow < 1_000; firstRow += 100 {
		rows := make([]string, 0, min(100, 1_000-firstRow))
		for row := firstRow; row < min(firstRow+100, 1_000); row++ {
			rows = append(rows, fmt.Sprintf("scale-empty-%04d", row))
		}
		rowReply, err := sampler.store.Step(ctx, tset.Step{Epoch: "0", Space: fixtureSpace,
			Entries: []tset.Entry{{Kind: "rows", Table: "work", Add: rows}}})
		if err != nil || rowReply.Status != "ok" {
			result.why = fmt.Sprintf("grow scale rows: status=%q err=%v", rowReply.Status, err)
			return result
		}
	}
	for i := range revs {
		revs[i] = "3"
	}
	move.Revs = revs
	second, sample, err := sampler.step(ctx, tset.Step{Epoch: "0", Space: fixtureSpace, Entries: []tset.Entry{move}})
	result.samples = append(result.samples, sample)
	if err != nil || second.Status != "ok" || second.Changed != tsetSizeChunk {
		result.why = fmt.Sprintf("1,000-row move: status=%q changed=%d err=%v", second.Status, second.Changed, err)
		return result
	}
	result.complete = true
	result.why = fmt.Sprintf("same 2,000 IDs: 10-row=%s; 1,000-row=%s", result.samples[0].store, result.samples[1].store)
	return result
}

func tsetBeatCase(ctx context.Context, sampler tsetSizeSampler, space string, cards int, route string) tsetSizeResult {
	result := tsetSizeResult{row: "L1-13", cards: cards, scenario: route, limit: 2 * time.Millisecond,
		correct: true, requireCounters: true}
	beatCtx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var walls []time.Duration
	var beatErrors int
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Schedule slightly above the required 125/s so timer quantization
		// cannot make an otherwise idle run miss the target by construction.
		ticker := time.NewTicker(7500 * time.Microsecond)
		defer ticker.Stop()
		member := 0
		for {
			select {
			case <-beatCtx.Done():
				return
			case <-ticker.C:
				key := fmt.Sprintf("%ssize:beat:%03d", space, member)
				start := time.Now()
				err := sampler.observer.HSet(beatCtx, key, "at", strconv.FormatInt(start.UnixNano(), 10)).Err()
				if err != nil {
					beatErrors++
				} else {
					walls = append(walls, time.Since(start))
				}
				member = (member + 1) % 500
			}
		}
	}()
	started := time.Now()
	for id := 10_000; id < 11_000; id++ {
		entries, err := tsetCardEntries(cards, "move", id, 1, "waiting", "ready", nil, "1")
		if err != nil {
			result.why = err.Error()
			break
		}
		reply, sample, err := sampler.step(ctx, tset.Step{Epoch: "0", Space: space, Entries: entries})
		result.samples = append(result.samples, sample)
		if err != nil || reply.Status != "ok" || reply.Changed != 1 {
			if err == nil {
				result.correct = false
			}
			result.why = fmt.Sprintf("single move %d: status=%q changed=%d err=%v", id, reply.Status, reply.Changed, err)
			break
		}
	}
	elapsed := time.Since(started)
	stop()
	wg.Wait()
	result.beatWalls = walls
	result.beatErrors = beatErrors
	if elapsed > 0 {
		result.beatRate = float64(len(walls)) / elapsed.Seconds()
	}
	result.complete = len(result.samples) == 1_000 && result.why == "" && beatErrors == 0 && result.beatRate >= 125
	if beatErrors != 0 {
		result.correct = false
	}
	result.why = fmt.Sprintf("beat writes=%d rate=%.2f/s errors=%d p99_wall=%s; moves=%d; %s",
		len(walls), result.beatRate, beatErrors, tsetQuantile(walls, .99), len(result.samples), result.why)
	return result
}

func tsetMemoryCase(ctx context.Context, direct *redis.Client, space string, cards int, route, beforeInfo string) tsetSizeResult {
	result := tsetSizeResult{row: "L1-21", cards: cards, scenario: route, correct: true}
	perRow := cards / 100
	var total float64
	for row := 0; row < 100; row++ {
		id := row*perRow + perRow/2
		record := fmt.Sprintf("%smember:work:card-%07d", space, id)
		cell := fmt.Sprintf("%stable:work:cell:stream-%03d:waiting", space, row)
		recordBytes, err := direct.MemoryUsage(ctx, record, 0).Result()
		if err != nil {
			result.why = fmt.Sprintf("MEMORY USAGE record: %v", err)
			return result
		}
		cellBytes, err := direct.MemoryUsage(ctx, cell, 0).Result()
		if err != nil {
			result.why = fmt.Sprintf("MEMORY USAGE cell: %v", err)
			return result
		}
		members, err := direct.ZCard(ctx, cell).Result()
		if err != nil || members != int64(perRow) {
			result.why = fmt.Sprintf("sample cell cardinality=%d want=%d err=%v", members, perRow, err)
			return result
		}
		total += float64(recordBytes) + float64(cellBytes)/float64(members)
		result.memorySamples++
	}
	afterInfo, err := direct.Info(ctx, "memory").Result()
	if err != nil {
		result.why = fmt.Sprintf("INFO memory after fill: %v", err)
		return result
	}
	result.memoryPerCard = total / float64(result.memorySamples)
	result.complete = true
	result.why = fmt.Sprintf("record+amortized-cell=%.1f B/card across %d rows; before/after used_memory=%s/%s; dataset=%s/%s; peak=%s; Lua=%s/%s; post-GC dataset and retained Lua heap NOT MEASURED",
		result.memoryPerCard, result.memorySamples,
		tsetInfoField(beforeInfo, "used_memory"), tsetInfoField(afterInfo, "used_memory"),
		tsetInfoField(beforeInfo, "used_memory_dataset"), tsetInfoField(afterInfo, "used_memory_dataset"),
		tsetInfoField(afterInfo, "used_memory_peak"),
		tsetInfoField(beforeInfo, "used_memory_lua"), tsetInfoField(afterInfo, "used_memory_lua"))
	return result
}

// tsetFill measures the exact 50/500 composed write calls after the caller has
// initialized 100 work rows in an isolated namespace. It does not assert the
// fill limit until every Step succeeded, changed its full candidate set, and
// every SLOWLOG FCALL was matched. The caller judges the 1.5x growth separately.
func tsetFill(ctx context.Context, sampler tsetSizeSampler, space string, cards int) tsetSizeResult {
	result := tsetSizeResult{row: "L1-1", cards: cards, scenario: "local", correct: true, requireCounters: true}
	if cards == 100_000 {
		result.limit = 2 * time.Second
	} else if cards == 1_000_000 {
		result.limit = 20 * time.Second
	} else {
		result.why = "unsupported card scale"
		return result
	}
	for first := 0; first < cards; first += tsetSizeChunk {
		entries, err := tsetFillEntries(cards, first, tsetSizeChunk)
		if err != nil {
			result.why = err.Error()
			return result
		}
		input := tset.Step{Epoch: "0", Space: space, Entries: entries}
		reply, sample, err := sampler.step(ctx, input)
		result.samples = append(result.samples, sample)
		if err != nil {
			result.why = err.Error()
			return result
		}
		if reply.Status != "ok" || reply.Changed != tsetSizeChunk {
			result.correct = false
			result.complete = true
			result.why = fmt.Sprintf("step %d: status=%q changed=%d, want %d", first/tsetSizeChunk, reply.Status, reply.Changed, tsetSizeChunk)
			return result
		}
	}
	result.complete = true
	return result
}

// tsetBootstrap writes only beneath a new, caller-selected dev- namespace in
// an owned container. Definitions are trusted fixture data; all rows and
// members then go through the same composed Step path as the measured calls.
func tsetBootstrap(ctx context.Context, direct *redis.Client, store tset.Store, space string, workRows int) error {
	if workRows != 10 && workRows != 100 {
		return errors.New("size fixture wants exactly 10 or 100 initial work rows")
	}
	epochKey := space + "sprint:epoch"
	claimed, err := direct.SetNX(ctx, space+"size:claimed", "tset/1", 0).Result()
	if err != nil {
		return err
	}
	if !claimed {
		return fmt.Errorf("size run namespace %q was already used", space)
	}
	if err := direct.HSet(ctx, epochKey, "engine", "tset/1", "n", "0", "tables", `["work","merge","fleet","readers"]`).Err(); err != nil {
		return err
	}
	if err := direct.HSet(ctx, space+"sprint:epoch@0", "engine", "tset/1", "n", "0", "tables", `["work","merge","fleet","readers"]`).Err(); err != nil {
		return err
	}
	definitions := []struct {
		name, order string
	}{
		{"work", "waiting,ready,working,review,merging,done"},
		{"merge", "queued,working,done"},
		{"fleet", "col0,col1,col2,col3,col4,col5,col6,col7,col8,col9"},
		{"readers", "asked,ok,done"},
	}
	for _, definition := range definitions {
		fields := map[string]any{
			"engine": "tset/1", "order": definition.order,
			"member_prefix": space + "member:" + definition.name + ":",
			"epoch_key":     epochKey, "epoch_field": "n",
		}
		for _, column := range strings.Split(definition.order, ",") {
			fields["col:"+column] = "set"
		}
		key := space + "table:" + definition.name
		if err := direct.HSet(ctx, key, fields).Err(); err != nil {
			return err
		}
		if err := direct.HSet(ctx, key+":definition", fields).Err(); err != nil {
			return err
		}
	}
	rows := map[string][]string{"work": {}, "merge": {"ctl"}, "fleet": {}, "readers": {}}
	for i := 0; i < workRows; i++ {
		rows["work"] = append(rows["work"], fmt.Sprintf("stream-%03d", i))
	}
	for i := 0; i < 500; i++ {
		rows["fleet"] = append(rows["fleet"], fmt.Sprintf("fleet-%03d", i))
	}
	for i := 0; i < 4; i++ {
		rows["readers"] = append(rows["readers"], fmt.Sprintf("reader-%d", i))
	}
	for _, table := range []string{"work", "merge", "fleet", "readers"} {
		for first := 0; first < len(rows[table]); first += 100 {
			end := min(first+100, len(rows[table]))
			reply, err := store.Step(ctx, tset.Step{Epoch: "0", Space: space, Entries: []tset.Entry{{Kind: "rows", Table: table, Add: rows[table][first:end]}}})
			if err != nil {
				return fmt.Errorf("seed %s rows %d..%d: %w", table, first, end, err)
			}
			if reply.Status != "ok" {
				return fmt.Errorf("seed %s rows: status %q", table, reply.Status)
			}
		}
	}
	for first := 0; first < 500; first += 100 {
		var entries []tset.Entry
		for id := first; id < first+100; id++ {
			member := fmt.Sprintf("fleet-member-%03d", id)
			entries = append(entries, tset.Entry{Kind: "create", Table: "fleet", To: fmt.Sprintf("fleet-%03d:col0", id),
				IDs: []string{member}, Scores: []string{"1"}, About: []string{member}})
		}
		if _, err := store.Step(ctx, tset.Step{Epoch: "0", Space: space, Entries: entries}); err != nil {
			return fmt.Errorf("seed fleet members %d..%d: %w", first, first+100, err)
		}
	}
	var readers []tset.Entry
	for id := 0; id < 4; id++ {
		member := fmt.Sprintf("reader-member-%d", id)
		readers = append(readers, tset.Entry{Kind: "create", Table: "readers", To: fmt.Sprintf("reader-%d:asked", id),
			IDs: []string{member}, Scores: []string{"1"}, About: []string{member}})
	}
	if _, err := store.Step(ctx, tset.Step{Epoch: "0", Space: space, Entries: readers}); err != nil {
		return fmt.Errorf("seed reader members: %w", err)
	}
	if _, err := store.Step(ctx, tset.Step{Epoch: "0", Space: space, Entries: []tset.Entry{{
		Kind: "create", Table: "merge", To: "ctl:queued", IDs: []string{"merge-control"},
		Scores: []string{"1"}, About: []string{"merge-control"},
	}}}); err != nil {
		return fmt.Errorf("seed merge control member: %w", err)
	}
	return nil
}

// tsetRunL1 is the opt-in execution path for the section 1.8 rows. A row
// remains NOT MEASURED if its prerequisites or measurement fail. The caller
// supplies the profile loader and Store constructor; Emma's proxy is only an
// explicit address and is not recreated here.
type tsetOwnedStore interface {
	tset.Store
	Close() error
}

func tsetRunL1(ctx context.Context, cfg tsetSizeConfig,
	load func(context.Context, *redis.Client) error,
	newStore func(string, string) (tsetOwnedStore, error), out io.Writer) error {
	return tsetRunL1Sizes(ctx, cfg, []int{100_000, 1_000_000}, load, newStore, out)
}

// tsetRunL1Sizes is tsetRunL1 for the given card counts, each 100,000 or
// 1,000,000, in the order given. The rows it prints are those of the sizes it
// ran. The functional size run uses it to measure one size per container run.
func tsetRunL1Sizes(ctx context.Context, cfg tsetSizeConfig, sizes []int,
	load func(context.Context, *redis.Client) error,
	newStore func(string, string) (tsetOwnedStore, error), out io.Writer) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	for _, cards := range sizes {
		if cards != 100_000 && cards != 1_000_000 {
			return fmt.Errorf("L1 size run wants 100,000 or 1,000,000 cards, got %d", cards)
		}
	}
	if load == nil || newStore == nil {
		return errors.New("L1 size run needs composed profile loader and Store constructor")
	}
	direct := redis.NewClient(&redis.Options{Addr: cfg.redisAddr, MaxRetries: -1})
	defer direct.Close()
	if err := direct.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("L1 direct Redis: %w", err)
	}
	if err := load(ctx, direct); err != nil {
		return fmt.Errorf("load composed tset functions in owned container: %w", err)
	}
	var results []tsetSizeResult
	for _, pending := range tsetPendingRows(cfg.proxyAddr != "") {
		for _, cards := range sizes {
			if pending.cards == cards {
				results = append(results, pending)
				break
			}
		}
	}
	routes := []struct{ name, addr string }{{"local", cfg.redisAddr}}
	if cfg.proxyAddr != "" {
		routes = append(routes, struct{ name, addr string }{"~128ms-per-trip-proxy", cfg.proxyAddr})
	}
	var failed bool
	for _, cards := range sizes {
		for _, route := range routes {
			space := cfg.space + strconv.Itoa(cards) + ":" + route.name + ":"
			clientName := fmt.Sprintf("tset-size-%d-%s", cards, route.name)
			if err := func() (routeErr error) {
				store, err := newStore(route.addr, clientName)
				if err != nil {
					return fmt.Errorf("L1 %s store: %w", route.name, err)
				}
				defer func() {
					if closeErr := store.Close(); routeErr == nil {
						routeErr = closeErr
					}
				}()
				if err := tsetBootstrap(ctx, direct, store, space, 100); err != nil {
					return err
				}
				beforeMemory, beforeMemoryErr := direct.Info(ctx, "memory").Result()
				result := tsetFill(ctx, tsetSizeSampler{store: store, observer: direct, clientName: clientName}, space, cards)
				result.scenario = route.name
				measured := []tsetSizeResult{result}
				if result.verdict() == "INSIDE" || result.verdict() == "OVER" {
					if beforeMemoryErr == nil {
						measured = append(measured, tsetMemoryCase(ctx, direct, space, cards, route.name, beforeMemory))
					} else {
						measured = append(measured, tsetSizeResult{row: "L1-21", cards: cards, scenario: route.name,
							why: fmt.Sprintf("INFO memory before fill: %v", beforeMemoryErr)})
					}
					measurer := tsetSizeSampler{store: store, observer: direct, clientName: clientName}
					simple := tsetSimpleCases(ctx, measurer, space, cards, route.name)
					measured = append(measured, simple...)
					if len(simple) == 6 && simple[len(simple)-1].correct && simple[len(simple)-1].complete {
						deal := tsetDealCase(ctx, measurer, space, cards, route.name)
						measured = append(measured, deal)
						if deal.correct && deal.complete {
							measured = append(measured, tsetRefusalCase(ctx, measurer, space, cards, route.name))
							byteCase := tsetByteCase(ctx, measurer, space, cards, route.name)
							measured = append(measured, byteCase)
							measured = append(measured, tsetReplayCase(ctx, measurer, space, cards, route.name))
							if byteCase.complete && byteCase.correct {
								measured = append(measured, tsetReadCases(ctx, measurer, space, cards, route.name)...)
							}
							measured = append(measured, tsetWideRowsCase(ctx, measurer, space, cards, route.name))
							measured = append(measured, tsetRowScaleCase(ctx, measurer, direct, space, cards, route.name))
							measured = append(measured, tsetBeatCase(ctx, measurer, space, cards, route.name))
							measured = append(measured, tsetTopologyCases(ctx, measurer, space, cards, route.name)...)
							measured = append(measured, tsetAdvanceCase(ctx, measurer, space, cards, route.name))
						}
					}
				}
				for _, measuredRow := range measured {
					for i := range results {
						if results[i].row == measuredRow.row && results[i].cards == cards && results[i].scenario == route.name {
							results[i] = measuredRow
							break
						}
					}
					if measuredRow.verdict() != "INSIDE" {
						failed = true
					}
				}
				return nil
			}(); err != nil {
				return err
			}
		}
	}
	profile := cfg.profile
	if profile == "" {
		profile = "composed"
		if raw, err := direct.Do(ctx, "FUNCTION", "LIST", "LIBRARYNAME", "nova_sprint", "WITHCODE").Result(); err == nil {
			text := fmt.Sprint(raw)
			if strings.Contains(text, "tset_profile = 'l1_only'") {
				profile = "l1_only"
			} else if strings.Contains(text, "tset_profile = 'composed'") {
				profile = "composed"
			}
		}
	}
	if err := tsetReport(out, profile, results); err != nil {
		return err
	}
	if failed {
		return errors.New("at least one measured L1 size row failed or was incomplete")
	}
	return errors.New("L1 size gate incomplete until every row is run and section 10 post-GC/retained-Lua memory evidence is collected")
}

func tsetSizeCLI(ctx context.Context, cfg tsetSizeConfig, out io.Writer) error {
	return tsetRunL1(ctx, cfg,
		func(ctx context.Context, client *redis.Client) error {
			if err := tsetCheckIsolatedServer(ctx, client); err != nil {
				return err
			}
			return fn.LoadTSet(ctx, client, fn.TSetComposed)
		},
		func(addr, clientName string) (tsetOwnedStore, error) {
			return tset.NewRedis(addr, "", "", tset.WithClientName(clientName))
		}, out)
}

func tsetInfoField(info, name string) string {
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, name+":") {
			return strings.TrimSuffix(strings.TrimPrefix(line, name+":"), "\r")
		}
	}
	return ""
}

// LoadTSet replaces a Redis Function library. Verify a fresh standalone
// Redis 8.10.2 container before allowing that operation, in addition to the
// explicit owned-container CLI assertion.
func tsetCheckIsolatedServer(ctx context.Context, client *redis.Client) error {
	info, err := client.Info(ctx, "server").Result()
	if err != nil {
		return fmt.Errorf("read Redis server identity: %w", err)
	}
	if tsetInfoField(info, "redis_mode") != "standalone" || tsetInfoField(info, "redis_version") != "8.10.2" {
		return fmt.Errorf("L1 size run wants a fresh standalone Redis 8.10.2 container, got mode=%q version=%q",
			tsetInfoField(info, "redis_mode"), tsetInfoField(info, "redis_version"))
	}
	size, err := client.DBSize(ctx).Result()
	if err != nil {
		return fmt.Errorf("read benchmark DB size: %w", err)
	}
	if size != 0 {
		return fmt.Errorf("L1 size run wants an empty disposable Redis DB, found %d keys", size)
	}
	libraries, err := client.FunctionList(ctx, redis.FunctionListQuery{}).Result()
	if err != nil {
		return fmt.Errorf("read benchmark Redis functions: %w", err)
	}
	if len(libraries) != 0 {
		return fmt.Errorf("L1 size run wants no preexisting Redis Function libraries, found %d", len(libraries))
	}
	return nil
}

// This library is deliberately absent from the supported composed profile.
// Redis 8.10.2 gives Functions and EVAL different Lua states; an EVAL GC would
// measure the wrong heap. The probe is installed only in the opt-in memory
// diagnostic, before its baseline, and stays installed through its final INFO.
const tsetMemoryProbeSource = `#!lua name=tset_size_gc_probe
redis.register_function{
  function_name='tset_size_gc_probe',
  callback=function(keys, args)
    collectgarbage('collect')
    return 'collected'
  end,
  flags={'no-writes'}
}`

type tsetMemorySnapshot struct {
	Used            uint64  `json:"used_memory"`
	Dataset         uint64  `json:"used_memory_dataset"`
	Peak            uint64  `json:"used_memory_peak"`
	RSS             uint64  `json:"used_memory_rss"`
	VMFunctions     uint64  `json:"used_memory_vm_functions"`
	VMEval          uint64  `json:"used_memory_vm_eval"`
	FunctionLibrary uint64  `json:"used_memory_functions"`
	LuaAllocated    *uint64 `json:"allocator_allocated_lua_debug,omitempty"`
}

type tsetMemoryDelta struct {
	Allocator uint64 `json:"redis_allocator_bytes"`
	Dataset   uint64 `json:"dataset_bytes"`
	Functions uint64 `json:"functions_vm_bytes"`
	Eval      uint64 `json:"eval_vm_bytes"`
	Whole     uint64 `json:"allocator_plus_both_vm_bytes"`
}

func tsetMemoryGrowth(before, after tsetMemorySnapshot) (tsetMemoryDelta, error) {
	if after.Used < before.Used || after.Dataset < before.Dataset || after.VMFunctions < before.VMFunctions || after.VMEval < before.VMEval {
		return tsetMemoryDelta{}, errors.New("memory decreased across growth snapshot; inspect raw values instead of inventing a positive delta")
	}
	delta := tsetMemoryDelta{
		Allocator: after.Used - before.Used,
		Dataset:   after.Dataset - before.Dataset,
		Functions: after.VMFunctions - before.VMFunctions,
		Eval:      after.VMEval - before.VMEval,
	}
	delta.Whole = delta.Allocator + delta.Functions + delta.Eval
	return delta, nil
}

func tsetMemorySnapshotFromInfo(memory, debug string) (tsetMemorySnapshot, error) {
	var snapshot tsetMemorySnapshot
	fields := []struct {
		name string
		out  *uint64
	}{
		{"used_memory", &snapshot.Used},
		{"used_memory_dataset", &snapshot.Dataset},
		{"used_memory_peak", &snapshot.Peak},
		{"used_memory_rss", &snapshot.RSS},
		{"used_memory_vm_functions", &snapshot.VMFunctions},
		{"used_memory_vm_eval", &snapshot.VMEval},
		{"used_memory_functions", &snapshot.FunctionLibrary},
	}
	for _, field := range fields {
		value := tsetInfoField(memory, field.name)
		if value == "" {
			return snapshot, fmt.Errorf("INFO memory lacks %s", field.name)
		}
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return snapshot, fmt.Errorf("INFO memory %s=%q: %w", field.name, value, err)
		}
		*field.out = parsed
	}
	if value := tsetInfoField(debug, "allocator_allocated_lua"); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return snapshot, fmt.Errorf("INFO DEBUG allocator_allocated_lua=%q: %w", value, err)
		}
		snapshot.LuaAllocated = &parsed
	}
	return snapshot, nil
}

func tsetCaptureMemory(ctx context.Context, client *redis.Client) (tsetMemorySnapshot, error) {
	memory, err := client.Info(ctx, "memory").Result()
	if err != nil {
		return tsetMemorySnapshot{}, err
	}
	// INFO DEBUG metrics are experimental. Missing or denied debug data does
	// not turn the required INFO memory values into zeroes.
	debug, _ := client.Info(ctx, "debug").Result()
	return tsetMemorySnapshotFromInfo(memory, debug)
}

func tsetMemoryClass(space, key string) string {
	switch {
	case strings.HasPrefix(key, space+"member:"):
		return "records"
	case strings.HasPrefix(key, space+"table:") && strings.Contains(key, ":cell:"):
		return "cells"
	case strings.HasPrefix(key, space+"sprint:log@"):
		return "logs"
	case strings.HasPrefix(key, space+"sprint:done@"):
		return "receipts"
	case strings.HasPrefix(key, space+"sprint:cl@"):
		return "change_log"
	case strings.HasPrefix(key, space+"table:") && strings.HasSuffix(key, ":rows"):
		return "row_indexes"
	case strings.HasPrefix(key, space+"table:"):
		return "definitions"
	case strings.HasPrefix(key, space+"sprint:epoch"):
		return "epoch"
	default:
		return "fixture_or_other"
	}
}

type tsetMemoryClassTotal struct {
	Keys  uint64 `json:"keys"`
	Bytes uint64 `json:"memory_usage_bytes"`
}

// Redis MEMORY USAGE SAMPLES 0 traverses each nested value rather than taking
// the default sample. SCAN is deduplicated before counting. This deliberately
// expensive reconciliation is outside every timed size row.
func tsetNamespaceMemoryClasses(ctx context.Context, client *redis.Client, space string) (map[string]tsetMemoryClassTotal, error) {
	keys := make(map[string]struct{})
	var cursor uint64
	for {
		page, next, err := client.Scan(ctx, cursor, space+"*", 1_000).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range page {
			keys[key] = struct{}{}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	totals := make(map[string]tsetMemoryClassTotal)
	for first := 0; first < len(ordered); first += 500 {
		end := min(first+500, len(ordered))
		pipe := client.Pipeline()
		commands := make([]*redis.IntCmd, 0, end-first)
		for _, key := range ordered[first:end] {
			commands = append(commands, pipe.MemoryUsage(ctx, key, 0))
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, err
		}
		for i, key := range ordered[first:end] {
			bytes, err := commands[i].Result()
			if err != nil || bytes < 0 {
				return nil, fmt.Errorf("MEMORY USAGE SAMPLES 0 %q: bytes=%d err=%v", key, bytes, err)
			}
			class := tsetMemoryClass(space, key)
			total := totals[class]
			total.Keys++
			total.Bytes += uint64(bytes)
			totals[class] = total
		}
	}
	return totals, nil
}

func tsetForceFunctionGC(ctx context.Context, client *redis.Client) error {
	value, err := client.FCallRo(ctx, "tset_size_gc_probe", nil).Result()
	if err != nil {
		return fmt.Errorf("test-only Functions VM GC: %w", err)
	}
	if value != "collected" {
		return fmt.Errorf("test-only Functions VM GC returned %v", value)
	}
	return nil
}

// tsetMemoryCLI is a separate disposable-container run. It does not produce
// latency verdicts and never adds the probe to the timing or supported writer
// profile. Each cardinality requires its own freshly empty container.
func tsetMemoryCLI(ctx context.Context, cfg tsetSizeConfig, cards int, out io.Writer) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if cfg.proxyAddr != "" {
		return errors.New("L1 memory diagnostic uses direct Redis only; omit --tset-proxy-redis")
	}
	if cards != 100_000 && cards != 1_000_000 {
		return errors.New("L1 memory diagnostic wants --tset-memory-cards 100000 or 1000000")
	}
	if out == nil {
		return errors.New("L1 memory diagnostic needs an output writer")
	}
	client := redis.NewClient(&redis.Options{Addr: cfg.redisAddr, MaxRetries: -1})
	defer client.Close()
	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("L1 direct Redis: %w", err)
	}
	if err := tsetCheckIsolatedServer(ctx, client); err != nil {
		return err
	}
	if err := fn.LoadTSet(ctx, client, fn.TSetComposed); err != nil {
		return fmt.Errorf("load composed profile in owned container: %w", err)
	}
	if err := client.FunctionLoad(ctx, tsetMemoryProbeSource).Err(); err != nil {
		return fmt.Errorf("load test-only GC probe without REPLACE: %w", err)
	}
	libraries, err := client.FunctionList(ctx, redis.FunctionListQuery{}).Result()
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, library := range libraries {
		seen[library.Name] = true
	}
	if len(libraries) != 2 || !seen[fn.Library] || !seen["tset_size_gc_probe"] {
		return fmt.Errorf("memory profile wants exactly composed and diagnostic libraries, got %v", seen)
	}
	// Slowlog entries consume memory; keep their setting fixed and empty for
	// this memory-only run. This mutation is confined to the verified owned
	// disposable container and occurs before the first baseline.
	if err := client.ConfigSet(ctx, "slowlog-log-slower-than", "-1").Err(); err != nil {
		return fmt.Errorf("disable slowlog in memory-only container: %w", err)
	}
	if err := client.SlowLogReset(ctx).Err(); err != nil {
		return fmt.Errorf("reset slowlog in memory-only container: %w", err)
	}
	if err := tsetForceFunctionGC(ctx, client); err != nil {
		return err
	}
	baseline, err := tsetCaptureMemory(ctx, client)
	if err != nil {
		return fmt.Errorf("capture post-GC baseline: %w", err)
	}
	space := cfg.space + "memory:" + strconv.Itoa(cards) + ":"
	store, err := tset.NewRedis(cfg.redisAddr, "", "")
	if err != nil {
		return fmt.Errorf("L1 memory store: %w", err)
	}
	defer store.Close()
	if err := tsetBootstrap(ctx, client, store, space, 100); err != nil {
		return err
	}
	if err := tsetForceFunctionGC(ctx, client); err != nil {
		return err
	}
	prefill, err := tsetCaptureMemory(ctx, client)
	if err != nil {
		return fmt.Errorf("capture post-GC fixture: %w", err)
	}
	for first := 0; first < cards; first += tsetSizeChunk {
		entries, err := tsetFillEntries(cards, first, tsetSizeChunk)
		if err != nil {
			return err
		}
		reply, err := store.Step(ctx, tset.Step{Epoch: "0", Space: space, Entries: entries})
		if err != nil || reply.Status != "ok" || reply.Changed != tsetSizeChunk {
			return fmt.Errorf("memory fill step %d: status=%q changed=%d err=%v", first/tsetSizeChunk, reply.Status, reply.Changed, err)
		}
	}
	preGC, err := tsetCaptureMemory(ctx, client)
	if err != nil {
		return fmt.Errorf("capture pre-GC fill: %w", err)
	}
	if err := tsetForceFunctionGC(ctx, client); err != nil {
		return err
	}
	postGC, err := tsetCaptureMemory(ctx, client)
	if err != nil {
		return fmt.Errorf("capture post-GC fill: %w", err)
	}
	perCard := tsetMemoryCase(ctx, client, space, cards, "memory-diagnostic", "")
	if !perCard.complete || !perCard.correct {
		return fmt.Errorf("record+cell sampling incomplete: %s", perCard.why)
	}
	classes, err := tsetNamespaceMemoryClasses(ctx, client, space)
	if err != nil {
		return fmt.Errorf("reconcile namespace MEMORY USAGE: %w", err)
	}
	fromFixture, fixtureGrowthErr := tsetMemoryGrowth(prefill, postGC)
	fromBaseline, baselineGrowthErr := tsetMemoryGrowth(baseline, postGC)
	report := struct {
		Status             string                          `json:"status"`
		Profile            string                          `json:"profile"`
		Cards              int                             `json:"cards"`
		Space              string                          `json:"space"`
		BaselinePostGC     tsetMemorySnapshot              `json:"baseline_post_gc"`
		FixturePostGC      tsetMemorySnapshot              `json:"fixture_post_gc"`
		FillPreGC          tsetMemorySnapshot              `json:"fill_pre_gc"`
		FillPostGC         tsetMemorySnapshot              `json:"fill_post_gc"`
		FillFromFixture    *tsetMemoryDelta                `json:"fill_from_fixture_post_gc,omitempty"`
		FillFromBaseline   *tsetMemoryDelta                `json:"fill_from_baseline_post_gc,omitempty"`
		DeltaError         string                          `json:"delta_error,omitempty"`
		RecordCellBytes    float64                         `json:"record_plus_amortized_cell_bytes_per_card"`
		RecordCellGate     string                          `json:"record_plus_cell_400b_gate"`
		RecordCellSamples  int                             `json:"record_cell_samples"`
		NamespaceKeyMemory map[string]tsetMemoryClassTotal `json:"namespace_key_memory"`
		AccountingNote     string                          `json:"accounting_note"`
	}{
		Status:  "NOT MEASURED: Rowan L2 full-lifecycle post-GC dataset-per-primary gate is outside this fill-only diagnostic",
		Profile: "composed+test-only tset_size_gc_probe; timing profile differs",
		Cards:   cards, Space: space, BaselinePostGC: baseline, FixturePostGC: prefill,
		FillPreGC: preGC, FillPostGC: postGC,
		RecordCellBytes: perCard.memoryPerCard, RecordCellGate: perCard.verdict(), RecordCellSamples: perCard.memorySamples,
		NamespaceKeyMemory: classes,
		AccountingNote:     "L2 5000 B gate is post-GC used_memory_dataset lifecycle delta per primary, not the fill delta or allocator+VM estimate here. This fill uses a shared short brief, no fix/finding and no op receipt; it does not attribute each log line or receipt to distinct abouts or reconcile a lifecycle breakdown within 5%. INFO used_memory excludes the Functions VM, which is reported separately. No lifecycle pass is claimed.",
	}
	if fixtureGrowthErr == nil {
		report.FillFromFixture = &fromFixture
	} else {
		report.DeltaError = "fixture delta: " + fixtureGrowthErr.Error()
	}
	if baselineGrowthErr == nil {
		report.FillFromBaseline = &fromBaseline
	} else {
		if report.DeltaError != "" {
			report.DeltaError += "; "
		}
		report.DeltaError += "baseline delta: " + baselineGrowthErr.Error()
	}
	return json.NewEncoder(out).Encode(report)
}
