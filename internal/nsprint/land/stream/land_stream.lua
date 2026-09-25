-- land_stream.lua: the stream lander's Redis writes (nova-tools#3598, #3611-#3613).
-- One EVAL script, self-contained (not part of the nova_sprint function
-- library), so every write the landing makes is one atomic call. ARGV[1] is
-- the op, ARGV[2] a JSON payload of strings. Keys (rowan-new specs/ws-index.md and the
-- pr record contract):
--   pr:<name>:<n>            hash  repo, n, head, base, base_sha, state, ci, mergeable,
--                                  stream, task, kind, reads (typed lines, newline-joined),
--                                  created_at, updated_at; park, landed_with, close on moves
--   land:<repo>:<slug>       hash  the stream landing: streams, slug, base, base_sha, branch,
--                                  head, members, tasks, parked, pr, state, tests, at
--   land:<repo>:streams      set   every slug with a land hash (status reads it, no scan)
--   ws:<stream>:merging|working|landed  zset  task ids (ws-index)
--   task:<id>                hash  state, state_at
--   ws:log                   stream one entry per move: id, stream, from, to, by, why, at
--
-- ops:
--   record  {repo,n,now,fields{...}}          create or update pr:<repo>:<n>
--   line    {repo,n,now,line}                 append one typed line to reads
--   built   {repo,slug,now,by,land{...},stream_pr{n,fields{...}}?,parked[{task,stream,n,why}]}
--   landed  {repo,slug,now,by,merge_sha,why,members[{task,stream,n,close}],pr}
local op = ARGV[1]
local p = cjson.decode(ARGV[2])

-- prkey mirrors internal/nsprint/prkey.Key: the bare repository name, so
-- owner/name and name hit one record.
local function prkey(repo, n) return 'pr:' .. (string.match(repo, '([^/]+)$') or repo) .. ':' .. n end
local function landkey(repo, slug) return 'land:' .. repo .. ':' .. slug end

local function hset(key, fields)
  local args = {}
  for k, v in pairs(fields) do
    args[#args + 1] = k
    args[#args + 1] = tostring(v)
  end
  if #args > 0 then redis.call('HSET', key, unpack(args)) end
end

local function wslog(id, stream, from, to, by, why, at)
  redis.call('XADD', 'ws:log', '*', 'id', id, 'stream', stream, 'from', from, 'to', to,
    'by', by, 'why', why, 'at', at)
end

-- move one task between two ws sets of its stream; returns 1 when it was in `from`.
local function move(id, stream, from, to, score, by, why, at)
  if id == nil or id == '' or stream == nil or stream == '' then return 0 end
  local fromkey = 'ws:' .. stream .. ':' .. from
  if not redis.call('ZSCORE', fromkey, id) then return 0 end
  redis.call('ZREM', fromkey, id)
  redis.call('ZADD', 'ws:' .. stream .. ':' .. to, score, id)
  redis.call('HSET', 'task:' .. id, 'state', to, 'state_at', at)
  wslog(id, stream, from, to, by, why, at)
  return 1
end

if op == 'record' then
  local k = prkey(p.repo, p.n)
  local f = p.fields or {}
  local exists = redis.call('EXISTS', k) == 1
  if not exists then
    if (f.head or '') == '' or (f.base or '') == '' or (f.stream or '') == '' then
      return {'REFUSED', 'no record ' .. k .. ': a new record needs --head, --base and --stream'}
    end
    redis.call('HSET', k, 'repo', p.repo, 'n', p.n, 'state', 'open', 'ci', 'pending',
      'mergeable', '', 'reads', '', 'created_at', p.now)
  end
  local old = redis.call('HGET', k, 'head')
  if (f.head or '') ~= '' and old and old ~= '' and old ~= f.head then
    -- a new head: CI and mergeable were for the old one
    if (f.ci or '') == '' then f.ci = 'pending' end
    if (f.mergeable or '') == '' then redis.call('HSET', k, 'mergeable', '') end
  end
  local set = {}
  for key, v in pairs(f) do if v ~= '' then set[key] = v end end
  set.updated_at = p.now
  hset(k, set)
  local r = redis.call('HMGET', k, 'head', 'base', 'stream', 'ci', 'mergeable', 'state')
  return {'OK', exists and '0' or '1', r[1] or '', r[2] or '', r[3] or '', r[4] or '', r[5] or '', r[6] or ''}
end

if op == 'line' then
  local k = prkey(p.repo, p.n)
  if redis.call('EXISTS', k) == 0 then
    return {'REFUSED', 'no record ' .. k .. ': run pr record first'}
  end
  local old = redis.call('HGET', k, 'reads') or ''
  local reads = p.line
  if old ~= '' then reads = old .. '\n' .. p.line end
  redis.call('HSET', k, 'reads', reads, 'updated_at', p.now)
  local n = 1
  for _ in string.gmatch(reads, '\n') do n = n + 1 end
  return {'OK', tostring(n)}
end

if op == 'built' then
  local lk = landkey(p.repo, p.slug)
  local moved = 0
  for _, m in ipairs(p.parked or {}) do
    moved = moved + move(m.task, m.stream, 'merging', 'working', p.now, p.by, m.why, p.now)
    if (m.n or '') ~= '' then
      local pk = prkey(p.repo, m.n)
      if redis.call('EXISTS', pk) == 1 then
        redis.call('HSET', pk, 'state', 'parked', 'park', m.why, 'updated_at', p.now)
      end
    end
  end
  redis.call('DEL', lk)
  hset(lk, p.land)
  redis.call('SADD', 'land:' .. p.repo .. ':streams', p.slug)
  if p.stream_pr and (p.stream_pr.n or '') ~= '' then
    hset(prkey(p.repo, p.stream_pr.n), p.stream_pr.fields)
  end
  if NS.ci and NS.ci.request and (p.land.head or '') ~= '' then
    local pr_n = ''
    if p.stream_pr and p.stream_pr.n then pr_n = p.stream_pr.n end
    NS.ci.request({}, {p.repo, p.land.head, pr_n, '', ''})
  end
  return {'OK', tostring(moved), tostring(#(p.parked or {}))}
end

if op == 'landed' then
  local lk = landkey(p.repo, p.slug)
  local state = redis.call('HGET', lk, 'state')
  if state == 'merged' then return {'ALREADY', redis.call('HGET', lk, 'merge_sha') or ''} end
  if state ~= 'open' then return {'REFUSED', 'land ' .. lk .. ' state is ' .. tostring(state) .. ', not open'} end
  local moved, missing = 0, 0
  for _, m in ipairs(p.members or {}) do
    local ok = move(m.task, m.stream, 'merging', 'landed', p.now, p.by, m.close, p.now)
    moved = moved + ok
    if ok == 0 then missing = missing + 1 end
    local pk = prkey(p.repo, m.n)
    if redis.call('EXISTS', pk) == 1 then
      redis.call('HSET', pk, 'state', 'landed', 'landed_with', p.repo .. '#' .. p.pr,
        'close', m.close, 'updated_at', p.now)
    end
  end
  redis.call('HSET', lk, 'state', 'merged', 'merge_sha', p.merge_sha, 'merged_at', p.now)
  local sk = prkey(p.repo, p.pr)
  if redis.call('EXISTS', sk) == 1 then
    redis.call('HSET', sk, 'state', 'merged', 'merge_sha', p.merge_sha, 'updated_at', p.now)
  end
  wslog(lk, redis.call('HGET', lk, 'streams') or '', 'merging', 'landed', p.by, p.why, p.now)
  return {'OK', tostring(moved), tostring(missing)}
end

return {'REFUSED', 'unknown op ' .. tostring(op)}
