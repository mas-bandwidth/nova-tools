-- The friend queue's push (nova-tools #3773): the one writer of a friend
-- queue task, the shape rowan-tools bin/friend-queue reads (list, take, done,
-- cancel, counts, fill, rebalance). It is the non-waiting `friend-queue push
-- --to <f> --id <id> --kind <k> --ref <ref> --title <t> [--head <sha>]
-- [--front]` path, command for command:
--   task:<id>                      HASH kind ref title owner state=open head
--                                       created_at (UTC seconds, the bash
--                                       `date -u +%FT%TZ`) front
--   sprint:<S>:tasks               SET  + id
--   sprint:<S>:idx:<f>:open        SET  + id (waiting, working, closed: - id)
--   q:<f> (q:<f>:front on front)   STREAM one entry `id`
-- No queue or xid field (a take writes them), no ws: set and no ws:log entry:
-- a task pushed here is the friend-queue shape until a ws verb moves it.
-- A Lua caller in a file that sorts after this one reaches it as NS.fq.push;
-- no caller writes these keys itself.

local FQ = {}

-- FQ.utc formats epoch ms as YYYY-MM-DDTHH:MM:SSZ (days to civil date in the
-- proleptic Gregorian calendar).
function FQ.utc(ms)
  local s = math.floor(ms / 1000)
  local days = math.floor(s / 86400)
  local rem = s - days * 86400
  local z = days + 719468
  local era = math.floor(z / 146097)
  local doe = z - era * 146097
  local yoe = math.floor((doe - math.floor(doe / 1460) + math.floor(doe / 36524) - math.floor(doe / 146096)) / 365)
  local doy = doe - (365 * yoe + math.floor(yoe / 4) - math.floor(yoe / 100))
  local mp = math.floor((5 * doy + 2) / 153)
  local d = doy - math.floor((153 * mp + 2) / 5) + 1
  local m = mp + 3
  if mp >= 10 then
    m = mp - 9
  end
  local y = yoe + era * 400
  if m <= 2 then
    y = y + 1
  end
  return string.format('%04d-%02d-%02dT%02d:%02d:%02dZ', y, m, d,
    math.floor(rem / 3600), math.floor((rem % 3600) / 60), rem % 60)
end

-- FQ.push(S, id, to, kind, ref, title, head, front, at_ms) writes one open
-- task on to's ready queue and returns the stream entry id, or nil and the
-- refusal friend-queue push gives: 'down' (friend:<to>:down exists: a down
-- friend gets nothing) or 'exists' (create-only: a re-push or a re-own is
-- friend-queue push's and `push --move`'s, never this function's).
function FQ.push(S, id, to, kind, ref, title, head, front, at_ms)
  if redis.call('EXISTS', 'friend:' .. to .. ':down') == 1 then
    return nil, 'down'
  end
  if redis.call('EXISTS', 'task:' .. id) == 1 then
    return nil, 'exists'
  end
  local ix = 'sprint:' .. S .. ':idx:' .. to .. ':'
  local queue, flag = 'q:' .. to, '0'
  if front then
    queue, flag = queue .. ':front', '1'
  end
  redis.call('HSET', 'task:' .. id, 'kind', kind, 'ref', ref, 'title', title, 'owner', to,
    'state', 'open', 'head', head or '', 'created_at', FQ.utc(at_ms), 'front', flag)
  redis.call('SADD', 'sprint:' .. S .. ':tasks', id)
  redis.call('SREM', ix .. 'waiting', id)
  redis.call('SADD', ix .. 'open', id)
  redis.call('SREM', ix .. 'working', id)
  redis.call('SREM', ix .. 'closed', id)
  return redis.call('XADD', queue, '*', 'id', id)
end

NS.fq = FQ
