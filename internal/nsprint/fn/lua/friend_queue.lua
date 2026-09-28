-- The friend queue's push (nova-tools #3773, #3778): a friend queue task is
-- a task card (02_card_move.lua, NS.task), created and placed on the
-- friend's ready set by the one task move, which also keeps the shape
-- rowan-tools bin/friend-queue reads (list, take, done, cancel, counts, fill,
-- rebalance) for one release:
--   task:<id>                      HASH kind ref title head front, the card
--                                       pointer (where=ready, friend=owner=to,
--                                       stream from the title's STREAM:
--                                       prefix, created_at ms, state=open),
--                                       queue and xid (the ready entry)
--   ws:<stream>:ready, friend:<to>:cards:ready    ZSETs, scored by created_at
--   sprint:<S>:tasks               SET  + id
--   sprint:<S>:idx:<f>:open        SET  + id
--   q:<f> (q:<f>:front on front)   STREAM one entry `id`
-- A Lua caller in a file that sorts after this one reaches it as NS.fq.push;
-- no caller writes these keys itself.

local FQ = {}

-- FQ.push(S, id, to, kind, ref, title, head, front, at_ms) writes one ready
-- task on to's queue and returns the stream entry id, or nil and the
-- refusal: 'down' (friend:<to>:down exists: a down friend gets nothing),
-- 'exists' (create-only: a re-push or a re-own is a move, never this
-- function's) or the task move's refusal.
function FQ.push(S, id, to, kind, ref, title, head, front, at_ms)
  if redis.call('EXISTS', 'friend:' .. to .. ':down') == 1 then
    return nil, 'down'
  end
  if redis.call('EXISTS', 'task:' .. id) == 1 then
    return nil, 'exists'
  end
  local flag = '0'
  if front then
    flag = '1'
  end
  local origin = ''
  local repo, n = string.match(ref or '', '^([%w._-]+/[%w._-]+)#(%d+)$')
  if repo then
    origin = 'https://github.com/' .. repo .. '/pull/' .. n
  end
  local err, info = NS.task.create(id, { 'kind', kind, 'ref', ref, 'origin', origin, 'title', title,
    'head', head or '', 'front', flag }, { where = 'ready', friend = to, sprint = S, front = flag,
    created = at_ms, why = 'push' })
  if err then
    return nil, err
  end
  return info.xid
end

NS.fq = FQ
