-- ns_census_read: nova-sprint census --sprint's read of one card index
-- (nova-tools #3037), in place of SORT <idx> BY nosort GET # GET
-- s:<S>:card:*-><field>: SORT is @dangerous and the coordinator ACL refuses
-- it (#3620). Read-only, so the census calls it with FCALL_RO, one per named
-- index, all in its one pipeline. The reply is SORT GET's shape, the same as
-- ns_expire_read: label, then each field's value ('' for a missing field or
-- card), per member of s:<S>:idx:card:<state>. It reads only the named set and
-- the hashes of its members; nothing is scanned.
-- args: sprint, state, field...
redis.register_function{
  function_name = 'ns_census_read',
  flags = { 'no-writes' },
  callback = function(keys, args)
    local S, state = args[1] or '', args[2] or ''
    if S == '' or state == '' then
      return redis.error_reply('ns_census_read: sprint and state are required')
    end
    local fields = {}
    for i = 3, #args do fields[#fields + 1] = args[i] end
    local out = {}
    for _, label in ipairs(redis.call('SMEMBERS', 's:' .. S .. ':idx:card:' .. state)) do
      out[#out + 1] = label
      if #fields > 0 then
        local vals = redis.call('HMGET', 's:' .. S .. ':card:' .. label, unpack(fields))
        for i = 1, #fields do out[#out + 1] = vals[i] or '' end
      end
    end
    return out
  end,
}
