-- The bus (nova-tools #3865, rowan-new specs/bus-redis.md): friends talk to
-- Rowan over Redis streams. No shebang: loader.go prepends the single library
-- header. Keys: bus:<to> (the inbox STREAM, consumer group <to>) and
-- bus:sent:<from> (the sender's outbox, the same fields plus xid, the inbox
-- entry's id). No TTL, no trim: a message is never lost.
--
-- ns_bus_post is the one multi-key step: the outbox copy carries the id the
-- inbox XADD returned, so the two XADDs are one call (both or neither). The
-- post's pipeline creates a person's inbox group at 0 just before this call
-- (internal/nsprint/bus Post), so a message posted before its reader's first
-- `bus read` is still unread; bus:all has one group per reader, created by
-- that reader's first read. The function runs TIME and XADD only.

local bus_max_body = 16384

local function bus_now_ms()
  local t = redis.call('TIME')
  return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end

-- ns_bus_post(from, to, kind, subject, body, ref, re)
-- Returns the inbox entry id. The Go verb (internal/nsprint/bus) validates the
-- names, the kind and the body before the call; the size and the required
-- fields are checked here too, so no caller writes an entry the verb refuses.
local function bus_post(keys, args)
  local from, to, kind, subject = args[1], args[2], args[3], args[4]
  local body, ref, re = args[5] or '', args[6] or '', args[7] or ''
  if not from or from == '' or not to or to == '' or not kind or kind == '' then
    return redis.error_reply('ns_bus_post needs from, to and kind')
  end
  if #body > bus_max_body then
    return redis.error_reply('ns_bus_post refuses a body over 16384 bytes')
  end
  local inbox = 'bus:' .. to
  local at = tostring(bus_now_ms())
  local xid = redis.call('XADD', inbox, '*',
    'from', from, 'to', to, 'kind', kind, 'subject', subject or '',
    'body', body, 'ref', ref, 're', re, 'at', at)
  redis.call('XADD', 'bus:sent:' .. from, '*',
    'from', from, 'to', to, 'kind', kind, 'subject', subject or '',
    'body', body, 'ref', ref, 're', re, 'at', at, 'xid', xid)
  return xid
end

redis.register_function('ns_bus_post', bus_post)
