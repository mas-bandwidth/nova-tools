package tset

// These bounds are the admission limits of tset/1 revision 2 with the
// confirmed A9 and A12 amendments. They are checked before any Redis call.
const (
	MaxWriteRequestBytes = 4 << 20
	MaxReadRequestBytes  = 4 << 20
	MaxJSONDepth         = 16
	MaxEntries           = 256
	MaxQueries           = 1024
	MaxTables            = 4
	MaxMemberCandidates  = 2000
	MaxGuardMembers      = 4000
	MaxIDsPerEntry       = 2000
	MaxRowsPerStep       = 100
	MaxRowsWithAdvance   = 1024
	MaxIdentifierBytes   = 256
	MaxFieldsPerMember   = 128
	MaxIntentBytes       = 64 << 10
	MaxFieldValueBytes   = 64 << 10
	MaxResultBytes       = 4 << 10
	MaxReceiptBytes      = 32 << 10
	MaxNotes             = 100
	MaxAboutBeforeDedup  = 4000
	MaxLineBytes         = 1 << 20
	MaxIDsPerLine        = 2000
	MaxPlannedCommands   = 65536
	MaxPlannedArgvBytes  = 8 << 20
	MaxFetchedBytes      = 8 << 20
	MaxReadReplyBytes    = 8 << 20
	MaxCardItemBytes     = 512 << 10 // A9: enough for a 64 KiB field after JSON escaping.
)
