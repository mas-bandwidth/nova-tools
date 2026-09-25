-- ns_harvest_due lua script
local key = KEYS[1]
local state = redis.call('HGET', key, 'state')
local outcome = redis.call('HGET', key, 'outcome')
local kind = redis.call('HGET', key, 'kind') or 'model'
local pushed_sha = redis.call('HGET', key, 'pushed_sha') or '-'
local check_head = redis.call('HGET', key, 'check_head') or ''

if string.lower(kind) == 'script' then
    return 0
end

if state == 'ended' and string.upper(outcome) == 'DONE' then
    local function check_bound(k)
        if not k or k == '' then return true end
        local l = string.lower(k)
        return l ~= 'script' and l ~= 'report'
    end

    if check_bound(kind) then
        if pushed_sha ~= '-' and check_head == pushed_sha then
            return 1
        return 0
        end
    else
        return 1
    end
end
return 0
