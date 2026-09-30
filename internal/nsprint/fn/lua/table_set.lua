-- tset/1 atomic planner. Helpers in table_set_*.lua are bound at call time.
-- No planner executes a mutation. Only commit consumes prepared descriptors.
if NS.tset_profile then
NS.tset = {}
do
  local S = NS.tset
  local current_ctx,current_seal
  local read_cleanup,log_cleanup
  local bindings_open=true
  local log_bound=false
  local read_bound=false
  local scope_active=false
  local callback_depth=0
  local active_callback_ctx
  local original_unchanged,read_identity_unchanged
  local raw_ops
  local load_defs
  S.profile = NS.tset_profile
  S.limits = {request_bytes=4194304,read_request_bytes=4194304,entries=256,queries=1024,
    ids_per_entry=2000,member_candidates=2000,guard_members=4000,rows=100,advance_rows=1024,
    field_names=128,field_value=65536,result=4096,intent=65536,notes=100,about=4000,
    commands=65536,argv_bytes=8388608,fetched_bytes=8388608,cell=20000,
    read_fields=1280000,write_fields=768000,record=10000,range_id=20000,log_id=200000,
    props=64,prop_entries=64}
  local fetched_cap=S.limits.fetched_bytes

  function S.refuse(code, detail, message)
    detail = detail or {}
    if not detail.ids or next(detail.ids)==nil then detail.ids=S.array() end
    if not detail.cells or next(detail.cells)==nil then detail.cells=S.array() end
    if not detail.rows or next(detail.rows)==nil then detail.rows=S.array() end
    return {status='refused',code=code,detail=detail,
      message=(message or code) .. '; nothing was changed'}
  end
  local function limit(ctx, unit, actual, bound)
    return S.refuse(ctx.operation == 'read' and 'BUDGET' or 'LIMIT',
      {budget=unit,actual=actual,limit=bound})
  end
  local function payload(v)
    if type(v) == 'string' then return #v end
    if type(v) == 'number' then return #tostring(v) end
    local n = 0
    if type(v) == 'table' then for k,x in pairs(v) do
      if type(k) == 'string' then n = n + #k end
      n = n + payload(x)
    end end
    return n
  end
  S.payload_bytes = payload
  -- Capture runtime primitives before any read callback. FUNCTION LOAD does
  -- not expose these builtins, and the private table is never given to callers.
  local function capture_raw_ops()
    if not raw_ops then
      raw_ops={kind=type,get=rawget,same=rawequal,iter=next,
        meta=getmetatable,setmeta=setmetatable,call=pcall}
    end
  end
  local function same_context(ctx)
    return raw_ops and raw_ops.same(ctx,current_ctx)
  end
  -- The only inert metatable admitted by the wire is the cjson array marker.
  -- Reassigning it proves getmetatable did not hide a protected impostor.
  local function inert_table(value)
    local mt=raw_ops.meta(value)
    if mt==nil then return true end
    if raw_ops.kind(mt)~='table' or raw_ops.meta(mt)~=nil or
        raw_ops.get(mt,'__is_cjson_array')~=true then return false end
    local count=0
    for key,item in raw_ops.iter,mt do
      if key~='__is_cjson_array' or item~=true then return false end
      count=count+1
    end
    if count~=1 then return false end
    raw_ops.setmeta(value,mt)
    return true
  end
  -- Iterative raw graph traversal covers both keys and values. The active
  -- state detects cycles; finished aliases are scanned only once. Retaining
  -- entry tables also covers containers detached from ctx by the callback.
  local function graph_tables(roots)
    local states,stack,retained={},{},{}
    for _,root in ipairs(roots) do
      if raw_ops.kind(root)=='table' then stack[#stack+1]={value=root} end
    end
    while #stack>0 do
      local frame=stack[#stack];stack[#stack]=nil
      local value=frame.value
      if frame.exit then states[value]=2
      elseif states[value]==1 then return nil
      elseif states[value]~=2 then
        if not inert_table(value) then return nil end
        states[value]=1;retained[#retained+1]=value
        stack[#stack+1]={value=value,exit=true}
        for key,item in raw_ops.iter,value do
          if raw_ops.kind(key)=='table' then stack[#stack+1]={value=key} end
          if raw_ops.kind(item)=='table' then stack[#stack+1]={value=item} end
        end
      end
    end
    return retained
  end
  local function budget_monotone(ctx,remember)
    local saved=same_context(ctx) and current_seal or nil
    local window=saved and saved.callback
    if callback_depth>0 and (not raw_ops.same(ctx,active_callback_ctx) or
        not read_identity_unchanged(ctx,saved)) then return nil,S.refuse('CONFIG') end
    if not window then return true,nil end
    local budget=raw_ops.get(ctx,'budget')
    if raw_ops.get(ctx,'query_index')~=window.index or
        not raw_ops.same(budget,window.budget) or raw_ops.meta(budget)~=nil then return nil,S.refuse('CONFIG') end
    for key,value in raw_ops.iter,budget do
      if raw_ops.kind(value)~='number' or value<0 or value==math.huge or value~=math.floor(value) then return nil,S.refuse('CONFIG') end
    end
    for key,floor in raw_ops.iter,window.floors do
      local value=raw_ops.get(budget,key)
      if raw_ops.kind(value)~='number' or value<floor then return nil,S.refuse('CONFIG') end
    end
    if remember then for key,value in raw_ops.iter,budget do window.floors[key]=value end end
    return true,nil
  end
  function S.charge(ctx, unit, count)
    local monotone,problem=budget_monotone(ctx,false);if problem then return nil,problem end
    if type(count) ~= 'number' or count < 0 or count ~= math.floor(count) then
      return nil,S.refuse('REQUEST')
    end
    local cap = S.limits[unit]
    if unit == 'field' then cap = ctx.operation == 'read' and S.limits.read_fields or S.limits.write_fields end
    if not cap then return nil,S.refuse('REQUEST') end
    local n = (ctx.budget[unit] or 0) + count
    if n > cap then return nil,limit(ctx,unit,n,cap) end
    ctx.budget[unit] = n
    return budget_monotone(ctx,true)
  end
  -- Redis invokes Functions serially. Keep one private context slot, cleared
  -- on replacement and by the protected public entry wrappers on all exits.
  -- Enclosing callers can explicitly release their last context as well.
  local function copy_value(value)
    if type(value)~='table' then return value end
    local out=S.is_array(value) and S.array() or {}
    for key,item in pairs(value) do out[key]=copy_value(item) end
    return out
  end
  local function equal_value(a,b,depth)
    if type(a)~=type(b) then return false end
    if type(a)~='table' then return a==b end
    depth=(depth or 0)+1
    if depth>16 or S.is_array(a)~=S.is_array(b) then return false end
    for key,value in pairs(a) do if not equal_value(value,b[key],depth) then return false end end
    for key in pairs(b) do if a[key]==nil then return false end end
    return true
  end
  local sealed_fields={'operation','profile','version','space','op','intent','intent_digest','result',
    'request_epoch','write_epoch','epoch_key','epoch_field','original_fence','notes_implicit',
    'original_note_count','original_advance','original_advance_index','original_advance_from',
    -- Key constructors remain authority after callbacks; seal their identities
    -- and transitive table_prefix dependency without invoking the functions.
    'record_key','table_key','table_prefix','rows_key','props_key','cell_key',
    'definition_key','log_key','history_key','done_key'}
  local request_identity={'space','epoch','op','intent','result','fence'}
  local function remember_context(ctx)
    local saved={request=copy_value(ctx.request),fields={},rowsets=copy_value(ctx.original_rowsets),
      notes=ctx.notes,read_request=ctx.operation=='read' and ctx.request or nil}
    for _,field in ipairs(sealed_fields) do saved.fields[field]=ctx[field] end
    saved.fields.write_epoch=ctx.original_advance and S.next(ctx.request_epoch) or ctx.request_epoch
    saved.fields.intent_digest=ctx.intent and redis.sha1hex(ctx.intent) or nil
    if ctx.operation=='read' then saved.read_before={};saved.read_row_scores={} end
    current_ctx=ctx;current_seal=saved
  end
  local function prefix_equal(current,original)
    if type(current)~='table' or type(original)~='table' or #current<#original then return false end
    if S.is_array(current)~=S.is_array(original) then return false end
    for i=1,#original do if not equal_value(current[i],original[i]) then return false end end
    return true
  end
  original_unchanged=function(ctx)
    local saved=same_context(ctx) and current_seal or nil
    if not saved or type(ctx.request)~='table' then return false end
    for _,field in ipairs(sealed_fields) do if ctx[field]~=saved.fields[field] then return false end end
    for _,field in ipairs(request_identity) do if ctx.request[field]~=saved.request[field] then return false end end
    if not equal_value(ctx.original_rowsets,saved.rowsets) then return false end
    if ctx.operation=='step' then
      local notes=saved.notes
      if not notes or ctx.notes~=notes or ctx.notes_array~=notes or ctx.request.notes~=notes then return false end
      if not prefix_equal(ctx.request.entries,saved.request.entries) or not prefix_equal(ctx.notes,saved.request.notes) then return false end
      if saved.planned and (not equal_value(ctx.request.entries,saved.planned.entries) or
          not equal_value(ctx.notes,saved.planned.notes)) then return false end
    elseif ctx.operation=='read' then
      -- A read has no append-only planning phase. Keep the exact validated
      -- request and its identity so replacing ctx.request cannot hide edits
      -- to the original array that the reader is still iterating.
      if ctx.request~=saved.read_request or not equal_value(ctx.request,saved.request) then return false end
      if saved.read_active_epoch and ctx.active_epoch~=saved.read_active_epoch then return false end
    end
    if saved.opened and (ctx.raw_request~=saved.raw_request or ctx.request_hash~=saved.request_hash or
        ctx.active_epoch~=saved.active_epoch or ctx.now_ms~=saved.now_ms) then return false end
    return true
  end
  -- Helpers consume sealed identity/private projections. Whole graph/request
  -- validation belongs at callback entry/exit, not every metered access.
  local read_maps={'budget','accesses','types','stream_info','defs','limits'}
  read_identity_unchanged=function(ctx,saved)
    if not raw_ops or not saved or not same_context(ctx) or saved~=current_seal or
        saved.fields.operation~='read' or raw_ops.meta(ctx)~=nil then return false end
    local request=raw_ops.get(ctx,'request')
    if not raw_ops.same(request,saved.read_request) or raw_ops.meta(request)~=nil then return false end
    for _,field in ipairs(sealed_fields) do
      if not raw_ops.same(raw_ops.get(ctx,field),saved.fields[field]) then return false end
    end
    for _,field in ipairs(request_identity) do
      if not raw_ops.same(raw_ops.get(request,field),saved.request[field]) then return false end
    end
    for _,field in ipairs(read_maps) do
      local value=raw_ops.get(ctx,field)
      if raw_ops.kind(value)~='table' or raw_ops.meta(value)~=nil then return false end
    end
    if saved.read_active_epoch and raw_ops.get(ctx,'active_epoch')~=saved.read_active_epoch then return false end
    if saved.opened and (raw_ops.get(ctx,'raw_request')~=saved.raw_request or
        raw_ops.get(ctx,'request_hash')~=saved.request_hash or raw_ops.get(ctx,'active_epoch')~=saved.active_epoch or
        raw_ops.get(ctx,'now_ms')~=saved.now_ms) then return false end
    return true
  end
  local function read_unchanged(ctx,saved)
    return read_identity_unchanged(ctx,saved)
  end
  local function clear_context()
    current_ctx=nil;current_seal=nil
    if read_cleanup then read_cleanup() end
    if log_cleanup then log_cleanup() end
  end
  function S.release_context(ctx)
    if callback_depth>0 then return nil,S.refuse('CONFIG') end
    if ctx~=current_ctx then return nil,S.refuse('REQUEST') end
    clear_context()
    return true,nil
  end
  function S.run_context(fn,...)
    if scope_active or callback_depth>0 then return S.json.encode(S.refuse('CONFIG')) end
    bindings_open=false
    clear_context()
    scope_active=true
    local ok,result=pcall(fn,...)
    clear_context()
    scope_active=false
    if not ok then error(result,0) end
    return result
  end
  function S.context(request, operation)
    if callback_depth>0 then return nil,S.refuse('CONFIG') end
    capture_raw_ops()
    bindings_open=false
    clear_context()
    local space = request.space
    local original_note_count = request.notes and #request.notes or 0
    local notes_implicit = operation == 'step' and request.notes == nil
    if notes_implicit then request.notes = S.array() end
    local original_rowsets = {}
    for _,entry in ipairs(request.entries or {}) do
      if entry.kind ~= 'rowset' then break end
      local rows = {}
      for i,item in ipairs(entry.rows) do rows[i] = {row=item.row,rank=item.rank} end
      original_rowsets[#original_rowsets + 1] = {t=entry.t,rows=rows}
    end
    local advance_index = #original_rowsets + 1
    local advance = request.entries and request.entries[advance_index]
    local original_advance = advance and advance.kind == 'advance' or false
    local limits={}
    for name,value in raw_ops.iter,S.limits do
      if raw_ops.kind(name)=='string' and raw_ops.kind(value)=='number' then limits[name]=value end
    end
    local ctx = {operation=operation,profile=S.profile,version='tset/1',request=request,
      space=space,op=request.op,intent=request.intent,result=request.result or '',
      original_fence=request.fence==true,
      notes=request.notes,notes_array=request.notes,notes_implicit=notes_implicit,
      original_note_count=original_note_count,
      request_epoch=request.epoch,write_epoch=request.epoch,limits=limits,
      original_rowsets=original_rowsets,original_advance=original_advance,
      original_advance_index=advance_index,
      original_advance_from=original_advance and advance.from or nil,
      defs={},before={},types={},accesses={},row_scores={},stream_info={},cell_deltas={},cell_incoming={},
      budget={store_commands=0,planned_commands=0,planned_argv_bytes=0,fetched_bytes=0,
        field=0,cell=0,record=0,range_id=0,log_id=0,generated_log_bytes=0}}
    ctx.epoch_key = space .. 'sprint:epoch'
    ctx.epoch_field = 'n'
    ctx.record_key = function(t,id) return ctx.defs[t].member_prefix .. id end
    ctx.table_key = function(t) return space .. 'table:' .. t end
    ctx.table_prefix = function(t,e) return space .. 'table:' .. t .. (e == '0' and '' or ':' .. e) end
    ctx.rows_key = function(t,e) return ctx.table_prefix(t,e) .. ':rows' end
    -- Amendment 2026-09-30 (property), section 1: one hash per table per
    -- epoch beside its rows key, inside the reserved table: namespace.
    ctx.props_key = function(t,e) return ctx.table_prefix(t,e) .. ':props' end
    ctx.cell_key = function(t,e,row,col) return ctx.table_prefix(t,e) .. ':cell:' .. row .. ':' .. col end
    ctx.definition_key = function(t,e) return ctx.table_prefix(t,e) .. ':definition' end
    ctx.log_key = function(e) return space .. 'sprint:log@' .. e end
    ctx.history_key = function(e,about) return space .. 'sprint:cl:' .. about .. '@' .. e end
    ctx.done_key = function(e) return space .. 'sprint:done@' .. e end
    ctx.intent_digest=request.intent and redis.sha1hex(request.intent) or nil
    remember_context(ctx)
    return ctx,nil
  end
  local read_kinds = {HGET='hash',HMGET='hash',HLEN='hash',HKEYS='hash',HSTRLEN='hash',HEXISTS='hash',
    HGETALL='hash',ZSCORE='zset',ZMSCORE='zset',ZCARD='zset',ZCOUNT='zset',ZRANGE='zset',
    ZRANGEBYSCORE='zset',ZREVRANGE='zset',ZREVRANGEBYSCORE='zset',XRANGE='stream',XREVRANGE='stream',
    XLEN='stream',XINFO='stream',LLEN='list',LRANGE='list',LINDEX='list',GET='string',STRLEN='string'}
  local function scalar_argv(argv)
    if type(argv) ~= 'table' then return nil,S.refuse('REQUEST') end
    local n,bytes = #argv,0
    if n < 1 then return nil,S.refuse('REQUEST') end
    if n > 2002 then return nil,S.refuse('LIMIT',{budget='argv',actual=n,limit=2002}) end
    for k,v in pairs(argv) do
      if type(k) ~= 'number' or k < 1 or k > n or k ~= math.floor(k) or type(v) ~= 'string' then return nil,S.refuse('REQUEST') end
    end
    for i=1,n do if type(argv[i]) ~= 'string' then return nil,S.refuse('REQUEST') end; bytes=bytes+#argv[i] end
    return bytes,nil
  end
  local function access_shape(ctx, descriptor, mode, kind, key)
    local access = descriptor.access
    if type(access) ~= 'table' or #access ~= 1 then return nil,S.refuse('REQUEST') end
    local a = access[1]
    if type(a) ~= 'table' or a.key ~= key or a.kind ~= kind or a.mode ~= mode or
        type(key) ~= 'string' or string.sub(key,1,#ctx.space) ~= ctx.space then return nil,S.refuse('REQUEST') end
    local previous = ctx.accesses[key]
    if previous and previous ~= kind then return nil,S.refuse('WRONGTYPE') end
    ctx.accesses[key] = kind
    return true,nil
  end
  function S.access(ctx,key,kind,mode)
    return access_shape(ctx,{access={{key=key,kind=kind,mode=mode}}},mode,kind,key)
  end
  local function acl(ctx, argv)
    ctx.budget.store_commands = ctx.budget.store_commands + 1
    if not redis.acl_check_cmd(unpack(argv)) then return nil,S.refuse('NOPERM') end
    return true,nil
  end
  local function successor_key(ctx,key)
    if ctx.write_epoch==ctx.request_epoch or not key then return false end
    if key==ctx.log_key(ctx.write_epoch) or key==ctx.space..'sprint:epoch@'..ctx.write_epoch then return true end
    if string.sub(key,1,#ctx.space+10)==ctx.space..'sprint:cl:' and string.sub(key,-#ctx.write_epoch-1)=='@'..ctx.write_epoch then return true end
    for t,def in pairs(ctx.defs) do
      local prefix=ctx.table_prefix(t,ctx.write_epoch)..':'
      if string.sub(key,1,#prefix)==prefix or string.sub(key,1,#def.member_prefix)==def.member_prefix then return true end
    end
    return false
  end
  local function readcmd(ctx, descriptor, reserve_bytes, probe_kind)
    local monotone,problem=budget_monotone(ctx,false);if problem then return nil,problem end
    local _,err = scalar_argv(descriptor.argv); if err then return nil,err end
    local argv = descriptor.argv
    local command = argv[1]
    local kind = read_kinds[command]
    local key = command == 'XINFO' and argv[3] or argv[2]
    if not kind and command ~= 'TYPE' and command ~= 'TIME' and command ~= 'EXISTS' then return nil,S.refuse('REQUEST') end
    if kind then
      _,err = access_shape(ctx,descriptor,'read',kind,key); if err then return nil,err end
    elseif command ~= 'TIME' then
      if type(key) ~= 'string' or string.sub(key,1,#ctx.space) ~= ctx.space then return nil,S.refuse('REQUEST') end
    end
    if type(reserve_bytes) ~= 'number' or reserve_bytes < 0 or reserve_bytes ~= math.floor(reserve_bytes) then return nil,S.refuse('REQUEST') end
    if ctx.budget.fetched_bytes + reserve_bytes > S.limits.fetched_bytes then
      return nil,limit(ctx,'fetched_bytes',ctx.budget.fetched_bytes+reserve_bytes,S.limits.fetched_bytes)
    end
    -- Record payload work is counted by record/field observations and actual
    -- commands; the cell/key ceiling covers cells and structural key probes.
    if command~='TIME' and probe_kind~='field' and probe_kind~='record' then _,err=S.charge(ctx,'cell',1);if err then return nil,err end end
    local category=(probe_kind or 'metadata')..'_commands'
    ctx.budget[category]=(ctx.budget[category] or 0)+1
    _,err=acl(ctx,argv)
    local _,floor_error=budget_monotone(ctx,true);if floor_error then return nil,floor_error end
    if err then return nil,err end
    ctx.budget.store_commands = ctx.budget.store_commands + 1
    budget_monotone(ctx,true)
    local value = redis.pcall(unpack(argv))
    if type(value) == 'table' and value.err then
      if string.find(value.err,'WRONGTYPE',1,true) then return nil,S.refuse(successor_key(ctx,key) and 'DRIFT' or 'WRONGTYPE') end
      if string.find(value.err,'NOPERM',1,true) then return nil,S.refuse('NOPERM') end
      -- A malformed internal descriptor is not a proved no-change refusal.
      error(value.err,0)
    end
    if kind then ctx.types[key] = ctx.types[key] == 'none' and 'none' or kind end
    if command=='XINFO' and argv[2]=='STREAM' then ctx.stream_info[key]=value end
    if command == 'TYPE' then ctx.types[key] = type(value)=='table' and value.ok or value end
    local bytes = payload(value)
    if bytes > reserve_bytes then return nil,S.refuse('DRIFT',{budget='read_reservation',actual=bytes,limit=reserve_bytes}) end
    ctx.budget.fetched_bytes = ctx.budget.fetched_bytes + bytes
    local monotone,problem=budget_monotone(ctx,true);if problem then return nil,problem end
    return value,nil
  end
  function S.readcmd(ctx,descriptor,reserve_bytes,probe_kind)
    if callback_depth>0 then return nil,S.refuse('CONFIG') end
    return readcmd(ctx,descriptor,reserve_bytes,probe_kind)
  end
  local function begin_query(ctx,index)
    local saved=same_context(ctx) and current_seal or nil
    if callback_depth>0 or not saved or saved.fields.operation~='read' or
        not read_unchanged(ctx,saved) or type(index)~='number' or index~=math.floor(index) or
        index<0 or index>=#saved.request.queries or index~=(saved.dispatch_index or -1)+1 then
      return nil,S.refuse('CONFIG')
    end
    if saved.dispatch_index==nil then saved.read_active_epoch=ctx.active_epoch end
    saved.dispatch_index=index
    ctx.query_index=index
    return true,nil
  end
  local function read_cache_state(ctx)
    local saved=same_context(ctx) and current_seal or nil
    if not saved or saved.fields.operation~='read' or not read_unchanged(ctx,saved) or
        saved.dispatch_index==nil or ctx.query_index~=saved.dispatch_index then return nil,S.refuse('CONFIG') end
    local _,err=budget_monotone(ctx,false);if err then return nil,err end
    return saved,nil
  end
  -- Installed once by the read fragment. Only its lexical helpers receive
  -- the private read/dispatch closures; extensions cannot recover them.
  function S.bind_read_helpers(factory)
    S.bind_read_helpers=nil
    if not bindings_open then error('tset read helpers must bind during initialization',0) end
    local require_first_read_binding=not read_bound and function() end
    require_first_read_binding()
    read_bound=true
    read_cleanup=factory(function(ctx,descriptor,reserve_bytes,probe_kind)
      if callback_depth>0 then return readcmd(ctx,descriptor,reserve_bytes,probe_kind) end
      return S.readcmd(ctx,descriptor,reserve_bytes,probe_kind)
    end,begin_query,function(ctx,t)
      local saved=same_context(ctx) and current_seal or nil
      if not saved or saved.fields.operation~='read' or not read_unchanged(ctx,saved) or
          saved.dispatch_index==nil or ctx.query_index~=saved.dispatch_index or
          saved.fields.request_epoch~=saved.read_active_epoch then return nil,S.refuse('CONFIG') end
      local _,err=budget_monotone(ctx,false);if err then return nil,err end
      return load_defs(ctx,t)
    end,function(ctx)
      local saved,err=read_cache_state(ctx);if err then return nil,err end
      return saved.read_row_scores,nil
    end)
  end
  -- L2 owns the line reservation. Its separate initializer receives only
  -- exact-line authority, never the reader's arbitrary-descriptor closure.
  function S.bind_log_helpers(line_raw_reservation,factory)
    S.bind_log_helpers=nil
    -- FUNCTION LOAD exposes no type/math/error helpers. Deliberately invoke
    -- a named local guard only when its predicate holds; a rejected binding
    -- fails load with that guard's native noncallable-local diagnostic.
    local require_open_log_binding=bindings_open and not log_bound and function() end
    require_open_log_binding()
    log_bound=true
    -- Arithmetic rejects nonnumeric types; strict equality rejects numeric
    -- strings. The comparisons exclude NaN/infinities, then modulo proves
    -- integrality without accessing the unavailable load-time math table.
    local require_valid_log_reservation=line_raw_reservation==line_raw_reservation+0 and
      line_raw_reservation>0 and line_raw_reservation<=fetched_cap and
      line_raw_reservation%1==0 and function() end
    require_valid_log_reservation()
    local function authorize_line(ctx,seq,index)
      local saved=same_context(ctx) and current_seal or nil
      if not saved or saved.fields.operation~='read' or not read_unchanged(ctx,saved) or
          saved.dispatch_index==nil or index~=saved.dispatch_index or ctx.query_index~=index or
          (callback_depth>0 and (ctx~=active_callback_ctx or not saved.callback or saved.callback.index~=index)) then
        return nil,S.refuse('CONFIG')
      end
      local _,err=budget_monotone(ctx,false);if err then return nil,err end
      if type(seq)~='string' or not S.uint(seq) or seq=='0' or S.cmp(seq,'9007199254740991')>0 then
        return nil,S.refuse('REQUEST',{query_index=index})
      end
      return saved.fields.space..'sprint:log@'..saved.fields.request_epoch,nil
    end
    local function read_line_raw(ctx,seq,index)
      local key,err=authorize_line(ctx,seq,index)
      if err then return nil,err,false end
      if fetched_cap-ctx.budget.fetched_bytes<line_raw_reservation then return nil,nil,true end
      local value
      value,err=readcmd(ctx,{argv={'XRANGE',key,seq..'-0',seq..'-0','COUNT','1'},
        access={{key=key,kind='stream',mode='read'}}},line_raw_reservation,'log')
      return value,err,false
    end
    local cleanup=factory(authorize_line,read_line_raw)
    -- The factory and its assignment-only cleanup must both be callable at
    -- load time. Validate cleanup before publishing it to the lifecycle.
    cleanup()
    log_cleanup=cleanup
  end
  function S.read_callback(ctx,fn,q,index)
    local saved=same_context(ctx) and current_seal or nil
    if callback_depth>0 or not saved or saved.callback or saved.fields.operation~='read' or
        saved.dispatch_index==nil or index~=saved.dispatch_index then return nil,S.refuse('CONFIG'),false end
    local boundary_error=S.refuse('CONFIG',{query_index=index})
    -- Keep the authority window closed until every check has completed. No
    -- caller-owned table may be inspected after a failed boundary at depth 0.
    callback_depth=callback_depth+1
    active_callback_ctx=ctx
    local ok,value,problem,valid=raw_ops.call(function()
      local retained=graph_tables({ctx,q})
      if not retained or not original_unchanged(ctx) or
          not read_identity_unchanged(ctx,saved) or raw_ops.get(ctx,'query_index')~=index then return nil,nil,false end
      local budget=raw_ops.get(ctx,'budget')
      local floors={};for key,item in raw_ops.iter,budget do floors[key]=item end
      saved.callback={floors=floors,budget=budget,index=index}
      local called,result,err=raw_ops.call(fn,ctx,q,index)
      if raw_ops.kind(result)=='table' then retained[#retained+1]=result end
      if raw_ops.kind(err)=='table' then retained[#retained+1]=err end
      if not graph_tables(retained) then return nil,nil,false end
      local _,budget_error=budget_monotone(ctx,false)
      if not called or budget_error or not original_unchanged(ctx) then return nil,nil,false end
      return result,err,true
    end)
    saved.callback=nil
    callback_depth=callback_depth-1
    active_callback_ctx=nil
    if not ok or not valid then return nil,boundary_error,false end
    return value,problem,true
  end
  function S.writecmd(ctx, argv, access)
    local bytes,err=scalar_argv(argv); if err then return nil,err end
    local commands=ctx.budget.planned_commands+1
    local total=ctx.budget.planned_argv_bytes+bytes
    if commands>S.limits.commands then return nil,limit(ctx,'commands',commands,S.limits.commands) end
    if total>S.limits.argv_bytes then return nil,limit(ctx,'argv_bytes',total,S.limits.argv_bytes) end
    ctx.budget.planned_commands=commands; ctx.budget.planned_argv_bytes=total
    return {argv=argv,access=access},nil
  end
  function S.command(ctx,command,key,kind,args)
    local argv={command,key}
    for _,v in ipairs(args or {}) do argv[#argv+1]=v end
    return S.writecmd(ctx,argv,{{key=key,kind=kind,mode='write'}})
  end
  local function rd(ctx,argv,kind,reserve,probe)
    local key=argv[1]=='XINFO' and argv[3] or argv[2]
    local descriptor={argv=argv,access={{key=key,kind=kind,mode='read'}}}
    if callback_depth>0 then return readcmd(ctx,descriptor,reserve,probe) end
    return S.readcmd(ctx,descriptor,reserve,probe)
  end
  S.rd=function(ctx,argv,kind,reserve,probe)
    local key=argv[1]=='XINFO' and argv[3] or argv[2]
    return S.readcmd(ctx,{argv=argv,access={{key=key,kind=kind,mode='read'}}},reserve,probe)
  end
  local function stage(ctx,plan,command,key,kind,args)
    local d,err=S.command(ctx,command,key,kind,args); if err then return nil,err end
    plan.commands[#plan.commands+1]=d; return true,nil
  end
  S.stage=stage
  function S.open(version,raw)
    local request,err=S.validate(version,raw,'step'); if err then return nil,err end
    local ctx;ctx,err=S.context(request,'step');if err then return nil,err end
    ctx.raw_request=raw; ctx.request_hash=redis.sha1hex(raw)
    ctx.intent_digest=request.intent and redis.sha1hex(request.intent) or nil
    local ok; ok,err=S.open_state(ctx); if err then return nil,err end
    if not ctx.replay then
      local now; now,err=S.readcmd(ctx,{argv={'TIME'},access={}},64,'metadata'); if err then return nil,err end
      ctx.now_ms=now[1] .. string.format('%03d',math.floor(tonumber(now[2])/1000))
    end
    local saved=current_seal
    saved.opened=true;saved.raw_request=raw;saved.request_hash=ctx.request_hash
    saved.active_epoch=ctx.active_epoch;saved.now_ms=ctx.now_ms
    return ctx,nil
  end

  -- Point projections are cached for the entire invocation, including S.before
  -- used by enclosing preplanners. Only newly requested fields cause reads.
  local function before(ctx,t,ids,fields,preload,record_charged)
    local _,problem=budget_monotone(ctx,false);if problem then return nil,problem end
    if type(ids)~='table' or #ids>10000 or type(fields or {})~='table' or #(fields or {})>384 then return nil,S.refuse('LIMIT') end
    for _,id in ipairs(ids) do if not S.name(id) then return nil,S.refuse('REQUEST') end end
    for _,field in ipairs(fields or {}) do if not S.name(field) then return nil,S.refuse('REQUEST') end end
    if ctx.operation=='read' then
      local ok,err=S.ensure_read_table(ctx,t,ctx.query_index);if err then return nil,err end
    elseif not ctx.defs[t] then
      local ok,err=S.load_defs(ctx,t);if err then return nil,err end
    end
    local def=ctx.defs[t]; if not def then return nil,S.refuse('NOTABLE',{table=t}) end
    if ctx.operation=='read' then
      local ok,err
      if not record_charged then ok,err=S.charge(ctx,'record',#ids);if err then return nil,err end end
      ok,err=S.charge(ctx,'field',#ids*#(fields or {}));if err then return nil,err end
    end
    local records,row_scores=ctx.before,ctx.row_scores
    if ctx.operation=='read' then
      local saved,err=read_cache_state(ctx);if err then return nil,err end
      records,row_scores=saved.read_before,saved.read_row_scores
    end
    local cache=records[t]; if not cache then cache={};records[t]=cache end
    local answer={}
    for _,id in ipairs(ids) do
      local key=ctx.operation=='read' and def.member_prefix..id or ctx.record_key(t,id)
      local rec=cache[id]
      local err
      if not rec then
        if ctx.operation=='step' then
          ctx.before_count=(ctx.before_count or 0)+1
          if ctx.before_count>6000 then return nil,S.refuse('LIMIT',{budget='before_records',actual=ctx.before_count,limit=6000}) end
          local charged,cerr=S.charge(ctx,'record',1);if cerr then return nil,cerr end
        end
        ctx.budget.record_metadata_fields=(ctx.budget.record_metadata_fields or 0)+3
        local vals=preload and preload.vals
        if not vals then vals,err=rd(ctx,{'HMGET',key,'epoch','revision','place:'..t},'hash',600,'record');if err then return nil,err end end
        local size=preload and preload.size
        if not size then size,err=rd(ctx,{'HLEN',key},'hash',32,'record');if err then return nil,err end end
        rec={exists=size>0,epoch=cjson.null,revision=cjson.null,place=cjson.null,score=cjson.null,fields={},field_count=0,hash_size=size}
        if rec.exists then
          if not S.uint(vals[1]) or not S.uint(vals[2]) or vals[2]=='0' then return nil,S.refuse('DRIFT',{table=t,ids={id}}) end
          rec.epoch=vals[1];rec.revision=vals[2]
          rec.field_count=size-2-(vals[3] and 1 or 0)
          if rec.field_count<0 or rec.field_count>128 then return nil,S.refuse('DRIFT',{table=t,ids={id}}) end
          if vals[3] then
            local row,col=S.cell(vals[3])
            if not row or not def.column_set[col] then return nil,S.refuse('DRIFT',{table=t,ids={id}}) end
            rec.place={row=row,col=col}
            local read_prefix
            if ctx.operation=='read' then
              read_prefix=current_seal.fields.space..'table:'..t..(rec.epoch=='0' and '' or ':'..rec.epoch)
            end
            local rowskey=read_prefix and read_prefix..':rows' or ctx.rows_key(t,rec.epoch)
            row_scores[rowskey]=row_scores[rowskey] or {}
            local rank=row_scores[rowskey][row]
            if rank==nil then
              rank,err=rd(ctx,{'ZSCORE',rowskey,row},'zset',32,'cell')
              if err then return nil,err end
              row_scores[rowskey][row]=rank
            end
            if not rank then return nil,S.refuse('DRIFT',{table=t,ids={id},rows={row}}) end
            local cellkey=read_prefix and read_prefix..':cell:'..row..':'..col or ctx.cell_key(t,rec.epoch,row,col)
            local score;score,err=rd(ctx,{'ZSCORE',cellkey,id},'zset',32,'cell')
            if err then return nil,err end
            if not score then return nil,S.refuse('DRIFT',{table=t,ids={id},cells={vals[3]}}) end
            if not S.score(score) then return nil,S.refuse('DRIFT',{table=t,ids={id}}) end
            rec.score=score
          end
        end
        cache[id]=rec
      end
      if preload and preload.fields then
        for field,value in pairs(preload.fields) do rec.fields[field]={present=true,value=value} end
      end
      local missing={}
      for _,field in ipairs(fields or {}) do if not rec.fields[field] then missing[#missing+1]=field end end
      if #missing>0 then
        if ctx.operation~='read' then
          local ok;ok,err=S.charge(ctx,'field',#missing);if err then return nil,err end
        end
        local argv={'HMGET',key}
        local reserve=#missing*S.limits.field_value
        if reserve>S.limits.fetched_bytes-ctx.budget.fetched_bytes then
          reserve=0
          for _,field in ipairs(missing) do
            local n;n,err=rd(ctx,{'HSTRLEN',key,field},'hash',32,'field');if err then return nil,err end
            if n>S.limits.field_value then return nil,S.refuse('DRIFT',{table=t,ids={id}}) end
            reserve=reserve+n
          end
        end
        for _,field in ipairs(missing) do argv[#argv+1]=field end
        local vals;vals,err=rd(ctx,argv,'hash',reserve,'field');if err then return nil,err end
        for i,field in ipairs(missing) do
          if vals[i] and #vals[i]>S.limits.field_value then return nil,S.refuse('DRIFT',{table=t,ids={id}}) end
          rec.fields[field]={present=vals[i]~=false and vals[i]~=nil,value=vals[i] or cjson.null}
        end
      end
      answer[id]=rec
    end
    return answer,nil
  end
  -- Copy only this occurrence's projection, not the accumulated field cache.
  -- Its work stays within the already charged record/field observations.
  local function read_projection(rec,fields)
    local place=rec.place
    if type(place)=='table' then place={row=place.row,col=place.col} end
    local out={exists=rec.exists,epoch=rec.epoch,revision=rec.revision,place=place,
      score=rec.score,fields={},field_count=rec.field_count,hash_size=rec.hash_size}
    for _,field in ipairs(fields or {}) do
      local value=rec.fields[field]
      out.fields[field]={present=value.present,value=value.value}
    end
    return out
  end
  function S.before(ctx,t,ids,fields)
    local found,err=before(ctx,t,ids,fields);if err then return nil,err end
    if ctx.operation~='read' then return found,nil end
    local answer={}
    for id,rec in pairs(found) do answer[id]=read_projection(rec,fields) end
    return answer,nil
  end
  local function whole_projection(rec,fields)
    local names=S.array();for i,field in ipairs(fields) do names[i]=field end
    local out=read_projection(rec,fields)
    out.whole_names=names
    return out,names,nil
  end
  -- Whole-record reads share metadata/placement validation and the projection
  -- cache. The preload is lexical, so extensions cannot fabricate facts.
  function S.before_whole(ctx,t,id)
    local _,err=budget_monotone(ctx,false);if err then return nil,nil,err end
    if ctx.operation~='read' or not S.name(id) then return nil,nil,S.refuse('REQUEST') end
    local def;def,err=S.ensure_read_table(ctx,t,ctx.query_index);if err then return nil,nil,err end
    local saved;saved,err=read_cache_state(ctx);if err then return nil,nil,err end
    local cached=saved.read_before[t] and saved.read_before[t][id]
    if cached and cached.whole_names then
      local found;found,err=before(ctx,t,{id},cached.whole_names)
      if err then return nil,nil,err end
      return whole_projection(found[id],cached.whole_names)
    end
    local ok;ok,err=S.charge(ctx,'record',1);if err then return nil,nil,err end
    local key=def.member_prefix..id
    local size=cached and cached.hash_size
    if not size then size,err=rd(ctx,{'HLEN',key},'hash',32,'record');if err then return nil,nil,err end end
    if size>131 then return nil,nil,S.refuse('DRIFT',{table=t,ids={id}}) end
    local preload={size=size}
    local names={}
    local reserve=size*(256+S.limits.field_value)
    -- The n-2 bound also reserves field occurrences before fetching payload.
    -- Near that ceiling HKEYS discovers the exact projection first.
    if reserve<=S.limits.fetched_bytes-ctx.budget.fetched_bytes and
        math.max(0,size-2)<=S.limits.read_fields-ctx.budget.field then
      local values;values,err=rd(ctx,{'HGETALL',key},'hash',reserve,'record');if err then return nil,nil,err end
      local raw={}
      for i=1,#values,2 do raw[values[i]]=values[i+1];names[#names+1]=values[i] end
      preload.vals={raw.epoch or false,raw.revision or false,raw['place:'..t] or false}
      preload.fields=raw
    else
      names,err=rd(ctx,{'HKEYS',key},'hash',size*256,'record');if err then return nil,nil,err end
    end
    local fields={}
    for _,name in ipairs(names) do
      if name~='epoch' and name~='revision' and string.sub(name,1,6)~='place:' then
        if not S.name(name) or (preload.fields and #preload.fields[name]>S.limits.field_value) then return nil,nil,S.refuse('DRIFT',{table=t,ids={id}}) end
        fields[#fields+1]=name
      elseif preload.fields then preload.fields[name]=nil end
    end
    if #fields>128 then return nil,nil,S.refuse('DRIFT',{table=t,ids={id}}) end
    table.sort(fields)
    local found;found,err=before(ctx,t,{id},fields,preload,true)
    if err then return nil,nil,err end
    found[id].whole_names=fields
    return whole_projection(found[id],fields)
  end
  local function sameplace(a,row,col) return type(a)=='table' and a.row==row and a.col==col end
  local function sorted_fields(set)
    local fields={};for f in pairs(set) do fields[#fields+1]=f end;table.sort(fields);return fields
  end
  local function delta(ctx,key,n,incoming)
    ctx.cell_deltas[key]=(ctx.cell_deltas[key] or 0)+n
    if incoming then ctx.cell_incoming[key]=true end
  end
  function S.zguard(ctx,key,g)
    if type(g)~='table' or g.kind~='rcount' or not S.bound(g.min) or not S.bound(g.max) then return nil,S.refuse('REQUEST') end
    for k in pairs(g) do if k~='kind' and k~='min' and k~='max' and k~='atleast' and k~='atmost' then return nil,S.refuse('REQUEST') end end
    if g.atleast==nil and g.atmost==nil then return nil,S.refuse('REQUEST') end
    for _,bound in ipairs({'atleast','atmost'}) do local v=g[bound]
      if v~=nil and (type(v)~='number' or v<0 or v>9007199254740991 or v~=math.floor(v)) then return nil,S.refuse('REQUEST') end
    end
    if g.atleast and g.atmost and g.atleast>g.atmost then return nil,S.refuse('REQUEST') end
    local n,err=rd(ctx,{'ZCOUNT',key,g.min,g.max},'zset',32,'cell');if err then return nil,err end
    if (g.atleast and n<g.atleast) or (g.atmost and n>g.atmost) then return nil,S.refuse('RANGECOUNT',{cells={key}}) end
    return n,nil
  end
  local function input_counters(ctx)
    local b=ctx.budget
    b.input_entries=#(ctx.request.entries or {})
    b.input_queries=#(ctx.request.queries or {})
    b.input_notes=#(ctx.request.notes or {})
    b.member_candidates=0;b.guard_members=0
    for _,e in ipairs(ctx.request.entries or {}) do
      if e.kind=='guard' then b.guard_members=b.guard_members+#e.ids
      elseif e.kind=='create' or e.kind=='move' or e.kind=='remove' then b.member_candidates=b.member_candidates+#e.ids end
    end
  end
  -- Table properties: L1 contract amendment 2026-09-30 (property), section 2.
  -- A prop and a propguard both compare with the write epoch's pre-state (one
  -- HGET per distinct name, charged as a field observation), so a guard beside
  -- a write on the same pair reads the value from before the step. A changed
  -- prop is one HSET; no log line is planned for it.
  local function prop_pre(ctx,t,name,counts)
    local key=ctx.props_key(t,ctx.write_epoch)
    ctx.prop_pre=ctx.prop_pre or {}
    local cache=ctx.prop_pre[key]
    if not cache then
      if ctx.write_epoch~=ctx.request_epoch then
        -- A touched successor hash must be empty even for a no-op or guard,
        -- which may never put this key into the final command preflight.
        local n,err=rd(ctx,{'HLEN',key},'hash',32,'cell')
        if err then return nil,nil,err end
        if n~=0 then return nil,nil,S.refuse('DRIFT',{table=t}) end
        -- Reuse this observation for new-name capacity in this plan.
        counts[t]=0
      end
      cache={};ctx.prop_pre[key]=cache
    end
    if cache[name]==nil then
      local ok,err=S.charge(ctx,'field',1);if err then return nil,nil,err end
      local value;value,err=rd(ctx,{'HGET',key,name},'hash',S.limits.field_value,'field');if err then return nil,nil,err end
      if value and #value>S.limits.field_value then return nil,nil,S.refuse('DRIFT',{table=t}) end
      cache[name]=value or false
    end
    return key,cache[name],nil
  end
  local function prop_entry(ctx,plan,e,ix,result,counts)
    local key,before,err=prop_pre(ctx,e.t,e.name,counts);if err then return nil,err end
    result.name=e.name;result.before=before or cjson.null
    if e.kind=='propguard' then
      if (e.value==nil and before) or (e.value~=nil and before~=e.value) then
        return nil,S.refuse('PROPGUARD',{entry_index=ix-1,table=e.t,name=e.name})
      end
      return true,nil
    end
    result.value=e.value
    if before==e.value then return true,nil end
    if not before then
      -- A new name counts toward the table's 64 properties at this epoch.
      local n=counts[e.t]
      if n==nil then n,err=rd(ctx,{'HLEN',key},'hash',32,'cell');if err then return nil,err end end
      n=n+1;counts[e.t]=n
      if n>S.limits.props then
        return nil,S.refuse('LIMIT',{entry_index=ix-1,table=e.t,budget='properties',actual=n,limit=S.limits.props})
      end
    end
    local ok;ok,err=stage(ctx,plan,'HSET',key,'hash',{e.name,e.value});if err then return nil,err end
    plan.changed=plan.changed+1;plan.changed_per_entry[ix]=plan.changed_per_entry[ix]+1
    return true,nil
  end
  local function fence_unchanged(ctx)
    return ctx.request.fence==(ctx.original_fence and true or nil)
  end
  function S.plan(ctx)
    if callback_depth>0 or ctx.operation~='step' then return nil,S.refuse('CONFIG') end
    if not original_unchanged(ctx) then return nil,S.refuse('REQUEST') end
    if not fence_unchanged(ctx) or ctx.original_fence then return nil,S.refuse('REQUEST') end
    -- Preplanners may append notes, but replacing either shared array loses
    -- the aligned order that the log planner uses for note_seqs.
    if ctx.notes~=ctx.notes_array or ctx.request.notes~=ctx.notes_array then
      return nil,S.refuse('REQUEST')
    end
    local _,err=S.validate_effective_step(ctx.request,ctx.original_note_count);if err then return nil,err end
    if ctx.request.space~=ctx.space or ctx.request.epoch~=ctx.request_epoch or ctx.request.op~=ctx.op or
        ctx.request.intent~=ctx.intent or (ctx.request.result or '')~=ctx.result then return nil,S.refuse('REQUEST') end
    local size_request=ctx.request
    if ctx.notes_implicit and #ctx.notes==0 then
      -- Preserve the original absent-notes request at the byte boundary.
      -- The shared empty array remains available to the log planner.
      size_request={}
      for k,v in pairs(ctx.request) do if k~='notes' then size_request[k]=v end end
    end
    local encoded_ok,encoded=pcall(S.json.encode,size_request)
    if not encoded_ok then return nil,S.refuse('REQUEST') end
    if #encoded>S.limits.request_bytes then return nil,limit(ctx,'request_bytes',#encoded,S.limits.request_bytes) end
    local rowset_count=0
    for _,entry in ipairs(ctx.request.entries) do
      if entry.kind~='rowset' then break end
      rowset_count=rowset_count+1
    end
    if rowset_count~=#ctx.original_rowsets then return nil,S.refuse('REQUEST') end
    for i,saved in ipairs(ctx.original_rowsets) do
      local entry=ctx.request.entries[i]
      if entry.kind~='rowset' or entry.t~=saved.t or #entry.rows~=#saved.rows then return nil,S.refuse('REQUEST') end
      for j,row in ipairs(saved.rows) do
        if entry.rows[j].row~=row.row or entry.rows[j].rank~=row.rank then return nil,S.refuse('REQUEST') end
      end
    end
    local advance=ctx.request.entries[ctx.original_advance_index]
    local has_advance=advance and advance.kind=='advance' or false
    if has_advance~=ctx.original_advance or
        (has_advance and advance.from~=ctx.original_advance_from) then return nil,S.refuse('REQUEST') end
    input_counters(ctx)
    _,err=S.load_defs(ctx);if err then return nil,err end
    local guarded;guarded,err=S.rowset_check(ctx);if err then return nil,err end
    local plan={commands={},entries=S.array(),before=ctx.before,rows=S.array(),advance=cjson.null,
      changed=0,guarded=0,changed_per_entry=S.array(),budget=ctx.budget}
    local topo;topo,err=S.rows_collect(ctx);if err then return nil,err end
    local seen,prop_counts={},{}
    for ix,e in ipairs(ctx.request.entries) do
      local detail={entry_index=ix-1,table=e.t}
      local result={kind=e.kind,table=e.t,entry_index=ix-1,changed_ids=S.array(),before_scores=S.array(),after_scores=S.array(),
        before_revs=S.array(),after_revs=S.array(),field_changes=S.array(),about=S.array(),meta=e.meta or {}}
      plan.entries[ix]=result;plan.changed_per_entry[ix]=0
      if e.kind=='rowset' then
        local rows=S.array()
        for i,row in ipairs(e.rows) do rows[i]={row=row.row,rank=row.rank} end
        result.rows=rows
      elseif e.kind=='advance' then
        plan.advance={from=ctx.request_epoch,to=ctx.write_epoch}
        result.from=ctx.request_epoch;result.to=ctx.write_epoch
      elseif e.kind=='count' or e.kind=='rcount' then
        local total,observed=0,{}
        for ci,cell in ipairs(e.cells) do
          local n=observed[cell]
          if n==nil then
            local row,col=S.cell(cell)
            if not ctx.defs[e.t].column_set[col] then detail.cells={cell};return nil,S.refuse('NOCOL',detail) end
            local ok;ok,err=S.rows_require(ctx,topo,e.t,row,'count',ix-1);if err then return nil,err end
            local argv=e.kind=='count' and {'ZCARD',ctx.cell_key(e.t,ctx.write_epoch,row,col)} or {'ZCOUNT',ctx.cell_key(e.t,ctx.write_epoch,row,col),e.min,e.max}
            n,err=rd(ctx,argv,'zset',32,'cell');if err then return nil,err end
            observed[cell]=n
            if e.kind=='rcount' then
              total=total+n
              if total>9007199254740991 then return nil,S.refuse('OVERFLOW',detail) end
            end
          end
          -- Every aligned constraint still applies; repeated cells therefore
          -- use their tightest maximum without repeating a store observation.
          if e.kind=='count' and n>e.max[ci] then detail.cells={cell};return nil,S.refuse('CELLFULL',detail) end
        end
        if e.kind=='rcount' and ((e.atleast and total<e.atleast) or (e.atmost and total>e.atmost)) then detail.cells=e.cells;return nil,S.refuse('RANGECOUNT',detail) end
      elseif e.kind=='prop' or e.kind=='propguard' then
        local ok;ok,err=prop_entry(ctx,plan,e,ix,result,prop_counts);if err then return nil,err end
      elseif e.kind~='rows' then
        local def=ctx.defs[e.t]
        if not def then return nil,S.refuse('NOTABLE',detail) end
        local sr,sc,dr,dc
        if e.from then sr,sc=S.cell(e.from);if not def.column_set[sc] then detail.cells={e.from};return nil,S.refuse('NOCOL',detail) end
          local ok;ok,err=S.rows_require(ctx,topo,e.t,sr,'source',ix-1);if err then return nil,err end end
        local dest=e.to or (e.kind~='remove' and e.from or nil)
        if dest then dr,dc=S.cell(dest);if not def.column_set[dc] then detail.cells={dest};return nil,S.refuse('NOCOL',detail) end
          if e.kind~='guard' and (dr~=sr or dc~=sc) then local ok;ok,err=S.rows_require(ctx,topo,e.t,dr,'destination',ix-1);if err then return nil,err end end end
        result.from=e.from;result.to=dest
        seen[e.t]=seen[e.t] or {}
        for i,id in ipairs(e.ids) do
          detail.ids={id}
          if seen[e.t][id] then return nil,S.refuse('TWICE',detail) end
          seen[e.t][id]=true
          local effective,projection={},{}
          for f,v in pairs(e.set or {}) do effective[f]=v;projection[f]=true end
          for f,v in pairs(e.each and e.each[i] or {}) do effective[f]=v;projection[f]=true end
          for _,f in ipairs(e.unset or {}) do projection[f]=true end
          for _,f in ipairs(e.before_fields or {}) do projection[f]=true end
          local before;before,err=S.before(ctx,e.t,{id},sorted_fields(projection));if err then
            err.detail.entry_index=ix-1;return nil,err end
          local b=before[id]
          if e.kind=='create' then
            if b.exists then return nil,S.refuse('EXISTS',detail) end
          else
            if not b.exists then return nil,S.refuse('MISSING',detail) end
            if b.epoch~=ctx.write_epoch then return nil,S.refuse('MEMBEREPOCH',detail) end
            if not sameplace(b.place,sr,sc) then detail.cells={e.from};return nil,S.refuse('PLACE',detail) end
            if e.revs and e.revs[i]~=b.revision then return nil,S.refuse('REVISION',detail) end
          end
          if e.kind=='guard' then plan.guarded=plan.guarded+1
          else
            local oldkey=sr and ctx.cell_key(e.t,ctx.write_epoch,sr,sc) or nil
            local newkey=dr and ctx.cell_key(e.t,ctx.write_epoch,dr,dc) or nil
            if newkey and newkey~=oldkey then
              local occupied;occupied,err=rd(ctx,{'ZSCORE',newkey,id},'zset',32,'cell');if err then return nil,err end
              if occupied then return nil,S.refuse('DRIFT',detail) end
            end
            local sets,unsets={},S.array()
            local fields=b.field_count
            for _,f in ipairs(sorted_fields(effective)) do local v=effective[f];local prior=b.fields[f]
              if not prior.present or prior.value~=v then sets[f]=v;if not prior.present then fields=fields+1 end end
            end
            local unset_seen={}
            for _,f in ipairs(e.unset or {}) do if not unset_seen[f] then
              unset_seen[f]=true
              if b.fields[f].present then unsets[#unsets+1]=f;fields=fields-1 end
            end end
            table.sort(unsets)
            if fields>128 then return nil,S.refuse('LIMIT',{entry_index=ix-1,table=e.t,ids={id},budget='stored_fields',actual=fields,limit=128}) end
            local after=e.kind=='remove' and cjson.null or (e.scores and e.scores[i] or b.score)
            local changed=e.kind=='create' or e.kind=='remove' or newkey~=oldkey or tonumber(after)~=tonumber(b.score) or next(sets)~=nil or #unsets>0
            if changed then
              if newkey and newkey==oldkey then
                local ok;ok,err=S.rows_require(ctx,topo,e.t,dr,'destination',ix-1);if err then return nil,err end
              end
              local revision=e.kind=='create' and '1' or S.next(b.revision)
              if not revision then return nil,S.refuse('OVERFLOW',detail) end
              local record=ctx.record_key(e.t,id)
              local hargs={'revision',revision}
              if e.kind=='create' then hargs[#hargs+1]='epoch';hargs[#hargs+1]=ctx.write_epoch end
              if newkey then hargs[#hargs+1]='place:'..e.t;hargs[#hargs+1]=dest end
              for _,f in ipairs(sorted_fields(sets)) do hargs[#hargs+1]=f;hargs[#hargs+1]=sets[f] end
              local ok;ok,err=stage(ctx,plan,'HSET',record,'hash',hargs);if err then return nil,err end
              local dels={};for _,f in ipairs(unsets) do dels[#dels+1]=f end
              if e.kind=='remove' then dels[#dels+1]='place:'..e.t end
              if #dels>0 then ok,err=stage(ctx,plan,'HDEL',record,'hash',dels);if err then return nil,err end end
              -- The same-cell score change also removes before adding.
              if oldkey then ok,err=stage(ctx,plan,'ZREM',oldkey,'zset',{id});if err then return nil,err end;delta(ctx,oldkey,-1) end
              if newkey then ok,err=stage(ctx,plan,'ZADD',newkey,'zset',{after,id});if err then return nil,err end;delta(ctx,newkey,1,newkey~=oldkey) end
              local j=#result.changed_ids+1
              result.changed_ids[j]=id;result.before_scores[j]=b.score;result.after_scores[j]=after
              result.before_revs[j]=b.revision;result.after_revs[j]=revision
              result.field_changes[j]={set=sets,unset=unsets};if e.about then result.about[j]=e.about[i] end
              plan.changed=plan.changed+1;plan.changed_per_entry[ix]=plan.changed_per_entry[ix]+1
            end
          end
        end
      end
    end
    local ok;ok,err=S.rows_plan(ctx,topo,plan);if err then return nil,err end
    if ctx.write_epoch~=ctx.request_epoch then ok,err=S.advance_plan(ctx,plan);if err then return nil,err end end
    current_seal.planned={entries=copy_value(ctx.request.entries),notes=copy_value(ctx.notes)}
    return plan,nil
  end

  function S.definition(ctx,name,h)
      if h.engine~='tset/1' then return nil,S.refuse('ENGINE',{table=name}) end
      if h.epoch_key~=ctx.epoch_key or h.epoch_field~=ctx.epoch_field or type(h.order)~='string' or
          type(h.member_prefix)~='string' or #h.member_prefix>512 or string.sub(h.member_prefix,-1)~=':' or
          string.sub(h.member_prefix,1,#ctx.space)~=ctx.space or h.member_prefix==ctx.space then
        return nil,S.refuse('CONFIG',{table=name})
      end
      local columns,column_set={},{}
      for col in string.gmatch(h.order,'[^,]+') do
        local spec=h['col:'..col]
        if not S.name(col) or not string.match(col,'^[A-Za-z0-9_][A-Za-z0-9_.-]*$') or column_set[col] or not spec or
            not (spec=='set' or string.match(spec,'^count:') or string.match(spec,'^members:') or string.match(spec,'^first:') or string.match(spec,'^last:')) then
          return nil,S.refuse('CONFIG',{table=name})
        end
        columns[#columns+1]=col;column_set[col]=true
      end
      if #columns==0 or #columns>32 or table.concat(columns,',')~=h.order then return nil,S.refuse('CONFIG',{table=name}) end
      -- Structural namespaces are reserved even when a particular key is absent.
      local structural={ctx.space..'table:',ctx.space..'sprint:',ctx.space..'tables'}
      for _,p in ipairs(structural) do
        if string.sub(p,1,#h.member_prefix)==h.member_prefix or string.sub(h.member_prefix,1,#p)==p then return nil,S.refuse('CONFIG',{table=name}) end
      end
      for other,def in pairs(ctx.defs) do
        local p=other~=name and def.member_prefix or nil
        if p then
        if string.sub(p,1,#h.member_prefix)==h.member_prefix or string.sub(h.member_prefix,1,#p)==p then return nil,S.refuse('CONFIG',{table=name}) end
      end
      end
      return {name=name,columns=columns,column_set=column_set,member_prefix=h.member_prefix,
        epoch_key=h.epoch_key,epoch_field=h.epoch_field,raw=h},nil
  end

  load_defs=function(ctx,extra_t)
    local names,seen={},{}
    if extra_t then
      if type(extra_t)~='string' or #extra_t>256 or not string.match(extra_t,'^[A-Za-z0-9_][A-Za-z0-9_.-]*$') then return nil,S.refuse('REQUEST') end
      names[1]=extra_t;seen[extra_t]=true
    end
    local request=callback_depth>0 and current_seal.request or ctx.request
    local entries=ctx.operation=='read' and request.queries or request.entries
    for _,e in ipairs(entries or {}) do if e.t and not seen[e.t] then seen[e.t]=true;names[#names+1]=e.t end end
    if ctx.original_advance then
      if not ctx.catalog then
        local length,err=rd(ctx,{'HSTRLEN',ctx.epoch_key,'tables'},'hash',32,'metadata')
        if err then return nil,err end
        if length>2048 then return nil,S.refuse('CONFIG') end
        local encoded;encoded,err=rd(ctx,{'HGET',ctx.epoch_key,'tables'},'hash',2048,'metadata')
        if err then return nil,err end
        if not encoded then return nil,S.refuse('CONFIG') end
        local valid,catalog=pcall(S.json.decode,encoded)
        if not valid or not S.is_array(catalog) or #catalog>4 then return nil,S.refuse('CONFIG') end
        local distinct={}
        for _,t in ipairs(catalog) do
          if type(t)~='string' or #t>256 or not string.match(t,'^[A-Za-z0-9_][A-Za-z0-9_.-]*$') or distinct[t] then return nil,S.refuse('CONFIG') end
          distinct[t]=true
        end
        ctx.catalog=catalog;ctx.catalog_set=distinct
      end
      for _,name in ipairs(names) do if not ctx.catalog_set[name] then return nil,S.refuse('CONFIG',{table=name}) end end
      for _,name in ipairs(ctx.catalog) do if not seen[name] then seen[name]=true;names[#names+1]=name end end
    end
    local total=0
    for _ in pairs(ctx.defs) do total=total+1 end
    for _,name in ipairs(names) do
      if not ctx.defs[name] then
      total=total+1
      if total>4 then return nil,S.refuse('LIMIT',{budget='tables',actual=total,limit=4}) end
      local key=ctx.operation=='read' and ctx.space..'table:'..name or ctx.table_key(name)
      local n,err=rd(ctx,{'HLEN',key},'hash',32,'metadata');if err then return nil,err end
      if n==0 then
        local detail={table=name}
        for i,e in ipairs(entries or {}) do if e.t==name then
          if ctx.operation=='read' then detail.query_index=i-1 else detail.entry_index=i-1 end
          break
        end end
        return nil,S.refuse(ctx.original_advance and ctx.catalog_set[name] and 'CONFIG' or 'NOTABLE',detail)
      end
      if n>48 then return nil,S.refuse('CONFIG',{table=name}) end
      local flat;flat,err=rd(ctx,{'HGETALL',key},'hash',49152,'metadata');if err then return nil,err end
      local h={};for i=1,#flat,2 do h[flat[i]]=flat[i+1] end
      local def;def,err=S.definition(ctx,name,h);if err then return nil,err end
      ctx.defs[name]=def
      end
    end
    return true,nil
  end
  function S.load_defs(ctx,extra_t)
    if callback_depth>0 then return nil,S.refuse('CONFIG') end
    if ctx.operation=='read' and (ctx~=current_ctx or ctx.request_epoch~=ctx.active_epoch or
        not original_unchanged(ctx)) then return nil,S.refuse('CONFIG') end
    return load_defs(ctx,extra_t)
  end

  -- This registry is intentionally closed. Adding a command requires proving
  -- its complete server-side failure domain here, before it may be committed.
  local write_kinds={HSET='hash',HDEL='hash',ZADD='zset',ZREM='zset',RPUSH='list',XADD='stream',SET='string'}
  local function validate_write(ctx,d)
    if type(d)~='table' then return nil,S.refuse('REQUEST') end
    local bytes,err=scalar_argv(d.argv);if err then return nil,err end
    local a=d.argv;local command,key=a[1],a[2];local n=#a
    local kind=write_kinds[command]
    if not kind then return nil,S.refuse('REQUEST') end
    local ok;ok,err=access_shape(ctx,d,'write',kind,key);if err then return nil,err end
    if command=='HSET' or command=='ZADD' then
      if n<4 or n%2~=0 then return nil,S.refuse('REQUEST') end
      if (n-2)/2>1000 then return nil,S.refuse('LIMIT') end
      if command=='ZADD' then for i=3,n,2 do if not S.score(a[i]) then return nil,S.refuse('REQUEST') end end end
    elseif command=='HDEL' or command=='ZREM' or command=='RPUSH' then
      if n<3 then return nil,S.refuse('REQUEST') end
      if n-2>1000 then return nil,S.refuse('LIMIT') end
    elseif command=='XADD' then
      if n<5 or n%2~=1 then return nil,S.refuse('REQUEST') end
      if (n-3)/2>1000 then return nil,S.refuse('LIMIT') end
      local seq=string.match(a[3],'^([0-9]+)%-0$')
      if not seq or not S.uint(seq) or seq=='0' or S.cmp(seq,'9007199254740991')>0 then return nil,S.refuse('LOGID') end
    elseif command=='SET' and n~=3 then return nil,S.refuse('REQUEST') end
    return bytes,nil
  end
  local function key_type(ctx,key)
    if ctx.types[key] then return ctx.types[key],nil end
    local value,err=S.readcmd(ctx,{argv={'TYPE',key},access={}},16,'metadata')
    if err then return nil,err end
    return type(value)=='table' and value.ok or value,nil
  end
  local function stream_head(ctx,key,kind)
    local info=ctx.stream_info[key]
    if not info then
      -- A typed read of an absent stream succeeds but does not establish existence.
      local observed,terr=S.readcmd(ctx,{argv={'TYPE',key},access={}},16,'metadata')
      if terr then return nil,terr end
      kind=type(observed)=='table' and observed.ok or observed
      if kind=='none' then return '0',nil end
      local err;info,err=rd(ctx,{'XINFO','STREAM',key},'stream',2101248,'log')
      if err then return nil,err end
    end
    local h={};for i=1,#info,2 do h[info[i]]=info[i+1] end
    local id=h['last-generated-id']
    local seq=type(id)=='string' and string.match(id,'^([0-9]+)%-0$') or nil
    if not seq or not S.uint(seq) or S.cmp(seq,'9007199254740991')>0 or
        type(h['entries-added'])~='number' or h['entries-added']~=tonumber(seq) or
        h.length~=h['entries-added'] then return nil,S.refuse('LOGID') end
    return seq,nil
  end
  local function empty_advance_key(ctx,key,kind)
    if ctx.write_epoch==ctx.request_epoch or key==ctx.epoch_key or key==ctx.done_key(ctx.request_epoch) or kind=='none' then return true,nil end
    local commands={hash='HLEN',zset='ZCARD',stream='XLEN',list='LLEN',string='STRLEN'}
    local n,err=rd(ctx,{commands[kind],key},kind,32,'metadata');if err then return nil,err end
    if n~=0 then return nil,S.refuse('DRIFT') end
    return true,nil
  end
  function S.prepare(ctx,table_plan,log_plan,other_plans)
    if callback_depth>0 or ctx.operation~='step' then return nil,S.refuse('CONFIG') end
    if not original_unchanged(ctx) then return nil,S.refuse('REQUEST') end
    if not fence_unchanged(ctx) then return nil,S.refuse('REQUEST') end
    local function aligned(value,count)
      if type(value)~='table' or #value~=count then return false end
      for key in pairs(value) do
        if type(key)~='number' or key~=math.floor(key) or key<1 or key>count then return false end
      end
      for i=1,count do if value[i]==nil then return false end end
      return true
    end
    local entries,notes=ctx.request.entries,ctx.notes
    if not current_seal.planned and (#entries>0 or #notes>0) then return nil,S.refuse('REQUEST') end
    if type(table_plan)~='table' or not aligned(table_plan.changed_per_entry,#entries) then return nil,S.refuse('REQUEST') end
    if ctx.profile~='l1_only' and (type(log_plan)~='table' or
        (not (log_plan.note_seqs==nil and #notes==0) and not aligned(log_plan.note_seqs,#notes))) then
      return nil,S.refuse('REQUEST')
    end
    local commands={}
    local function append(plan)
      if type(plan)~='table' or type(plan.commands)~='table' then return nil,S.refuse('REQUEST') end
      for k in pairs(plan.commands) do if type(k)~='number' or k<1 or k>#plan.commands or k~=math.floor(k) then return nil,S.refuse('REQUEST') end end
      for i=1,#plan.commands do
        if plan.commands[i]==nil then return nil,S.refuse('REQUEST') end
        commands[#commands+1]=plan.commands[i]
      end
      return true,nil
    end
    local ok,err=append(table_plan);if err then return nil,err end
    ok,err=append(log_plan);if err then return nil,err end
    for _,plan in ipairs(other_plans or {}) do ok,err=append(plan);if err then return nil,err end end
    if not S.uint(log_plan.first_seq) or not S.uint(log_plan.last_seq) then return nil,S.refuse('REQUEST') end
    local fenced=ctx.original_fence
    if fenced and (#commands~=0 or ctx.write_epoch~=ctx.request_epoch or
        table_plan.changed~=0 or table_plan.guarded~=0 or #table_plan.changed_per_entry~=0 or
        log_plan.first_seq~='0' or log_plan.last_seq~='0' or log_plan.line_count~=0 or
        ctx.result~='') then return nil,S.refuse('REQUEST') end
    local reply={status=fenced and 'fenced' or 'ok',epoch_before=ctx.active_epoch,epoch_after=ctx.write_epoch,
      changed=table_plan.changed,guarded=table_plan.guarded,changed_per_entry=table_plan.changed_per_entry,
      first_seq=log_plan.first_seq,last_seq=log_plan.last_seq,lines=log_plan.line_count,
      result=ctx.result,replay=false,counters=ctx.budget}
    if ctx.op then
      local receipt;receipt,err=S.receipt_prepare(ctx,reply);if err then return nil,err end
      if not receipt then return nil,S.refuse('REQUEST') end
      commands[#commands+1]=receipt
    end
    if #commands>S.limits.commands then return nil,limit(ctx,'commands',#commands,S.limits.commands) end
    local total=0
    local projected,heads,checked={},{},{}
    local frozen={}
    for _,d in ipairs(commands) do
      local bytes;bytes,err=validate_write(ctx,d);if err then return nil,err end
      total=total+bytes
      if total>S.limits.argv_bytes then return nil,limit(ctx,'argv_bytes',total,S.limits.argv_bytes) end
      local a=d.argv;local command,key=a[1],a[2];local want=write_kinds[command]
      local actual=projected[key]
      if not actual then actual,err=key_type(ctx,key);if err then return nil,err end end
      if actual~='none' and actual~=want then return nil,S.refuse(successor_key(ctx,key) and 'DRIFT' or 'WRONGTYPE') end
      if not checked[key] then
        ok,err=empty_advance_key(ctx,key,actual);if err then return nil,err end
        checked[key]=true
      end
      if command=='XADD' then
        local head=heads[key]
        if not head then head,err=stream_head(ctx,key,actual);if err then return nil,err end end
        local seq=string.sub(a[3],1,-3)
        if S.next(head)~=seq then return nil,S.refuse('LOGID') end
        heads[key]=seq
      end
      -- Removal can make a key absent, but never changes its permitted type.
      -- Conservatively retaining that type rules out alias tricks across plans.
      projected[key]=want
      ok,err=acl(ctx,a);if err then return nil,err end
      local argv={};for i,v in ipairs(a) do argv[i]=v end
      frozen[#frozen+1]=argv
    end
    ctx.budget.planned_commands=#commands
    ctx.budget.planned_argv_bytes=total
    ctx.budget.store_commands=ctx.budget.store_commands+#commands
    local encoded_ok,encoded=pcall(S.json.encode,reply)
    if not encoded_ok then return nil,S.refuse('REQUEST') end
    if #encoded>8388608 then return nil,limit(ctx,'reply_bytes',#encoded,8388608) end
    return {commands=frozen,reply=encoded},nil
  end
  -- Enclosing callers must take this path immediately after open/replay, before
  -- invoking any preplanner. The prepared plan has only the done receipt write.
  function S.fence_prepare(ctx)
    if callback_depth>0 or ctx.operation~='step' then return nil,S.refuse('CONFIG') end
    if not original_unchanged(ctx) then return nil,S.refuse('REQUEST') end
    if ctx.operation~='step' or not ctx.original_fence or ctx.replay or
        not fence_unchanged(ctx) or ctx.request.space~=ctx.space or
        ctx.request.epoch~=ctx.request_epoch or ctx.request.op~=ctx.op or
        ctx.request.intent~=ctx.intent or (ctx.request.result or '')~=ctx.result or
        ctx.result~='' or type(ctx.request.entries)~='table' or
        #ctx.request.entries~=0 or type(ctx.request.notes)~='table' or
        #ctx.request.notes~=0 then return nil,S.refuse('REQUEST') end
    local table_plan={commands={},changed=0,guarded=0,changed_per_entry=S.array()}
    local log_plan={commands={},first_seq='0',last_seq='0',line_count=0,about_appends=0}
    return S.prepare(ctx,table_plan,log_plan,{})
  end
  function S.commit(plan)
    for i=1,#plan.commands do redis.call(unpack(plan.commands[i])) end
    return plan.reply
  end
  local function step(keys,args)
    if #keys~=0 or #args~=2 then return S.json.encode(S.refuse('ARGS')) end
    local ctx,err=S.open(args[1],args[2]);if err then return S.json.encode(err) end
    if ctx.replay then return S.json.encode(ctx.replay) end
    if ctx.original_fence then
      local commit;commit,err=S.fence_prepare(ctx);if err then return S.json.encode(err) end
      return S.commit(commit)
    end
    local table_plan;table_plan,err=S.plan(ctx);if err then return S.json.encode(err) end
    local log_plan={commands={},first_seq='0',last_seq='0',line_count=0,about_appends=0}
    if ctx.profile~='l1_only' then
      log_plan,err=NS.tlog.plan(ctx,table_plan);if err then return S.json.encode(err) end
    end
    local commit;commit,err=S.prepare(ctx,table_plan,log_plan,{});if err then return S.json.encode(err) end
    return S.commit(commit)
  end
  redis.register_function('ns_tset_step',function(keys,args) return S.run_context(step,keys,args) end)
  local function bad_args() return S.json.encode(S.refuse('ARGS')) end
  redis.register_function({function_name='ns_tset_read',flags={'no-writes'},callback=function(keys,args)
    if #keys~=0 or #args~=2 then return S.run_context(bad_args) end
    return S.read(args[1],args[2],NS.tlog and NS.tlog.read or nil)
  end})
  -- Layer 1's lifecycle (the L1 contract amendment, lifecycle, 2026-09-30):
  -- table_set_lifecycle.lua installs both callbacks, resolved when called.
  redis.register_function('ns_tset_define',function(keys,args) return S.run_context(S.lifecycle_define,keys,args) end)
  redis.register_function('ns_tset_teardown',function(keys,args) return S.run_context(S.lifecycle_teardown,keys,args) end)
end
end
