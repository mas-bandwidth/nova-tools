-- A bounded, read-only page of a table's durable change receipts. A stream ID
-- is an opaque anchor; table revision fields, not ID arithmetic, prove that no
-- accepted table write was skipped. All checks and the page are one snapshot.
do
  local function refusal(code, detail)
    return {'REFUSED', code, detail or ''}
  end

  local function uint(s)
    return type(s) == 'string' and (s == '0' or string.match(s, '^[1-9][0-9]*$') ~= nil) and
      (#s < 20 or (#s == 20 and s <= '18446744073709551615'))
  end

  local function next_uint(s)
    if not uint(s) or s == '18446744073709551615' then return nil end
    local carry, out = 1, {}
    for i = #s, 1, -1 do
      local digit = string.byte(s, i) - 48 + carry
      if digit == 10 then digit, carry = 0, 1 else carry = 0 end
      out[#out + 1] = string.char(48 + digit)
    end
    if carry == 1 then out[#out + 1] = '1' end
    local forward = {}
    for i = #out, 1, -1 do forward[#forward + 1] = out[i] end
    return table.concat(forward)
  end

  local function fields(entry)
    if type(entry) ~= 'table' or #entry ~= 2 or type(entry[1]) ~= 'string' or
        type(entry[2]) ~= 'table' or #entry[2] % 2 ~= 0 then return nil end
    local out = {}
    for i = 1, #entry[2], 2 do
      local key, value = entry[2][i], entry[2][i + 1]
      if type(key) ~= 'string' or type(value) ~= 'string' or out[key] ~= nil then return nil end
      out[key] = value
    end
    if not uint(out.rev_before) or not uint(out.rev_after) or
        next_uint(out.rev_before) ~= out.rev_after or not uint(out.epoch) or
        type(out.verb) ~= 'string' or out.verb == '' then return nil end
    return out
  end

  local function wire_bytes(entry)
    local n = #entry[1]
    for _, value in ipairs(entry[2]) do n = n + #value end
    return n
  end

  redis.register_function{function_name = 'ns_table_receipts', flags = {'no-writes'}, callback = function(keys, args)
    if #args ~= 5 then return refusal('ARGS', 'table, cursor ID, revision, epoch, limit required') end
    local name, cursor_id, cursor_rev, cursor_epoch, limit_text = unpack(args)
    if type(name) ~= 'string' or not string.match(name, '^[A-Za-z0-9_][A-Za-z0-9_.-]*$') or
        type(cursor_id) ~= 'string' or not uint(cursor_rev) or not uint(limit_text) then
      return refusal('ARGS', 'invalid table, cursor or limit')
    end
    local limit = tonumber(limit_text)
    if not limit or limit < 1 or limit > 128 then return refusal('LIMIT', 'receipt page limit is 1..128') end
    if cursor_id == '' then
      if cursor_rev ~= '0' or cursor_epoch ~= '' then return refusal('ARGS', 'initial cursor must have revision zero and no epoch') end
    else
      local ms, seq = string.match(cursor_id, '^([0-9]+)%-([0-9]+)$')
      if not uint(ms) or not uint(seq) or not uint(cursor_epoch) then
        return refusal('ARGS', 'canonical cursor ID and epoch required')
      end
    end

    local base = 'table:' .. name
    local stream, revision_key = base .. ':changes', base .. ':revision'
    local stream_type = redis.call('TYPE', stream).ok
    local revision_type = redis.call('TYPE', revision_key).ok
    if stream_type ~= 'none' and stream_type ~= 'stream' then return refusal('WRONGTYPE', stream) end
    if revision_type ~= 'none' and revision_type ~= 'hash' then return refusal('WRONGTYPE', revision_key) end
    local current = redis.call('HGET', revision_key, 'n') or '0'
    if not uint(current) then return refusal('CURSORGAP', 'invalid stored table revision') end
    if stream_type == 'none' and cursor_id ~= '' then return refusal('CURSORGAP', 'cursor stream is missing') end
    if stream_type == 'none' and current ~= '0' then return refusal('CURSORGAP', 'change stream missing behind table revision') end
    if stream_type == 'none' and current == '0' and redis.call('TYPE', base).ok == 'none' then
      return refusal('NOTABLE', 'table has no receipt history')
    end
    if #cursor_rev > #current or (#cursor_rev == #current and cursor_rev > current) then
      return refusal('CURSORGAP', 'cursor revision is ahead of table revision')
    end

    if cursor_id ~= '' then
      local anchor = redis.call('XRANGE', stream, cursor_id, cursor_id, 'COUNT', 1)
      if #anchor ~= 1 or anchor[1][1] ~= cursor_id then return refusal('CURSORGAP', 'cursor event is missing') end
      local event = fields(anchor[1])
      if not event or event.rev_after ~= cursor_rev or event.epoch ~= cursor_epoch then
        return refusal('CURSORGAP', 'cursor event does not match cursor')
      end
    end

    local start = cursor_id == '' and '-' or '(' .. cursor_id
    local raw = redis.call('XRANGE', stream, start, '+', 'COUNT', limit + 1)
    local expected, bytes = cursor_rev, 0
    for _, entry in ipairs(raw) do
      local event = fields(entry)
      if not event or event.rev_before ~= expected then return refusal('CURSORGAP', 'receipt revision chain is broken') end
      expected = event.rev_after
      bytes = bytes + wire_bytes(entry)
      if bytes > 4194304 then return refusal('LIMIT', 'receipt page exceeds 4194304 bytes; lower limit') end
    end
    local more = #raw > limit
    if not more and expected ~= current then return refusal('CURSORGAP', 'receipt tail is missing') end
    if more then raw[#raw] = nil end
    return {'PAGE', current, more and '1' or '0', raw}
  end}
end
