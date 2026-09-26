-- The task ref index (nova-tools#3779; rowan-new specs/ws-index.md, "Tasks
-- are cards"): which sprint tasks name an issue or a PR, so the events that
-- move tasks (a harvest opening a PR, the stream lander's merge, a CLOSE
-- line) find them with one SMEMBERS per ref and never a scan of task:*.
--
--   ref:<repo>#<n>:tasks   SET of task ids naming issue or PR <n> of <repo>
--                          (the bare repository name, as prkey: owner/name
--                          and name are one ref)
--   task:<id> refs         the refs the task is indexed under, space-joined,
--                          so a re-index drops the ones it no longer names
--
-- A task names a ref through its fields pr, ref, origin and issue (a GitHub
-- issue or pull URL, owner/repo#n, repo#n, or #n and n with the task's repo
-- field) and through its id when the repo is known: read-<n>-<head8> names PR
-- n, build-<n>-<slug> and fix-<n>-<slug> name issue n. Issues and PRs share a
-- repository's numbers, so one key serves both.
--
-- TR.index(id) is called by every writer of the pr field (TK.move and
-- TK.create when their fields carry pr: a push with pr, done with a PR, task
-- move --set pr, the route's merging move; a copy's end with a PR; the
-- lander for its member task), so an event's lookup by PR is complete. This
-- file loads before 02_card_move.lua and reads nothing from NS, so the card
-- model's push can call it; it exports NS.tref.

local TR = {}

-- TR.bare is the repository name without its owner.
function TR.bare(repo)
  if type(repo) ~= 'string' then return '' end
  return string.match(repo, '([^/%s]+)$') or ''
end

function TR.key(repo, n)
  return 'ref:' .. TR.bare(repo) .. '#' .. tostring(n) .. ':tasks'
end

-- TR.parse(v, repo) -> bare repo, n: the one issue or PR a field names, or
-- nil. repo is the task's own repository, for a bare #n or n.
function TR.parse(v, repo)
  if type(v) ~= 'string' or v == '' then return nil end
  v = string.match(v, '^%s*(.-)%s*$')
  local r, n = string.match(v, '/([%w%._-]+)/issues/(%d+)')
  if not r then r, n = string.match(v, '/([%w%._-]+)/pulls?/(%d+)') end
  if not r then
    local pre, num = string.match(v, '^([%w%._/-]*)#(%d+)$')
    if num then
      r, n = pre, num
      if r == '' then r = repo end
    elseif string.match(v, '^%d+$') then
      r, n = repo, v
    end
  end
  r = TR.bare(r)
  if r == '' or not n or tonumber(n) == 0 then return nil end
  return r, n
end

-- TR.refs(id) -> list of 'repo#n' the task names (deduplicated, in field
-- order), from one HMGET.
function TR.refs(id)
  local f = redis.call('HMGET', 'task:' .. id, 'repo', 'pr', 'ref', 'origin', 'issue')
  local repo = TR.bare(f[1] or '')
  local out, seen = {}, {}
  local function add(r, n)
    if r and n then
      local ref = r .. '#' .. tonumber(n)
      if not seen[ref] then
        seen[ref] = true
        out[#out + 1] = ref
      end
    end
  end
  for i = 2, 5 do
    local r, n = TR.parse(f[i] or '', repo)
    add(r, n)
    if repo == '' and r then repo = r end
  end
  if repo ~= '' then
    local n = string.match(id, '^read%-(%d+)%-') or string.match(id, '^build%-(%d+)%-') or string.match(id, '^fix%-(%d+)%-')
    add(repo, n)
  end
  return out
end

-- TR.index(id): the task's ref keys hold it, and no key it no longer names
-- does. Returns the refs. A task with no record is left out of every key.
function TR.index(id)
  if type(id) ~= 'string' or id == '' then return {} end
  local tk = 'task:' .. id
  if redis.call('EXISTS', tk) == 0 then return {} end
  local old = redis.call('HGET', tk, 'refs') or ''
  local refs = TR.refs(id)
  local now = {}
  for _, ref in ipairs(refs) do
    now[ref] = true
    redis.call('SADD', 'ref:' .. ref .. ':tasks', id)
  end
  for ref in string.gmatch(old, '%S+') do
    if not now[ref] then redis.call('SREM', 'ref:' .. ref .. ':tasks', id) end
  end
  local joined = table.concat(refs, ' ')
  if joined ~= old then redis.call('HSET', tk, 'refs', joined) end
  return refs
end

-- TR.ids(refs) -> the task ids under any of the refs ('repo#n'), each once,
-- sorted (a stable move order).
function TR.ids(refs)
  local out, seen = {}, {}
  for _, ref in ipairs(refs) do
    for _, id in ipairs(redis.call('SMEMBERS', 'ref:' .. ref .. ':tasks')) do
      if not seen[id] then
        seen[id] = true
        out[#out + 1] = id
      end
    end
  end
  table.sort(out)
  return out
end

-- ns_task_refs(id...): index the ids given: the backfill for tasks pushed
-- before this index (the one-time task migrate calls TR.index for every
-- record it walks). Returns {n ids, n refs}.
redis.register_function('ns_task_refs', function(keys, args)
  local refs = 0
  for _, id in ipairs(args) do refs = refs + #TR.index(id) end
  return { tostring(#args), tostring(refs) }
end)

NS.tref = TR
