-- A dependency that is not landed keeps the card in waiting. Release moves
-- that one card into the pool only when every dependency has state landed
-- and a 40-hex merge commit. It does not copy the waiting set across.

-- Every state write here is NS.card (02_card_move.lua): push creates the
-- record and moves it to waiting (then ready), release and land move it.
local CARD = NS.card

local function safe_id(s)
  return type(s) == 'string' and string.match(s, '^[A-Za-z0-9][A-Za-z0-9._-]*$') ~= nil
end

local function is_merge(s)
  return type(s) == 'string' and #s == 40 and string.match(s, '^[0-9a-f]+$') ~= nil
end

local function now_s()
  local t = redis.call('TIME')
  return t[1]
end

-- keys: card, log, idx queued (NS.card writes the index; the dealer's lists are the epoch's, #4238)
-- args: label, payload_sha, priority, base, base_sha, paths, repo, kind,
--       depends_on, card_type (the optional TYPE: line, nova-tools#3091;
--       empty: not stored), depends_on_typed, ready (0|1), route (the
--       card's ROUTE: pro|flash tier; empty or absent: not stored), bench
--       (the card's BENCH: line, nova-tools#3650; empty or absent: any bench,
--       not stored). A bench not in the benches set returns NOBENCH and
--       writes nothing, a bench whose role is friends returns ROLE friends
--       (#3634); a named bench is stored as the card's bench field,
--       the pin ns_card_deal honours (pin == '' or pin == bench).
--       card's ROUTE: pro|flash tier; empty or absent: not stored), est
--       (the card's EST: line in minutes, #3653; empty or absent: not stored),
--       test (the card's TEST: line, #3689), stream (its STREAM: line: its
--       ws:<stream>:<where> view) and origin (its ORIGIN: line, the GitHub
--       issue it came from; #3692): args 16, 17, 18; leg (arg 19: its
--       LEG: line, the one leg a bench profile must carry for the deal's
--       leg filter, nova-tools#3255; empty or absent: any bench, not stored).
-- The record is created with no place and moved to waiting by NS.card, then
-- to ready when place is pool (the waiting -> ready move of a card whose
-- dependencies are already met); the state index is NS.card's.
-- priority is the card's PRIORITY: line (0 when absent) and its pool score.
-- ready is resolved by the Go caller (card.Push); a caller that omits it
-- (the pre-#3503 ten-argument shape) gets pool only when depends_on is empty.
-- The card also gets cut_at, Redis TIME seconds at this push (nova-tools#3091).
redis.register_function('ns_card_push', function(keys, args)
  -- keys: card, log, idx queued (nova-tools#4238: the dealer's lists are
  -- the epoch's, derived by NS.card, and are not declared)
  local card, log = keys[1], keys[2]
  local label, payload, priority = args[1], args[2], args[3]
  local base, base_sha, paths = args[4], args[5], args[6]
  local repo, kind, depends_on = args[7], args[8], args[9]
  local card_type = args[10]
  local depends_on_typed, ready_arg = args[11], args[12]
  local route = args[13]
  local bench = args[14]
  local est = args[15]
  -- test (#3689): the card's TEST line, which the wrapper runs at card end.
  local test = args[16]
  local stream, origin = args[17] or '', args[18] or ''
  local S = string.match(card, '^s:([-a-z0-9]+):card:')
  if not S or card ~= 's:' .. S .. ':card:' .. tostring(label) then
    return redis.error_reply('ns_card_push: key ' .. tostring(card) .. ' is not s:<S>:card:<label>')
  end
  -- leg (#3255): the one leg a bench profile must carry (deal leg filter),
  -- a record field CARD.create stores with the rest.
  local leg = args[19]
  if type(depends_on) ~= 'string' then
    depends_on = ''
  end
  if redis.call('EXISTS', card) == 1 then
    if redis.call('HGET', card, 'payload_sha') == payload then
      return 'EXISTS'
    end
    return 'CONFLICT'
  end
  if type(bench) ~= 'string' then
    bench = ''
  end
  if bench ~= '' and redis.call('SISMEMBER', 'benches', bench) == 0 then
    return 'NOBENCH'
  end
  -- #3634: a bench whose registry role is friends takes no swarm card, so a
  -- pin to it is refused here and nothing is written.
  if bench ~= '' and redis.call('HGET', 'bench:' .. bench .. ':desired', 'role') == 'friends' then
    return 'ROLE friends'
  end
  if type(depends_on_typed) ~= 'string' then
    depends_on_typed = ''
  end
  local ready = ready_arg == '1'
  if ready_arg == nil and depends_on == '' then
    ready = true
  end
  local place = 'waiting'
  if ready then
    place = 'pool'
  end
  local fields = {
    'label', label,
    'kind', kind,
    'repo', repo,
    'base', base,
    'base_sha', base_sha,
    'paths', paths,
    'depends_on', depends_on,
    'depends_on_typed', depends_on_typed,
    'priority', priority,
    'payload_sha', payload,
    'cut_at', now_s()}
  if type(card_type) == 'string' and card_type ~= '' then
    table.insert(fields, 'card_type')
    table.insert(fields, card_type)
  end
  if type(route) == 'string' and route ~= '' then
    table.insert(fields, 'route')
    table.insert(fields, route)
  end
  if type(est) == 'string' and est ~= '' then
    table.insert(fields, 'est')
    table.insert(fields, est)
  end
  if type(test) == 'string' and test ~= '' then
    table.insert(fields, 'test')
    table.insert(fields, test)
  end
  if type(leg) == 'string' and leg ~= '' then
    table.insert(fields, 'leg')
    table.insert(fields, leg)
  end
  if origin ~= '' then
    table.insert(fields, 'origin')
    table.insert(fields, origin)
  end
  local err = CARD.create(card, fields, { bench = bench, stream = stream, by = 'card-push' })
  if not err and place == 'pool' then
    err = CARD.move(card, 'ready', { by = 'card-push', why = 'push: no unmet dependency' })
  end
  if err then
    return redis.error_reply('ns_card_push: ' .. err)
  end
  redis.call('XADD', log, '*',
    'kind', 'card',
    'id', label,
    'from', '',
    'to', 'queued',
    'place', place,
    'actor', 'card-push',
    'reason', 'push',
    'at', now_s())
  return 'OK place=' .. place
end)

-- keys: waiting, pool, log
-- args: sprint, then the labels whose typed dependencies the caller resolved
--       in this pass (GitHub reads are cached by the caller).
redis.register_function('ns_card_release', function(keys, args)
  -- keys: log (the waiting list is the epoch's, derived below, #4238)
  local log = keys[1]
  local sprint = args[1]
  if type(sprint) ~= 'string' or string.match(sprint, '^[-a-z0-9]+$') == nil or #sprint > 40 or #sprint < 1 then
    return redis.error_reply('bad sprint')
  end
  -- the dealer's lists under the current epoch (#4238): the key names the
  -- caller declared are for the ACL, the list is the epoch's
  local waiting = CARD.skey(CARD.epoch(), sprint, 'waiting')
  local releasable = {}
  for i = 2, #args do
    if safe_id(args[i]) then
      releasable[args[i]] = true
    end
  end
  local labels = redis.call('SMEMBERS', waiting)
  local moved, still = 0, 0
  for _, label in ipairs(labels) do
    if not safe_id(label) then
      still = still + 1
    else
      local card = 's:' .. sprint .. ':card:' .. label
      if releasable[label] and redis.call('HGET', card, 'state') == 'queued' and
          not CARD.move(card, 'ready', { by = 'card-release', why = 'release' }) then
        redis.call('XADD', log, '*',
          'kind', 'card',
          'id', label,
          'from', 'queued',
          'to', 'queued',
          'place', 'pool',
          'actor', 'card-release',
          'reason', 'release',
          'at', now_s())
        moved = moved + 1
      else
        still = still + 1
      end
    end
  end
  return 'moved=' .. tostring(moved) .. ' waiting=' .. tostring(still)
end)

-- keys: card, log, idx queued, idx landed (NS.card moves
-- the label from its own state's index)
-- args: label, merge_sha
-- The merge commit is the landing. This does not move dependents; release does.
redis.register_function('ns_card_land', function(keys, args)
  -- keys: card, log, idx queued, idx landed (the dealer's lists are the
  -- epoch's, derived by NS.card, #4238)
  local card, log = keys[1], keys[2]

  local label, merge = args[1], args[2]
  if redis.call('EXISTS', card) == 0 then
    return 'ABSENT'
  end
  if not is_merge(merge) then
    return 'REFUSED'
  end
  local st = redis.call('HGET', card, 'state')
  if type(st) ~= 'string' then
    st = ''
  end
  local old = redis.call('HGET', card, 'merge_sha')
  if st == 'landed' and old == merge then
    return 'OK'
  end
  if st == 'landed' then
    return 'CONFLICT'
  end
  if CARD.move(card, 'done', { state = 'landed', ok = 'ok', fields = { 'merge_sha', merge },
      by = 'card-land', why = 'merged' }) then
    return 'REFUSED'
  end
  redis.call('XADD', log, '*',
    'kind', 'card',
    'id', label,
    'from', st,
    'to', 'landed',
    'actor', 'card-land',
    'reason', 'merged',
    'evidence', merge,
    'at', now_s())
  return 'OK'
end)

-- ns_card_resume is drain's resume step (#3035), a Function since #3419: the
-- card package sends no EVAL/EVALSHA on any path. It takes the member out of
-- the paused set and moves each parked card that is still queued into the
-- pool at its own priority. A parked label whose card is gone or no longer
-- queued (landed, cancelled) leaves the parked set and is not pooled.
-- keys: paused, parked, pool, log; args: member, sprint
-- Returns {was_paused, moved, dropped}.
redis.register_function('ns_card_resume', function(keys, args)
  -- keys: paused, parked, log (the pool is the epoch's, derived below, #4238)
  local paused, parked, log = keys[1], keys[2], keys[3]

  local member, sprint = args[1], args[2]
  -- the pool under the current epoch (#4238), not the declared key
  local pool = CARD.skey(CARD.epoch(), sprint, 'pool')

  local was = redis.call('SREM', paused, member)
  local moved, dropped = 0, 0
  for _, label in ipairs(redis.call('SMEMBERS', parked)) do
    redis.call('SREM', parked, label)
    local card = 's:' .. sprint .. ':card:' .. label
    if safe_id(label) and redis.call('HGET', card, 'state') == 'queued' then
      local priority = redis.call('HGET', card, 'priority')
      if type(priority) ~= 'string' or priority == '' then
        priority = '0'
      end
      redis.call('ZADD', pool, priority, label)
      redis.call('XADD', log, '*',
        'kind', 'card', 'id', label, 'from', 'queued', 'to', 'queued',
        'place', 'pool', 'actor', 'resume', 'reason', 'resume ' .. member, 'at', now_s())
      moved = moved + 1
    else
      dropped = dropped + 1
    end
  end
  return { was, moved, dropped }
end)

-- ns_card_header: the card's DONE-WHEN: line, written once at push
-- (card.Push pipelines it after ns_card_push) so harvest's PR body carries
-- it without the card file; the STREAM: line is the record's own stream
-- field, which CARD.create writes. keys: card. args: payload_sha, done_when
-- [, task]: task is the card's TASK: line (#3712, the PR title), written the
-- same way when the card carries one.
-- The write is refused when the card is not stored (NOTFOUND) or is stored
-- from another payload (CONFLICT: the push before it was a label conflict and
-- wrote nothing). The field is HSETNX: a repeat push with the same payload
-- writes nothing and returns OK.
redis.register_function('ns_card_header', function(keys, args)
  local card = keys[1]
  local payload, done_when, task_line = args[1] or '', args[2] or '', args[3] or ''
  if type(card) ~= 'string' or card == '' or payload == '' then
    return 'USAGE'
  end
  local stored = redis.call('HGET', card, 'payload_sha')
  if not stored then
    return 'NOTFOUND'
  end
  if stored ~= payload then
    return 'CONFLICT'
  end
  redis.call('HSETNX', card, 'done_when', done_when)
  if task_line ~= '' then
    redis.call('HSETNX', card, 'task', task_line)
  end
  return 'OK'
end)
