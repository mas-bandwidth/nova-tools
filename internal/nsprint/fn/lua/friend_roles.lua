-- Seat-owned routing roles for #3103.  These helpers are intentionally in a
-- file that sorts before redistribute*.lua, so every routing function reads
-- the same roster from Redis inside its FCALL.

local function fr_csv(s)
  local out = {}
  for role in string.gmatch(s or '', '[^,]+') do out[#out + 1] = role end
  return out
end

local function fr_roles(friend)
  local set = {}
  for _, role in ipairs(fr_csv(redis.call('HGET', 'friend:' .. friend .. ':roles', 'roles'))) do
    set[role] = true
  end
  return set
end

local function fr_actor(actor)
  if not actor or actor == '' or redis.call('SISMEMBER', 'friends', actor) == 0 then
    return redis.error_reply('ERR ACTOR ' .. (actor or ''))
  end
  return nil
end

local function fr_roster()
  local friends = redis.call('SMEMBERS', 'friends')
  table.sort(friends)
  local mayhold, builders, coordinators, any = {}, {}, {}, false
  for _, friend in ipairs(friends) do
    local key = 'friend:' .. friend .. ':roles'
    if redis.call('EXISTS', key) == 1 then any = true end
    local roles = fr_roles(friend)
    if roles['may-hold'] then mayhold[#mayhold + 1] = friend end
    if roles['builder'] then builders[#builders + 1] = friend end
    if roles['coordinator'] then coordinators[#coordinators + 1] = friend end
  end
  if not any then return nil end
  return { friends = friends, mayhold = mayhold, builders = builders,
    coordinators = coordinators, coordinator = coordinators[1] or '' }
end

local function fr_has_role(friend, role)
  return fr_roles(friend)[role] == true
end

-- args = target, roles csv, actor, idem.
local function friend_roles(keys, args)
  local target, roles_csv, actor, idem = args[1], args[2] or '', args[3], args[4]
  local actor_err = fr_actor(actor)
  if actor_err then return actor_err end
  if not target or target == '' or redis.call('SISMEMBER', 'friends', target) == 0 then
    return { 'UNKNOWN', target or '' }
  end
  local wanted, normalized = {}, {}
  for _, role in ipairs(fr_csv(roles_csv)) do
    if role ~= 'may-hold' and role ~= 'builder' and role ~= 'coordinator' then
      return { 'BADROLE', role }
    end
    if not wanted[role] then wanted[role], normalized[#normalized + 1] = true, role end
  end
  table.sort(normalized)
  local roster = fr_roster()
  local coordinators = roster and roster.coordinators or {}
  local bootstrap = #coordinators == 0
  if bootstrap and not wanted['coordinator'] then return { 'LASTCOORD' } end
  if not bootstrap and not fr_has_role(actor, 'coordinator') then
    return { 'REFUSED', actor }
  end
  if #coordinators == 1 and coordinators[1] == target and not wanted['coordinator'] then
    return { 'LASTCOORD' }
  end
  local key = 'friend:' .. target .. ':roles'
  local old = redis.call('HGET', key, 'roles') or ''
  local new = table.concat(normalized, ',')
  local t = redis.call('TIME')
  local at = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
  redis.call('HSET', key, 'roles', new, 'at', tostring(at), 'by', actor)
  redis.call('XADD', 'cap:log', 'MAXLEN', '~', 100000, '*',
    'kind', 'friend-roles', 'subject', target, 'old', old, 'new', new,
    'actor', actor, 'bootstrap', bootstrap and '1' or '0', 'idem', idem or '', 'at', tostring(at))
  return { 'OK', target, new, bootstrap and '1' or '0' }
end

redis.register_function('ns_friend_roles', friend_roles)

-- The cross-file surface redistribute*.lua import (loader.go: every file is
-- its own do-block; NS is the one chunk-level local).
NS.friend_roles = { fr_actor = fr_actor, fr_has_role = fr_has_role, fr_roster = fr_roster }
