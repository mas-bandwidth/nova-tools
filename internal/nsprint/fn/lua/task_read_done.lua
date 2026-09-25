-- A read is done only by the verb (nova-tools #3897). No shebang: loader.go
-- prepends the single library header. ns_task_read_done is the one call that
-- finishes a read of a PR: it checks the card's lease, validates the typed
-- line against the card (who is the card's friend, head is the card's head,
-- SCORE or HOLD), stores it through the line store (line.lua's ns_line_post
-- body: the gates measured, a typed gate that disagrees refused, the record,
-- pr:<name>:<n>:lines and the lander's reads) and closes the card through
-- ns_task_done's body (task_claim.lua) with the READ_LINE capability, all in
-- this one call. Every refusal comes before the first write, so a refused
-- read writes nothing: no line without the close, no close without the line.

local LN = NS.line
local CL = NS.claim
local HD = NS.HD

-- ns_task_read_done args: S, id, token, as, actor, idem, evidence ('' is the
-- line's first line), and the line as internal/nsprint/line parsed it: head,
-- who, kind, score, gates as typed, scope (ok|no|'' as the caller measured it
-- in the mirror), scope why, text (the whole line).
-- Reply: NOTFOUND | FENCED | CLOSED | CONFLICT | INVALID |
-- NOLINE <remedy> | BADLINE <why> | DONE <line key> <lines in the log> <ready>.
local function read_done(keys, args)
  local S, id, token, as, actor, idem = args[1] or '', args[2] or '', args[3] or '', args[4] or '', args[5] or '', args[6] or ''
  local evidence, head, who, kind = args[7] or '', string.lower(args[8] or ''), string.lower(args[9] or ''), args[10] or ''
  local score, typed, scope, scope_why, text = args[11] or '', args[12] or '', args[13] or '', args[14] or '', args[15] or ''
  local key = 'task:' .. id
  if redis.call('EXISTS', key) == 0 then
    return { 'NOTFOUND' }
  end
  local t = redis.call('HMGET', key, 'kind', 'owner', 'token', 'state', 'head', 'repo', 'pr', 'evidence')
  local tkind, owner, thead, repo, pr = t[1] or '', t[2] or '', t[5] or '', t[6] or '', t[7] or ''
  if tkind ~= 'read' and tkind ~= 'review' then
    return { 'BADLINE', 'task ' .. id .. ' is kind ' .. tkind .. ', not a read; close it with task done --evidence' }
  end
  if token == '' and as ~= '' then
    if owner ~= as then return { 'FENCED' } end
  elseif t[3] ~= token then
    return { 'FENCED' }
  end
  if evidence == '' then evidence = string.match(text, '^[^\n]*') or '' end
  local state = t[4] or ''
  if state == 'closed' then
    if (t[8] or '') == evidence then return { 'CLOSED' } end
    return { 'CONFLICT' }
  end
  if state == 'cancelled' then return { 'CONFLICT' } end
  if state ~= 'claimed' and state ~= 'working' then return { 'INVALID' } end
  if text == '' then
    return { 'NOLINE', CL.read_remedy(id, owner, thead) }
  end
  if kind ~= 'SCORE' and kind ~= 'HOLD' then
    return { 'BADLINE', 'a read is done by a SCORE (or HOLD) line, not ' .. kind .. '; --line ' .. "'SCORE who=" .. owner .. ' head=' .. thead .. " score=N/10 ...'" }
  end
  if thead == '' or head ~= thead then
    return { 'BADLINE', 'head=' .. string.sub(head, 1, 12) .. ' is not the task head ' .. thead .. '; read the task head and type it whole' }
  end
  if HD.resolve_who(who) ~= owner then
    return { 'BADLINE', 'who=' .. who .. ' is not the task friend ' .. owner }
  end
  local name = string.match(repo, '([^/]*)$') or ''
  if name == '' or pr == '' or pr == '0' then
    return { 'BADLINE', 'task ' .. id .. ' names no PR (repo=' .. repo .. ' pr=' .. pr .. '); a report read closes with task done --evidence' }
  end
  local p = LN.post(nil, { 'post', name, pr, head, who, kind, score, typed, scope, scope_why, text, '' })
  if type(p) ~= 'table' or p[1] ~= 'POSTED' then
    if type(p) == 'table' and p[1] == 'MISSING' then
      return { 'BADLINE', p[2] .. ' has no head; the PR record is written when the PR is opened (pr record)' }
    end
    if type(p) == 'table' and p[1] == 'REFUSED' then
      return { 'BADLINE', p[2] }
    end
    return { 'BADLINE', 'line store: ' .. tostring(type(p) == 'table' and (p.err or p[1]) or p) }
  end
  local verdict = 'APPROVE'
  if kind == 'HOLD' then verdict = 'HOLD' end
  if score == '' then score = '0' end
  local d = CL.done(CL.read_line, { S, id, token, evidence, verdict, score, head, actor, idem, as })
  if d[1] ~= 'DONE' then
    -- Unreachable: every guard of the close is checked above, before the line.
    return redis.error_reply('ns_task_read_done: close refused ' .. tostring(d[1]) .. ' after the line was stored')
  end
  return { 'DONE', p[2], p[6], tostring(d[2] or '0') }
end

redis.register_function('ns_task_read_done', read_done)
