-- The waiting-resolve duty's pass in one call (Glenn 2026-09-27: "You
-- always need to batch redis"; the pass it replaced made five dependent round trips for it). The same
-- rounds, in the server: every stream of ws:order a pit stop does not hold,
-- each stream's waiting set under the epoch, each waiter's DEPENDS-ON, the
-- records those name (a task, or every stream member that names a repo#n),
-- then the verdict: a waiter whose every entry is met moves waiting ->
-- ready through the one task move, grouped by its DEPENDS-ON text so the
-- ws:log why names the entries met. A stream's waiting sentinel with no
-- live card left is named ready for the coordinator's acceptance (#4412)
-- and never moved here. A plan (kind plan, #4317) is never released.
--
-- No shebang: the loader prepends the library header; this file sorts after
-- 02_card_move.lua and reaches it through NS.
do
  local function str(v)
    if v == nil or v == false then return '' end
    return tostring(v)
  end

  local function trim(s)
    return (string.gsub(string.gsub(str(s), '^%s+', ''), '%s+$', ''))
  end

  -- split_deps is ws.SplitDeps: entries split on commas, semicolons and
  -- white space, none and - dropped, each once.
  local function split_deps(text)
    local parts = {}
    for p in string.gmatch(str(text), '[^,;%s]+') do parts[#parts + 1] = p end
    if #parts == 1 and (parts[1] == 'none' or parts[1] == '-') then return {} end
    local seen, out = {}, {}
    for _, p in ipairs(parts) do
      if p ~= '' and p ~= 'none' and p ~= '-' and not seen[p] then
        seen[p] = true
        out[#out + 1] = p
      end
    end
    return out
  end

  -- ref_key is reconcile.wrRefKey: owner/repo#n (the owner defaulting to
  -- mas-bandwidth, prkey.DefaultOwner), lower-cased, for an owner/repo#n,
  -- repo#n or GitHub issue/PR URL; '' for anything else.
  local function ref_key(s)
    s = trim(s)
    local owner, name, n = string.match(s, '^([%w_.-]+)/([%w_.-]+)#(%d+)$')
    if not owner then
      name, n = string.match(s, '^([%w_.-]+)#(%d+)$')
      if name then owner = '' end
    end
    if not name then
      local head = string.match(s, '^https?://github%.com/[%w_.-]+/[%w_.-]+/issues/%d+') or
        string.match(s, '^https?://github%.com/[%w_.-]+/[%w_.-]+/pull/%d+')
      if head then
        local rest = string.sub(s, #head + 1)
        if rest == '' or string.match(rest, '^[/?#]') then
          owner, name, n = string.match(head, '^https?://github%.com/([%w_.-]+)/([%w_.-]+)/%a+/(%d+)$')
        end
      end
    end
    if not name then return '' end
    if owner == '' then owner = 'mas-bandwidth' end
    return string.lower(owner .. '/' .. name .. '#' .. n)
  end

  -- is_id is reconcile.wrIDRE: a task id, or a stream sentinel's.
  local function is_id(s)
    return string.match(s, '^[%w][%w._-]*$') ~= nil or string.match(s, '^[a-z0-9][a-z0-9-]*:sentinel$') ~= nil
  end

  -- parse_deps is reconcile.wrParse: the entries with their kind (task,
  -- ref, or neither) and key; none is true for "none" or "-" alone.
  local function parse_deps(text)
    local parts = split_deps(text)
    if #parts == 0 then
      local t = trim(text)
      return {}, t == 'none' or t == '-'
    end
    local deps = {}
    for _, p in ipairs(parts) do
      local dep = { raw = p, kind = '', key = '' }
      local id = string.match(p, '^task:(.*)$')
      if id then
        if is_id(id) then dep.kind, dep.key = 'task', id end
      else
        local k = ref_key(p)
        if k ~= '' then
          dep.kind, dep.key = 'ref', k
        elseif is_id(p) and p ~= 'none' then
          dep.kind, dep.key = 'task', p
        end
      end
      deps[#deps + 1] = dep
    end
    return deps, false
  end

  local function landed(state, where, ok)
    return state == 'landed' or where == 'landed' or (where == 'done' and ok ~= 'fail')
  end

  -- full is prkey.Full: owner/name, a bare name under the default owner;
  -- nil for anything else.
  local function full(repo)
    repo = trim(repo)
    if repo == '' or string.find(repo, '[ :\t\r\n]') then return nil end
    local owner, name = string.match(repo, '^([^/]+)/([^/]+)$')
    if not owner then
      if string.find(repo, '/', 1, true) then return nil end
      owner, name = 'mas-bandwidth', repo
    end
    return owner .. '/' .. name
  end

  -- names is reconcile.wrNames: the ref keys a member's pr, ref and origin
  -- fields name, and its pr number under its repo (or its ref's).
  local function names(pr, ref, origin, repo)
    local out = {}
    for _, s in ipairs({ ref, origin, pr }) do
      local k = ref_key(s)
      if k ~= '' then out[#out + 1] = k end
    end
    local n = trim(pr)
    if string.sub(n, 1, 1) == '#' then n = string.sub(n, 2) end
    if n ~= '' and string.match(n, '^%d+$') then
      local r = repo
      if r == '' then
        local k = ref_key(ref)
        if k ~= '' then r = string.sub(k, 1, (string.find(k, '#[^#]*$') or 1) - 1) end
      end
      if r ~= '' then
        local f = full(r)
        if f then out[#out + 1] = string.lower(f .. '#' .. n) end
      end
    end
    return out
  end

  -- ns_waiting_resolve_pass actor (only <n> <stream>... | all <n> <lifted>...)...
  -- The holds are the pass's pit stops (pitstop.Holds): a stop naming
  -- streams holds those; a whole stop holds every stream but the lifted.
  -- Reply, flat rows, the streams in ws:order:
  --   line <stream> <stop id or ''>     one per stream with waiters or a ready stop
  --   still <stream> <n>                 the waiters left waiting
  --   on <stream> <entry>                an unmet entry with a record
  --   unknown <stream> <entry>           an entry with no record
  --   ready <stream> <id>                moved waiting -> ready
  --   refused <stream> <id> <why>        the move refused (left waiting, in still)
  --   stitch <id> <why or ''>            a released stitch; its brief written here (NS.stitch.write), or why not
  local function resolve_pass(keys, args)
    local actor = str(args[1])
    if actor == '' then actor = 'reconciler' end
    local holds, i = {}, 2
    while args[i] do
      local kind, n = args[i], tonumber(args[i + 1]) or 0
      local set = {}
      for j = 1, n do set[args[i + 1 + j]] = true end
      holds[#holds + 1] = { kind = kind, set = set }
      i = i + 2 + n
    end
    local function held(s)
      for _, h in ipairs(holds) do
        if h.kind == 'only' and h.set[s] then return true end
        if h.kind == 'all' and not h.set[s] then return true end
      end
      return false
    end
    local out = {}
    local function emit(...)
      local n, t = select('#', ...), { ... }
      for k = 1, n do out[#out + 1] = str(t[k]) end
    end
    -- round 1: the streams in rank order, the held ones left out
    local streams = {}
    for _, s in ipairs(redis.call('ZRANGE', 'ws:order', 0, -1)) do
      if not held(s) then streams[#streams + 1] = s end
    end
    if #streams == 0 then return out end
    local e = NS.card.epoch()
    -- round 2: every stream's waiting set under the epoch, oldest first
    local waiters, stops, any_stop = {}, {}, false
    for _, s in ipairs(streams) do
      for _, id in ipairs(redis.call('ZRANGE', NS.card.wskey(e, s, 'waiting'), 0, -1)) do
        if NS.task.is_sentinel(id) then
          stops[s] = id
          any_stop = true
        else
          waiters[#waiters + 1] = { id = id, stream = s }
        end
      end
    end
    -- a waiting stop with no live card left is ready for acceptance
    local ready_stop = {}
    for s, sid in pairs(stops) do
      local n = 0
      for _, w in ipairs({ 'waiting', 'ready', 'working', 'review', 'merging', 'parked' }) do
        for _, m in ipairs(redis.call('ZRANGE', NS.card.wskey(e, s, w), 0, -1)) do
          if m ~= sid then n = n + 1 end
        end
      end
      ready_stop[s] = n == 0
    end
    if #waiters == 0 and not any_stop then return out end
    -- round 3: each waiter's DEPENDS-ON, phase and kind; a plan is no waiter
    local kept = {}
    for _, w in ipairs(waiters) do
      local v = redis.call('HMGET', 'task:' .. w.id, 'blocked_on', 'phase', 'kind')
      if str(v[3]) ~= 'plan' then
        w.blocked_on = trim(v[1])
        w.stitch = str(v[2]) == 'stitch'
        w.deps, w.none = parse_deps(w.blocked_on)
        kept[#kept + 1] = w
      end
    end
    waiters = kept
    local lines, by_stream = {}, {}
    local function line_of(s)
      if not by_stream[s] then
        by_stream[s] = { stream = s, stop = '', ready = {}, still = 0, on = {}, unknown = {}, refused = {}, seen = {} }
        lines[#lines + 1] = by_stream[s]
      end
      return by_stream[s]
    end
    for _, s in ipairs(streams) do
      if ready_stop[s] then line_of(s).stop = stops[s] end
    end
    local function flush()
      for _, l in ipairs(lines) do
        emit('line', l.stream, l.stop)
        emit('still', l.stream, l.still)
        table.sort(l.on)
        table.sort(l.unknown)
        for _, d in ipairs(l.on) do emit('on', l.stream, d) end
        for _, d in ipairs(l.unknown) do emit('unknown', l.stream, d) end
        for _, id in ipairs(l.ready) do emit('ready', l.stream, id) end
        for _, x in ipairs(l.refused) do emit('refused', l.stream, x[1], x[2]) end
      end
      return out
    end
    if #waiters == 0 then return flush() end
    -- round 4: the task records named and, when a repo#n is named, every
    -- stream member (the tasks that can name it)
    local task_deps, ref_deps, any_ref = {}, {}, false
    for _, w in ipairs(waiters) do
      for _, d in ipairs(w.deps) do
        if d.kind == 'task' then
          task_deps[d.key] = 'unknown'
        elseif d.kind == 'ref' then
          ref_deps[d.key] = 'unknown'
          any_ref = true
        end
      end
    end
    for id in pairs(task_deps) do
      local v = redis.call('HMGET', 'task:' .. id, 'state', 'where', 'where_ok')
      local state, where, ok = str(v[1]), str(v[2]), str(v[3])
      if state == '' and where == '' then
        task_deps[id] = 'unknown'
      elseif NS.task.is_sentinel(id) then
        task_deps[id] = (state == 'landed' or where == 'landed') and 'met' or 'unmet'
      elseif landed(state, where, ok) then
        task_deps[id] = 'met'
      else
        task_deps[id] = 'unmet'
      end
    end
    -- round 5: the members' names and whether each landed
    if any_ref then
      local seen, members = {}, {}
      for _, s in ipairs(streams) do
        for _, w in ipairs({ 'waiting', 'ready', 'working', 'review', 'merging', 'landed', 'parked' }) do
          for _, m in ipairs(redis.call('ZRANGE', NS.card.wskey(e, s, w), 0, -1)) do
            if not seen[m] then
              seen[m] = true
              members[#members + 1] = m
            end
          end
        end
      end
      for _, m in ipairs(members) do
        local v = redis.call('HMGET', 'task:' .. m, 'state', 'where', 'where_ok', 'pr', 'ref', 'origin', 'repo')
        local is_landed = landed(str(v[1]), str(v[2]), str(v[3]))
        for _, k in ipairs(names(str(v[4]), str(v[5]), str(v[6]), str(v[7]))) do
          local st = ref_deps[k]
          if st and st ~= 'met' then ref_deps[k] = is_landed and 'met' or 'unmet' end
        end
      end
    end
    -- the verdicts, grouped by stream and DEPENDS-ON text
    local groups, group_of = {}, {}
    for _, w in ipairs(waiters) do
      local r = line_of(w.stream)
      local met = w.none
      if not w.none and #w.deps > 0 then
        met = true
        for _, d in ipairs(w.deps) do
          local st = 'unknown'
          if d.kind == 'task' then st = task_deps[d.key] elseif d.kind == 'ref' then st = ref_deps[d.key] end
          if st ~= 'met' then
            local list = st == 'unmet' and r.on or r.unknown
            local key = (st == 'unmet' and 'on:' or 'unknown:') .. d.raw
            if not r.seen[key] then
              r.seen[key] = true
              list[#list + 1] = d.raw
            end
            met = false
          end
        end
      end
      if not met then
        r.still = r.still + 1
      else
        local gk = w.stream .. '\0' .. w.blocked_on
        local g = group_of[gk]
        if not g then
          local why = 'depends-on met: none'
          if not w.none then
            local raws = {}
            for _, d in ipairs(w.deps) do raws[#raws + 1] = d.raw end
            why = 'depends-on met: ' .. table.concat(raws, ',')
          end
          g = { stream = w.stream, why = why, ids = {}, stitches = {} }
          group_of[gk] = g
          groups[#groups + 1] = g
        end
        g.ids[#g.ids + 1] = w.id
        if w.stitch then g.stitches[#g.stitches + 1] = w.id end
      end
    end
    -- the moves, through the one task move (W.move_one's refusals kept)
    for _, g in ipairs(groups) do
      local r = line_of(g.stream)
      local refused = {}
      for _, id in ipairs(g.ids) do
        local p = NS.task.read(id)
        local err
        if not p then
          err = 'no task ' .. id
        elseif p.stream == '' then
          err = 'task ' .. id .. ' has no stream'
        else
          err = NS.task.move(id, 'ready', { by = actor, why = g.why })
        end
        if err then
          refused[id] = true
          r.still = r.still + 1
          r.refused[#r.refused + 1] = { id, err }
        else
          r.ready[#r.ready + 1] = id
        end
      end
      for _, id in ipairs(g.stitches) do
        if not refused[id] then
          -- the released stitch starts with the whole picture (#4317): its
          -- brief is generated and written in this call (stitch_brief.lua)
          local text, why = NS.stitch.write(id)
          emit('stitch', id, text and '' or why)
        end
      end
    end
    return flush()
  end

  redis.register_function('ns_waiting_resolve_pass', resolve_pass)
end
