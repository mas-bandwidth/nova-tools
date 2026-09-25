// merging.go: why a stream is not landing (nova-tools#3900). Glenn watched
// merging sit at 19 cards while 7 had no read on the record and the batch
// test was dead, and the table could not say so. Two things fix that:
//
//   - the merging cell prints <read>/<unread>: read are the cards the stream
//     lander would take (land stream: a pr:<name>:<n> record open at its head,
//     in this stream, with a typed SCORE or APPROVE line at head of at least
//     cfg:land min_score[:<repo>] (default 8) and no HOLD at head), unread the
//     rest of ws:<s>:merging;
//   - one LAND line per open landing, from land:<repo>:<slug> and its stream
//     PR's record: stream, members, head, ci=green|red|pending, age.
//
// Both come from ONE read-only script in the tick's own pipeline, so the
// table stays one round trip a tick and reads Redis only.
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
	"sort"
	"strconv"
	"strings"
	"time"

	landstream "github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
)

// DefaultLandRepos are the repos whose landings the table lists when
// SprintConfig.LandRepos is empty; the first also names the repo of a bare
// PR number on a card with no repo field.
var DefaultLandRepos = []string{prkey.DefaultOwner + "/nova-tools", prkey.DefaultOwner + "/rowan-tools"}

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
// with its PR record's head, state, stream and reads; cfg:land; and, for each
// repo named, every land:<repo>:<slug> of land:<repo>:streams with its stream
// PR's ci. Sent as EVAL_RO, so Redis refuses a write; no KEYS, no SCAN:
// every key comes from a set member.
//
// ARGV: default repo name, #streams, streams..., #repos, repos (owner/name)...
// Reply: {cfg:land flat, per stream {id, name, n, head, state, stream, reads}...,
// per repo {slug, streams, state, head, members, pr, at, ci}...}.
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
local nr = tonumber(ARGV[3 + ns])
local lands = {}
for i = 1, nr do
  local repo = ARGV[3 + ns + i]
  local rows = {}
  for _, slug in ipairs(redis.call('SMEMBERS', 'land:' .. repo .. ':streams')) do
    local l = redis.call('HMGET', 'land:' .. repo .. ':' .. slug, 'streams', 'state', 'head', 'members', 'pr', 'at')
    local ci = ''
    if s(l[5]) ~= '' then ci = s(redis.call('HGET', 'pr:' .. base(repo) .. ':' .. s(l[5]), 'ci')) end
    for _, v in ipairs({slug, s(l[1]), s(l[2]), s(l[3]), s(l[4]), s(l[5]), s(l[6]), ci}) do table.insert(rows, v) end
  end
  lands[i] = rows
end
return {redis.call('HGETALL', 'cfg:land'), streams, lands}`

// LandRow is one open landing: land:<repo>:<slug> and its stream PR's ci.
type LandRow struct {
	Repo, Slug, Streams, State, Head, PR, CI string
	Members                                  int
	At                                       int64 // ms; 0 when the hash has none
}

// detailArgs is the script's ARGV for the streams whose merging split is
// computed from records and the repos whose landings are listed.
func detailArgs(streams, repos []string) []any {
	args := []any{prkey.Name(repos[0]), strconv.Itoa(len(streams))}
	for _, s := range streams {
		args = append(args, s)
	}
	args = append(args, strconv.Itoa(len(repos)))
	for _, r := range repos {
		args = append(args, r)
	}
	return args
}

// landRepos is the configured repos, else DefaultLandRepos.
func landRepos(cfg SprintConfig) []string {
	if len(cfg.LandRepos) > 0 {
		return cfg.LandRepos
	}
	return DefaultLandRepos
}

// applyDetail folds the script's reply into the snapshot: each stream's read
// count (the cards of merging the lander would take) and the open landings.
// A reply of the wrong shape leaves MergingRead at -1 (unknown), never a guess.
func applyDetail(snap *SprintSnapshot, reply any, streams, repos []string) {
	top, ok := reply.([]any)
	if !ok || len(top) != 3 {
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
	perRepo, _ := top[2].([]any)
	for i, repo := range repos {
		if i >= len(perRepo) {
			break
		}
		rows, _ := perRepo[i].([]any)
		var open []LandRow
		for k := 0; k+7 < len(rows); k += 8 {
			f := make([]string, 8)
			for x := range f {
				f[x] = pipeValue(rows[k+x])
			}
			row := LandRow{Repo: repo, Slug: f[0], Streams: f[1], State: f[2], Head: f[3], Members: len(strings.Fields(f[4])), PR: f[5], CI: f[7]}
			row.At, _ = strconv.ParseInt(f[6], 10, 64)
			if landingOpen(row.State) {
				open = append(open, row)
			}
		}
		// Repos in the configured order, slugs sorted within a repo.
		sort.Slice(open, func(a, b int) bool { return open[a].Slug < open[b].Slug })
		snap.Landings = append(snap.Landings, open...)
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

// landingOpen is a landing still on its way: built, pushed, open, or stopped
// on a conflict or a red base. merged is done; empty and dry-run never were.
func landingOpen(state string) bool {
	switch state {
	case "merged", "landed", "empty", "dry-run", "":
		return false
	}
	return true
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

// LandLine is one open landing as the table prints it:
// LAND stream=<s> members=<n> head=<sha8> ci=green|red|pending age=<d>
// [state=<s> when not open]. ci is - while the landing has no stream PR.
func (l LandRow) LandLine(now time.Time) string {
	streams := l.Streams
	if streams == "" {
		streams = l.Slug
	}
	if strings.ContainsAny(streams, " =\t") {
		streams = strconv.Quote(streams)
	}
	head := l.Head
	if len(head) > 8 {
		head = head[:8]
	}
	ci := "-"
	if l.PR != "" {
		ci = l.CI
		if ci == "" {
			ci = "pending"
		}
	}
	line := fmt.Sprintf("LAND stream=%s members=%d head=%s ci=%s age=%s", streams, l.Members, orDash(head), ci, landAge(l.At, now))
	if l.State != "open" {
		line += " state=" + l.State
	}
	return line
}

// landAge is how long ago at (ms) was: 45s, 6m, 2h05m; - when unknown.
func landAge(at int64, now time.Time) string {
	if at <= 0 {
		return "-"
	}
	d := now.Sub(time.UnixMilli(at))
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}
