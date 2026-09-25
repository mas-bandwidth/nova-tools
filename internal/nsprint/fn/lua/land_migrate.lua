-- The one-time move of the fenced lander's stream-PR records (nova-tools
-- #4079): before it, land_take.lua kept an offered stream PR on the
-- sprint-scoped s:<S>:pr:<repo>:<n>; now it lives on the unit record
-- pr:<name>:<n> as land_* fields. ns_land_migrate S walks the sprint's queue
-- index (s:<S>:land:queues, every queued number of each row) and its landed
-- set (s:<S>:land:landed, so a landed PR still answers LANDED to a new offer),
-- never SCAN, and copies each old record's fields onto the unit record under
-- the land_* names. It is the only reader of the old record and never deletes
-- it. A unit record that already has land_state is kept as it is, so a second
-- run moves nothing.
--
-- ns_land_migrate S -> { 'OK', moved, kept, missing }: missing counts queued
-- or landed numbers with no old record to move.

local LM_FIELDS = {
  stream = 'land_stream', base = 'land_base', created_at = 'land_created_at',
  body_first = 'land_body_first', offered_by = 'land_offered_by', state = 'land_state',
  skip_reason = 'land_skip_reason', lane = 'land_lane', lane_at = 'land_lane_at',
  lane_gen = 'land_lane_gen', land_head = 'land_head', merge_sha = 'land_merge_sha',
  pushed_at = 'land_pushed_at', push_run = 'land_push_run', landed_at = 'land_landed_at',
  drop_reason = 'land_drop_reason',
}

-- lm_one moves one PR n of repo R (owner/name) and says what it did.
local function lm_one(S, R, n)
  local unit = 'pr:' .. (string.match(R, '([^/]+)$') or R) .. ':' .. n
  if redis.call('HEXISTS', unit, 'land_state') == 1 then
    return 'kept'
  end
  local old = redis.call('HGETALL', 's:' .. S .. ':pr:' .. R .. ':' .. n)
  if #old == 0 then
    return 'missing'
  end
  local set, slug = { 'land_sprint', S }, ''
  for i = 1, #old - 1, 2 do
    local to = LM_FIELDS[old[i]]
    if to then
      set[#set + 1] = to
      set[#set + 1] = old[i + 1]
    end
    if old[i] == 'stream' then
      slug = old[i + 1]
    end
  end
  redis.call('HSET', unit, unpack(set))
  redis.call('HSETNX', unit, 'repo', R)
  redis.call('HSETNX', unit, 'n', n)
  redis.call('HSETNX', unit, 'kind', 'stream')
  if slug ~= '' then
    redis.call('HSETNX', unit, 'slug', slug)
  end
  return 'moved'
end

local function land_migrate(keys, args)
  local S = args[1]
  if not S or S == '' then
    return redis.error_reply('ns_land_migrate: sprint is required')
  end
  local count = { moved = 0, kept = 0, missing = 0 }
  local rows = redis.call('SMEMBERS', 's:' .. S .. ':land:queues')
  table.sort(rows)
  for _, row in ipairs(rows) do
    local R, B = string.match(row, '^(.*):([^:]*)$')
    if R and R ~= '' then
      for _, n in ipairs(redis.call('ZRANGE', 's:' .. S .. ':land:queue:' .. R .. ':' .. B, 0, -1)) do
        local what = lm_one(S, R, n)
        count[what] = count[what] + 1
      end
    end
  end
  for _, id in ipairs(redis.call('ZRANGE', 's:' .. S .. ':land:landed', 0, -1)) do
    local R, n = string.match(id, '^(.*)#(%d+)$')
    if R and R ~= '' then
      local what = lm_one(S, R, n)
      count[what] = count[what] + 1
    end
  end
  return { 'OK', tostring(count.moved), tostring(count.kept), tostring(count.missing) }
end

redis.register_function('ns_land_migrate', land_migrate)
