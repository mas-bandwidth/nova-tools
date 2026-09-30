-- Epoch admission and compact, durable operation receipts for tset/1.
-- All helpers plan or read. The common executor appends the done HSET last.
if NS.tset_profile then
  local S = NS.tset
  local MAX_RECEIPT_BYTES = 32768

  local function read_hash(ctx, key, field, reserve)
    if reserve>S.limits.fetched_bytes-ctx.budget.fetched_bytes then
      local length,err=S.readcmd(ctx,{
        argv={'HSTRLEN',key,field},
        access={{key=key,kind='hash',mode='read'}},
      },5,'metadata')
      if err then return nil,err end
      if length>reserve then return nil,S.refuse('DRIFT') end
      reserve=length
    end
    return S.readcmd(ctx, {
      argv = {'HGET', key, field},
      access = {{key = key, kind = 'hash', mode = 'read'}},
    }, reserve, 'metadata')
  end

  local function decode_receipt(value)
    local ok, r = pcall(cjson.decode, value)
    if not ok or type(r) ~= 'table' or type(r.intent_digest) ~= 'string' or
      #r.intent_digest~=40 or string.find(r.intent_digest,'[^0-9a-f]') or
      (r.status~='ok' and r.status~='fenced') or type(r.epoch_before) ~= 'string' or
      type(r.epoch_after) ~= 'string' or type(r.first_seq) ~= 'string' or
      type(r.last_seq) ~= 'string' or type(r.changed) ~= 'number' or
      type(r.result) ~= 'string' or #r.result>S.limits.result or
      -- Effective properties contribute to changed independently of members.
      r.changed < 0 or r.changed>S.limits.member_candidates+S.limits.prop_entries or
      r.changed ~= math.floor(r.changed) or not S.uint(r.epoch_before) or
      not S.uint(r.epoch_after) or not S.uint(r.first_seq) or
      not S.uint(r.last_seq) or
      (r.status=='fenced' and (r.epoch_before~=r.epoch_after or
        r.first_seq~='0' or r.last_seq~='0' or r.changed~=0 or r.result~='')) then
      return nil
    end
    return r
  end

  local function compact(r, replay)
    return {status=r.status, epoch_before=r.epoch_before,
      epoch_after=r.epoch_after, first_seq=r.first_seq,
      last_seq=r.last_seq, changed=r.changed, result=r.result,
      replay=replay}
  end

  -- Called after static validation and configuration, before the active epoch
  -- guard. A replay uses the request's original epoch namespace, never a scan.
  function S.receipt_open(ctx)
    if ctx.op == nil then return nil,nil end
    local key = ctx.done_key(ctx.request_epoch)
    local value,err = read_hash(ctx,key,ctx.op,MAX_RECEIPT_BYTES)
    if err then return nil,err end
    if value == false or value == nil then return nil,nil end
    local r = decode_receipt(value)
    if not r then return nil,S.refuse('DRIFT',{ids={},cells={},rows={}}) end
    if r.intent_digest ~= ctx.intent_digest then
      return nil,S.refuse('OPCONFLICT',{ids={},cells={},rows={}})
    end
    return compact(r,true),nil
  end

  -- The receipt is deliberately smaller than the ordinary success envelope.
  -- Counters, guarded counts, per-entry counts and line counts cannot be
  -- reconstructed after an advance and are neither stored nor invented.
  function S.receipt_prepare(ctx, reply)
    -- The caller-supplied fence marker is sealed by S.context. A preplanner
    -- cannot turn an ordinary named step into a fence or erase a real fence.
    local expected_fence=ctx.original_fence and true or nil
    if type(ctx.original_fence)~='boolean' or ctx.request.fence~=expected_fence then
      return nil,S.refuse('REQUEST')
    end
    if ctx.op == nil then return nil,nil end
    if ctx.original_fence then
      if reply.status~='fenced' or reply.epoch_before~=ctx.request_epoch or
        reply.epoch_after~=ctx.request_epoch or reply.first_seq~='0' or
        reply.last_seq~='0' or reply.changed~=0 or reply.result~='' then
        return nil,S.refuse('REQUEST')
      end
    elseif reply.status~='ok' then
      return nil,S.refuse('REQUEST')
    end
    local r = {intent_digest=ctx.intent_digest, status=reply.status,
      epoch_before=reply.epoch_before, epoch_after=reply.epoch_after,
      first_seq=reply.first_seq, last_seq=reply.last_seq,
      changed=reply.changed, result=reply.result}
    local ok,encoded = pcall(cjson.encode,r)
    if not ok or #encoded > MAX_RECEIPT_BYTES then
      return nil,S.refuse('LIMIT',{budget='receipt_bytes',
        actual=ok and #encoded or MAX_RECEIPT_BYTES+1,limit=MAX_RECEIPT_BYTES})
    end
    local key=ctx.done_key(ctx.request_epoch)
    return S.writecmd(ctx,{'HSET',key,ctx.op,encoded},
      {{key=key,kind='hash',mode='write'}})
  end

  local check_snapshot

  -- A done query is aligned with the caller's set, including mixed original
  -- epochs. A mismatch is a slot result, while a write mismatch refuses.
  function S.done_read(ctx, ops)
    local slots=S.array()
    local checked={}
    for i,identity in ipairs(ops) do
      if S.cmp(identity.epoch,ctx.active_epoch)>0 then
        return nil,S.refuse('EPOCHAHEAD',{query_index=ctx.query_index,
          active_epoch=ctx.active_epoch})
      end
      if identity.epoch~=ctx.active_epoch and not checked[identity.epoch] then
        local ok,err=check_snapshot(ctx,identity.epoch)
        if err then return nil,err end
        checked[identity.epoch]=true
      end
      local key=ctx.done_key(identity.epoch)
      local value,err=read_hash(ctx,key,identity.op,MAX_RECEIPT_BYTES)
      if err then return nil,err end
      if value == false or value == nil then
        slots[i]={status='absent'}
      else
        local r=decode_receipt(value)
        if not r then return nil,S.refuse('DRIFT',{query_index=ctx.query_index}) end
        if r.intent_digest == identity.intent_digest then
          slots[i]={status='match',intent_digest=r.intent_digest,receipt={status=r.status,
            epoch_before=r.epoch_before,epoch_after=r.epoch_after,
            first_seq=r.first_seq,last_seq=r.last_seq,
            changed=r.changed,result=r.result}}
        else
          slots[i]={status='conflict',intent_digest=r.intent_digest}
        end
      end
    end
    return slots,nil
  end

  local function read_epoch(ctx)
    local value,err=read_hash(ctx,ctx.epoch_key,ctx.epoch_field,32)
    if err then return nil,err end
    if value == false or value == nil or not S.uint(value) then
      return nil,S.refuse('CONFIG')
    end
    return value,nil
  end

  local function check_engine(ctx)
    local engine,err=read_hash(ctx,ctx.epoch_key,'engine',32)
    if err then return nil,err end
    if engine==false or engine==nil then return nil,S.refuse('CONFIG') end
    if engine~='tset/1' then return nil,S.refuse('ENGINE') end
    return true,nil
  end

  local function snapshot_key(ctx,epoch)
    return ctx.space..'sprint:epoch@'..epoch
  end

  check_snapshot = function(ctx,epoch)
    local key=snapshot_key(ctx,epoch)
    local engine,err=read_hash(ctx,key,'engine',32)
    if err then return nil,err end
    local n;n,err=read_hash(ctx,key,'n',32)
    if err then return nil,err end
    if engine~='tset/1' or n~=epoch then return nil,S.refuse('EPOCHGONE',{active_epoch=ctx.active_epoch}) end
    return true,nil
  end

  local function historical_defs(ctx)
    local names,seen={},{}
    for _,query in ipairs(ctx.request.queries or {}) do
      local t=query.t
      if t and not seen[t] then names[#names+1]=t;seen[t]=true end
    end
    if #names>4 then return nil,S.refuse('LIMIT',{budget='tables',actual=#names,limit=4}) end
    for _,t in ipairs(names) do
      local key=ctx.definition_key(t,ctx.request_epoch)
      local raw,err=S.readcmd(ctx,{argv={'HGETALL',key},
        access={{key=key,kind='hash',mode='read'}}},49152,'metadata')
      if err then return nil,err end
      if #raw==0 then return nil,S.refuse('EPOCHGONE',{table=t,active_epoch=ctx.active_epoch}) end
      local h={};for i=1,#raw,2 do h[raw[i]]=raw[i+1] end
      local historical;historical,err=S.definition(ctx,t,h)
      if err then return nil,err end
      ctx.defs[t]=historical
    end
    return true,nil
  end

  -- Read-only setup shared by writes and reads. Historical reads require a
  -- materialized definition snapshot for each named table. Done identities
  -- carry their own epochs and are checked independently by S.done_read.
  function S.open_state(ctx)
    if ctx.operation=='step' then
      local ok,err=S.load_defs(ctx)
      if err then return nil,err end
      ok,err=check_engine(ctx)
      if err then return nil,err end
      local replay;replay,err=S.receipt_open(ctx)
      if err then return nil,err end
      if replay then ctx.replay=replay;return true,nil end
    end
    local ok,err
    if ctx.operation~='step' then
      ok,err=check_engine(ctx)
      if err then return nil,err end
    end
    local active;active,err=read_epoch(ctx)
    if err then return nil,err end
    ctx.active_epoch=active
    if ctx.operation=='step' then
      local cmp=S.cmp(ctx.request_epoch,active)
      if cmp>0 then return nil,S.refuse('EPOCHAHEAD',{active_epoch=active}) end
      if cmp<0 then return nil,S.refuse('STALE',{active_epoch=active}) end
      for _,entry in ipairs(ctx.request.entries or {}) do
        if entry.kind=='advance' then
          if entry.from~=ctx.request_epoch then
            return nil,S.refuse('ADVANCE',{active_epoch=active})
          end
          local next_epoch=S.next(ctx.request_epoch)
          if not next_epoch then return nil,S.refuse('OVERFLOW') end
          ctx.write_epoch=next_epoch
          break
        end
      end
      return true,nil
    end
    local cmp=S.cmp(ctx.request_epoch,active)
    if cmp>0 then return nil,S.refuse('EPOCHAHEAD',{active_epoch=active}) end
    if cmp<0 then
      ok,err=check_snapshot(ctx,ctx.request_epoch);if err then return nil,err end
      ok,err=historical_defs(ctx);if err then return nil,err end
    else
      ok,err=S.load_defs(ctx);if err then return nil,err end
    end
    return true,nil
  end

  -- The caller supplied the complete restoration list. Never scan previous
  -- rows or create a second receipt in the successor epoch.
  function S.advance_plan(ctx,plan)
    local names={};for t in pairs(ctx.defs) do names[#names+1]=t end
    table.sort(names)
    for _,t in ipairs(names) do
      local def=ctx.defs[t]
      local raw=def.raw
      if type(raw)~='table' then return nil,S.refuse('CONFIG',{table=t}) end
      local fields={};for field in pairs(raw) do fields[#fields+1]=field end
      table.sort(fields)
      local argv={'HSET',ctx.definition_key(t,ctx.write_epoch)}
      for _,field in ipairs(fields) do
        if type(raw[field])~='string' then return nil,S.refuse('CONFIG',{table=t}) end
        argv[#argv+1]=field;argv[#argv+1]=raw[field]
      end
      if #fields>0 then
        local d,err=S.writecmd(ctx,argv,
          {{key=argv[2],kind='hash',mode='write'}})
        if err then return nil,err end
        plan.commands[#plan.commands+1]=d
      end
    end
    local marker=snapshot_key(ctx,ctx.write_epoch)
    if not ctx.catalog then return nil,S.refuse('CONFIG') end
    local catalog=S.json.encode(ctx.catalog)
    local md,me=S.writecmd(ctx,{'HSET',marker,'engine','tset/1','n',ctx.write_epoch,
      'tables',catalog},
      {{key=marker,kind='hash',mode='write'}})
    if me then return nil,me end
    plan.commands[#plan.commands+1]=md
    local d,err=S.writecmd(ctx,{'HSET',ctx.epoch_key,ctx.epoch_field,ctx.write_epoch},
      {{key=ctx.epoch_key,kind='hash',mode='write'}})
    if err then return nil,err end
    plan.commands[#plan.commands+1]=d
    return true,nil
  end
end
