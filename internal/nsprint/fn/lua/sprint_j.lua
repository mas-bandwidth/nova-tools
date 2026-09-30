-- J, the judgments of a step: the upper design (EVENT-DRIVEN-TICK version 2.1)
-- 1.3.4 and 2.2, as errata 1 to 3 correct them; item IT15. NS.SP.j_decide is J's
-- pre stage (note requests to the step's notes, from the real state of jopen, jn,
-- jh, jtext, the quarantine, the clock and the overdue entry of a review wait)
-- and NS.SP.j_cmds is its X.plan (the keys that follow, with note ids from the
-- seqs Layer 2 gave the lines). Written to the interfaces of sprint_00_core.lua
-- and sprint_zz_fn.lua and parsed, and run under gopher-lua against stubs of
-- Layer 1's S in internal/nsprint/fn; no store loads it before gate G0. Its Go
-- twin is internal/sprint/sprintfn/twin_j.go, whose comments state the ops and
-- the readings the design left to take; both halves are held to the vectors in
-- internal/sprint/sprintfn/testdata/j_vectors.json.
--
-- J runs for a step that carries a note request and for any step that carries
-- entries (the core calls it once it is registered), since it closes the
-- lateness judgment of each timed state the entries end. It writes at
-- ctx.write_epoch, the next epoch on a step that advances, where the twin reads
-- the advance entry from State.Entries.
--
-- The differences from the twin. S.writecmd can refuse when the step's commands
-- pass the shared bound, and the core takes j_cmds's first return only, so j_cmds
-- raises that refusal as an error, which prepare's own totals make a last resort.
-- J asks S.before itself for the due fields of the cards every entry moves or
-- removes, derived entries included; the twin asks for those of the caller's
-- entries (Phases.JBefore, before derive runs). Derive's entries stay on the
-- work table (1.3.3), which has no timed state of a due kind, so the two agree.
if NS.tset_profile then
do
  local SP = NS.SP

  -- BEGIN generated: TestJTablesMatchLua holds this block equal to
  -- sprint.Judgments (2.2), sprint.Notices (2.5), sprint.DueKinds (1.2) and the
  -- bounds and reasons of twin_j.go. A type maps to true when the tick keeps its
  -- condition.
  SP.j_bounds = {subjects = 2000, notes = 100, about = 4000, name = 256, piece = 1000,
    overdue_ms = 600000, digits = 15}
  SP.j_judgments = {
    ['a card reached its bound'] = true,
    ['a primary is blocked on something dropped'] = false,
    ['a primary is blocked on something missing'] = false,
    ['a read card is past its deadline'] = true,
    ['a reminder could not be delivered'] = true,
    ['a stream has had no merge step past its deadline'] = true,
    ['a verb in parts stopped before its end'] = true,
    ['a work card is past its deadline'] = true,
    ['an invariant is broken'] = true,
    ['cannot ask'] = true,
    ['ci red on a primary'] = false,
    ['no fleet member is up'] = true,
    ['reads exhausted'] = false,
    ['returned to review'] = false,
    ['sentinel reached'] = false,
    ['stalled'] = true,
    ['stranded in review'] = false,
    ['stream stopped: conflict on a card'] = false,
    ['stream stopped: needs a card of another stream first'] = false,
    ['stream stopped: stream branch red'] = false,
    ['stream stopped: the merge queue rejected'] = false,
    ['the machine could not move a card'] = true,
    ['the machine is STOPPED and moves are due'] = true,
    ['the machine is falling behind'] = true,
    ['the machine\'s step was refused'] = true,
    ['the sprint is done'] = false,
  }
  SP.j_notices = {
    ['a judgment has waited past its due time'] = true,
    ['a member\'s ok rate fell below OkRateFloor'] = true,
    ['a read of p by r: its one-line summary'] = true,
    ['a reader\'s broken rate rose above BrokenRateCeiling'] = true,
    ['accepted by the machine'] = true,
    ['an unknown machine is beating'] = true,
    ['batch landed'] = true,
    ['cards returned to ready because no member is up'] = true,
    ['ci green'] = true,
    ['fleet member down'] = true,
    ['fleet member up'] = true,
    ['k cards of s made ready'] = true,
    ['k ready cards of s went back to waiting behind G'] = true,
    ['replaced a late read'] = true,
    ['replaced a late work card'] = true,
    ['replaced a work card not taken'] = true,
    ['reworked by the machine'] = true,
    ['sentinel landed'] = true,
    ['stream landed'] = true,
    ['stream resumed: the card it needed landed'] = true,
    ['stream s has landed nothing for IdleSpan'] = true,
    ['stream started merging'] = true,
    ['the machine started'] = true,
    ['the machine stopped'] = true,
    ['the sprint was cleared, and is STOPPED'] = true,
    ['work came back ok'] = true,
  }
  SP.j_stream_prefix = 'stream:'
  SP.j_late = {
    {kind = 'untaken', table = 'fleet', col = 'ready', field = 'due_untaken', of_row = false,
      type = 'a work card is past its deadline', moved = {withdrawn = 'replaced', working = 'taken'},
      other = 'state ended', cleared = 'cleared'},
    {kind = 'unfinished', table = 'fleet', col = 'working', field = 'due_unfinished', of_row = false,
      type = 'a work card is past its deadline', moved = {failed = 'finished', ok = 'finished', withdrawn = 'replaced'},
      other = 'state ended', cleared = 'cleared'},
    {kind = 'unbegun', table = 'readers', col = 'asked', field = 'due_unbegun', of_row = false,
      type = 'a read card is past its deadline', moved = {broken = 'reported', ok = 'reported', reading = 'begun'},
      other = 'state ended', cleared = 'cleared'},
    {kind = 'unreported', table = 'readers', col = 'reading', field = 'due_unreported', of_row = false,
      type = 'a read card is past its deadline', moved = {broken = 'reported', ok = 'reported'},
      other = 'state ended', cleared = 'cleared'},
    {kind = 'mergeidle', table = 'merge', col = 'ctl', field = 'due_mergeidle', of_row = true,
      type = 'a stream has had no merge step past its deadline', moved = {},
      other = 'stream no longer merging', cleared = 'stream no longer merging'},
  }
  -- END generated

  local B = SP.j_bounds
  -- Layer 1's S, resolved when a call runs: table_set*.lua sorts after this file.
  local function layer_one() return NS.tset end
  local NOTE, HOLD, SEP = 'n', 'h', '|'
  local CANNOT_ASK = 'cannot ask'
  local STOPPED = 'the machine is STOPPED and moves are due'
  local DONE = 'the sprint is done'
  local UNTIL_MAX = 999999999999999
  local STATE_OPS = {open = true, close = true, update = true, hold = true, unhold = true}
  local OPS = {open = true, close = true, update = true, hold = true, unhold = true, know = true, request = true, tickend = true}
  -- The tick's end note (errata 3 amendment 8; sprintfn JOpTickEnd): its type,
  -- its one subject (who it is to) and its text's head.
  local TICK_END, TICK_END_TO = 'tick-end', 'coordinator'
  -- What a read of a quarantine record may cost, a field at a time.
  local QUARANTINE_FIELD_BYTES = 1024

  -- A per epoch sprint key: {p}<name>@<epoch>. {p} is the prefix and "sprint:".
  local function jkey(ctx, name) return ctx.space .. 'sprint:' .. name .. '@' .. ctx.write_epoch end
  local function clock_key(ctx) return ctx.space .. 'sprint:clock' end
  local function field_of(typ, cause) return typ .. SEP .. cause end
  local function note_of(existing)
    if string.sub(existing, 1, 2) == HOLD .. NOTE then return string.sub(existing, 2) end
    return existing
  end
  -- The id of the note whose line has the seq: "n" and the seq, then the epoch
  -- after "~" from epoch 1 (sprint.OpFamily).
  local function note_id(ctx, seq)
    if ctx.write_epoch == '0' then return NOTE .. seq end
    return NOTE .. seq .. '~' .. ctx.write_epoch
  end
  local function num(n) return string.format('%.0f', n) end

  -- digest is the digest of a note's text and decisions, which jtext holds for an
  -- open note: SHA-1 of the lengths and the words (sprintfn jDigest), so that no
  -- two different pairs read alike. A note with neither has the empty digest,
  -- which jtext does not store: an absent field is that digest.
  local function digest(text, decisions)
    local parts = {string.format('%d:', #text), text, string.format('%d;', #decisions)}
    for _, d in ipairs(decisions) do
      parts[#parts + 1] = string.format('%d:', #d)
      parts[#parts + 1] = d
    end
    return redis.sha1hex(table.concat(parts))
  end

  -- utf8_ok is utf8.ValidString: no bad continuation, overlong form, surrogate
  -- or value past U+10FFFF.
  local function utf8_ok(s)
    local i, n = 1, #s
    while i <= n do
      local c = string.byte(s, i)
      if c < 0x80 then
        i = i + 1
      elseif c >= 0xC2 and c <= 0xDF then
        local c2 = string.byte(s, i + 1)
        if not c2 or c2 < 0x80 or c2 > 0xBF then return false end
        i = i + 2
      elseif c >= 0xE0 and c <= 0xEF then
        local c2, c3 = string.byte(s, i + 1, i + 2)
        if not c3 or c2 < 0x80 or c2 > 0xBF or c3 < 0x80 or c3 > 0xBF then return false end
        if c == 0xE0 and c2 < 0xA0 then return false end
        if c == 0xED and c2 > 0x9F then return false end
        i = i + 3
      elseif c >= 0xF0 and c <= 0xF4 then
        local c2, c3, c4 = string.byte(s, i + 1, i + 3)
        if not c4 or c2 < 0x80 or c2 > 0xBF or c3 < 0x80 or c3 > 0xBF or c4 < 0x80 or c4 > 0xBF then return false end
        if c == 0xF0 and c2 < 0x90 then return false end
        if c == 0xF4 and c2 > 0x8F then return false end
        i = i + 4
      else
        return false
      end
    end
    return true
  end
  local function name_ok(s, allow_empty)
    return type(s) == 'string' and (allow_empty or #s > 0) and #s <= B.name and utf8_ok(s)
  end

  -- dedup keeps the first of equal strings, in order.
  local function dedup(list)
    local seen, out = {}, {}
    for _, s in ipairs(list) do
      if not seen[s] then seen[s] = true; out[#out + 1] = s end
    end
    return out
  end

  -- The refusals, with the words of the twin's: the reason is in the message and
  -- never in a field of the detail, which Layer 1 fixes.
  local function refuse_req(S, index, why)
    return S.refuse('REQUEST', {}, 'REQUEST: note request ' .. index .. ' ' .. why)
  end
  local function refuse_limit(S, index, budget, limit, actual)
    return S.refuse('LIMIT', {budget = budget, limit = limit, actual = actual},
      'LIMIT: note request ' .. index .. ' is over the bound of ' .. budget)
  end

  -- A request as decoded from the sprint half: fields may be absent.
  local function normalize(r)
    local subjects = {}
    if type(r.subjects) == 'table' then for _, s in ipairs(r.subjects) do subjects[#subjects + 1] = s end end
    local decisions = {}
    if type(r.decisions) == 'table' then for _, s in ipairs(r.decisions) do decisions[#decisions + 1] = s end end
    local untilms = 0
    local u = r['until']
    if type(u) == 'string' then
      if #u > B.digits or not string.match(u, '^[0-9]+$') then untilms = -1 else untilms = tonumber(u) end
    end
    return {op = r.op, type = r.type, cause = r.cause or '', subjects = subjects,
      text = r.text or '', decisions = decisions, ['until'] = untilms}
  end

  -- check is the static shape of one request (sprintfn jCheck).
  local function check(S, i, r)
    if type(r.op) ~= 'string' or not OPS[r.op] then return refuse_req(S, i, 'has an op J does not take') end
    if #r.subjects == 0 then return refuse_req(S, i, 'names no subject') end
    if #r.subjects > B.subjects then return refuse_limit(S, i, 'about', B.subjects, #r.subjects) end
    for _, s in ipairs(r.subjects) do
      if not name_ok(s, false) then return refuse_req(S, i, 'names a subject that is empty, over 256 bytes or not UTF-8') end
    end
    local state = STATE_OPS[r.op] == true
    if not name_ok(r.type, false) or (state and string.find(r.type, SEP, 1, true)) then
      return refuse_req(S, i, 'has a type that is empty, over 256 bytes, not UTF-8 or holds a bar')
    end
    if not name_ok(r.cause, not state) then return refuse_req(S, i, 'has a cause that is empty, over 256 bytes or not UTF-8') end
    if type(r.text) ~= 'string' or not utf8_ok(r.text) then return refuse_req(S, i, 'has text that is not UTF-8') end
    for _, d in ipairs(r.decisions) do
      if type(d) ~= 'string' or not utf8_ok(d) then return refuse_req(S, i, 'has a decision that is not UTF-8') end
    end
    if r.op == 'know' then
      if not SP.j_notices[r.type] then return refuse_req(S, i, 'is a notice of a type 2.5 does not have') end
    elseif r.op == 'tickend' then
      local n = string.match(r.text, '^judgments=(%d+)$')
      if r.type ~= TICK_END or n == nil or (#n > 1 and string.sub(n, 1, 1) == '0') or #r.subjects ~= 1 or r.subjects[1] ~= TICK_END_TO then
        return refuse_req(S, i, 'is a tick-end note that is not of its type, to the coordinator, with judgments=N')
      end
    elseif r.op ~= 'request' then
      if SP.j_judgments[r.type] == nil then return refuse_req(S, i, 'is on a judgment type 2.2 does not have') end
    end
    if r['until'] < 0 or r['until'] > UNTIL_MAX then
      return refuse_req(S, i, 'has a time that is negative or over 15 digits')
    end
    if r.op == 'hold' and r['until'] <= 0 then return refuse_req(S, i, 'holds with no time to hold until') end
    return nil
  end

  -- The reads, typed, each once a call. An absent field is nil.
  local function hget(d, k, field, reserve)
    local v, err = d.S.readcmd(d.ctx, {argv = {'HGET', k, field},
      access = {{key = k, kind = 'hash', mode = 'read'}}}, reserve, 'cell')
    if err then return nil, err end
    if v == false or v == nil then return nil, nil end
    return v, nil
  end
  -- cell is one field of a hash, read once a call.
  local function cell(d, k, field, reserve)
    local memo = k .. '\0' .. field
    local c = d.cells[memo]
    if c then return c.v, nil end
    local v, err = hget(d, k, field, reserve)
    if err then return nil, err end
    d.cells[memo] = {v = v}
    return v, nil
  end
  local function state_of(d, subject, field)
    local v, err = cell(d, jkey(d.ctx, 'jopen:' .. subject), field, 600)
    if err then return nil, err end
    if v == nil then return {kind = ''}, nil end
    if #v > 2 and string.sub(v, 1, 2) == HOLD .. NOTE then return {kind = 'held', id = string.sub(v, 2)}, nil end
    if #v > 1 and string.sub(v, 1, 1) == NOTE then return {kind = 'open', id = v}, nil end
    return nil, d.S.refuse('DRIFT', {})
  end
  -- is_count says a value is a count as jn and jh hold it: one to nine digits, no
  -- leading zero, at least one.
  local function is_count(v) return #v <= 9 and string.match(v, '^[1-9][0-9]*$') ~= nil end
  -- count_of is jn[note]: DRIFT when there is none, or it is no count.
  local function count_of(d, note)
    local v, err = cell(d, jkey(d.ctx, 'jn'), note, 32)
    if err then return nil, err end
    if v == nil or not is_count(v) then return nil, d.S.refuse('DRIFT', {}) end
    return tonumber(v), nil
  end
  -- held_of is jh[note], the held subjects of a note: DRIFT for a note whose
  -- subjects a step takes out of the hold (must) and jh has none of, or no count;
  -- zero for a note a step holds more subjects of, with none held yet.
  local function held_of(d, note, must)
    local v, err = cell(d, jkey(d.ctx, 'jh'), note, 32)
    if err then return nil, err end
    if v == nil and not must then return 0, nil end
    if v == nil or not is_count(v) then return nil, d.S.refuse('DRIFT', {}) end
    return tonumber(v), nil
  end
  -- empty_digest is the digest of a note with no text and no decisions, worked
  -- out once a call.
  local function empty_digest(d)
    if not d.empty then d.empty = digest('', {}) end
    return d.empty
  end
  -- text_digest is the digest an open note has: jtext's field, or the empty one.
  local function text_digest(d, note)
    local v, err = cell(d, jkey(d.ctx, 'jtext'), note, 64)
    if err then return nil, err end
    if v == nil then return empty_digest(d), nil end
    return v, nil
  end
  -- overdue_score is the score of overdue:<note> in {p}due@e, nil when absent,
  -- read once a call for each note: a review wait finds the review time already
  -- there and writes nothing (sprintfn overdueScore).
  local function overdue_score(d, note)
    local memo = d.overdue[note]
    if memo then return memo.v, nil end
    local k = jkey(d.ctx, 'due')
    local v, err = d.S.readcmd(d.ctx, {argv = {'ZSCORE', k, 'overdue:' .. note},
      access = {{key = k, kind = 'zset', mode = 'read'}}}, 32, 'cell')
    if err then return nil, err end
    local score = nil
    if v ~= false and v ~= nil then score = tonumber(v) end
    d.overdue[note] = {v = score}
    return score, nil
  end
  -- quarantined reads {p}quarantine@e over the subjects in pieces of at most
  -- B.piece: a read for each piece and never one for each subject.
  local function quarantined(d, subjects, into)
    local S, ctx = d.S, d.ctx
    local k = jkey(ctx, 'quarantine')
    local acc = {{key = k, kind = 'hash', mode = 'read'}}
    local i = 1
    while i <= #subjects do
      local last = math.min(i + B.piece - 1, #subjects)
      local argv = {'HMGET', k}
      for j = i, last do argv[#argv + 1] = subjects[j] end
      local got, err = S.readcmd(ctx, {argv = argv, access = acc}, (last - i + 1) * QUARANTINE_FIELD_BYTES + 16, 'cell')
      if err then return err end
      for j = i, last do
        local v = got[j - i + 1]
        if v ~= false and v ~= nil then into[subjects[j]] = true end
      end
      i = last + 1
    end
    return nil
  end
  -- digits reads a clock field: absent is 0; anything but 1 to 15 digits fails.
  local function digits(v)
    if v == nil or v == false then return 0, true end
    if #v == 0 or #v > B.digits or not string.match(v, '^[0-9]+$') then return 0, false end
    return tonumber(v), true
  end
  -- running is R (1.2): the wall time less the STOPPED time before now and the
  -- present STOPPED span, from the call's one time and the clock.
  local function running(d)
    if d.r then return d.r, nil end
    local S, ctx = d.S, d.ctx
    local ck = clock_key(ctx)
    local got, err = S.readcmd(ctx, {argv = {'HMGET', ck, 'stopped_ms', 'stopped_since_ms'},
      access = {{key = ck, kind = 'hash', mode = 'read'}}}, 128, 'cell')
    if err then return nil, err end
    local wall, ok = digits(ctx.now_ms)
    if not ok or type(ctx.now_ms) ~= 'string' then return nil, S.refuse('CONFIG', {}) end
    local stopped
    stopped, ok = digits(got[1])
    if not ok then return nil, S.refuse('CONFIG', {}) end
    local r = wall - stopped
    local since = got[2]
    if since ~= false and since ~= nil and since ~= '' then
      local s
      s, ok = digits(since)
      if not ok or s > wall then return nil, S.refuse('CONFIG', {}) end
      r = r - (wall - s)
    end
    if r < 0 then return nil, S.refuse('CONFIG', {}) end
    d.r = r
    return r, nil
  end
  -- jopen_fields is every field of a subject's jopen hash, bounded by its HLEN,
  -- as a set of names.
  local function jopen_fields(d, subject)
    if d.all[subject] then return d.all[subject], nil end
    local S, ctx = d.S, d.ctx
    local k = jkey(ctx, 'jopen:' .. subject)
    local acc = {{key = k, kind = 'hash', mode = 'read'}}
    local n, err = S.readcmd(ctx, {argv = {'HLEN', k}, access = acc}, 32, 'cell')
    if err then return nil, err end
    local fields = {}
    if n > 0 then
      local keys
      keys, err = S.readcmd(ctx, {argv = {'HKEYS', k}, access = acc}, n * 520 + 16, 'cell')
      if err then return nil, err end
      for i = 1, #keys do fields[keys[i]] = true end
    end
    d.all[subject] = fields
    return fields, nil
  end

  -- decide expands one request into the lines it makes (sprintfn decide): each
  -- a group {req = ..., existing = "", digest = ""}, existing the note id, "h"
  -- before it for a hold.
  local function copy_req(r, subjects, op)
    return {op = op or r.op, type = r.type, cause = r.cause, subjects = subjects, text = r.text,
      decisions = r.decisions, ['until'] = r['until']}
  end
  local function decide(d, r)
    local subjects = dedup(r.subjects)
    if not STATE_OPS[r.op] then return {{req = copy_req(r, subjects), existing = '', digest = ''}}, nil end
    local field = field_of(r.type, r.cause)
    local order, buckets, fresh = {}, {}, {}
    for _, s in ipairs(subjects) do
      local js, err = state_of(d, s, field)
      if err then return nil, err end
      if js.kind == '' then
        fresh[#fresh + 1] = s
      else
        local k = js.kind .. '\0' .. js.id
        local b = buckets[k]
        if not b then b = {state = js, subjects = {}}; buckets[k] = b; order[#order + 1] = k end
        b.subjects[#b.subjects + 1] = s
      end
    end
    local dg = digest(r.text, r.decisions)
    if r.op == 'open' then
      if #fresh == 0 then return {}, nil end
      return {{req = copy_req(r, fresh), existing = '', digest = dg}}, nil
    end
    local tick_kept = SP.j_judgments[r.type] == true
    local out = {}
    for _, k in ipairs(order) do
      local b = buckets[k]
      local held = b.state.kind == 'held'
      local g = {req = copy_req(r, b.subjects), existing = b.state.id, digest = ''}
      if held then g.existing = HOLD .. b.state.id end
      local skip = false
      if r.op == 'update' then
        if held then
          skip = true
        else
          local have, err = text_digest(d, b.state.id)
          if err then return nil, err end
          skip = have == dg
          g.digest = dg
        end
      elseif r.op == 'hold' then
        skip = held
        if not skip and not tick_kept then
          -- A review wait moves the overdue entry to the review time: with the
          -- entry already there, the wait was run before on this state. The type
          -- that is never overdue has no entry, and writes its line every time.
          if r.type ~= DONE then
            local at, err = overdue_score(d, b.state.id)
            if err then return nil, err end
            skip = at ~= nil and at == r['until']
          end
          if not skip then g.req.op = 'review' end
        end
      elseif r.op == 'unhold' then
        skip = not held
      end
      if not skip then out[#out + 1] = g end
    end
    return out, nil
  end

  -- merge_key is what makes two state requests one note: the op, the type and
  -- cause, the text and decisions, and the time, each length-prefixed.
  local function merge_key(r)
    local parts = {string.format('%d:%s%d:%s%d:%s%d:%s%d;', #r.op, r.op, #r.type, r.type, #r.cause, r.cause,
      #r.text, r.text, #r.decisions)}
    for _, x in ipairs(r.decisions) do parts[#parts + 1] = string.format('%d:%s', #x, x) end
    parts[#parts + 1] = num(r['until'])
    return table.concat(parts)
  end
  -- merge makes the state requests of one op, type, cause, text, decisions and
  -- time one request, in the place of the first, and cuts one of more than 2,000
  -- subjects into pieces of 2,000 (sprintfn jMerge): one pass over the requests.
  local function merge(list)
    local out, at = {}, {}
    for _, r in ipairs(list) do
      if not STATE_OPS[r.op] then
        out[#out + 1] = r
      else
        local subjects = dedup(r.subjects)
        local k = merge_key(r)
        local i = at[k]
        if i then
          local dst = out[i].subjects
          for _, s in ipairs(subjects) do dst[#dst + 1] = s end
        else
          out[#out + 1] = copy_req(r, subjects)
          at[k] = #out
        end
      end
    end
    local cut = {}
    for _, r in ipairs(out) do
      if not STATE_OPS[r.op] or #r.subjects <= B.subjects then
        cut[#cut + 1] = r
      else
        local i = 1
        while i <= #r.subjects do
          local last = math.min(i + B.subjects - 1, #r.subjects)
          local piece = {}
          for j = i, last do piece[#piece + 1] = r.subjects[j] end
          cut[#cut + 1] = copy_req(r, piece)
          i = last + 1
        end
      end
    end
    return cut
  end

  -- meta_of is the line's meta: an object of strings and one list of strings,
  -- no numbers (L2 1.1), each key left out when empty. The list is marked a
  -- JSON array (S.array): Layer 2's body encoder reads an unmarked table as an
  -- object and refuses its numeric keys, so the store refused every note with
  -- decisions REQUEST while the twin, which has no such mark, took it.
  local function meta_of(S, g)
    local r = g.req
    local m = {op = r.op, type = r.type}
    if r.cause ~= '' then m.cause = r.cause end
    if r.text ~= '' then m.text = r.text end
    if #r.decisions > 0 then
      local list = S.array()
      for i = 1, #r.decisions do list[i] = r.decisions[i] end
      m.decisions = list
    end
    if g.existing ~= '' then m.note = note_of(g.existing) end
    if r.op == 'open' then
      m.kind = 'judgment'
    elseif r.op == 'update' then
      m.kind = 'judgment'
      m.verb = 'updated'
    elseif r.op == 'close' or r.op == 'unhold' then
      m.kind = 'decided'
    elseif r.op == 'know' then
      m.kind = 'happened'
    elseif r.op == 'tickend' then
      m.kind = TICK_END
      m.to = TICK_END_TO
    end
    if r.op == 'hold' or r.op == 'review' then m['until'] = num(r['until']) end
    return m
  end

  -- endings are the requests that close the lateness judgments of the timed
  -- states the step's entries end (sprintfn jEndings), each on the subject R11
  -- raised it on: the card's id, or for a kind keyed by row the stream's subject.
  local function cell_of(ref)
    local row, col = string.match(ref, '^(.*):([^:]*)$')
    if row then return row, col end
    return ref, ''
  end
  local function card_id(stored)
    local head = string.match(stored, '^(.+)~[^~]*$')
    return head or stored
  end
  -- ends says whether an entry takes the ith card out of the due kind's state,
  -- and with which reason: the kind's own for where a move goes (sprintfn jEnds).
  local function ends(e, i, k, rec)
    if e.kind == 'remove' then return 'removed', true end
    if e.to and e.to ~= '' and e.to ~= (e.from or '') then
      local row, col = cell_of(e.to)
      if col ~= k.col or (k.of_row and row ~= rec.place.row) then return k.moved[col] or k.other, true end
    end
    for _, f in ipairs(e.unset or {}) do
      if f == k.field then return k.cleared, true end
    end
    if e.set and e.set[k.field] ~= nil and e.set[k.field] == '' then return k.cleared, true end
    local each = e.each and e.each[i]
    if type(each) == 'table' and each[k.field] ~= nil and each[k.field] == '' then return k.cleared, true end
    return '', false
  end
  local function endings(d, named)
    local S, ctx = d.S, d.ctx
    local entries = ctx.request.entries
    -- the before-state of the due fields of each table's moved cards, through
    -- S.before, which reuses what the pre stage read.
    local obs = {}
    local function observed(table_name)
      if obs[table_name] ~= nil then return obs[table_name], nil end
      local ids, seen, fields = {}, {}, {}
      for _, k in ipairs(SP.j_late) do
        if k.table == table_name then fields[#fields + 1] = k.field end
      end
      for _, e in ipairs(entries or {}) do
        if (e.kind == 'move' or e.kind == 'remove') and e.t == table_name then
          for _, id in ipairs(e.ids) do
            if not seen[id] then seen[id] = true; ids[#ids + 1] = id end
          end
        end
      end
      local got, err = S.before(ctx, table_name, ids, fields)
      if err then return nil, err end
      obs[table_name] = got
      return got, nil
    end
    local order, groups = {}, {}
    for _, e in ipairs(entries or {}) do
      if e.kind == 'move' or e.kind == 'remove' then
        for _, k in ipairs(SP.j_late) do
          if k.table == e.t then
            local got, err = observed(e.t)
            if err then return nil, err end
            for i, id in ipairs(e.ids) do
              local rec = got[id]
              if rec and rec.exists and type(rec.place) == 'table' and rec.place.col == k.col then
                local due = rec.fields and rec.fields[k.field]
                if due and due.present and due.value ~= '' then
                  local reason, yes = ends(e, i, k, rec)
                  if yes then
                    local subject = card_id(id)
                    if k.of_row then subject = SP.j_stream_prefix .. rec.place.row end
                    if not named[k.type .. '\0' .. k.kind .. '\0' .. subject] then
                      local key = k.kind .. '\0' .. reason
                      if not groups[key] then groups[key] = {kind = k.kind, reason = reason, type = k.type, subjects = {}}; order[#order + 1] = key end
                      local g = groups[key]
                      g.subjects[#g.subjects + 1] = subject
                    end
                  end
                end
              end
            end
          end
        end
      end
    end
    local out = {}
    for _, key in ipairs(order) do
      local g = groups[key]
      out[#out + 1] = {op = 'close', type = g.type, cause = g.kind, subjects = dedup(g.subjects), text = g.reason,
        decisions = {}, ['until'] = 0}
    end
    return out, nil
  end

  -- effects is what a list of notes does to the counts and to askwait (sprintfn
  -- jEffectsOf): how many open subjects a close or a hold takes off each note's
  -- count, how many a hold puts in the held count and a close or an unhold of
  -- held subjects takes out, and the subjects whose "cannot ask" fields leave.
  local function effects(jnotes)
    local e = {taken = {}, taken_order = {}, held = {}, held_order = {}, leaving = {}, leave_order = {}, staying = {}}
    for _, n in ipairs(jnotes) do
      local req = n.req
      local is_held = n.existing ~= '' and string.sub(n.existing, 1, 1) == HOLD
      if n.existing ~= '' and not is_held and (req.op == 'close' or req.op == 'hold') then
        if not e.taken[n.existing] then e.taken[n.existing] = 0; e.taken_order[#e.taken_order + 1] = n.existing end
        e.taken[n.existing] = e.taken[n.existing] + #req.subjects
      end
      if n.existing ~= '' and (req.op == 'hold' or (is_held and (req.op == 'close' or req.op == 'unhold'))) then
        local note = note_of(n.existing)
        local h = e.held[note]
        if not h then
          h = {enter = 0, leave = 0, type = req.type}
          e.held[note] = h
          e.held_order[#e.held_order + 1] = note
        end
        if req.op == 'hold' then h.enter = h.enter + #req.subjects else h.leave = h.leave + #req.subjects end
      end
      if req.type == CANNOT_ASK then
        local field = field_of(req.type, req.cause)
        if req.op == 'open' then
          for _, s in ipairs(req.subjects) do e.staying[s] = true end
        elseif req.op == 'close' or req.op == 'unhold' then
          for _, s in ipairs(req.subjects) do
            if not e.leaving[s] then e.leaving[s] = {}; e.leave_order[#e.leave_order + 1] = s end
            e.leaving[s][field] = true
          end
        end
      end
    end
    return e
  end
  -- drops is the subjects that leave askwait in this step: those whose last
  -- "cannot ask" field, of any cause, a close or an unhold takes, with none
  -- opened on them in the same step. fields(s) is their jopen fields.
  local function drops_of(e, fields)
    local out = {}
    for _, s in ipairs(e.leave_order) do
      if not e.staying[s] then
        local keep = false
        for name in pairs(fields(s) or {}) do
          if string.sub(name, 1, #CANNOT_ASK + 1) == CANNOT_ASK .. SEP and not e.leaving[s][name] then keep = true; break end
        end
        if not keep then out[#out + 1] = s end
      end
    end
    return out
  end

  -- j_decide is J's pre stage: requests (decoded note requests of the sprint
  -- half, and those derive made) to the step's notes and J's plan.
  function SP.j_decide(ctx, reqs, obs)
    local S = layer_one()
    local d = {S = S, ctx = ctx, cells = {}, all = {}, overdue = {}}
    local list = {}
    for i, r in ipairs(reqs) do
      list[i] = normalize(r)
      local err = check(S, i - 1, list[i])
      if err then return nil, nil, err end
    end
    local named = {}
    for i, r in ipairs(list) do
      if STATE_OPS[r.op] then
        for _, s in ipairs(dedup(r.subjects)) do
          local k = r.type .. '\0' .. r.cause .. '\0' .. s
          if named[k] then return nil, nil, refuse_req(S, i - 1, 'names a type, cause and subject that another request of the step names') end
          named[k] = true
        end
      end
    end
    local auto, err = endings(d, named)
    if err then return nil, nil, err end
    for _, r in ipairs(auto) do list[#list + 1] = r end
    list = merge(list)
    local notes, jnotes = {}, {}
    local base = #ctx.notes
    local about, opens = 0, false
    for i, r in ipairs(list) do
      local groups
      groups, err = decide(d, r)
      if err then return nil, nil, err end
      for _, g in ipairs(groups) do
        if #notes == B.notes then return nil, nil, refuse_limit(S, i - 1, 'notes', B.notes, B.notes + 1) end
        opens = opens or g.req.op == 'open'
        about = about + #g.req.subjects
        if about > B.about then return nil, nil, refuse_limit(S, i - 1, 'about', B.about, about) end
        local arr = S.array()
        for _, s in ipairs(g.req.subjects) do arr[#arr + 1] = s end
        notes[#notes + 1] = {line = {kind = 'note', meta = meta_of(S, g)}, about = arr}
        jnotes[#jnotes + 1] = {index = base + #notes, req = g.req, existing = g.existing, digest = g.digest}
      end
    end
    local eff = effects(jnotes)
    local have_n, have_h, fields, quar = {}, {}, {}, {}
    -- A close or a hold of open subjects takes them off their note's count: it
    -- must have the count, and one to take them from (jn agrees with jopen).
    for _, note in ipairs(eff.taken_order) do
      local have
      have, err = count_of(d, note)
      if err then return nil, nil, err end
      if have < eff.taken[note] then return nil, nil, S.refuse('DRIFT', {}) end
      have_n[note] = have
    end
    -- A hold puts subjects in the note's held count, and a close or an unhold of
    -- held subjects takes them out (jh agrees with jopen).
    for _, note in ipairs(eff.held_order) do
      local h = eff.held[note]
      local have
      have, err = held_of(d, note, h.leave ~= 0)
      if err then return nil, nil, err end
      if have < h.leave then return nil, nil, S.refuse('DRIFT', {}) end
      have_h[note] = have
    end
    -- The fields of each subject that may leave askwait, and the primaries a
    -- "cannot ask" opens on that are quarantined.
    for _, s in ipairs(eff.leave_order) do
      fields[s], err = jopen_fields(d, s)
      if err then return nil, nil, err end
    end
    local asking, seen = {}, {}
    for _, n in ipairs(jnotes) do
      if n.req.op == 'open' and n.req.type == CANNOT_ASK then
        for _, s in ipairs(n.req.subjects) do
          if not seen[s] then seen[s] = true; asking[#asking + 1] = s end
        end
      end
    end
    err = quarantined(d, asking, quar)
    if err then return nil, nil, err end
    local r
    if opens then
      r, err = running(d)
      if err then return nil, nil, err end
    end
    return notes, {notes = jnotes, have_n = have_n, have_h = have_h, fields = fields, quar = quar, r = r}, nil
  end

  -- j_cmds is J's X.plan: the commands of the plan, in the order A1 wants:
  -- what records owed work before the state it is owed for, and what forgets a
  -- trigger after. It reads nothing: everything it needs j_decide read. A note
  -- with no seq writes nothing, and neither do its counts, holds or askwait: they
  -- are worked out over the notes that have a line.
  function SP.j_cmds(ctx, jp, lp)
    local S = layer_one()
    local records, states, after, seqs = {}, {}, {}, {}
    local function add(list, command, key, kind, args)
      local argv = {command, key}
      for _, a in ipairs(args) do argv[#argv + 1] = a end
      local desc, err = S.writecmd(ctx, argv, {{key = key, kind = kind, mode = 'write'}})
      if err then error(S.json.encode(err), 0) end
      list[#list + 1] = desc
    end
    local function pairs_of(list, key, score, members)
      local i = 1
      while i <= #members do
        local args = {}
        local last = math.min(i + B.piece - 1, #members)
        for j = i, last do args[#args + 1] = score; args[#args + 1] = members[j] end
        add(list, 'ZADD', key, 'zset', args)
        i = last + 1
      end
    end
    local function members_of(list, key, members)
      local i = 1
      while i <= #members do
        local args = {}
        local last = math.min(i + B.piece - 1, #members)
        for j = i, last do args[#args + 1] = members[j] end
        add(list, 'ZREM', key, 'zset', args)
        i = last + 1
      end
    end
    local due, jnotes, jn = jkey(ctx, 'due'), jkey(ctx, 'jnotes'), jkey(ctx, 'jn')
    local jh, jtext = jkey(ctx, 'jh'), jkey(ctx, 'jtext')
    local askwait = jkey(ctx, 'askwait')
    local seqed = {}
    for _, n in ipairs(jp.notes) do
      local seq = lp.note_seqs and lp.note_seqs[n.index]
      if type(seq) == 'string' and S.uint(seq) then
        seqed[#seqed + 1] = n
        seqs[#seqs + 1] = seq
      end
    end
    if #seqs == 0 then return {commands = {}} end
    local eff = effects(seqed)
    local r = jp.r or 0
    local rs = num(r)
    local empty = digest('', {})
    for i, n in ipairs(seqed) do
      local seq = seqs[i]
      local req = n.req
      local field = field_of(req.type, req.cause)
      local untilms = num(req['until'])
      if req.op == 'open' then
        local id = note_id(ctx, seq)
        if req.type ~= DONE then
          add(records, 'ZADD', due, 'zset', {num(r + B.overdue_ms), 'overdue:' .. id})
        end
        add(records, 'ZADD', jnotes, 'zset', {rs, id})
        add(records, 'HSET', jn, 'hash', {id, string.format('%d', #req.subjects)})
        if n.digest ~= empty then add(records, 'HSET', jtext, 'hash', {id, n.digest}) end
        if req.type == CANNOT_ASK then
          local put = {}
          for _, s in ipairs(req.subjects) do
            if not jp.quar[s] then put[#put + 1] = s end
          end
          pairs_of(records, askwait, rs, put)
        end
        for _, s in ipairs(req.subjects) do
          add(states, 'HSET', jkey(ctx, 'jopen:' .. s), 'hash', {field, id})
        end
      elseif req.op == 'update' then
        add(records, 'HSET', jtext, 'hash', {n.existing, n.digest})
      elseif req.op == 'hold' then
        local id = note_of(n.existing)
        if req.type == STOPPED then
          add(records, 'HSET', clock_key(ctx), 'hash', {'stophold_ms', untilms})
        else
          add(records, 'ZADD', due, 'zset', {untilms, 'hold:' .. id})
        end
        for _, s in ipairs(req.subjects) do
          add(states, 'HSET', jkey(ctx, 'jopen:' .. s), 'hash', {field, HOLD .. id})
        end
      elseif req.op == 'close' or req.op == 'unhold' then
        for _, s in ipairs(req.subjects) do
          add(states, 'HDEL', jkey(ctx, 'jopen:' .. s), 'hash', {field})
        end
      elseif req.op == 'review' then
        if req.type ~= DONE then
          add(records, 'ZADD', due, 'zset', {untilms, 'overdue:' .. note_of(n.existing)})
        end
      end
    end
    -- The held counts a hold raises are recorded before the fields become holds;
    -- the ones a close or an unhold lowers are forgotten after the fields go, and
    -- a count that reaches zero takes the hold entry with it (or the STOPPED
    -- judgment's stophold_ms, which is that judgment's hold).
    for _, note in ipairs(eff.held_order) do
      local h = eff.held[note]
      local net = (jp.have_h[note] or 0) + h.enter - h.leave
      local into = after
      if h.enter ~= 0 then into = records end
      if net > 0 then
        add(into, 'HSET', jh, 'hash', {note, string.format('%d', net)})
      else
        add(into, 'HDEL', jh, 'hash', {note})
        if h.type == STOPPED then
          add(into, 'HDEL', clock_key(ctx), 'hash', {'stophold_ms'})
        else
          add(into, 'ZREM', due, 'zset', {'hold:' .. note})
        end
      end
    end
    local head = {}
    local i = 1
    while i <= #seqs do
      local last = math.min(i + B.piece - 1, #seqs)
      local args = {}
      for j = i, last do args[#args + 1] = seqs[j] end
      add(head, 'RPUSH', jkey(ctx, 'notes'), 'list', args)
      i = last + 1
    end
    members_of(after, askwait, drops_of(eff, function(s) return jp.fields[s] end))
    for _, id in ipairs(eff.taken_order) do
      local left = jp.have_n[id] - eff.taken[id]
      if left > 0 then
        add(after, 'HSET', jn, 'hash', {id, string.format('%d', left)})
      else
        add(after, 'HDEL', jn, 'hash', {id})
        add(after, 'ZREM', jnotes, 'zset', {id})
        add(after, 'ZREM', due, 'zset', {'overdue:' .. id})
        add(after, 'HDEL', jtext, 'hash', {id})
      end
    end
    local out = {}
    for _, list in ipairs({head, records, states, after}) do
      for _, c in ipairs(list) do out[#out + 1] = c end
    end
    return {commands = out}
  end

  SP.phase('j_decide', SP.j_decide)
  SP.phase('j_cmds', SP.j_cmds)
end
end
