// merging.go: why a stream is not landing (nova-tools#3900). Glenn watched
// merging sit at 19 cards while 7 had no read on the record and the batch
// test was dead, and the table could not say so. The fix: the merging cell
// prints <read>/<unread>: read are the cards the stream lander would take
// (land stream: a pr:<name>:<n> record open at its head, in this stream,
// with a typed SCORE or APPROVE line at head of at least cfg:land
// min_score[:<repo>] (default 8) and no HOLD at head), unread the rest of
// ws:<s>:merging.
//
// The table used to also print one LAND line per open landing, from
// land:<repo>:<slug> and its stream PR's record (#3900, #3973). Glenn
// 2026-09-25 3:50 PM ET: "It is cluttered" — the live table no longer prints
// them; the same facts are `nova-sprint stream status --repo <owner/repo>`
// (cmd/nova-sprint/stream_life.go), reading land:<repo>:<slug> on its own.
//
// The split comes from ONE read-only script in the tick's own pipeline, so
// the table stays one round trip a tick and reads Redis only.
//
// The reading column (rowan-new specs/table-moves.md, Glenn 2026-09-25 1:25 PM:
// waiting, working, reading, merging, landed; nova-tools#3929 builds the
// ws:<s>:reading set on the card model) is the same split from its other
// source: ReadSplit is the one function the renderer asks, and it answers
// from the PR records until a reading set exists, then from the two sets
// (reading = unread, merging = read), printed as their own columns.
package table

import (
	"fmt"
	"strconv"
	"strings"

	landstream "github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
)

// defaultMergingRepo names the repo of a bare PR number on a card with no
// repo field (a fallback for the merging split alone; #3900 used to also
// list this repo's open landings as LAND lines, removed 2026-09-25).
const defaultMergingRepo = prkey.DefaultOwner + "/nova-tools"

// Read sources for the merging split (SprintSnapshot.ReadSource).
const (
	// ReadFromRecords: the split is computed from the PR records of the
	// cards in ws:<s>:merging (before #3929).
	ReadFromRecords = "records"
	// ReadFromSet: the split is the sets themselves, ws:<s>:reading (unread)
	// and ws:<s>:merging (read), the reading set of #3929.
	ReadFromSet = "set"
)

// detailScript reads, for each stream named, every card of ws:<s>:merging
// with its PR record's head, state, stream and reads, plus cfg:land. Sent as
// EVAL_RO, so Redis refuses a write; no KEYS, no SCAN: every key comes from a
// set member.
//
// ARGV: default repo name, #streams, streams...
// Reply: {cfg:land flat, per stream {id, name, n, head, state, stream, reads}...}.
const detailScript = `local function s(v) if v == false or v == nil then return '' end return tostring(v) end
local function base(r) return string.match(r, '([^/]+)$') or r end
local function prref(f, repo)
  f = string.match(f, '^%s*(.-)%s*$')
  if f == '' then return nil end
  local own, n = string.match(f, '([^/]+)/pull/(%d+)/?$')
  if n then return own, n end
  local pre, num = string.match(f, '^(.-)#(%d+)$')
  if num then
    if pre == '' then return repo, num end
    return base(pre), num
  end
  if string.match(f, '^%d+$') then return repo, f end
  return nil
end
local ns = tonumber(ARGV[2])
local streams = {}
for i = 1, ns do
  local rows = {}
  for _, id in ipairs(redis.call('ZRANGE', 'ws:' .. ARGV[2 + i] .. ':merging', 0, -1)) do
    local t = redis.call('HMGET', 'task:' .. id, 'pr', 'repo')
    if not t[1] and not t[2] then t = redis.call('HMGET', 'card:' .. id, 'pr', 'repo') end
    local repo = ARGV[1]
    if s(t[2]) ~= '' then repo = base(s(t[2])) end
    local name, n = prref(s(t[1]), repo)
    local r = {false, false, false, false}
    if name then
      r = redis.call('HMGET', 'pr:' .. name .. ':' .. n, 'head', 'state', 'stream', 'reads')
    else
      name, n = '', ''
    end
    for _, v in ipairs({id, name, n, s(r[1]), s(r[2]), s(r[3]), s(r[4])}) do table.insert(rows, v) end
  end
  streams[i] = rows
end
return {redis.call('HGETALL', 'cfg:land'), streams}`

// detailArgs is the script's ARGV for the streams whose merging split is
// computed from records.
func detailArgs(streams []string) []any {
	args := []any{prkey.Name(defaultMergingRepo), strconv.Itoa(len(streams))}
	for _, s := range streams {
		args = append(args, s)
	}
	return args
}

// applyDetail folds the script's reply into the snapshot: each stream's read
// count, the cards of merging the lander would take. A reply of the wrong
// shape leaves MergingRead at -1 (unknown), never a guess.
func applyDetail(snap *SprintSnapshot, reply any, streams []string) {
	top, ok := reply.([]any)
	if !ok || len(top) != 2 {
		return
	}
	cfg := map[string]string{}
	if flat, ok := top[0].([]any); ok {
		for i := 0; i+1 < len(flat); i += 2 {
			cfg[pipeValue(flat[i])] = strings.TrimSpace(pipeValue(flat[i+1]))
		}
	}
	perStream, _ := top[1].([]any)
	byName := map[string]int{}
	for i := range snap.Streams {
		byName[snap.Streams[i].Name] = i
	}
	for i, s := range streams {
		if i >= len(perStream) {
			break
		}
		rows, ok := perStream[i].([]any)
		j, known := byName[s]
		if !ok || !known {
			continue
		}
		var read int64
		for k := 0; k+6 < len(rows); k += 7 {
			f := make([]string, 7)
			for x := range f {
				f[x] = pipeValue(rows[k+x])
			}
			// reads is newline-joined typed lines: kept raw, never flattened.
			f[6], _ = rows[k+6].(string)
			if landable(s, f[2], f[3], f[4], f[5], f[6], minScore(cfg, f[1])) {
				read++
			}
		}
		snap.Streams[j].MergingRead = read
	}
}

// minScore is cfg:land min_score:<owner/name>, else min_score, else 8: the
// floor land stream reads (landstream.LoadConfig), for the PR's repo.
func minScore(cfg map[string]string, name string) int {
	full, err := prkey.Full(name)
	if err != nil {
		full = name
	}
	for _, k := range []string{"min_score:" + full, "min_score"} {
		if n, err := strconv.Atoi(cfg[k]); err == nil {
			return n
		}
	}
	return 8
}

// landable is the stream lander's member test (landstream.Members) on one
// card of ws:<s>:merging: a PR record at a head, open, in this stream, read
// at head to at least min with no hold at head.
func landable(s, n, head, state, recStream, reads string, min int) bool {
	if n == "" || head == "" || (recStream != "" && recStream != s) || (state != "" && state != "open") {
		return false
	}
	var lines []string
	for _, l := range strings.Split(reads, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	r := landstream.ReadAt(lines, head)
	return r.Held == "" && r.Score >= 0 && r.Score >= min
}

// ReadSplit is the one function behind the merging cell and the reading
// column: the stream's cards past working split by read. Read are the cards
// whose read lets them land, unread the rest. Before #3929 (ReadFromRecords)
// it is computed from the PR records of ws:<s>:merging; once the reading set
// exists (ReadFromSet) it is the two sets, reading = unread, merging = read.
// ok is false when the records could not be read this tick.
func (s *SprintSnapshot) ReadSplit(r StreamRow) (read, unread int64, ok bool) {
	if s.ReadSource == ReadFromSet {
		return r.Merging, r.Reading, true
	}
	if r.MergingRead < 0 {
		return 0, r.Merging, false
	}
	read = min(r.MergingRead, r.Merging)
	return read, r.Merging - read, true
}

// mergingCell is the merging cell in records mode: <read>/<unread>, or the
// plain count when the records were not read.
func (s *SprintSnapshot) mergingCell(r StreamRow) string {
	read, unread, ok := s.ReadSplit(r)
	if !ok {
		return strconv.FormatInt(r.Merging, 10)
	}
	return fmt.Sprintf("%d/%d", read, unread)
}
