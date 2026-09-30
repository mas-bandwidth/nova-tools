package stepbuild

// The planned commands, modelled: Layer 1's command layout.
//
// Section 6 bounds the summed argv bytes of every command a step plans (8 MiB,
// the table plan, the log plan and the receipt together), and section 1.4 says
// an argv is the command name and every argument, keys included. Layer 1
// refuses a step over it with LIMIT at prepare, after the request has been
// accepted; so the builder counts those bytes as an upper bound and cuts to it,
// so that a step it emitted is never refused for them.
//
// The layout below is the one place this package spells a command or a key. It
// has a row for each command and each key Layer 1 plans, and beside each row
// the section of the contract that fixes it and, where Layer 1 has an
// implementation, the place it lays the command out (the tset-l1 branch at
// e269dd7aa: internal/nsprint/fn/lua, table_set.lua and table_set_rows.lua;
// table_set_receipt.lua for the receipt). Layer 2's commands (the log's XADD
// and the histories' RPUSH) are not on that branch: their rows are the
// contract's (sections 1.2, 1.3, 1.4), and what the contract leaves to Layer 2
// (the field an XADD carries its line in) is a width taken generously.
//
// The count is an upper bound over the layout:
//
//   - every changed member is charged its own commands, one of each kind its
//     entry needs (no batching: the most commands any splitting could make),
//     each with its name and its key, a value the store supplies (a revision,
//     a seq, a rank, a score the request leaves out) at its widest, and a name
//     or key that can be shorter at its longest;
//   - a step is cut to the count with a margin of argvMarginPercent on top
//     (withMargin), for what the layout does not say: Layer 2's encodings, a
//     key or a field spelled a little differently, a command Layer 1 adds.
//
// It is an upper bound for a member prefix of at most the length the caller
// names (Config.MemberPrefixBytes; the default is the longest Layer 1 accepts)
// and for the commands of create, move, remove and rows entries and of notes.
// The commands of an advance are not modelled: the builder does not cut
// advance entries. Commands that only read are not planned argv.

// slot indexes the layout.
type slot int

// The rows of the layout.
const (
	cmdCreateHSET slot = iota // a created member's record
	cmdMoveHSET               // a moved (or staying) member's record
	cmdRemoveHSET             // a removed member's record
	cmdHDEL                   // a member's unset names, and a removed member's placement
	cmdZREM                   // a member out of its cell
	cmdZADD                   // a member into its cell
	cmdRPUSH                  // a seq into the history of a primary
	cmdXADD                   // a log line
	cmdRowsZADD               // a row into the rows key
	cmdRowsZREM               // a row out of the rows key
	cmdDoneHSET               // the receipt
	keyRecord
	keyCell
	keyRows
	keyLog
	keyHistory
	keyDone
	slots
)

// layoutRow is one line of the layout: a command or a key of Layer 1's plan.
type layoutRow struct {
	what    string // the command or the key
	spelled string // its argv (the arguments after the name), or its text, as Layer 1 spells it; <...> is a value
	fixed   int    // the bytes of literal text in it (a command's name included) that no value changes
	section string // the contract's section, and where Layer 1 lays it out
}

// lits is the total length of literal texts.
func lits(ss ...string) int {
	n := 0
	for _, s := range ss {
		n += len(s)
	}
	return n
}

// The names Layer 1 spells in a record's fields (section 4: epoch, revision
// and place:* are reserved to it).
const (
	fieldRevision = "revision"
	fieldEpoch    = "epoch"
	fieldPlace    = "place:" // the placement field is named for the table: place:<table>, and holds the cell reference
)

// The widths of the values in the layout: what the store supplies, at its
// widest, and what the contract bounds.
const (
	uintBytes        = len(maxUintText)        // a revision, an epoch: a decimal uint64 at its widest (section 4)
	seqBytes         = len("9007199254740991") // a seq: at most 2^53-1, the live-sequence ceiling (section 4)
	rankBytes        = len("9007199254740991") // a row rank: at most 2^53-1 (section 3)
	scoreBytes       = lineScoreBytes - 2      // a score at its widest canonical spelling, unquoted
	streamIDBytes    = seqBytes + len("-0")    // an XADD's stream ID: <seq>-0 (section 1.4)
	streamFieldBytes = 16                      // the name of the field an XADD carries its line in: Layer 2's to name; taken generously

	maxUintText = "18446744073709551615"
)

var layout = [slots]layoutRow{
	cmdCreateHSET: {"HSET (create)", "<record> revision <rev> epoch <epoch> place:<table> <cell> {<name> <value>}...",
		lits("HSET", fieldRevision, fieldEpoch, fieldPlace),
		"1.4, 3 (create), 4; table_set.lua:415-419"},
	cmdMoveHSET: {"HSET (move)", "<record> revision <rev> place:<table> <cell> {<name> <value>}...",
		lits("HSET", fieldRevision, fieldPlace),
		"1.4, 3 (move), 4; table_set.lua:415-419 (a member that stays keeps its cell in the field)"},
	cmdRemoveHSET: {"HSET (remove)", "<record> revision <rev> {<name> <value>}...",
		lits("HSET", fieldRevision),
		"1.4, 3 (remove: no placement), 4; table_set.lua:415, 418-419"},
	cmdHDEL: {"HDEL", "<record> {<unset name>}... [place:<table> for a remove]",
		lits("HDEL"),
		"1.4, 1.6, 3 (remove: place is removed, other fields retained); table_set.lua:420-422"},
	cmdZREM: {"ZREM", "<cell key> <id>",
		lits("ZREM"),
		"1.4, 3 (a member's ZREM precedes its ZADD); table_set.lua:424 (a create has no source cell: none)"},
	cmdZADD: {"ZADD", "<cell key> <score> <id>",
		lits("ZADD"),
		"1.4, 3, 4; table_set.lua:425 (a remove has no destination: none)"},
	cmdRPUSH: {"RPUSH", "<history key> <seq>",
		lits("RPUSH"),
		"1.2 (history_key), 1.3 (one append per about of a line), 1.4; Layer 2"},
	cmdXADD: {"XADD", "<log key> <seq>-0 <field> <line>",
		lits("XADD") + streamIDBytes + streamFieldBytes,
		"1.2 (log_key), 1.3 (one line per event and per note), 1.4; table_set.lua:541-545 (the stream ID); Layer 2"},
	cmdRowsZADD: {"ZADD (rows)", "<rows key> <rank> <row>",
		lits("ZADD"),
		"1.4, 3 (rows: add); table_set_rows.lua:234-247 (Layer 1 batches a table's rows to 1,000 a command; counted one each)"},
	cmdRowsZREM: {"ZREM (rows)", "<rows key> <row>",
		lits("ZREM"),
		"1.4, 3 (rows: delete); table_set_rows.lua:234-247"},
	cmdDoneHSET: {"HSET (done)", "<done key> <op> <receipt>",
		lits("HSET"),
		"1.4 (appended last), 5, 6 (receipt 32 KiB); table_set_receipt.lua:66-80 (the receipt is counted whole)"},

	keyRecord: {"record key", "<member prefix><id>", 0,
		"1.2 (record_key); table_set.lua:64"},
	keyCell: {"cell key", "<ns>table:<table>:<epoch>:cell:<row>:<col>", lits("table:", ":", ":cell:"),
		"1.2 (cell_key), 3 (the cell reference is <row>:<col>); table_set.lua:66-68"},
	keyRows: {"rows key", "<ns>table:<table>:<epoch>:rows", lits("table:", ":", ":rows"),
		"1.2 (rows_key); table_set.lua:66-67"},
	keyLog: {"log key", "<ns>sprint:log@<epoch>", lits("sprint:log@"),
		"1.2 (log_key); table_set.lua:70"},
	keyHistory: {"history key", "<ns>sprint:cl:<about>@<epoch>", lits("sprint:cl:", "@"),
		"1.2 (history_key); table_set.lua:71"},
	keyDone: {"done key", "<ns>sprint:done@<epoch>", lits("sprint:done@"),
		"1.2 (done_key), 5; table_set.lua:72"},
}

// argvMarginPercent is the margin a step's planned argv bytes are cut with, on
// top of the count of the layout.
const argvMarginPercent = 25

// withMargin is n with the margin on top, rounded up: what the cut compares to
// the bound.
func withMargin(n int) int { return n + (n*argvMarginPercent+99)/100 }

// keySizes are the sizes the planned keys work to, from the Config: the
// longest member prefix, the widest namespace (the longest header value: the
// request's namespace member is a name and every structural key starts with
// it) and the epoch's digits.
type keySizes struct{ member, ns, epoch int }

func newKeySizes(cfg Config) keySizes {
	k := keySizes{member: cfg.MemberPrefixBytes, epoch: len(cfg.Epoch)}
	if k.member == 0 {
		k.member = DefaultMemberPrefixBytes
	}
	for _, m := range cfg.Header {
		k.ns = max(k.ns, len(m.Value))
	}
	return k
}

func (k keySizes) record(id string) int { return layout[keyRecord].fixed + k.member + len(id) }

// cell is a cell key of table and the cell reference "<row>:<col>".
func (k keySizes) cell(table, ref string) int {
	return k.ns + layout[keyCell].fixed + len(table) + k.epoch + len(ref)
}

func (k keySizes) history(about string) int {
	return k.ns + layout[keyHistory].fixed + len(about) + k.epoch
}

func (k keySizes) rows(table string) int { return k.ns + layout[keyRows].fixed + len(table) + k.epoch }

func (k keySizes) log() int { return k.ns + layout[keyLog].fixed + k.epoch }

func (k keySizes) done() int { return k.ns + layout[keyDone].fixed + k.epoch }

// xadd is the planned argv bytes of a log line beside the line itself.
func (k keySizes) xadd() int { return layout[cmdXADD].fixed + k.log() }

// rpush is the planned argv bytes of one history append.
func (k keySizes) rpush(about string) int {
	return layout[cmdRPUSH].fixed + k.history(about) + seqBytes
}

// receipt is the planned argv bytes of the done HSET of a step whose op is op:
// the receipt is reserved whole, its size not being known before the step runs.
func (k keySizes) receipt(op string) int {
	return layout[cmdDoneHSET].fixed + k.done() + len(op) + LimitReceiptBytes
}

// rowAdd and rowDel are the planned argv bytes of one row of a rows entry: its
// ZADD or ZREM of the rows key.
func (k keySizes) rowAdd(table, row string) int {
	return layout[cmdRowsZADD].fixed + k.rows(table) + rankBytes + len(row)
}

func (k keySizes) rowDel(table, row string) int {
	return layout[cmdRowsZREM].fixed + k.rows(table) + len(row)
}

// memberCommands is the planned argv bytes of the commands one member of an
// entry needs, beside the member's ID, its record's key, its effective fields
// and its unset names, which are its own: each field is the bytes of the
// command's name and the arguments the entry fixes. Zero means the kind plans
// no such command.
type memberCommands struct{ hset, hdel, zrem, zadd int }

// commands are the commands of a member of an entry of the kind, in table,
// from src and to dst (dst is src for a member that stays), with unset when
// the entry names fields to unset. The record's HSET carries the record's own
// fields (its revision, and on a create its epoch; on a create and a move the
// placement: a field named for the table, holding the cell reference); a
// remove instead deletes its placement with an HDEL, beside the names it
// unsets (table_set.lua:415-422).
func (k keySizes) commands(kind Kind, table, src, dst string, unset bool) memberCommands {
	var c memberCommands
	switch kind {
	case KindCreate:
		c.hset = layout[cmdCreateHSET].fixed + uintBytes + k.epoch + len(table) + len(dst)
	case KindMove:
		c.hset = layout[cmdMoveHSET].fixed + uintBytes + len(table) + len(dst)
	default:
		c.hset = layout[cmdRemoveHSET].fixed + uintBytes
	}
	if kind == KindRemove {
		c.hdel = layout[cmdHDEL].fixed + len(fieldPlace) + len(table)
	} else if unset {
		c.hdel = layout[cmdHDEL].fixed
	}
	if kind != KindCreate {
		c.zrem = layout[cmdZREM].fixed + k.cell(table, src)
	}
	if kind != KindRemove {
		c.zadd = layout[cmdZADD].fixed + k.cell(table, dst)
	}
	return c
}
