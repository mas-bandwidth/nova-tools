#!lua name=nsprint_card
redis.register_function('ns_card_end', function(keys, args)
    local sprint = args[1]
    local label = args[2]
    local attempt = args[3]
    local route = args[4]
    local rc = args[5]
    local tokens_in = args[6]
    local tokens_out = args[7]
    local silent = args[8]
    local class = args[9]
    local resolved = args[10]
    local bench = args[11]

    if resolved ~= "1" then
        return "NOT_RESOLVED"
    end

    local dedup_key = "s:" .. sprint .. ":card:" .. label
    local field = "route_charged:" .. attempt
    local set_nx = redis.call('HSETNX', dedup_key, field, 'pending')
    if set_nx == 0 then
        return "ALREADY_CHARGED"
    end

    local stream_key = "route:" .. route .. ":attempts"
    local id = sprint .. "/" .. label .. "/" .. attempt
    local stream_id = redis.call('XADD', stream_key, 'MAXLEN', '~', '1000', '*',
        'sprint', sprint,
        'label', label,
        'attempt', attempt,
        'bench', bench,
        'rc', rc,
        'tokens_in', tokens_in,
        'tokens_out', tokens_out,
        'silent', silent,
        'class', class,
        'id', id
    )
    redis.call('HSET', dedup_key, field, stream_id)
    return stream_id
end)
