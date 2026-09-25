-- ns_card_end lua script
local key = KEYS[1]
local outcome = ARGV[8] -- record_outcome
local kind = redis.call('HGET', key, 'kind') or 'model'

local function check_bound(k)
    if not k or k == '' then return true end
    local l = string.lower(k)
    return l ~= 'script' and l ~= 'report'
end

if string.upper(outcome) == 'DONE' and check_bound(kind) then
    local check_sha = redis.call('HGET', key, 'check_sha256')
    if not check_sha or check_sha == '' then
        return redis.error_reply('NO-CHECK-PASS')
    end

    local rec_sha = ARGV[14]
    local comp_sha = ARGV[15]
    local r_ident = ARGV[16]
    local r_head = ARGV[17]
    local r_check = ARGV[18]
    local r_expect = ARGV[19]
    local h_exit = ARGV[20]
    local h_match = ARGV[21]
    local b_exit = ARGV[22]
    local b_match = ARGV[23]

    if not rec_sha or rec_sha ~= comp_sha or string.len(rec_sha) ~= 64 then
        return redis.error_reply('NO-CHECK-PASS')
    end
    local card_ident = redis.call('HGET', key, 'identity')
    if r_ident ~= card_ident then
        return redis.error_reply('NO-CHECK-PASS')
    end
    local pushed_sha = ARGV[10] -- record_pushed_sha
    if r_head ~= pushed_sha then
        return redis.error_reply('NO-CHECK-PASS')
    end
    local card_check_sha = redis.call('HGET', key, 'check_sha256')
    local card_expect_sha = redis.call('HGET', key, 'expect_sha256')
    if r_check ~= card_check_sha or r_expect ~= card_expect_sha then
        return redis.error_reply('NO-CHECK-PASS')
    end
    if h_exit ~= '0' or h_match ~= 'true' or b_exit ~= '1' or b_match ~= 'false' then
        return redis.error_reply('NO-CHECK-PASS')
    end

    redis.call('HSET', key, 'check_head', r_head, 'check_receipt', rec_sha)
end

redis.call('HSET', key, 'state', 'ended', 'outcome', outcome)
return 1
