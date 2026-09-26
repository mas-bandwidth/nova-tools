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
--   ws:<stream>:merging|working  zset  task ids (ws-index; built parks a member back)
--   task:<id>                hash  state, state_at
--   ws:log                   stream one entry per move: id, stream, from, to, by, why, at
--
-- ops:
--   record  {repo,n,now,fields{...}}          create or update pr:<repo>:<n>
--   line    {repo,n,now,line}                 append one typed line to reads
--   built   {repo,slug,now,by,land{...},stream_pr{n,fields{...}}?,parked[{task,stream,n,why}],
--            commit_closes[{n,closes}] (each kept member's commit-message closes, on its record)}
--   landed  {repo,slug,now,by,merge_sha,why,members[{task,stream,n,close}],pr,
--            release{key,train,landed}? (a landing into the release branch)}
--
-- landed marks the landing and the records merged and, for a landing into
-- the release branch, writes the release in the same call (#4050): version
-- v<x>.<y>.<z>-dev.<merge sha8> (the train of the version already stored,
-- else release.train) and commit <merge sha>, so every landing names the
-- release the reconciler's deploy duty converges the fleet to; a re-run is
-- ALREADY and never writes an older landing over a newer one. Each
-- member's tasks move to landed, with the CLOSE line on its record, in the
-- nova_sprint library's ns_land_member (one call per member, fenced by this
-- landing's merged state; nova-tools#3779), the same move every event makes.
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

-- ci_request: creates ci:<repo>:<head> and adds it to ci:pool, idempotent
-- on the sha (nova-tools#3717). Returns without writing when the record
-- already exists.
local function ci_request(repo, head, pr, now)
  local ci_key = 'ci:' .. repo .. ':' .. head
  if redis.call('EXISTS', ci_key) == 1 then return end
  local cfg_key = 'cfg:ci:' .. repo
  local checks = redis.call('HGET', cfg_key, 'checks')
  if not checks or checks == '' then
    if repo == 'nova-tools' then
      redis.call('HSET', cfg_key,
        'checks', 'go-build,go-vet,go-test-cmd,go-test-internal,internal-ci',
        'cmd:go-build', 'go build ./...',
        'cmd:go-vet', 'go vet ./...',
        'cmd:go-test-cmd', 'go test -count=1 ./cmd/...',
        'cmd:go-test-internal', 'go test -count=1 ./internal/...',
        'cmd:internal-ci', 'go test -count=1 ./internal/ci/...')
    elseif repo == 'rowan-tools' then
      redis.call('HSET', cfg_key,
        'checks', 'shellcheck,bats',
        'cmd:shellcheck', 'shellcheck -S warning bin',
        'cmd:bats', 'bats tests')
    end
    checks = redis.call('HGET', cfg_key, 'checks')
  end
  if not checks or checks == '' then return end
  local pr_str = ''
  if pr then pr_str = tostring(pr) end
  local now_str = tostring(now or 0)
  local argv = {
    ci_key,
    'repo', repo,
    'sha', head,
    'pr', pr_str,
    'url', '',
    'checks', checks,
    'requested_at', now_str,
    'ci', 'pending',
    'attempt', '0',
    'bench', '',
    'token', '',
    'lease_until', '0',
    'claimed_at', '0',
    'ended_at', '0',
  }
  redis.call('HSET', argv[1], argv[2], argv[3], argv[4], argv[5], argv[6], argv[7], argv[8], argv[9], argv[10], argv[11], argv[12], argv[13], argv[14], argv[15], argv[16], argv[17], argv[18], argv[19], argv[20], argv[21], argv[22], argv[23], argv[24], argv[25], argv[26])
  for name in string.gmatch(checks, '[^,]+') do
    if name ~= '' then
      local cmd = redis.call('HGET', cfg_key, 'cmd:' .. name)
      if cmd and cmd ~= '' then redis.call('HSET', ci_key, 'cmd:' .. name, cmd) end
    end
  end
  redis.call('ZADD', 'ci:pool', tonumber(now_str) or 0, repo .. ':' .. head)
end

-- wskey(stream, w): the stream's set under the sprint epoch (nova-tools#4238):
-- ws:<s>:<w> at epoch 0, ws:<e>:<s>:<w> after the first sprint clear. The
-- rule of internal/nsprint/ws/epoch.go, repeated here because this script is
-- not in the function library (it cannot reach NS.card.wskey).
local function wskey(stream, w)
  local e = redis.call('HGET', 'sprint:epoch', 'n')
  if not e or e == '' or e == '0' then return 'ws:' .. stream .. ':' .. w end
  return 'ws:' .. e .. ':' .. stream .. ':' .. w
end

-- move one task between two ws sets of its stream; returns 1 when it was in
-- `from`. The score is the task's age and a move keeps it (ws-index).
local function move(id, stream, from, to, by, why, at)
  if id == nil or id == '' or stream == nil or stream == '' then return 0 end
  local fromkey = wskey(stream, from)
  local score = redis.call('ZSCORE', fromkey, id)
  if not score then return 0 end
  redis.call('ZREM', fromkey, id)
  redis.call('ZADD', wskey(stream, to), score, id)

  redis.call('HSET', 'task:' .. id, 'state', to, 'state_at', at)
  wslog(id, stream, from, to, by, why, at)
  return 1
end

if op == 'record' then
  local k = prkey(p.repo, p.n)
  local f = p.fields or {}
  -- A record is new until it has a head: the fenced lander's offer
  -- (land_take.lua, #4079) may have made it with only its land_* fields and
  -- repo, n, kind, slug, and a new record still starts open, pending, with a
  -- CI request. reads is kept if a typed line came first.
  local exists = redis.call('HEXISTS', k, 'head') == 1
  if not exists then
    if (f.head or '') == '' or (f.base or '') == '' or (f.stream or '') == '' then
      return {'REFUSED', 'no record ' .. k .. ': a new record needs --head, --base and --stream'}
    end
    redis.call('HSET', k, 'repo', p.repo, 'n', p.n, 'state', 'open', 'ci', 'pending',
      'mergeable', '', 'created_at', p.now)
    redis.call('HSETNX', k, 'reads', '')
    ci_request(p.repo, f.head, p.n, p.now)
  end
  local old = redis.call('HGET', k, 'head')
  if (f.head or '') ~= '' and old and old ~= '' and old ~= f.head then
    -- a new head: CI and mergeable were for the old one
    if (f.ci or '') == '' then f.ci = 'pending' end
    if (f.mergeable or '') == '' then redis.call('HSET', k, 'mergeable', '') end
    ci_request(p.repo, f.head, p.n, p.now)
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
    moved = moved + move(m.task, m.stream, 'merging', 'working', p.by, m.why, p.now)
    if (m.n or '') ~= '' then
      local pk = prkey(p.repo, m.n)
      if redis.call('EXISTS', pk) == 1 then
        redis.call('HSET', pk, 'state', 'parked', 'park', m.why, 'updated_at', p.now)
      end
    end
  end
  for _, m in ipairs(p.commit_closes or {}) do
    local pk = prkey(p.repo, m.n)
    if redis.call('EXISTS', pk) == 1 then redis.call('HSET', pk, 'commit_closes', m.closes) end
  end
  redis.call('DEL', lk)
  hset(lk, p.land)
  redis.call('SADD', 'land:' .. p.repo .. ':streams', p.slug)
  if p.stream_pr and (p.stream_pr.n or '') ~= '' then
    hset(prkey(p.repo, p.stream_pr.n), p.stream_pr.fields)
    local sp_head = (p.stream_pr.fields or {}).head or ''
    if sp_head ~= '' then
      ci_request(p.repo, sp_head, p.stream_pr.n, p.now)
    end
  end
  return {'OK', tostring(moved), tostring(#(p.parked or {}))}
end

if op == 'landed' then
  local lk = landkey(p.repo, p.slug)
  local state = redis.call('HGET', lk, 'state')
  if state == 'merged' then return {'ALREADY', redis.call('HGET', lk, 'merge_sha') or ''} end
  if state ~= 'open' then return {'REFUSED', 'land ' .. lk .. ' state is ' .. tostring(state) .. ', not open'} end
  for _, m in ipairs(p.members or {}) do
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
  if p.release then
    local cur = redis.call('HGET', p.release.key, 'version') or ''
    local train = string.match(cur, '^(v%d+%.%d+%.%d+)%-dev%.%x+$') or p.release.train
    local version = train .. '-dev.' .. string.sub(p.merge_sha, 1, 8)
    redis.call('HSET', p.release.key, 'version', version, 'commit', p.merge_sha,
      'landed', p.release.landed, 'landed_at', p.now)
    return {'OK', version}
  end
  return {'OK'}
end

return {'REFUSED', 'unknown op ' .. tostring(op)}
