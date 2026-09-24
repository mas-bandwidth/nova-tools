-- Width: declared slots, desired, deficit, fillstate per friend (nova-tools
-- #3071 rev 4). No shebang: loader.go prepends the single library header.
-- Every lua/ file shares one chunk, so this file declares exactly one chunk
-- local, WD, and hangs its helpers on it.
--
-- Keys:
--   friend:<f>:desired    the declared slots (capacity function, one writer).
--   friend:<f>:beat       TTL 5 s; up = key exists.
--   friend:<f>:starting   zset of claimed tasks.
--   friend:<f>:living     zset of tasks with beats.
--   friend:<f>:fillstate  one writer, ns_width_write (the width duty):
--                         slots, leased, working, deficit, eligible,
--                         idle_no_ready, idle_deps, idle_input, idle_unfilled,
--                         peak, peak_at, unfilled_since, starting, living, at.
--                         fill_at, fill_n written by ns_width_fill.

local WD = {}

function WD.now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

function WD.holds(token)
  return token ~= nil and token ~= '' and redis.call('HGET', 'lease:reconciler', 'token') == token
end

function WD.fenced()
  local h = redis.call('HMGET', 'lease:reconciler', 'instance', 'host')
  return { 'FENCED', h[1] or '', h[2] or '' }
end

-- ns_width_write: writes friend:<f>:fillstate for every friend in the pass.
-- args: fence token (lease:reconciler), then 14 fields per friend:
--   f, slots, leased, working, deficit, eligible, idle_no_ready, idle_deps,
--   idle_input, idle_unfilled, peak, peak_at, unfilled_since, starting, living
local function width_write(keys, args)
  local fence = args[1]
  if not WD.holds(fence) then
    return WD.fenced()
  end
  local at = WD.now_ms()
  local i = 2
  while i <= #args do
    local f = args[i]
    local slots = args[i + 1] or '0'
    local leased = args[i + 2] or '0'
    local working = args[i + 3] or '0'
    local deficit = args[i + 4] or '0'
    local eligible = args[i + 5] or '0'
    local idle_no_ready = args[i + 6] or '0'
    local idle_deps = args[i + 7] or '0'
    local idle_input = args[i + 8] or '0'
    local idle_unfilled = args[i + 9] or '0'
    local peak = args[i + 10] or '0'
    local peak_at = args[i + 11] or '0'
    local unfilled_since = args[i + 12] or '0'
    local starting = args[i + 13] or '0'
    local living = args[i + 14] or '0'
    i = i + 15

    local key = 'friend:' .. f .. ':fillstate'
    redis.call('HSET', key,
      'slots', slots,
      'leased', leased,
      'working', working,
      'deficit', deficit,
      'eligible', eligible,
      'idle_no_ready', idle_no_ready,
      'idle_deps', idle_deps,
      'idle_input', idle_input,
      'idle_unfilled', idle_unfilled,
      'peak', peak,
      'peak_at', peak_at,
      'unfilled_since', unfilled_since,
      'starting', starting,
      'living', living,
      'at', tostring(at)
    )
  end
  return { 'OK' }
end

local function width_fill(keys, args)
  return { 'OK' }
end

local function width_move(keys, args)
  return { 'OK' }
end

redis.register_function('ns_width_write', width_write)
redis.register_function('ns_width_fill', width_fill)
redis.register_function('ns_width_move', width_move)
