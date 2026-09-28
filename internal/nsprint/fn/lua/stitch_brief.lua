-- The stitch brief, generated in the store (Stella's read of nova-tools
-- #4449, 2026-09-27: the waiting-resolve duty releasing one stitch took
-- seven trips, the move plus WriteStitchBrief's dependent reads and its
-- write). One implementation: taskcard.WriteStitchBrief and `card stitch`
-- call ns_stitch_brief, and the waiting-resolve pass writes a released
-- stitch's brief inside its own call (NS.stitch.write). The text is the
-- one the Go StitchBrief rendered (the functional tests pin it).
--
-- No shebang: the loader prepends the library header; this file sorts after
-- 02_card_move.lua and before waiting_resolve.lua and reaches the one task
-- writer through NS.
do
  local MARKER = '## Children'

  local function str(v)
    if v == nil or v == false then return '' end
    return tostring(v)
  end

  local function trim(s)
    return (string.gsub(string.gsub(str(s), '^%s+', ''), '%s+$', ''))
  end

  local function dash(s)
    if s == '' then return '-' end
    return s
  end

  -- fields(s): strings.Fields; one_line(s): the fields joined by one space.
  local function fields(s)
    local out = {}
    for w in string.gmatch(str(s), '%S+') do out[#out + 1] = w end
    return out
  end

  local function one_line(s)
    return table.concat(fields(s), ' ')
  end

  local function hgetall(key)
    local h = redis.call('HGETALL', key)
    local rec = {}
    for i = 1, #h, 2 do rec[h[i]] = h[i + 1] end
    return rec, #h > 0
  end

  -- child_of: taskcard.childOf, the fields the brief prints.
  local function child_of(id, rec)
    return { id = id, title = str(rec.title), where = str(rec.where), where_ok = str(rec.where_ok),
      ref = str(rec.ref), repo = str(rec.repo), pr = str(rec.pr), head = str(rec.head),
      line2 = str(rec.line2), finding = str(rec.finding), evidence = str(rec.evidence),
      score = str(rec.score), paths = str(rec.paths) }
  end

  -- read_plan(parent): taskcard.ReadPlan: the parent's record, its children
  -- in the record's order, and its stitch; nil, why when there is no record.
  local function read_plan(parent)
    local rec, ok = hgetall('task:' .. parent)
    if not ok then return nil, 'no task:' .. parent end
    local p = { id = parent, title = str(rec.title), stream = str(rec.stream), where = str(rec.where),
      where_ok = str(rec.where_ok), ref = str(rec.ref), done_when = str(rec.done_when),
      children = {}, stitch = { id = '', where = '', where_ok = '' } }
    for _, cid in ipairs(fields(rec.children)) do
      local crec = hgetall('task:' .. cid)
      p.children[#p.children + 1] = child_of(cid, crec)
    end
    if str(rec.stitch) ~= '' then
      local srec = hgetall('task:' .. rec.stitch)
      p.stitch = child_of(rec.stitch, srec)
    end
    return p
  end

  -- pr_ref: taskcard.Child.PRRef: <repo name>#<pr> (the name after the
  -- owner), #<pr> without a repo, else the ref.
  local function pr_ref(c)
    if c.pr ~= '' and c.pr ~= '0' then
      local repo = c.repo
      local i = string.find(repo, '/', 1, true)
      if i then repo = string.sub(repo, i + 1) end
      if repo == '' then return '#' .. c.pr end
      return repo .. '#' .. c.pr
    end
    return c.ref
  end

  local function stitch_ended(p)
    return p.stitch.id ~= '' and (p.stitch.where == 'done' or p.stitch.where == '')
  end

  local function failed(p)
    local out = {}
    for _, c in ipairs(p.children) do
      if c.where == 'done' and c.where_ok == 'fail' then out[#out + 1] = c.id end
    end
    return out
  end

  -- state(p): taskcard.Plan.State, the same order of checks.
  local function state(p)
    if p.where == 'landed' or p.where == 'done' or p.where == 'parked' then return p.where end
    local sw = p.stitch.where
    if sw == 'landed' or sw == 'review' or sw == 'merging' or sw == 'working' or sw == 'ready' then return sw end
    if stitch_ended(p) then return 'stuck' end
    for _, c in ipairs(p.children) do
      if c.where == 'done' and c.where_ok == 'fail' then return 'stuck' end
    end
    if sw == 'parked' then return 'parked' end
    local ready = false
    for _, c in ipairs(p.children) do
      if c.where == 'parked' then return 'parked' end
      if c.where == 'working' or c.where == 'review' or c.where == 'merging' then return 'working' end
      if c.where == 'ready' then ready = true end
    end
    if ready then return 'ready' end
    return 'waiting'
  end

  -- next_stitch_id: taskcard.NextStitchID: <parent>-stitch-<n+1>, n from
  -- the old id's suffix when it is a number above 1.
  local function next_stitch_id(parent, old)
    local base = parent .. '-stitch'
    local n = 1
    if string.sub(old, 1, #base + 1) == base .. '-' then
      local rest = string.sub(old, #base + 2)
      if string.match(rest, '^%d+$') then
        local k = tonumber(rest)
        if k > 1 then n = k end
      end
    end
    return base .. '-' .. tostring(n + 1)
  end

  -- remedy(p): taskcard.Plan.Remedy, the stuck plan's way out.
  local function remedy(p)
    if state(p) ~= 'stuck' then return '' end
    local f = failed(p)
    if not stitch_ended(p) then
      return string.format('stuck: %s ended done/fail and the stitch waits on it; nova-sprint card stitch --drop %s drops it from the plan, or nova-sprint card cut --parent %s --from <children.tsv> cuts a replacement (then drop the failed one)',
        table.concat(f, ','), f[1], p.id)
    end
    local ended = 'has no record'
    if p.stitch.where == 'done' then ended = 'ended done/' .. dash(p.stitch.where_ok) end
    local why = string.format('stuck: stitch %s %s and the plan lands only with its stitch', p.stitch.id, ended)
    if #f > 0 then
      return string.format('%s, and %s ended done/fail; nova-sprint card stitch --drop %s drops it, then nova-sprint card cut --parent %s re-cuts the stitch (%s, DEPENDS-ON every child)',
        why, table.concat(f, ','), f[1], p.id, next_stitch_id(p.id, p.stitch.id))
    end
    return string.format('%s; nova-sprint card cut --parent %s re-cuts the stitch (%s, DEPENDS-ON every child)',
      why, p.id, next_stitch_id(p.id, p.stitch.id))
  end

  -- brief(p): the generated section: the marker line, the plan line with its
  -- state and the stitch's DONE-WHEN, the remedy when stuck, one entry per
  -- child in the plan's order with PR, head, score, result, finding,
  -- evidence and paths.
  local function brief(p)
    local b = {}
    b[#b + 1] = string.format('%s (generated by nova-sprint from the records; %d, stitch %s)\n\n', MARKER, #p.children, dash(p.stitch.id))
    local ref = ''
    if p.ref ~= '' then ref = ' (' .. p.ref .. ')' end
    b[#b + 1] = string.format("Plan %s%s, state %s. DONE-WHEN (the stitch's): %s\n\n", p.id, ref, state(p), dash(trim(p.done_when)))
    local r = remedy(p)
    if r ~= '' then b[#b + 1] = r .. '\n\n' end
    for _, c in ipairs(p.children) do
      local score = '-'
      if c.score ~= '' then score = c.score .. '/10' end
      local head = dash(c.head)
      if #head > 12 then head = string.sub(head, 1, 12) end
      b[#b + 1] = string.format('- %s %s pr=%s head=%s score=%s', c.id, dash(c.where), dash(pr_ref(c)), head, score)
      if c.where_ok == 'fail' then b[#b + 1] = ' outcome=fail' end
      b[#b + 1] = '\n'
      if trim(c.title) ~= '' then b[#b + 1] = '  title: ' .. one_line(c.title) .. '\n' end
      if trim(c.line2) ~= '' then b[#b + 1] = '  result: ' .. one_line(c.line2) .. '\n' end
      if trim(c.finding) ~= '' then b[#b + 1] = '  finding: ' .. one_line(c.finding) .. '\n' end
      local e = trim(c.evidence)
      if e ~= '' and e ~= trim(c.finding) then b[#b + 1] = '  evidence: ' .. one_line(c.evidence) .. '\n' end
      if trim(c.paths) ~= '' then b[#b + 1] = '  paths: ' .. one_line(c.paths) .. '\n' end
    end
    if #p.children == 0 then b[#b + 1] = '- (no children)\n' end
    return table.concat(b)
  end

  -- with_brief(body, text): the stitch body with its generated section
  -- replaced: the text before the marker kept, the section after it
  -- replaced (never appended).
  local function with_brief(body, text)
    local head = body
    local i = string.find(body, '\n' .. MARKER, 1, true)
    if i then
      head = string.sub(body, 1, i - 1)
    elseif string.sub(body, 1, #MARKER) == MARKER then
      head = ''
    end
    head = string.gsub(head, '\n+$', '')
    local t = string.gsub(text, '\n+$', '')
    if head == '' then return t .. '\n' end
    return head .. '\n\n' .. t .. '\n'
  end

  -- parent_of(id): the plan a stitch or plan id names, and the stitch:
  -- a plan (kind plan, or a stitch field) names itself; a stitch names its
  -- parent. nil, why when id is neither.
  local function parent_of(id)
    local v = redis.call('HMGET', 'task:' .. id, 'parent', 'phase', 'stitch', 'kind')
    local parent, stitch = str(v[1]), id
    if str(v[4]) == 'plan' or str(v[3]) ~= '' then parent, stitch = id, str(v[3]) end
    local is_plan = str(v[4]) == 'plan'
    if parent == '' or (str(v[2]) ~= 'stitch' and not is_plan) then
      return nil, nil, 'task:' .. id .. ' is not a stitch or a plan: nova-sprint card cut --parent ' .. id .. ' --from <children.tsv> cuts one'
    end
    return parent, stitch
  end

  -- render(parent): the brief of parent's plan; nil, why without a record.
  local function render(parent)
    local p, why = read_plan(parent)
    if not p then return nil, why end
    return brief(p), nil, p
  end

  -- write(id): taskcard.WriteStitchBrief: id is the stitch's id or its
  -- parent's; the brief is generated from the plan's records and written
  -- into the stitch's body through the one writer (a move to the stitch's
  -- own where carrying the body field). Returns the brief, or nil, why.
  local function write(id)
    local parent, stitch, why = parent_of(id)
    if not parent then return nil, why end
    if stitch == '' then
      return nil, 'task:' .. id .. ' is not a stitch or a plan: nova-sprint card cut --parent ' .. id .. ' --from <children.tsv> cuts one'
    end
    local text, rerr, p = render(parent)
    if not text then return nil, rerr end
    if p.stitch.id == '' then
      return nil, 'task:' .. parent .. ' has no stitch record: nova-sprint card cut --parent ' .. parent .. ' --from <children.tsv>'
    end
    local body = str(redis.call('HGET', 'task:' .. stitch, 'body'))
    local where = str(redis.call('HGET', 'task:' .. stitch, 'where'))
    if where == '' then return nil, 'task:' .. stitch .. ' has no where; nothing written' end
    local merr = NS.task.move(stitch, where, { by = 'nova-sprint', why = 'stitch brief', fields = { 'body', with_brief(body, text) } })
    if merr then return nil, 'task:' .. stitch .. ' stitch brief: ' .. merr end
    return text
  end

  -- ns_stitch_brief(id, write) -> OK <brief> | REFUSED <why>. write 1 writes
  -- the stitch's body; 0 renders the plan's brief (id a plan or its stitch)
  -- and writes nothing.
  local function stitch_brief(keys, args)
    local id, dry = str(args[1]), str(args[2]) == '0'
    if id == '' then return { 'REFUSED', 'ns_stitch_brief: an id is required' } end
    if dry then
      local parent, _, why = parent_of(id)
      if not parent then return { 'REFUSED', why } end
      local text, rerr = render(parent)
      if not text then return { 'REFUSED', rerr } end
      return { 'OK', text }
    end
    local text, why = write(id)
    if not text then return { 'REFUSED', why } end
    return { 'OK', text }
  end

  redis.register_function('ns_stitch_brief', stitch_brief)
  NS.stitch = { write = write }
end
