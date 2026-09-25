-- reap.lua: nova-sprint pr reap (nova-tools#3156). The reap decides from the
-- records alone and GitHub's close is its one outward write. Its candidates
-- are the sprint's card PRs, s:<S>:prcard (repo#pr -> card label, written by
-- the harvest), so nothing is scanned. For each it reads the PR record
-- pr:<name>:<n>, the card's origin issue and the sprint tasks naming the PR
-- or that issue (the ref index, NS.tref); read tasks are left out, since a
-- read of the PR is not work that needs the PR kept.
--
-- The reason record is on the PR record itself, written before any close:
--   reap           landed-by | read-under-8 | branch-gone
--   reap_by        what superseded it (repo#m, a reader, the gone mark)
--   reap_why       the evidence, one line
--   reap_head      the head decided at; reap_at ms
--   reap_close     pending | done | failed:<status> | revoked:<why>
--   reap_attempts  close attempts that failed
-- done sets state=closed and closed_at, as the lander's close does. Each
-- write adds one 'pr reap' entry to s:<S>:log.
--
--   ns_pr_reap_read(S)                       read only: the facts, 17 per PR
--   ns_pr_reap(S, key, head, readslen, rule, by, why)
--                                            fenced decision -> OK | DONE | MISSING | STALE|<why>
--   ns_pr_reap_gate(S, key, head, readslen, ref...)
--                                            the check next to one close -> GO | DONE | REVOKED|<why>
--   ns_pr_reap_end(S, key, outcome)          done | failed:<status> | revoked:<why> -> attempts
--
-- The fence is the record's head and the length of its reads field (typed
-- lines only append), so a line or a push after the Go-side read refuses the
-- decision and the next run decides again.

local RP = {
  TR = NS.tref,
  LIVE = { waiting = true, ready = true, working = true, parked = true },
  OVER = { landed = true, merged = true, closed = true },
  FIELDS = { 'state', 'head', 'reads', 'closed_at', 'branch_gone', 'reap', 'reap_close',
    'reap_attempts', 'reap_by', 'reap_why' },
}

function RP.now()
  local t = redis.call('TIME')
  return tostring(tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
end

function RP.log(S, key, what, a, b)
  redis.call('XADD', 's:' .. S .. ':log', 'MAXLEN', '~', '200000', '*', 'kind', 'pr reap', 'id', key,
    'what', what, 'rule', a or '', 'evidence', b or '', 'at', RP.now())
end

-- RP.tasks(refs, repo) -> 'id|state|pr' words of the work tasks naming the
-- refs, once each. state is where when set (the card model), else state; pr
-- is the number the task's pr field names ('' for none).
function RP.tasks(refs, repo)
  local seen, out = {}, {}
  for _, ref in ipairs(refs) do
    local r, n = string.match(ref, '^(.+)#(%d+)$')
    for _, id in ipairs(redis.call('SMEMBERS', RP.TR.key(r, n))) do
      if not seen[id] then
        seen[id] = true
        local f = redis.call('HMGET', 'task:' .. id, 'where', 'state', 'kind', 'pr')
        local st = f[1] or ''
        if st == '' then st = f[2] or '' end
        if f[3] ~= 'read' and string.sub(id, 1, 5) ~= 'read-' then
          local _, pn = RP.TR.parse(f[4] or '', repo)
          out[#out + 1] = id .. '|' .. st .. '|' .. (pn and tostring(tonumber(pn)) or '')
        end
      end
    end
  end
  return table.concat(out, ' ')
end

-- RP.fence(key, head, readslen) -> nil when the record is as read, else the reply.
function RP.fence(key, head, readslen)
  if redis.call('EXISTS', key) == 0 then return 'MISSING' end
  local f = redis.call('HMGET', key, 'state', 'head', 'reads', 'closed_at')
  if RP.OVER[f[1] or ''] or (f[4] or '') ~= '' then return 'DONE' end
  if (f[2] or '') ~= head then return 'STALE|head' end
  if tostring(#(f[3] or '')) ~= readslen then return 'STALE|reads' end
  return nil
end

function RP.pending(key)
  local c = redis.call('HGET', key, 'reap_close') or ''
  return c == 'pending' or string.sub(c, 1, 7) == 'failed:'
end

redis.register_function{
  function_name = 'ns_pr_reap_read',
  flags = { 'no-writes' },
  callback = function(keys, args)
    local S = args[1] or ''
    if S == '' then return redis.error_reply('ns_pr_reap_read: sprint is required') end
    local out = {}
    local pc = redis.call('HGETALL', 's:' .. S .. ':prcard')
    for i = 1, #pc, 2 do
      local repo, n = string.match(pc[i], '^(.+)#(%d+)$')
      if repo then
        local label = pc[i + 1] or ''
        local key = 'pr:' .. RP.TR.bare(repo) .. ':' .. n
        local f = redis.call('HMGET', key, unpack(RP.FIELDS))
        local origin = redis.call('HGET', 's:' .. S .. ':card:' .. label, 'origin') or ''
        local refs = { RP.TR.bare(repo) .. '#' .. n }
        local orepo, on = RP.TR.parse(origin, repo)
        local oref = ''
        if orepo then
          oref = orepo .. '#' .. tostring(tonumber(on))
          if oref ~= refs[1] then refs[2] = oref end
        end
        out[#out + 1] = repo
        out[#out + 1] = n
        out[#out + 1] = label
        out[#out + 1] = tostring(redis.call('EXISTS', key))
        for j = 1, #RP.FIELDS do out[#out + 1] = f[j] or '' end
        out[#out + 1] = oref
        out[#out + 1] = RP.tasks(refs, repo)
        out[#out + 1] = key
      end
    end
    return out
  end,
}

redis.register_function('ns_pr_reap', function(keys, args)
  local S, key, head, readslen = args[1] or '', args[2] or '', args[3] or '', args[4] or ''
  local rule, by, why = args[5] or '', args[6] or '', args[7] or ''
  if S == '' or string.sub(key, 1, 3) ~= 'pr:' or head == '' or rule == '' then
    return redis.error_reply('ns_pr_reap: sprint, pr key, head and rule are required')
  end
  local no = RP.fence(key, head, readslen)
  if no then return no end
  if not RP.pending(key) then
    redis.call('HSET', key, 'reap_close', 'pending', 'reap_attempts', '0')
  end
  redis.call('HSET', key, 'reap', rule, 'reap_by', by, 'reap_why', why, 'reap_head', head, 'reap_at', RP.now())
  RP.log(S, key, 'decided', rule, 'by=' .. by .. ' ' .. why)
  return 'OK'
end)

redis.register_function('ns_pr_reap_gate', function(keys, args)
  local S, key, head, readslen = args[1] or '', args[2] or '', args[3] or '', args[4] or ''
  if S == '' or string.sub(key, 1, 3) ~= 'pr:' then
    return redis.error_reply('ns_pr_reap_gate: sprint and pr key are required')
  end
  local no = RP.fence(key, head, readslen)
  if no == 'DONE' or no == 'MISSING' then return no end
  if not no and not RP.pending(key) then no = 'REVOKED|not pending' end
  if not no then
    local refs = {}
    for i = 5, #args do refs[#refs + 1] = args[i] end
    for w in string.gmatch(RP.tasks(refs, ''), '%S+') do
      local id, st = string.match(w, '^(.-)|(.-)|')
      if RP.LIVE[st] then
        no = 'REVOKED|live ' .. id
        break
      end
    end
  end
  if not no then return 'GO' end
  if string.sub(no, 1, 8) ~= 'REVOKED|' then no = 'REVOKED|' .. string.lower(string.gsub(no, '|', ' ')) end
  redis.call('HSET', key, 'reap_close', 'revoked:' .. string.sub(no, 9))
  RP.log(S, key, 'revoked', '', string.sub(no, 9))
  return no
end)

redis.register_function('ns_pr_reap_end', function(keys, args)
  local S, key, outcome = args[1] or '', args[2] or '', args[3] or ''
  if S == '' or string.sub(key, 1, 3) ~= 'pr:' or outcome == '' then
    return redis.error_reply('ns_pr_reap_end: sprint, pr key and outcome are required')
  end
  if redis.call('EXISTS', key) == 0 then return 'MISSING' end
  local attempts = tonumber(redis.call('HGET', key, 'reap_attempts') or '0') or 0
  if outcome == 'done' then
    local now = RP.now()
    redis.call('HSET', key, 'reap_close', 'done', 'state', 'closed', 'closed_at', now, 'updated_at', now)
  elseif string.sub(outcome, 1, 7) == 'failed:' then
    attempts = redis.call('HINCRBY', key, 'reap_attempts', 1)
    redis.call('HSET', key, 'reap_close', outcome)
  elseif string.sub(outcome, 1, 8) == 'revoked:' then
    redis.call('HSET', key, 'reap_close', outcome)
  else
    return redis.error_reply('ns_pr_reap_end: outcome is done, failed:<status> or revoked:<why>')
  end
  RP.log(S, key, outcome, '', '')
  return tostring(attempts)
end)
