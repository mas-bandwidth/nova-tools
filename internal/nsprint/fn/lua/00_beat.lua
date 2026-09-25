-- Beat liveness (nova-tools #3878, "keys do not expire"). A beat hash carries
-- no TTL: its writer stamps `at` (Redis TIME ms) and `stale_ms`, the window
-- it promises to beat again within, and a reader judges it live while
-- TIME - at < stale_ms (the TTL it replaces, read from at). A friend or bench
-- that stops beating keeps its hash and reads down, dated by its at, instead
-- of vanishing from the store.
--
-- Judged here: friend:<f>:beat (presence.lua hello/beat, friend_serve.lua)
-- and bench:<b>:beat (presence.lua bench_beat). A hash with no stale_ms (one
-- written before #3878, whose TTL still bounds it) is judged by existence, as
-- every reader did before. This file reads nothing from NS and exports
-- NS.beat; the Go half of the rule is internal/nsprint/beat.

local BEAT = {}

-- BEAT.STALE is the field a beat's writer stamps its window into.
BEAT.STALE = 'stale_ms'

function BEAT.now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

-- BEAT.live(key[, now_ms]) -> true while the beat at key keeps its promise.
-- An absent key is down; a key the rule cannot date is judged by existence.
function BEAT.live(key, now)
  local f = redis.pcall('HMGET', key, 'at', BEAT.STALE)
  if type(f) ~= 'table' or f.err then
    return redis.call('EXISTS', key) == 1
  end
  local at, stale = tonumber(f[1] or ''), tonumber(f[2] or '')
  if not at or not stale then
    return redis.call('EXISTS', key) == 1
  end
  return (now or BEAT.now_ms()) - at < stale
end

-- BEAT.up(key[, now_ms]) is BEAT.live as the 1 or 0 the EXISTS it replaces
-- answered.
function BEAT.up(key, now)
  if BEAT.live(key, now) then return 1 end
  return 0
end

NS.beat = BEAT
