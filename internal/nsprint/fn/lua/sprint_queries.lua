-- The sprint's composite queries and bounded sprint-key reads, registered with
-- NS.SP.query (upper design version 2.1, 1.0 "Composite queries"; errata 1 E4
-- to E6 and the addendum; item IT30). A skeleton for gate G0: no store loads
-- it before Layer 1's revision 4 is pinned and Layer 2 is accepted again
-- against its hash. Like every tset fragment it runs only when the tset
-- assembler set NS.tset_profile; the legacy prelude never does, so in that
-- library this file defines nothing. Its Go twin is
-- internal/sprint/sprintfn, twin_queries*.go, which holds the same meaning.
--
--   spec.validate(q, index) -> true, nil | nil, refusal   pure; before TIME and any store access
--   spec.read(ctx, q, index) -> answer, nil | nil, refusal   complete or refused, never partial
--
-- A query reads only through Layer 1's checked helpers: S.ensure_read_table (a
-- table the query names, at the requested epoch's definition), S.read_record
-- (a record, charged for every occurrence), S.read_probe and S.read_range_head
-- (the sprint's keys and the tables' cells and rows, typed and counted),
-- S.emit_read_item (each growing output item), and Layer 2's L.read_line_at (a
-- line by seq). There is no raw Redis call here, and no S.readcmd. Layer 1's
-- S and Layer 2's L are resolved when a call runs: table_set*.lua sorts after
-- this file (errata 2, item 8).
--
-- Every query carries an explicit "fields" array (E6). Every query leaves out
-- the ids in {p}quarantine@e (1.3.5), naming them in left_out, except front(s),
-- which still takes a quarantined sentinel as sigma and returns it marked.
-- An id taken from an index or a line must have a record at the epoch
-- (MISSING, MEMBEREPOCH), the lower layers' refusals of a card.
--
-- The wire of each query and of each answer is the Go file
-- twin_queries_wire.go's and twin_queries_answers.go's, key for key.
if NS.tset_profile then
do
  local SP = NS.SP
  local Q = {}

  -- The tables of the sprint, by their logical names, and the parts of them a
  -- query knows (sprint/schema.go).
  Q.WORK, Q.READERS, Q.MERGE, Q.FLEET = 'work', 'readers', 'merge', 'fleet'
  Q.WAITING, Q.READY, Q.REVIEW, Q.LANDED, Q.WITHDRAWN = 'waiting', 'ready', 'review', 'landed', 'withdrawn'
  Q.CTL, Q.STUCK = 'ctl', 'stuck'
  -- The five open cells of a stream: every cell of the work table but landed.
  Q.OPEN_CELLS = {'waiting', 'ready', 'working', 'review', 'merging'}
  -- 1.3.1, sprint.IndexDefs: the indexes a card's place could put it in.
  -- A test holds this list equal to the Go table.
  Q.INDEX_DEFS = {
    {index = 'sent', table = 'work', col = 'waiting'},
    {index = 'elig', table = 'work', col = 'waiting'},
    {index = 'fresh', table = 'work', col = 'ready'},
    {index = 'again', table = 'work', col = 'ready'},
  }
  -- 1.2, sprint.DueKinds: the due entries a card's state could give it.
  Q.DUE_KINDS = {
    {kind = 'untaken', table = 'fleet', col = 'ready', of_row = false},
    {kind = 'unfinished', table = 'fleet', col = 'working', of_row = false},
    {kind = 'unbegun', table = 'readers', col = 'asked', of_row = false},
    {kind = 'unreported', table = 'readers', col = 'reading', of_row = false},
    {kind = 'mergeidle', table = 'merge', col = 'ctl', of_row = true},
  }
  -- The fields a follow derives its ids from, and the fields of a stream's
  -- control card that `streams` tests (2.3 R5). The card a cross stop waits
  -- for is where the writer records it, the control card's `other`; `need_card`
  -- is read when `other` is empty, as IT11's held rule reads the two.
  Q.F_ATTEMPT, Q.F_RCARDS, Q.F_NEEDS, Q.F_MEMBER = 'attempt', 'rcards', 'needs', 'member'
  Q.C_STATE, Q.C_CAUSE, Q.C_OTHER, Q.C_NEED = 'state', 'cause', 'other', 'need_card'
  Q.STOPPED, Q.CROSS = 'stopped', 'cross'

  -- The bounds, each the design's or Layer 1's (L1 1.4, 6, 7; 1.0).
  Q.PROBE_CHUNK = 2000          -- ids in one HMGET or ZMSCORE: argv is at most 2,002 values
  Q.MAX_FIELDS = 128            -- fields one record read names (L1 6)
  -- The fields a projection may name: a read also asks the fields its follows
  -- derive from (attempt, rcards, needs, member), and `streams` the four of
  -- a control card, which together stay within what one record read names.
  Q.MAX_PROJECTION = 124
  Q.MAX_NAME = 256              -- bytes of an id, row, column or field name (L1 6)
  Q.MAX_RECORDS = 10000         -- records a read requests or returns (L1 6)
  Q.MAX_RANGE_IDS = 20000       -- range ids a read returns (L1 6)
  Q.MAX_HEAD = 2000             -- ids one range head returns (L1 7)
  Q.MAX_LINE_IDS = 2000         -- ids in one generated line (1.0)
  Q.MAX_ABOUT = 4000            -- ids a note line's about names (1.0)
  Q.MAX_STREAMS, Q.MAX_MEMBERS, Q.MAX_READERS = 250, 250, 1024   -- 0.1, F1-20
  Q.MAX_NEEDS = 64              -- needs a card names (1.0)
  Q.MAX_RCARDS = 15             -- read cards a primary keeps (1.3.1)
  Q.MAX_SEQ = 9007199254740991  -- the live sequence ceiling (L2 2: 2^53 - 1)
  Q.MAX_COLUMNS = 32            -- columns of a table (L1 1.2)
  Q.MAX_PROBES = 20000          -- cell and key probes of one read (L1 6)
  -- The bytes one HMGET of the quarantine may fetch for an id: a mark's code,
  -- rule, stream, cells and note (1.3.1). A longer mark is DRIFT.
  Q.QUARANTINE_ENTRY_BYTES = 1024
  Q.FIELD_BYTES = 1024          -- the most a sprint hash field is read for (HMGET reserve)
  Q.HEARTBEAT_FIELD_BYTES = 8192 -- the heartbeat's rules field holds a count for every rule
  Q.SCORE_BYTES = 40            -- a score's reply: at most 24 digits and a margin
  Q.NAME_RE = '^[A-Za-z0-9_][A-Za-z0-9_.~-]*$'
  Q.FOLLOWS = {work = 1, withdrawn = 1, rcards = 15, merge = 1, control = 1, needs = 64, member = 1, jopen = 1, due = 1, index = 1}
  Q.HEAD_INDEXES = {elig = true, ['fresh-below'] = true, ['fresh-above'] = true, again = true}
  Q.INDEX_PREFIXES = {sent = true, elig = true, fresh = true, again = true, wait = true}
  -- The sprint keys a composite query may also read (IT08's `keys`), and the
  -- kinds whose answer reaches streams, which alone may name them. The jopen
  -- keys give the types of the judgments open on a subject, which only an
  -- enumeration of its jopen hash finds, and S.read_probe admits none (no HKEYS
  -- or HGETALL): a query naming one is REQUEST (sprintfn.queryKeys).
  Q.QUERY_KEYS = {dropping = true, ['next.streams'] = true}
  Q.KEYS_KINDS = {front = true, waiters = true, streams = true}

  -- The sprint-key kinds (the addendum): the names are this item's choice.
  Q.CLOCK_FIELDS = {'stopped_ms', 'stopped_since_ms', 'stophold_ms', 'due_since_ms', 'stopraised_ms'}
  Q.LEASE_FIELDS = {'owner', 'name', 'until_ms', 'gen'}
  Q.TICK_FIELDS = {'cur', 'behind_n'}
  Q.HEARTBEAT_FIELDS = {'tick_at', 'ticks', 'error', 'failures', 'backlog', 'agenda', 'heldq', 'due_now',
    'owner', 'gen', 'swept', 'looked_at', 'rules', 'idle_loop', 'idle_at'}

  function Q.S() return NS.tset end
  local function layers() return Q.S(), NS.tlog end

  ---------------------------------------------------------------- shapes (pure)

  function Q.is_array(v) return Q.S().is_array(v) end
  function Q.is_object(v) return type(v) == 'table' and not Q.S().is_array(v) end
  function Q.is_int(v, lo, hi)
    return type(v) == 'number' and v == v and v == math.floor(v) and v >= lo and v <= hi
  end
  function Q.valid_name(s)
    return type(s) == 'string' and #s <= Q.MAX_NAME and s:match(Q.NAME_RE) ~= nil
  end
  function Q.valid_field(s)
    return Q.valid_name(s) and s ~= 'epoch' and s ~= 'revision' and s:sub(1, 6) ~= 'place:'
  end
  function Q.valid_text(s)
    return type(s) == 'string' and #s >= 1 and #s <= Q.MAX_NAME and not s:find('[%z\r\n]')
  end
  -- A field of {p}next@e (1.3.1; sprintfn.NextField): score, streams, id:<s>
  -- or gate:<s>, s a stream's name as sprint.ValidID has it.
  function Q.next_field(f)
    if f == 'score' or f == 'streams' then return true end
    if type(f) ~= 'string' then return false end
    local s = f:match('^id:(.*)$') or f:match('^gate:(.*)$')
    return s ~= nil and #s <= 128 and s:match('^[%w_][%w_%-]*$') ~= nil
  end
  -- only: the object has no key outside the set given.
  function Q.only(o, allowed)
    for k in pairs(o) do if not allowed[k] then return false end end
    return true
  end
  -- A list of distinct values, each passing valid; an array and nothing else.
  function Q.distinct(list, valid, most)
    if not Q.is_array(list) then return false end
    local n = #list
    if most and n > most then return false end
    for k in pairs(list) do
      if type(k) ~= 'number' or k < 1 or k > n or k ~= math.floor(k) then return false end
    end
    local seen = {}
    for i = 1, n do
      local v = list[i]
      if not valid(v) or seen[v] then return false end
      seen[v] = true
    end
    return true
  end
  function Q.canonical_uint(s, most)
    if type(s) ~= 'string' or #s > 16 then return nil end
    if s ~= '0' and not s:match('^[1-9][0-9]*$') then return nil end
    local n = tonumber(s)
    if n == nil or n > most then return nil end
    return n
  end
  -- An index of 1.3.1 and its argument, or nil when the key is not one.
  function Q.head_kind(key)
    if key == 'askwait' or key == 'missing' or key == 'jnotes' then return key, '' end
    local i = key:find(':', 1, true)
    if i and i > 1 and Q.INDEX_PREFIXES[key:sub(1, i - 1)] and Q.valid_name(key:sub(i + 1)) then
      return key:sub(1, i - 1), key:sub(i + 1)
    end
    return nil, nil
  end
  function Q.cell_of(cell)
    if type(cell) ~= 'string' then return nil end
    local i = cell:find(':', 1, true)
    if not i or i == 1 then return nil end
    local row, col = cell:sub(1, i - 1), cell:sub(i + 1)
    if Q.valid_name(row) and Q.valid_name(col) then return row, col end
    return nil
  end
  function Q.valid_head_key(key)
    if type(key) ~= 'string' then return false end
    if Q.head_kind(key) then return true end
    return Q.cell_of(key) ~= nil
  end
  function Q.valid_follow(follow)
    if not Q.is_array(follow) then return false end
    local n, seen = #follow, {}
    for i = 1, n do
      local f = follow[i]
      if type(f) ~= 'string' or not Q.FOLLOWS[f] or seen[f] then return false end
      seen[f] = true
    end
    return true
  end
  -- The most a line's window holds: from offset, at most limit ids.
  function Q.line_window(src)
    local most = src.about and Q.MAX_ABOUT or Q.MAX_LINE_IDS
    local offset = math.max(src.offset or 0, 0)
    local n = math.max(most - offset, 0)
    if src.limit and src.limit > 0 and src.limit < n then n = src.limit end
    return offset, n
  end
  -- valid_source: the shape of an id source is right (true) or it is not
  -- (false, REQUEST). kinds is the set of source kinds the query takes. How many
  -- ids a list names is a size and not a shape: Q.check refuses it (LIMIT) after
  -- every shape, in the order the Go's ValidateSprintQ does.
  function Q.valid_source(src, kinds)
    if not Q.is_object(src) or type(src.kind) ~= 'string' or not kinds[src.kind] then return false end
    if src.kind == 'ids' then
      if not Q.only(src, {kind = true, ids = true}) then return false end
      if not Q.distinct(src.ids, Q.valid_name) then return false end
    elseif src.kind == 'head' then
      if not Q.only(src, {kind = true, key = true, limit = true}) or not Q.valid_head_key(src.key) or
          not Q.is_int(src.limit, 1, Q.MAX_HEAD) then return false end
    elseif src.kind == 'line' then
      if not Q.only(src, {kind = true, seq = true, about = true, offset = true, limit = true}) then return false end
      local seq = Q.canonical_uint(src.seq, Q.MAX_SEQ)
      local most = src.about == true and Q.MAX_ABOUT or Q.MAX_LINE_IDS
      if not seq or seq < 1 or (src.about ~= nil and type(src.about) ~= 'boolean') or
          not Q.is_int(src.offset or 0, 0, most - 1) or not Q.is_int(src.limit or 0, 0, math.huge) then return false end
    else
      return false
    end
    return true
  end
  -- How many ids a source names at most, and the range ids that find them.
  function Q.source_size(src)
    if src.kind == 'ids' then return #src.ids, 0 end
    if src.kind == 'head' then return src.limit, src.limit end
    local _, n = Q.line_window(src)
    return n, 0
  end
  function Q.follow_cost(follow)
    local n = 0
    for i = 1, #follow do n = n + Q.FOLLOWS[follow[i]] end
    return n
  end
  -- sprint.QueryCost's records and range ids, from the query's arguments alone.
  function Q.declared(q)
    local kind = q.kind
    if kind == 'related' then
      local n, ranged = Q.source_size(q.src)
      return n * (1 + Q.follow_cost(q.follow)), ranged
    elseif kind == 'front' then
      local records, ranged = 1, 1
      for i = 1, #q.heads do
        local h = q.heads[i]
        records = records + h.limit * (1 + Q.follow_cost(h.follow))
        ranged = ranged + h.limit
      end
      return records, ranged
    elseif kind == 'waiters' then
      local n, ranged = Q.source_size(q.src)
      return n * (1 + q.limit), ranged + n * q.limit
    elseif kind == 'streams' then
      local units = q.units or Q.MAX_STREAMS
      if units == 0 then units = Q.MAX_STREAMS end
      return 2 * units, units * q.limit
    elseif kind == 'fleet' or kind == 'readers' then
      local units = q.units or 0
      if units == 0 then units = kind == 'fleet' and Q.MAX_MEMBERS or Q.MAX_READERS end
      return units, 0
    elseif kind == 'needchain' then
      local _, ranged = Q.source_size(q.src)
      return q.limit, ranged
    elseif kind == 'jnote' then
      local n, ranged = Q.source_size(q.src)
      local subjects = q.subjects or 0
      if subjects == 0 then subjects = Q.MAX_ABOUT end
      return n * (1 + subjects), ranged
    end
    return Q.MAX_RECORDS, Q.MAX_RANGE_IDS
  end
  -- Layer 1's checked probe charges a cell for every name an HMGET or a ZMSCORE
  -- asks for and one for any other probe, so the probes a query declares are
  -- names and commands, not commands: an id left out or looked up is a probe.
  -- The probes that find a source's ids: a list costs none, an index head one,
  -- a cell head two (its row, then its range) and a line none (it is a line).
  function Q.source_probes(src)
    if src.kind == 'head' then
      if Q.head_kind(src.key) then return 1 end
      return 2
    end
    return 0
  end
  -- The probes the follows make for one record (per), and the most records
  -- they name besides it, whose quarantine is probed a name each.
  function Q.follow_probes(follow)
    local per, targets = 0, 0
    for i = 1, #follow do
      local f = follow[i]
      if f == 'work' or f == 'withdrawn' or f == 'merge' or f == 'control' or f == 'member' then
        targets = targets + 1
      elseif f == 'rcards' then
        targets = targets + Q.MAX_RCARDS
      elseif f == 'needs' then
        targets = targets + Q.MAX_NEEDS
        per = per + Q.MAX_NEEDS
      elseif f == 'jopen' or f == 'due' then
        per = per + 1
      elseif f == 'index' then
        per = per + 2
      end
    end
    return per, targets
  end
  -- sprintfn.QueryProbes: the most cell and key probes a composite query may
  -- make, from its arguments alone.
  function Q.declared_probes(q)
    return Q.query_probes(q) + Q.extension_probes(q)
  end
  -- sprintfn.extensionProbes: a name for each stream the dropping marks are read
  -- of (the most a query reaches), one for next.streams, and a ZCARD for each
  -- cell a streams query counts.
  function Q.extension_probes(q)
    local total = 0
    local units = q.units or 0
    if units == 0 then units = Q.MAX_STREAMS end
    for _, k in ipairs(q.keys or {}) do
      if k == 'dropping' then
        if q.kind == 'front' then
          total = total + 1
        elseif q.kind == 'waiters' then
          total = total + Q.source_size(q.src) * q.limit
        elseif q.kind == 'streams' then
          total = total + units
        end
      elseif k == 'next.streams' then
        total = total + 1
      end
    end
    if q.kind == 'streams' then total = total + units * #(q.counts or {}) end
    return total
  end
  function Q.query_probes(q)
    local kind = q.kind
    if kind == 'front' then
      local total = 3 + #Q.OPEN_CELLS
      for i = 1, #q.heads do
        local h = q.heads[i]
        local per, targets = Q.follow_probes(h.follow)
        total = total + 1 + h.limit + h.limit * targets + h.limit * per
      end
      return total
    elseif kind == 'streams' then
      -- the rows' head; the quarantine of the control cards and of the need
      -- cards; a stuck cell's row and range and the quarantine of its ids, for
      -- each stream
      local units = q.units or 0
      if units == 0 then units = Q.MAX_STREAMS end
      return 1 + 2 * units + units * (2 + q.limit)
    elseif kind == 'fleet' or kind == 'readers' then
      -- the rows' head, the quarantine of the control cards, and a cell's count
      -- for each column of each row
      local units = q.units or 0
      if units == 0 then units = kind == 'fleet' and Q.MAX_MEMBERS or Q.MAX_READERS end
      return 1 + units + units * Q.MAX_COLUMNS
    end
    local n = Q.source_size(q.src)
    local found = Q.source_probes(q.src)
    if kind == 'related' then
      local per, targets = Q.follow_probes(q.follow)
      return found + n + n * targets + n * per
    elseif kind == 'waiters' then
      return found + 2 * n + n * (1 + q.limit)
    elseif kind == 'needchain' then
      return found + n + q.limit * Q.MAX_NEEDS
    elseif kind == 'jnote' then
      local subjects = q.subjects or 0
      if subjects == 0 then subjects = Q.MAX_ABOUT end
      return found + n * 3 * subjects
    end
    return Q.MAX_PROBES
  end
  -- The probes a sprint-key read makes at most (sprintfn.KeyProbes): an HMGET
  -- of a hash's fixed fields is a probe for each field.
  function Q.declared_key_probes(q)
    local kind = q.kind
    if kind == 'clock' then return #Q.CLOCK_FIELDS end
    if kind == 'lease' then return #Q.LEASE_FIELDS end
    if kind == 'tick' then return #Q.TICK_FIELDS end
    if kind == 'heartbeat' then return #Q.HEARTBEAT_FIELDS end
    if kind == 'duecount' then return #Q.CLOCK_FIELDS + 1 end
    if kind == 'dropping' then return 1 + #q.streams end
    if kind == 'parked' then return 1 + #q.keys end
    if kind == 'missing' then return #q.ids end
    if kind == 'jopen' then return #q.subjects * (1 + #q.names) end
    if kind == 'next' then return #q.names end
    return Q.MAX_PROBES
  end
  -- The cost a query declares, from its arguments alone: the records and range
  -- ids of sprint.QueryCost and the probes the design's table states beside
  -- them (errata 1, the addendum). Dispatch sizes a read from it.
  function Q.cost(q)
    local records, ranged = Q.declared(q)
    return {records = records, range_ids = ranged, probes = Q.declared_probes(q)}
  end
  function Q.key_cost(q)
    return {records = 0, range_ids = 0, probes = Q.declared_key_probes(q)}
  end

  -- note_seq: the seq of a note's id n<seq> and its epoch (~<epoch> after it).
  function Q.note_seq(id)
    if type(id) ~= 'string' then return nil end
    local body, epoch = id:match('^n([0-9]+)~([0-9]+)$')
    if not body then
      body = id:match('^n([0-9]+)$')
      epoch = '0'
    end
    if not body or body:sub(1, 1) == '0' or #body > 16 then return nil end
    if epoch ~= '0' and not epoch:match('^[1-9][0-9]*$') then return nil end
    local n = tonumber(body)
    if n > Q.MAX_SEQ then return nil end
    return n, epoch
  end

  -- The code that refuses a query, or nil when it is well formed and fits: the
  -- shape is checked whole first, REQUEST for a malformed query, and only then
  -- the sizes, LIMIT for one whose declared cost cannot fit a read (1.4.2: sized
  -- before it is sent, never refused BUDGET). A query that is malformed and too
  -- large is REQUEST, as the Go's ValidateSprintQ has it.
  function Q.check(q)
    local S = Q.S()
    if not Q.is_object(q) or type(q.kind) ~= 'string' then return 'REQUEST' end
    local kind = q.kind
    local shapes = {
      related = {kind = true, t = true, src = true, follow = true, fields = true},
      front = {kind = true, stream = true, heads = true, fields = true, keys = true},
      waiters = {kind = true, src = true, limit = true, fields = true, after = true, missing = true, keys = true},
      streams = {kind = true, limit = true, units = true, fields = true, counts = true, keys = true},
      fleet = {kind = true, units = true, fields = true},
      readers = {kind = true, units = true, fields = true},
      needchain = {kind = true, src = true, limit = true, fields = true},
      jnote = {kind = true, src = true, subjects = true, fields = true},
    }
    local shape = shapes[kind]
    if not shape or not Q.only(q, shape) then return 'REQUEST' end
    if not Q.distinct(q.fields, Q.valid_field, Q.MAX_PROJECTION) then return 'REQUEST' end
    local any = {ids = true, head = true, line = true}
    if kind == 'related' then
      if not Q.valid_name(q.t) or not Q.valid_follow(q.follow) or not Q.valid_source(q.src, any) then return 'REQUEST' end
    elseif kind == 'front' then
      if not Q.valid_name(q.stream) or not Q.is_array(q.heads) or #q.heads > 4 then return 'REQUEST' end
      local seen = {}
      for i = 1, #q.heads do
        local h = q.heads[i]
        if not Q.is_object(h) or not Q.only(h, {index = true, limit = true, follow = true}) or
            type(h.index) ~= 'string' or not Q.HEAD_INDEXES[h.index] or seen[h.index] or
            not Q.is_int(h.limit, 1, Q.MAX_HEAD) or not Q.valid_follow(h.follow) then return 'REQUEST' end
        seen[h.index] = true
      end
    elseif kind == 'waiters' then
      if not Q.is_int(q.limit, 1, Q.MAX_HEAD) or not Q.valid_source(q.src, any) then return 'REQUEST' end
    elseif kind == 'streams' then
      if not Q.is_int(q.limit, 0, Q.MAX_HEAD) or not Q.is_int(q.units or 0, 0, Q.MAX_STREAMS) then return 'REQUEST' end
    elseif kind == 'fleet' then
      if not Q.is_int(q.units or 0, 0, Q.MAX_MEMBERS) then return 'REQUEST' end
    elseif kind == 'readers' then
      if not Q.is_int(q.units or 0, 0, Q.MAX_READERS) then return 'REQUEST' end
    elseif kind == 'needchain' then
      if not Q.is_int(q.limit, 1, Q.MAX_RECORDS) or not Q.valid_source(q.src, any) then return 'REQUEST' end
    elseif kind == 'jnote' then
      if not Q.is_int(q.subjects or 0, 0, Q.MAX_ABOUT) or not Q.valid_source(q.src, {ids = true, head = true}) then
        return 'REQUEST'
      end
      if q.src.kind == 'head' and q.src.key ~= 'jnotes' then return 'REQUEST' end
      if q.src.kind == 'ids' then
        for i = 1, #q.src.ids do
          if not Q.note_seq(q.src.ids[i]) then return 'REQUEST' end
        end
      end
    end
    if not Q.valid_extensions(q) then return 'REQUEST' end
    -- The sizes, after every shape. A list longer than a read may return is past
    -- a bound, as Layer 1's own ids query refuses one (LIMIT), and not a
    -- malformed source.
    if q.src and q.src.kind == 'ids' and #q.src.ids > Q.MAX_RECORDS then return 'LIMIT', 'record' end
    local records, ranged = Q.declared(q)
    if records > Q.MAX_RECORDS then return 'LIMIT', 'record' end
    if ranged > Q.MAX_RANGE_IDS then return 'LIMIT', 'range_id' end
    return nil
  end

  -- IT08's extensions of a query (sprintfn.validExtensions): keys, each a key
  -- of Q.QUERY_KEYS named once; counts, distinct column names, at most a
  -- table's columns; missing a boolean; after a string, and when not empty the
  -- cursor of a list of one id, itself an id. Which kinds may carry them is the
  -- shape's (Q.check).
  function Q.valid_extensions(q)
    if q.keys ~= nil and not Q.distinct(q.keys, function(k) return Q.QUERY_KEYS[k] == true end) then return false end
    if q.counts ~= nil and not Q.distinct(q.counts, Q.valid_name, Q.MAX_COLUMNS) then return false end
    if q.missing ~= nil and type(q.missing) ~= 'boolean' then return false end
    if q.after ~= nil then
      if type(q.after) ~= 'string' then return false end
      if q.after ~= '' and (q.src.kind ~= 'ids' or #q.src.ids ~= 1 or not Q.valid_name(q.after)) then return false end
    end
    return true
  end

  -- The sprint-key kinds' shapes: a fields that is the empty array, and only
  -- the names the kind takes.
  function Q.check_key(q)
    if not Q.is_object(q) or type(q.kind) ~= 'string' then return 'REQUEST' end
    local allowed = {kind = true, fields = true}
    local extra = {dropping = {'streams'}, parked = {'keys'}, missing = {'ids'}, jopen = {'subjects', 'names'}, next = {'names'}}
    for _, name in ipairs(extra[q.kind] or {}) do allowed[name] = true end
    if not Q.only(q, allowed) then return 'REQUEST' end
    if not Q.is_array(q.fields) or #q.fields ~= 0 then return 'REQUEST' end
    local most = Q.PROBE_CHUNK
    if q.kind == 'dropping' then
      if not Q.distinct(q.streams, Q.valid_name, most) then return 'REQUEST' end
    elseif q.kind == 'parked' then
      if not Q.distinct(q.keys, Q.valid_text, most) then return 'REQUEST' end
    elseif q.kind == 'missing' then
      if not Q.distinct(q.ids, Q.valid_name, most) then return 'REQUEST' end
    elseif q.kind == 'jopen' then
      if not Q.distinct(q.subjects, Q.valid_name, most) or not Q.distinct(q.names, Q.valid_text, most) then return 'REQUEST' end
    elseif q.kind == 'next' then
      if not Q.distinct(q.names, Q.next_field, most) then return 'REQUEST' end
    end
    return nil
  end

  function Q.refuse_shape(code, index, budget)
    local S = Q.S()
    local detail = {query_index = index}
    if code == 'LIMIT' then detail.budget = budget or 'record' end
    return nil, S.refuse(code, detail)
  end
  function Q.validator(check)
    return function(q, index)
      local code, budget = check(q)
      if code then return Q.refuse_shape(code, index, budget) end
      return true, nil
    end
  end

  ---------------------------------------------------------------- keys and reads

  -- The sprint's keys: {p}<name>@e per epoch, {p}<name> with none.
  function Q.key(ctx, name) return ctx.space .. 'sprint:' .. name .. '@' .. ctx.request_epoch end
  function Q.bare(ctx, name) return ctx.space .. 'sprint:' .. name end

  function Q.array(list)
    local a = Q.S().array()
    for i = 1, #list do a[i] = list[i] end
    return a
  end
  function Q.fail(ctx, code, index, detail)
    detail = detail or {}
    detail.query_index = index
    return Q.S().refuse(code, detail)
  end
  -- A read's first refusal carries the query's index; a refusal that has none
  -- gets it.
  function Q.indexed(err, index)
    if type(err) == 'table' then
      err.detail = err.detail or {}
      if err.detail.query_index == nil then err.detail.query_index = index end
    end
    return err
  end

  -- One HMGET probe: each field's value, false (Redis's nil) for one the hash lacks.
  function Q.hmget(ctx, key, fields, index, per)
    local S = Q.S()
    local argv = {'HMGET', key}
    for i = 1, #fields do argv[#argv + 1] = fields[i] end
    local vals, err = S.read_probe(ctx, argv, key, 'hash', #fields * (per or Q.FIELD_BYTES) + 64)
    if err then return nil, err end
    return vals, nil
  end
  function Q.hlen(ctx, key, index)
    local n, err = Q.S().read_probe(ctx, {'HLEN', key}, key, 'hash', 32)
    if err then return nil, err end
    return n, nil
  end
  function Q.zmscore(ctx, key, members, index)
    local argv = {'ZMSCORE', key}
    for i = 1, #members do argv[#argv + 1] = members[i] end
    local vals, err = Q.S().read_probe(ctx, argv, key, 'zset', #members * Q.SCORE_BYTES + 64)
    if err then return nil, err end
    return vals, nil
  end
  function Q.zscore(ctx, key, member, index)
    local v, err = Q.S().read_probe(ctx, {'ZSCORE', key, member}, key, 'zset', Q.SCORE_BYTES + 64)
    if err then return nil, err end
    return v, nil
  end
  function Q.zcount(ctx, key, min, max, index)
    local n, err = Q.S().read_probe(ctx, {'ZCOUNT', key, min, max}, key, 'zset', 32)
    if err then return nil, err end
    return n, nil
  end
  function Q.head_of(ctx, key, min, max, limit, index)
    local head, err = Q.S().read_range_head(ctx, key, {min = min, max = max}, limit)
    if err then return nil, err end
    return head, nil
  end

  -- The quarantined among ids: one HMGET for each 2,000.
  function Q.quarantined(ctx, ids, index)
    local bad = {}
    local n = #ids
    local key = Q.key(ctx, 'quarantine')
    local i = 1
    while i <= n do
      local j = math.min(i + Q.PROBE_CHUNK - 1, n)
      local argv = {'HMGET', key}
      for k = i, j do argv[#argv + 1] = ids[k] end
      local vals, err = Q.S().read_probe(ctx, argv, key, 'hash', (j - i + 1) * Q.QUARANTINE_ENTRY_BYTES + 64)
      if err then return nil, err end
      for k = i, j do
        if vals[k - i + 1] then bad[ids[k]] = true end
      end
      i = j + 1
    end
    return bad, nil
  end
  -- The ids without the quarantined ones, in order; those are appended to left once each.
  function Q.leave(ctx, ids, left, index)
    if #ids == 0 then return ids, nil end
    local bad, err = Q.quarantined(ctx, ids, index)
    if err then return nil, err end
    local kept = {}
    for i = 1, #ids do
      local id = ids[i]
      if not bad[id] then
        kept[#kept + 1] = id
      elseif not left.seen[id] then
        left.seen[id] = true
        left.list[#left.list + 1] = id
      end
    end
    return kept, nil
  end
  function Q.new_left() return {list = {}, seen = {}} end

  -- Records of ids of a table with a projection, one read_record for each
  -- occurrence, in order.
  function Q.records(ctx, t, ids, fields, index)
    local out = {}
    for i = 1, #ids do
      local rec, err = Q.S().read_record(ctx, t, ids[i], fields, index)
      if err then return nil, err end
      out[i] = rec
    end
    return out, nil
  end

  -- A record's field value, '' when absent.
  function Q.field(rec, name)
    local f = rec.fields and rec.fields[name]
    if f and f.present then return f.value end
    return ''
  end
  -- A comma list without empty items (sprint.Split).
  function Q.split(s)
    local out = {}
    for item in string.gmatch(s, '[^,]+') do
      item = item:match('^%s*(.-)%s*$')
      if item ~= '' then out[#out + 1] = item end
    end
    return out
  end

  -- The fields a read asks of a record: the projection and the fields the
  -- follows derive from, sorted (and the extra names).
  function Q.union(fields, follow, extra)
    local seen, out = {}, {}
    local function add(f)
      if not seen[f] then seen[f] = true; out[#out + 1] = f end
    end
    for i = 1, #fields do add(fields[i]) end
    for _, f in ipairs(extra or {}) do add(f) end
    for i = 1, #(follow or {}) do
      local f = follow[i]
      if f == 'work' or f == 'withdrawn' then add(Q.F_ATTEMPT)
      elseif f == 'rcards' then add(Q.F_RCARDS)
      elseif f == 'needs' then add(Q.F_NEEDS)
      elseif f == 'member' then add(Q.F_MEMBER) end
    end
    table.sort(out)
    return out
  end
  -- A record with only the fields of the projection.
  function Q.project(rec, fields)
    local out = {}
    for k, v in pairs(rec) do out[k] = v end
    local f = {}
    for i = 1, #fields do
      local v = rec.fields and rec.fields[fields[i]]
      if v ~= nil then f[fields[i]] = v end
    end
    out.fields = f
    return out
  end

  -- An id taken from an index or a line has a record at the epoch.
  function Q.present(ctx, t, recs, index)
    for i = 1, #recs do
      local r = recs[i]
      if not r.exists then return Q.fail(ctx, 'MISSING', index, {table = t, ids = {r.id}}) end
      if r.epoch ~= ctx.request_epoch then return Q.fail(ctx, 'MEMBEREPOCH', index, {table = t, ids = {r.id}}) end
    end
    return nil
  end

  -- The table's definition at the epoch (S.ensure_read_table).
  function Q.table(ctx, t, index)
    local def, err = Q.S().ensure_read_table(ctx, t, index)
    if err then return nil, err end
    return def, nil
  end

  -- A line by seq (L.read_line_at): its kind, ids, about and meta.
  function Q.line(ctx, seq, index)
    local _, L = layers()
    if not L or not L.read_line_at then return nil, Q.fail(ctx, 'CONFIG', index) end
    local line, err = L.read_line_at(ctx, seq, index)
    if err then return nil, Q.indexed(err, index) end
    if type(line) ~= 'table' then return nil, Q.fail(ctx, 'DRIFT', index) end
    local meta = line.meta
    if type(meta) == 'string' then
      local ok, decoded = pcall(Q.S().json.decode, meta)
      meta = ok and decoded or nil
    end
    return {kind = line.kind, ids = line.ids or {}, about = line.about or {}, meta = meta}, nil
  end

  -- The ids a source names, and whether they were taken from an index or a line.
  function Q.source_ids(ctx, src, t, index)
    if src.kind == 'ids' then
      local out = {}
      for i = 1, #src.ids do out[i] = src.ids[i] end
      return out, false, nil
    elseif src.kind == 'head' then
      local name, arg = Q.head_kind(src.key)
      if name then
        local full = arg ~= '' and (name .. ':' .. arg) or name
        local head, err = Q.head_of(ctx, Q.key(ctx, full), '-inf', '+inf', src.limit, index)
        if err then return nil, nil, err end
        local out = {}
        for i = 1, #head.ids do out[i] = head.ids[i] end
        return out, name ~= 'missing', nil
      end
      local row, col = Q.cell_of(src.key)
      local def, err = Q.table(ctx, t, index)
      if err then return nil, nil, err end
      if not def.column_set[col] then return nil, nil, Q.fail(ctx, 'NOCOL', index, {table = t, cells = {src.key}}) end
      local rk = ctx.rows_key(t, ctx.request_epoch)
      local rank
      rank, err = Q.S().read_probe(ctx, {'ZSCORE', rk, row}, rk, 'zset', 64)
      if err then return nil, nil, err end
      if not rank then return nil, nil, Q.fail(ctx, 'NOROW', index, {table = t, cells = {src.key}}) end
      local head
      head, err = Q.head_of(ctx, ctx.cell_key(t, ctx.request_epoch, row, col), '-inf', '+inf', src.limit, index)
      if err then return nil, nil, err end
      local out = {}
      for i = 1, #head.ids do out[i] = head.ids[i] end
      return out, true, nil
    end
    local line, err = Q.line(ctx, tonumber(src.seq), index)
    if err then return nil, nil, err end
    local list = src.about and line.about or line.ids
    local offset, n = Q.line_window(src)
    if offset > #list then offset = #list end
    local out = {}
    local last = math.min(#list, offset + n)
    for i = offset + 1, last do out[#out + 1] = list[i] end
    -- the fourth value says a line has ids beyond the window read (more_ids)
    return out, true, nil, last < #list
  end

  ---------------------------------------------------------------- the follows

  -- The records a follow reads from a record, or DRIFT when what it names is
  -- past a bound.
  function Q.follow_targets(ctx, follow, t, rec, index)
    local function drift() return nil, Q.fail(ctx, 'DRIFT', index, {table = t, ids = {rec.id}}) end
    if follow == 'work' or follow == 'withdrawn' then
      local v = Q.field(rec, Q.F_ATTEMPT)
      if v == '' then return {}, nil end
      if not v:match('^[0-9]+$') or #v > 9 then return drift() end
      local n = tonumber(v)
      if n == 0 then return {}, nil end
      return {{Q.FLEET, rec.id .. '.w' .. string.format('%d', n)}}, nil
    elseif follow == 'rcards' then
      local ids = Q.split(Q.field(rec, Q.F_RCARDS))
      if #ids > Q.MAX_RCARDS then return drift() end
      local out = {}
      for i = 1, #ids do out[i] = {Q.READERS, ids[i]} end
      return out, nil
    elseif follow == 'merge' then
      return {{Q.MERGE, rec.id}}, nil
    elseif follow == 'control' then
      if not Q.placed(rec) then return {}, nil end
      return {{Q.MERGE, 'ctl-' .. rec.place.row}}, nil
    elseif follow == 'needs' then
      local ids = Q.split(Q.field(rec, Q.F_NEEDS))
      if #ids > Q.MAX_NEEDS then return drift() end
      local out = {}
      for i = 1, #ids do out[i] = {Q.WORK, ids[i]} end
      return out, nil
    elseif follow == 'member' then
      local m = Q.field(rec, Q.F_MEMBER)
      if m ~= '' then return {{Q.FLEET, 'ctl-' .. m}}, nil end
    end
    return {}, nil
  end

  function Q.placed(rec)
    local p = rec.place
    return type(p) == 'table' and p.row ~= nil
  end

  -- A card's due entries: the kinds of 1.2 its state could give it, by one ZMSCORE.
  function Q.due_of(ctx, t, rec, index)
    if not Q.placed(rec) then return nil, nil end
    local members = {}
    for _, k in ipairs(Q.DUE_KINDS) do
      if k.table == t and k.col == rec.place.col then
        members[#members + 1] = k.kind .. ':' .. (k.of_row and rec.place.row or rec.id)
      end
    end
    if #members == 0 then return nil, nil end
    local scores, err = Q.zmscore(ctx, Q.key(ctx, 'due'), members, index)
    if err then return nil, err end
    local out = {}
    for i = 1, #members do
      if scores[i] then out[#out + 1] = {key = members[i], score = scores[i]} end
    end
    return out, nil
  end

  -- A work card's indexes: the ones its place could put it in, and askwait for
  -- a card in review.
  function Q.index_of(ctx, t, rec, index)
    if t ~= Q.WORK or not Q.placed(rec) then return nil, nil end
    local out = {}
    for _, d in ipairs(Q.INDEX_DEFS) do
      if d.table == t and d.col == rec.place.col then
        local name = d.index .. ':' .. rec.place.row
        local s, err = Q.zscore(ctx, Q.key(ctx, name), rec.id, index)
        if err then return nil, err end
        if s then out[#out + 1] = {key = name, score = s} end
      end
    end
    if rec.place.col == Q.REVIEW then
      local s, err = Q.zscore(ctx, Q.key(ctx, 'askwait'), rec.id, index)
      if err then return nil, err end
      if s then out[#out + 1] = {key = 'askwait', score = s} end
    end
    return out, nil
  end

  function Q.project_list(recs, fields)
    local out = {}
    for i = 1, #recs do out[i] = Q.project(recs[i], fields) end
    return out
  end

  -- What the follows of a list of records (of one table, all existing) reach,
  -- aligned with them: the targets of every record are found first, the
  -- quarantined left out together, and a table's read in one go.
  function Q.follows(ctx, t, recs, follow, fields, left, index)
    local out = {}
    for i = 1, #recs do out[i] = {} end
    if #follow == 0 then return out, nil end
    local slots, all = {}, {}
    for i = 1, #recs do
      for f = 1, #follow do
        local targets, err = Q.follow_targets(ctx, follow[f], t, recs[i], index)
        if err then return nil, err end
        for _, target in ipairs(targets) do
          slots[#slots + 1] = {rec = i, follow = follow[f], table = target[1], id = target[2]}
          all[#all + 1] = target[2]
        end
      end
    end
    local kept, err = Q.leave(ctx, all, left, index)
    if err then return nil, err end
    local keep = {}
    for i = 1, #kept do keep[kept[i]] = true end
    local order, read = {}, {}
    for i, s in ipairs(slots) do
      if keep[s.id] then
        if not read[s.table] then read[s.table] = {}; order[#order + 1] = s.table end
        read[s.table][#read[s.table] + 1] = i
      end
    end
    local got = {}
    for _, tbl in ipairs(order) do
      local idx = read[tbl]
      local ids = {}
      for j = 1, #idx do ids[j] = slots[idx[j]].id end
      local rs
      rs, err = Q.records(ctx, tbl, ids, fields, index)
      if err then return nil, err end
      for j = 1, #idx do got[idx[j]] = rs[j] end
    end
    for i, s in ipairs(slots) do
      if keep[s.id] then
        local f, r = out[s.rec], got[i]
        if s.follow == 'work' then
          if r.exists and Q.placed(r) and r.place.col ~= Q.WITHDRAWN then
            f.work = f.work or {}; f.work[#f.work + 1] = r
          end
        elseif s.follow == 'withdrawn' then
          if r.exists and Q.placed(r) and r.place.col == Q.WITHDRAWN then
            f.withdrawn = f.withdrawn or {}; f.withdrawn[#f.withdrawn + 1] = r
          end
        elseif s.follow == 'rcards' then
          f.rcards = f.rcards or {}; f.rcards[#f.rcards + 1] = r
        elseif s.follow == 'merge' then
          if r.exists then f.merge = f.merge or {}; f.merge[#f.merge + 1] = r end
        elseif s.follow == 'control' then
          if r.exists then f.control = f.control or {}; f.control[#f.control + 1] = r end
        elseif s.follow == 'needs' then
          local s2, err2 = Q.zscore(ctx, Q.key(ctx, 'wait:' .. s.id), recs[s.rec].id, index)
          if err2 then return nil, err2 end
          f.needs = f.needs or {}
          f.needs[#f.needs + 1] = {id = s.id, record = r, in_wait = s2 ~= false and s2 ~= nil}
        elseif s.follow == 'member' then
          if r.exists then f.member = f.member or {}; f.member[#f.member + 1] = r end
        end
      end
    end
    for i = 1, #recs do
      for f = 1, #follow do
        local name = follow[f]
        if name == 'jopen' then
          local n, err2 = Q.hlen(ctx, Q.key(ctx, 'jopen:' .. recs[i].id), index)
          if err2 then return nil, err2 end
          out[i].jopen = {count = n}
        elseif name == 'due' then
          local d, err2 = Q.due_of(ctx, t, recs[i], index)
          if err2 then return nil, err2 end
          if d and #d > 0 then out[i].due = d end
        elseif name == 'index' then
          local x, err2 = Q.index_of(ctx, t, recs[i], index)
          if err2 then return nil, err2 end
          if x and #x > 0 then out[i].index = x end
        end
      end
    end
    return out, nil
  end

  -- A follows object as the answer has it: only the follows that found
  -- something, each list an array, every record with the projection only.
  function Q.follows_answer(f, fields)
    local out = {}
    for _, name in ipairs({'work', 'withdrawn', 'rcards', 'merge', 'control', 'member'}) do
      if f[name] and #f[name] > 0 then out[name] = Q.array(Q.project_list(f[name], fields)) end
    end
    if f.needs and #f.needs > 0 then
      local needs = {}
      for i, n in ipairs(f.needs) do needs[i] = {id = n.id, record = Q.project(n.record, fields), in_wait = n.in_wait} end
      out.needs = Q.array(needs)
    end
    if f.jopen then out.jopen = f.jopen end
    if f.due then out.due = Q.array(f.due) end
    if f.index then out.index = Q.array(f.index) end
    return out
  end

  -- One item of the answer: an id, its record and, when follows were asked, its follows.
  function Q.item(ctx, rec, fields, follow, f, index)
    local it = {id = rec.id, record = Q.project(rec, fields)}
    if #follow > 0 then it.follows = Q.follows_answer(f or {}, fields) end
    local S = Q.S()
    local ok, err = S.emit_read_item(ctx, it, index)
    if err then return nil, err end
    return it, nil
  end

  ---------------------------------------------------------------- the queries

  function Q.related(ctx, q, index)
    local S = Q.S()
    local left = Q.new_left()
    local res = {kind = 'related'}
    local _, err = Q.table(ctx, q.t, index)
    if err then return nil, err end
    local ids, named
    ids, named, err = Q.source_ids(ctx, q.src, q.t, index)
    if err then return nil, err end
    local kept
    kept, err = Q.leave(ctx, ids, left, index)
    if err then return nil, err end
    local recs
    recs, err = Q.records(ctx, q.t, kept, Q.union(q.fields, q.follow), index)
    if err then return nil, err end
    if named then
      err = Q.present(ctx, q.t, recs, index)
      if err then return nil, err end
    end
    local existing, at = {}, {}
    for i = 1, #recs do
      if recs[i].exists then existing[#existing + 1] = recs[i]; at[#at + 1] = i end
    end
    local fs
    fs, err = Q.follows(ctx, q.t, existing, q.follow, q.fields, left, index)
    if err then return nil, err end
    local by = {}
    for j = 1, #at do by[at[j]] = fs[j] end
    local items = {}
    for i = 1, #recs do
      local it
      it, err = Q.item(ctx, recs[i], q.fields, q.follow, by[i], index)
      if err then return nil, err end
      items[i] = it
    end
    res.ids, res.left_out, res.items = Q.array(kept), Q.array(left.list), Q.array(items)
    return res, nil
  end

  function Q.front(ctx, q, index)
    local S = Q.S()
    local left = Q.new_left()
    local res = {kind = 'front', stream = q.stream, g = '', sigma = '', n_before = 0, g_quarantined = false,
      g_record = cjson.null}
    local _, err = Q.table(ctx, Q.WORK, index)
    if err then return nil, err end
    local sent
    sent, err = Q.head_of(ctx, Q.key(ctx, 'sent:' .. q.stream), '-inf', '+inf', 1, index)
    if err then return nil, err end
    local has_g = #sent.ids == 1
    if has_g then
      res.g, res.sigma = sent.ids[1], sent.scores[1]
      local bad
      bad, err = Q.quarantined(ctx, {res.g}, index)
      if err then return nil, err end
      res.g_quarantined = bad[res.g] == true
      -- A row the work table does not have is NOROW, as Layer 1's own count refuses it.
      local rk = ctx.rows_key(Q.WORK, ctx.request_epoch)
      local rank
      rank, err = S.read_probe(ctx, {'ZSCORE', rk, q.stream}, rk, 'zset', 64)
      if err then return nil, err end
      if not rank then
        return nil, Q.fail(ctx, 'NOROW', index, {table = Q.WORK, cells = {q.stream .. ':' .. Q.OPEN_CELLS[1]}})
      end
      local before = 0
      for _, col in ipairs(Q.OPEN_CELLS) do
        local n
        n, err = Q.zcount(ctx, ctx.cell_key(Q.WORK, ctx.request_epoch, q.stream, col), '-inf', '(' .. res.sigma, index)
        if err then return nil, err end
        before = before + n
      end
      res.n_before = before
      if not res.g_quarantined then
        local rs
        rs, err = Q.records(ctx, Q.WORK, {res.g}, Q.union(q.fields, nil), index)
        if err then return nil, err end
        err = Q.present(ctx, Q.WORK, rs, index)
        if err then return nil, err end
        res.g_record = Q.project(rs[1], q.fields)
      end
    end
    local heads = {}
    for _, h in ipairs(q.heads) do
      local hr = {index = h.index, ids = Q.array({}), scores = Q.array({}), has_more = false, items = Q.array({})}
      local name, min, max = nil, '-inf', '+inf'
      local skip = false
      if h.index == 'elig' or h.index == 'fresh-below' then
        name = h.index == 'elig' and 'elig' or 'fresh'
        if has_g then max = '(' .. res.sigma end
      elseif h.index == 'fresh-above' then
        name = 'fresh'
        if has_g then min = '(' .. res.sigma else skip = true end
      else
        name = 'again'
      end
      if not skip then
        local head
        head, err = Q.head_of(ctx, Q.key(ctx, name .. ':' .. q.stream), min, max, h.limit, index)
        if err then return nil, err end
        local kept
        kept, err = Q.leave(ctx, head.ids, left, index)
        if err then return nil, err end
        local score_of = {}
        for i = 1, #head.ids do score_of[head.ids[i]] = head.scores[i] end
        local kept_scores = {}
        for i = 1, #kept do kept_scores[i] = score_of[kept[i]] end
        hr.ids, hr.scores, hr.has_more = Q.array(kept), Q.array(kept_scores), head.has_more
        local recs
        recs, err = Q.records(ctx, Q.WORK, kept, Q.union(q.fields, h.follow), index)
        if err then return nil, err end
        err = Q.present(ctx, Q.WORK, recs, index)
        if err then return nil, err end
        local fs
        fs, err = Q.follows(ctx, Q.WORK, recs, h.follow, q.fields, left, index)
        if err then return nil, err end
        local items = {}
        for i = 1, #recs do
          items[i], err = Q.item(ctx, recs[i], q.fields, h.follow, fs[i], index)
          if err then return nil, err end
        end
        hr.items = Q.array(items)
      end
      heads[#heads + 1] = hr
    end
    res.heads, res.left_out = Q.array(heads), Q.array(left.list)
    local keys
    keys, err = Q.sprint_keys(ctx, q, {q.stream}, index)
    if err then return nil, err end
    res.keys = keys
    return res, nil
  end

  -- sprint_keys reads the sprint keys a query names (IT08's `keys`), after
  -- everything else it read, in the order named: the dropping marks of the
  -- streams the query reached (reach, in order, each once), one HMGET and none
  -- when it reached none, and {p}next@e.streams, one HMGET of one field. nil
  -- when the query names none, so the answer has no `keys`.
  function Q.sprint_keys(ctx, q, reach, index)
    if q.keys == nil or #q.keys == 0 then return nil, nil end
    local out = {}
    for _, k in ipairs(q.keys) do
      local kr = {key = k, streams = Q.array({}), n = ''}
      if k == 'dropping' then
        local names, seen = {}, {}
        for _, st in ipairs(reach) do
          if st ~= '' and not seen[st] then seen[st] = true; names[#names + 1] = st end
        end
        if #names > 0 then
          local marks, err = Q.hmget(ctx, Q.key(ctx, 'dropping'), names, index)
          if err then return nil, err end
          local marked = {}
          for i = 1, #names do
            if marks[i] then marked[#marked + 1] = names[i] end
          end
          kr.streams = Q.array(marked)
        end
      elseif k == 'next.streams' then
        local v, err = Q.hmget(ctx, Q.key(ctx, 'next'), {'streams'}, index)
        if err then return nil, err end
        kr.n = '0'
        if v[1] then
          -- an exact decimal of at most 2^64 - 1, as the Go's ParseUint reads it
          local d = v[1]
          if (d ~= '0' and not d:match('^[1-9][0-9]*$')) or #d > 20 or (#d == 20 and d > '18446744073709551615') then
            return nil, Q.fail(ctx, 'DRIFT', index)
          end
          kr.n = d
        end
      else
        return nil, Q.fail(ctx, 'REQUEST', index)
      end
      out[#out + 1] = kr
    end
    return Q.array(out), nil
  end

  -- waiters reads, for each id n of the source, its record, its score in
  -- {p}missing@e and the head of wait:n with the waiters' records; `missing`
  -- reads the head only for the ids with a score. The head's `last` is the last
  -- member it read, left out or not: the cursor of the next head (IT08's R4),
  -- which moves past a head whose members were all left out. A cursor
  -- (`after`) starts the head after a member in the order of wait:n's members,
  -- which are all scored 0: Layer 1's checked reads give a sorted set's head by
  -- score only (S.read_range_head, no lexicographic bound), so a query with a
  -- cursor is refused CONFIG, before it reads anything, until Layer 1 has one.
  -- The twin answers it (sprintfn waitHead).
  function Q.waiters(ctx, q, index)
    if q.after ~= nil and q.after ~= '' then return nil, Q.fail(ctx, 'CONFIG', index) end
    local left = Q.new_left()
    local res = {kind = 'waiters'}
    local _, err = Q.table(ctx, Q.WORK, index)
    if err then return nil, err end
    local ids, more_ids
    ids, _, err, more_ids = Q.source_ids(ctx, q.src, Q.WORK, index)
    if err then return nil, err end
    local kept
    kept, err = Q.leave(ctx, ids, left, index)
    if err then return nil, err end
    local recs
    recs, err = Q.records(ctx, Q.WORK, kept, q.fields, index)
    if err then return nil, err end
    local missing = {}
    local i = 1
    while i <= #kept do
      local chunk = {}
      for k = i, math.min(i + Q.PROBE_CHUNK - 1, #kept) do chunk[#chunk + 1] = kept[k] end
      local got
      got, err = Q.zmscore(ctx, Q.key(ctx, 'missing'), chunk, index)
      if err then return nil, err end
      for k = 1, #chunk do missing[#missing + 1] = got[k] end
      i = i + Q.PROBE_CHUNK
    end
    local items, reach = {}, {}
    for n = 1, #kept do
      local it = {id = kept[n], record = recs[n], missing = missing[n] or cjson.null,
        wait = {ids = Q.array({}), has_more = false, last = '', left_out = Q.array({}), items = Q.array({})}}
      if not q.missing or missing[n] then
        local wleft = Q.new_left()
        local head
        head, err = Q.head_of(ctx, Q.key(ctx, 'wait:' .. kept[n]), '-inf', '+inf', q.limit, index)
        if err then return nil, err end
        local wk
        wk, err = Q.leave(ctx, head.ids, wleft, index)
        if err then return nil, err end
        local wrecs
        wrecs, err = Q.records(ctx, Q.WORK, wk, q.fields, index)
        if err then return nil, err end
        err = Q.present(ctx, Q.WORK, wrecs, index)
        if err then return nil, err end
        local refs = {}
        for w = 1, #wrecs do
          refs[w] = {id = wrecs[w].id, record = wrecs[w]}
          local place = wrecs[w].place
          if type(place) == 'table' and type(place.row) == 'string' then reach[#reach + 1] = place.row end
        end
        it.wait = {ids = Q.array(wk), has_more = head.has_more, last = head.ids[#head.ids] or '',
          left_out = Q.array(wleft.list), items = Q.array(refs)}
      end
      local ok
      ok, err = Q.S().emit_read_item(ctx, it, index)
      if err then return nil, err end
      items[n] = it
    end
    res.ids, res.left_out, res.items, res.more_ids = Q.array(kept), Q.array(left.list), Q.array(items), more_ids == true
    local keys
    keys, err = Q.sprint_keys(ctx, q, reach, index)
    if err then return nil, err end
    res.keys = keys
    return res, nil
  end

  -- streams reads every stream of the work table (up to the units), its control
  -- card, and for one stopped on a cross need that need card's record and the
  -- first `limit` ids of its stuck cell. Every card it would read leaves out the
  -- quarantined ones (1.0, 1.3.5): a stream whose control card is quarantined is
  -- listed with no control card, one stopped on a quarantined need card has no
  -- need record, and the ids are named in left_out; a stream stopped on a cross
  -- need is still told apart by its stuck list, which it has either way.
  function Q.streams(ctx, q, index)
    local S = Q.S()
    local left = Q.new_left()
    local res = {kind = 'streams'}
    local units = q.units or 0
    if units == 0 then units = Q.MAX_STREAMS end
    local def, err = Q.table(ctx, Q.WORK, index)
    if err then return nil, err end
    local rows
    rows, err = Q.head_of(ctx, ctx.rows_key(Q.WORK, ctx.request_epoch), '-inf', '+inf', units, index)
    if err then return nil, err end
    res.rows, res.has_more = Q.array(rows.ids), rows.has_more
    local merge_def
    merge_def, err = Q.table(ctx, Q.MERGE, index)
    if err then return nil, err end
    local ctl_ids = {}
    for i = 1, #rows.ids do ctl_ids[i] = 'ctl-' .. rows.ids[i] end
    local kept_ctl
    kept_ctl, err = Q.leave(ctx, ctl_ids, left, index)
    if err then return nil, err end
    local ctl_recs
    ctl_recs, err = Q.records(ctx, Q.MERGE, kept_ctl, Q.union(q.fields, nil, {Q.C_STATE, Q.C_CAUSE, Q.C_OTHER, Q.C_NEED}), index)
    if err then return nil, err end
    local ctls, k = {}, 1      -- ctls[i] is nil when the control card was left out
    for i = 1, #ctl_ids do
      if kept_ctl[k] == ctl_ids[i] then ctls[i] = ctl_recs[k]; k = k + 1 end
    end
    local crossed, need_ids = {}, {}
    for i = 1, #ctl_ids do
      local c = ctls[i]
      if c and c.exists and Q.field(c, Q.C_STATE) == Q.STOPPED and Q.field(c, Q.C_CAUSE) == Q.CROSS then
        local need = Q.cross_need(c)
        if need ~= '' then
          crossed[#crossed + 1] = i
          need_ids[#need_ids + 1] = need
        end
      end
    end
    local kept_needs
    kept_needs, err = Q.leave(ctx, need_ids, left, index)
    if err then return nil, err end
    local need_recs
    need_recs, err = Q.records(ctx, Q.WORK, kept_needs, q.fields, index)
    if err then return nil, err end
    local needs = {}           -- needs[j] is nil when the need card was left out
    k = 1
    for j = 1, #need_ids do
      if kept_needs[k] == need_ids[j] then needs[j] = need_recs[k]; k = k + 1 end
    end
    local items = {}
    for i = 1, #rows.ids do
      items[i] = {stream = rows.ids[i], control = cjson.null, need = cjson.null, stuck = cjson.null}
      if ctls[i] and ctls[i].exists then items[i].control = Q.project(ctls[i], q.fields) end
    end
    for j = 1, #crossed do
      local i = crossed[j]
      if needs[j] then items[i].need = needs[j] end
      local st = {ids = Q.array({}), has_more = false, left_out = Q.array({})}
      if q.limit > 0 then
        local row = rows.ids[i]
        local head
        head, err = Q.cell_head(ctx, Q.MERGE, row, Q.STUCK, q.limit, index)
        if err then return nil, err end
        local sleft = Q.new_left()
        local kept
        kept, err = Q.leave(ctx, head.ids, sleft, index)
        if err then return nil, err end
        st.ids, st.has_more, st.left_out = Q.array(kept), head.has_more, Q.array(sleft.list)
      end
      items[i].stuck = st
    end
    local counts = q.counts or {}
    if #counts > 0 and #rows.ids > 0 then
      -- the counts of the cells named, of every stream listed (2.3 R15), one
      -- ZCARD a cell; a column the work table does not have is NOCOL
      for i = 1, #rows.ids do
        for _, col in ipairs(counts) do
          if not def.column_set[col] then
            return nil, Q.fail(ctx, 'NOCOL', index, {table = Q.WORK, cells = {rows.ids[i] .. ':' .. col}})
          end
        end
      end
      for i = 1, #rows.ids do
        local cs = {}
        for _, col in ipairs(counts) do
          local key = ctx.cell_key(Q.WORK, ctx.request_epoch, rows.ids[i], col)
          local n
          n, err = S.read_probe(ctx, {'ZCARD', key}, key, 'zset', 32)
          if err then return nil, err end
          cs[#cs + 1] = {col = col, n = n}
        end
        items[i].counts = Q.array(cs)
      end
    end
    for i = 1, #items do
      local ok
      ok, err = S.emit_read_item(ctx, items[i], index)
      if err then return nil, err end
    end
    res.items, res.left_out = Q.array(items), Q.array(left.list)
    local keys
    keys, err = Q.sprint_keys(ctx, q, rows.ids, index)
    if err then return nil, err end
    res.keys = keys
    return res, nil
  end

  -- The card a stopped stream's control card says it waits for: `other`, which
  -- is where the writer records the cross fact, and when that is empty
  -- `need_card`, which IT11's held rule reads beside it.
  function Q.cross_need(ctl)
    local need = Q.field(ctl, Q.C_OTHER)
    if need ~= '' then return need end
    return Q.field(ctl, Q.C_NEED)
  end

  -- The first ids of a table's cell (a range head), a row the table lacks being NOROW.
  function Q.cell_head(ctx, t, row, col, limit, index)
    local def, err = Q.table(ctx, t, index)
    if err then return nil, err end
    local cell = row .. ':' .. col
    if not def.column_set[col] then return nil, Q.fail(ctx, 'NOCOL', index, {table = t, cells = {cell}}) end
    local rk = ctx.rows_key(t, ctx.request_epoch)
    local rank
    rank, err = Q.S().read_probe(ctx, {'ZSCORE', rk, row}, rk, 'zset', 64)
    if err then return nil, err end
    if not rank then return nil, Q.fail(ctx, 'NOROW', index, {table = t, cells = {cell}}) end
    return Q.head_of(ctx, ctx.cell_key(t, ctx.request_epoch, row, col), '-inf', '+inf', limit, index)
  end

  -- listing reads every member's (reader's) row: its control card and the
  -- counts of its cells, one ZCARD a cell. A control card that is quarantined is
  -- left out (1.0, 1.3.5): the row is listed, its counts with it, and the card is
  -- named in left_out.
  function Q.listing(ctx, q, index)
    local S = Q.S()
    local left = Q.new_left()
    local t = q.kind == 'fleet' and Q.FLEET or Q.READERS
    local res = {kind = q.kind}
    local units = q.units or 0
    if units == 0 then units = q.kind == 'fleet' and Q.MAX_MEMBERS or Q.MAX_READERS end
    local def, err = Q.table(ctx, t, index)
    if err then return nil, err end
    local rows
    rows, err = Q.head_of(ctx, ctx.rows_key(t, ctx.request_epoch), '-inf', '+inf', units, index)
    if err then return nil, err end
    res.rows, res.has_more = Q.array(rows.ids), rows.has_more
    local counted = {}
    for _, col in ipairs(def.columns) do
      if col ~= Q.CTL and def.raw and def.raw['col:' .. col] == 'set' then counted[#counted + 1] = col end
    end
    local ctl_ids = {}
    for i = 1, #rows.ids do ctl_ids[i] = 'ctl-' .. rows.ids[i] end
    local kept_ctl
    kept_ctl, err = Q.leave(ctx, ctl_ids, left, index)
    if err then return nil, err end
    local ctl_recs
    ctl_recs, err = Q.records(ctx, t, kept_ctl, q.fields, index)
    if err then return nil, err end
    local ctls, k = {}, 1      -- ctls[i] is nil when the control card was left out
    for i = 1, #ctl_ids do
      if kept_ctl[k] == ctl_ids[i] then ctls[i] = ctl_recs[k]; k = k + 1 end
    end
    local items = {}
    for i = 1, #rows.ids do
      local it = {row = rows.ids[i], control = cjson.null}
      if ctls[i] and ctls[i].exists then it.control = ctls[i] end
      local counts = {}
      for _, col in ipairs(counted) do
        local key = ctx.cell_key(t, ctx.request_epoch, rows.ids[i], col)
        local n
        n, err = S.read_probe(ctx, {'ZCARD', key}, key, 'zset', 32)
        if err then return nil, err end
        counts[#counts + 1] = {col = col, n = n}
      end
      it.counts = Q.array(counts)
      local ok
      ok, err = S.emit_read_item(ctx, it, index)
      if err then return nil, err end
      items[i] = it
    end
    res.items, res.left_out = Q.array(items), Q.array(left.list)
    return res, nil
  end

  function Q.needchain(ctx, q, index)
    local S = Q.S()
    local left = Q.new_left()
    local res = {kind = 'needchain'}
    local _, err = Q.table(ctx, Q.WORK, index)
    if err then return nil, err end
    local ids, named
    ids, named, err = Q.source_ids(ctx, q.src, Q.WORK, index)
    if err then return nil, err end
    local frontier
    frontier, err = Q.leave(ctx, ids, left, index)
    if err then return nil, err end
    local visited, start = {}, {}
    for i = 1, #frontier do visited[frontier[i]] = true; start[frontier[i]] = true end
    local items, read = {}, 0
    while #frontier > 0 and read < q.limit do
      local batch, rest = {}, {}
      local room = q.limit - read
      for i = 1, #frontier do
        if i <= room then batch[#batch + 1] = frontier[i] else rest[#rest + 1] = frontier[i] end
      end
      local recs
      recs, err = Q.records(ctx, Q.WORK, batch, Q.union(q.fields, nil, {Q.F_NEEDS}), index)
      if err then return nil, err end
      read = read + #batch
      local nxt = {}
      for _, r in ipairs(recs) do
        if named and start[r.id] then
          err = Q.present(ctx, Q.WORK, {r}, index)
          if err then return nil, err end
        end
        local it = {id = r.id, record = Q.project(r, q.fields), needs = Q.array({}), start = start[r.id] == true}
        if r.exists then
          local needs = Q.split(Q.field(r, Q.F_NEEDS))
          if #needs > Q.MAX_NEEDS then return nil, Q.fail(ctx, 'DRIFT', index, {table = Q.WORK, ids = {r.id}}) end
          it.needs = Q.array(needs)
          if Q.placed(r) and r.place.col == Q.WAITING then
            for _, n in ipairs(needs) do
              if not visited[n] then visited[n] = true; nxt[#nxt + 1] = n end
            end
          end
        end
        local ok
        ok, err = S.emit_read_item(ctx, it, index)
        if err then return nil, err end
        items[#items + 1] = it
      end
      nxt, err = Q.leave(ctx, nxt, left, index)
      if err then return nil, err end
      frontier = {}
      for i = 1, #rest do frontier[#frontier + 1] = rest[i] end
      for i = 1, #nxt do frontier[#frontier + 1] = nxt[i] end
    end
    res.cut = #frontier > 0
    res.items, res.left_out = Q.array(items), Q.array(left.list)
    return res, nil
  end

  function Q.jnote(ctx, q, index)
    local S = Q.S()
    local res = {kind = 'jnote'}
    local ids, _, err = Q.source_ids(ctx, q.src, nil, index)
    if err then return nil, err end
    res.ids = Q.array(ids)
    local most = q.subjects or 0
    if most <= 0 then most = Q.MAX_ABOUT end
    local items = {}
    for _, id in ipairs(ids) do
      local seq, epoch = Q.note_seq(id)
      if not seq or epoch ~= ctx.request_epoch then return nil, Q.fail(ctx, 'REQUEST', index) end
      local line
      line, err = Q.line(ctx, seq, index)
      if err then return nil, err end
      local typ = type(line.meta) == 'table' and line.meta.type or nil
      local cause = type(line.meta) == 'table' and line.meta.cause or nil
      if line.kind ~= 'note' or type(typ) ~= 'string' or typ == '' or type(cause) ~= 'string' or cause == '' then
        return nil, Q.fail(ctx, 'DRIFT', index, {ids = {id}})
      end
      if #line.about > most then
        return nil, S.refuse('BUDGET', {query_index = index, budget = 'subjects', actual = #line.about, limit = most})
      end
      local left = Q.new_left()
      local kept
      kept, err = Q.leave(ctx, line.about, left, index)
      if err then return nil, err end
      local field = typ .. '|' .. cause
      local subjects = {}
      for _, s in ipairs(kept) do
        local key = Q.key(ctx, 'jopen:' .. s)
        local n
        n, err = Q.hlen(ctx, key, index)
        if err then return nil, err end
        local own
        own, err = Q.hmget(ctx, key, {field}, index)
        if err then return nil, err end
        subjects[#subjects + 1] = {id = s, count = n, own = own[1] or cjson.null}
      end
      local it = {note = id, seq = string.format('%d', seq), type = typ, cause = cause, subjects = Q.array(subjects),
        left_out = #left.list}
      local ok
      ok, err = S.emit_read_item(ctx, it, index)
      if err then return nil, err end
      items[#items + 1] = it
    end
    res.items = Q.array(items)
    return res, nil
  end

  ---------------------------------------------------------------- the sprint-key reads

  -- A field of a hash read by HMGET, as the JSON null when the hash lacks it.
  function Q.nullable(v)
    if v == false or v == nil then return cjson.null end
    return v
  end
  function Q.whole(v)
    if v == false or v == nil or v == '' then return 0, true end
    if v ~= '0' and not v:match('^[1-9][0-9]*$') then return 0, false end
    if #v > 15 then return 0, false end
    return tonumber(v), true
  end

  -- The clock and R(t) = t - stopped_ms - (stopped_since_ms == "" ? 0 : t - stopped_since_ms),
  -- from the call's one TIME.
  function Q.clock_at(ctx, index)
    local vals, err = Q.hmget(ctx, Q.bare(ctx, 'clock'), Q.CLOCK_FIELDS, index)
    if err then return nil, nil, err end
    local wall = tonumber(ctx.now_ms)
    local stopped, ok1 = Q.whole(vals[1])
    local since, ok2 = Q.whole(vals[2])
    local _, ok3 = Q.whole(vals[3])
    local _, ok4 = Q.whole(vals[4])
    local _, ok5 = Q.whole(vals[5])
    if not wall or not (ok1 and ok2 and ok3 and ok4 and ok5) then return nil, nil, Q.fail(ctx, 'DRIFT', index) end
    local r = wall - stopped
    if vals[2] and vals[2] ~= '' then r = r - (wall - since) end
    if r < 0 then return nil, nil, Q.fail(ctx, 'DRIFT', index) end
    local clock = {}
    for i, name in ipairs(Q.CLOCK_FIELDS) do clock[name] = Q.nullable(vals[i]) end
    return clock, string.format('%d', r), nil
  end

  function Q.read_key(ctx, q, index)
    local S = Q.S()
    local kind = q.kind
    if kind == 'clock' then
      local clock, r, err = Q.clock_at(ctx, index)
      if err then return nil, err end
      return {kind = kind, wall_ms = ctx.now_ms, r = r, clock = clock}, nil
    elseif kind == 'lease' or kind == 'tick' then
      local names = kind == 'lease' and Q.LEASE_FIELDS or Q.TICK_FIELDS
      local key = kind == 'lease' and Q.bare(ctx, 'lease') or Q.key(ctx, 'tick')
      local vals, err = Q.hmget(ctx, key, names, index)
      if err then return nil, err end
      local out = {kind = kind}
      for i, name in ipairs(names) do out[name] = Q.nullable(vals[i]) end
      if kind == 'lease' then out.now_ms = ctx.now_ms end
      return out, nil
    elseif kind == 'heartbeat' then
      local vals, err = Q.hmget(ctx, Q.bare(ctx, 'heartbeat'), Q.HEARTBEAT_FIELDS, index, Q.HEARTBEAT_FIELD_BYTES)
      if err then return nil, err end
      local fields = {}
      for i, name in ipairs(Q.HEARTBEAT_FIELDS) do
        if vals[i] then fields[name] = vals[i] end
      end
      return {kind = kind, fields = fields}, nil
    elseif kind == 'dropping' or kind == 'parked' then
      local key = Q.key(ctx, kind)
      local names = kind == 'dropping' and q.streams or q.keys
      local n, err = Q.hlen(ctx, key, index)
      if err then return nil, err end
      local found = {}
      if #names > 0 then
        local vals
        vals, err = Q.hmget(ctx, key, names, index)
        if err then return nil, err end
        for i = 1, #names do
          if vals[i] then found[names[i]] = vals[i] end
        end
      end
      if kind == 'dropping' then return {kind = kind, count = n, marks = found}, nil end
      return {kind = kind, count = n, notes = found}, nil
    elseif kind == 'missing' then
      local scores = {}
      local i = 1
      while i <= #q.ids do
        local chunk = {}
        for k = i, math.min(i + Q.PROBE_CHUNK - 1, #q.ids) do chunk[#chunk + 1] = q.ids[k] end
        local got, err = Q.zmscore(ctx, Q.key(ctx, 'missing'), chunk, index)
        if err then return nil, err end
        for k = 1, #chunk do scores[#scores + 1] = Q.nullable(got[k]) end
        i = i + Q.PROBE_CHUNK
      end
      return {kind = kind, scores = Q.array(scores)}, nil
    elseif kind == 'jopen' then
      local items = {}
      for _, s in ipairs(q.subjects) do
        local key = Q.key(ctx, 'jopen:' .. s)
        local n, err = Q.hlen(ctx, key, index)
        if err then return nil, err end
        local found = {}
        if #q.names > 0 then
          local vals
          vals, err = Q.hmget(ctx, key, q.names, index)
          if err then return nil, err end
          for i, name in ipairs(q.names) do found[name] = Q.nullable(vals[i]) end
        end
        items[#items + 1] = {id = s, count = n, fields = found}
      end
      return {kind = kind, items = Q.array(items)}, nil
    elseif kind == 'next' then
      -- {p}next@e (1.3.1): the fields named that the hash holds (IT19).
      local found = {}
      if #q.names > 0 then
        local vals, err = Q.hmget(ctx, Q.key(ctx, 'next'), q.names, index)
        if err then return nil, err end
        for i, name in ipairs(q.names) do
          if vals[i] then found[name] = vals[i] end
        end
      end
      return {kind = kind, fields = found}, nil
    elseif kind == 'duecount' then
      local _, r, err = Q.clock_at(ctx, index)
      if err then return nil, err end
      local n
      n, err = Q.zcount(ctx, Q.key(ctx, 'due'), '-inf', r, index)
      if err then return nil, err end
      return {kind = kind, r = r, due = n}, nil
    end
    return nil, Q.fail(ctx, 'REQUEST', index)
  end

  ---------------------------------------------------------------- registration

  -- A read that returns nothing and no refusal is a bug of this file, which
  -- Layer 1 turns into CONFIG; a refusal keeps its own code.
  for _, name in ipairs({'related', 'front', 'waiters', 'streams', 'fleet', 'readers', 'needchain', 'jnote'}) do
    local reader = name == 'fleet' and Q.listing or (name == 'readers' and Q.listing or Q[name])
    SP.query(name, {validate = Q.validator(Q.check), read = reader, cost = Q.cost})
  end
  for _, name in ipairs({'clock', 'lease', 'tick', 'heartbeat', 'dropping', 'parked', 'missing', 'jopen', 'duecount', 'next'}) do
    SP.query(name, {validate = Q.validator(Q.check_key), read = Q.read_key, cost = Q.key_cost})
  end
end
end
