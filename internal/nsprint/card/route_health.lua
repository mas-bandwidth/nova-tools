#!lua name=nsprint_route
redis.register_function('ns_route_health_apply', function(keys, args)
    local r = args[1]
    local W = args[2]
    local decision = args[3]
    local deaths = args[4]
    local attempts = args[5]
    local rate = args[6]
    local threshold = args[7]
    local by = args[8]

    local bench_key = "route:" .. r .. ":bench"
    local stored_window = redis.call('HGET', bench_key, 'window')
    local current_state = redis.call('HGET', bench_key, 'state')

    local newer_window = (stored_window == false or stored_window == nil or stored_window == "" or W > stored_window)

    if not newer_window then
        return "SAME"
    end

    if decision == "BENCH" and current_state ~= "benched" then
        local benched_at = tostring(redis.call('TIME')[1])
        redis.call('HSET', bench_key,
            'state', 'benched',
            'window', W,
            'why', 'threshold_exceeded',
            'deaths', deaths,
            'attempts', attempts,
            'rate', rate,
            'threshold', threshold,
            'by', by,
            'benched_at', benched_at
        )
        redis.call('SADD', 'route:benched', r)
        redis.call('XADD', 'route:log', '*',
            'action', 'bench',
            'route', r,
            'window', W,
            'by', by,
            'deaths', deaths,
            'attempts', attempts,
            'rate', rate,
            'threshold', threshold
        )
        return "APPLIED"
    else
        redis.call('HSET', bench_key, 'window', W)
        if current_state == "benched" then
            return "ALREADY_BENCHED"
        end
        return "SAME"
    end
end)

redis.register_function('ns_route_probe', function(keys, args)
    local r = args[1]
    local receipt = args[2]
    local decision = args[3]
    local by = args[4]

    local probe_key = "route:" .. r .. ":probes"
    local set_nx = redis.call('HSETNX', probe_key, receipt, '1')
    if set_nx == 0 then
        return "DUP"
    end

    local bench_key = "route:" .. r .. ":bench"
    if decision == "pass" then
        redis.call('HSET', bench_key,
            'state', 'unbenched',
            'why', 'probe ' .. receipt,
            'by', by
        )
        redis.call('SREM', 'route:benched', r)
        redis.call('XADD', 'route:log', '*',
            'action', 'unbench',
            'route', r,
            'why', 'probe ' .. receipt,
            'by', by
        )
        return "OK"
    elseif decision == "fail" then
        local benched_at = tostring(redis.call('TIME')[1])
        redis.call('HSET', bench_key,
            'state', 'benched',
            'why', 'probe ' .. receipt,
            'by', by,
            'benched_at', benched_at
        )
        redis.call('SADD', 'route:benched', r)
        redis.call('XADD', 'route:log', '*',
            'action', 'bench',
            'route', r,
            'why', 'probe ' .. receipt,
            'by', by
        )
        return "BENCH"
    end
    return "UNKNOWN"
end)
