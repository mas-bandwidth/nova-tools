-- The done-already leg (nova-tools#3919). A card whose model ended it
-- `ABSTAIN done-already <sha>` names the commit that already did its work;
-- ns_card_end (card_run.lua) queues its label on s:<S>:done-already. The
-- reconciler's DoneAlready duty (internal/nsprint/reconcile) checks the sha
-- against the card's base in its own mirror, closes the card's origin issue
-- by REST, and then calls ns_card_done_already here, fenced by the
-- reconciler lease, to write the verdict on the record, take the label off
-- the queue and land every sprint task naming that issue, in one call. A
-- second call for the same label finds it off the queue and writes nothing,
-- so a second pass does nothing.
do
local TE = NS.tev

-- ns_card_done_already(token, S, label, verdict, sha, base, evidence)
-- verdict closed (the issue is closed: tasks naming it land) or refused (the
-- sha is not on the base, or the card names no issue: the record says why
-- and nothing lands). Reply {'OK', verdict, moved, same, skipped, note...},
-- {'NOTHING'} when the label is not queued, {'FENCED', instance, host}, or
-- {'USAGE', ...}.
redis.register_function('ns_card_done_already', function(keys, args)
  local token, S, label = args[1] or '', args[2] or '', args[3] or ''
  local verdict, sha, base, evidence = args[4] or '', args[5] or '', args[6] or '', args[7] or ''
  if token == '' or redis.call('HGET', 'lease:reconciler', 'token') ~= token then
    local h = redis.call('HMGET', 'lease:reconciler', 'instance', 'host')
    return { 'FENCED', h[1] or '', h[2] or '' }
  end
  if S == '' or label == '' or (verdict ~= 'closed' and verdict ~= 'refused') or evidence == '' then
    return { 'USAGE', 'token S label closed|refused sha base evidence' }
  end
  local queue = 's:' .. S .. ':done-already'
  if not redis.call('ZSCORE', queue, label) then return { 'NOTHING' } end
  local card = 's:' .. S .. ':card:' .. label
  local t = redis.call('TIME')
  local now = tostring(tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000))
  redis.call('HSET', card, 'done_already', verdict, 'done_already_sha', sha, 'done_already_base', base,
    'done_already_why', string.sub((string.gsub(evidence, '[\r\n]', ' ')), 1, 1024), 'done_already_at', now)
  redis.call('ZREM', queue, label)
  local r = { moved = 0, same = 0, skipped = 0, notes = {} }
  if verdict == 'closed' then
    local f = redis.call('HMGET', card, 'origin', 'repo')
    local repo, n = TE.TR.parse(f[1] or '', f[2] or '')
    if repo then r = TE.landed({ repo .. '#' .. n }, {}, 'reconciler', evidence, sha) end
  end
  redis.call('XADD', 's:' .. S .. ':log', '*', 'kind', 'card', 'id', label, 'actor', 'reconciler',
    'reason', 'done-already-' .. verdict, 'evidence', evidence, 'at', now)
  local out = { 'OK', verdict, tostring(r.moved), tostring(r.same), tostring(r.skipped) }
  for _, note in ipairs(r.notes) do out[#out + 1] = note end
  return out
end)
end
