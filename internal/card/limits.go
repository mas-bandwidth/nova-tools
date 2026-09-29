package card

// The table layer's bounds, as the table extension's manifest states them
// (mas-bandwidth/ideas#825, "Manifest, identity and bounds"). The card layer's
// bounds below are derived from these so that any request the card layer
// accepts fits one table manifest, and a request that would not fit is refused
// here, whole, before a store call.
const (
	// TableChangedEntries bounds the entries of a manifest that carry changes.
	TableChangedEntries = 128
	// TableGuardOnlyEntries bounds the entries that only guard (no changes).
	TableGuardOnlyEntries = 1024
	// TableManifestBytes bounds the canonical encoded manifest.
	TableManifestBytes = 1 << 20
	// TableMemberIDBytes bounds a member ID; a card ID is at most MaxIDBytes.
	TableMemberIDBytes = 256
	// TableFieldValueBytes bounds one member field value.
	TableFieldValueBytes = 64 << 10
	// TableSetFieldsPerMember bounds the fields one entry sets.
	TableSetFieldsPerMember = 128
)

// CardOperationRecordEntries is the entries of every mutating table batch that
// the card manager keeps for its own operation record (the recorded card request
// hash and receipt that make a replay recognisable). One of the table's 128
// changed entries is reserved for it, so a card request has at most 127.
const CardOperationRecordEntries = 1

// Count bounds of a request. Each names the limit a refusal cites; exceeding one
// refuses the whole request and never splits it into several. They are the
// bounds of the manager design (docs/SPEC-CARD-MANAGER.md, "Size arithmetic and
// the card layer's bounds"), kept here so that a request valid at the card layer
// is never refused by the table for size.
const (
	// MaxChangedEntries bounds an array whose entries change cards: admissions,
	// lifecycle inputs, evidence entries. It is the table's 128 changed entries
	// less the one the card operation record takes.
	MaxChangedEntries = TableChangedEntries - CardOperationRecordEntries
	// MaxReplacementPairs bounds a replacement array: each pair is two table
	// members (the old one ended, the new one created), so 63 pairs are 126 of
	// the 127 entries a card request may change.
	MaxReplacementPairs = MaxChangedEntries / 2
	// MaxGuardOnlyEntries bounds the cards a request reads or guards without a
	// change: the table's guard-only entries.
	MaxGuardOnlyEntries = TableGuardOnlyEntries
	// MaxScopeCards bounds an ID scope of an inspect.
	MaxScopeCards = TableGuardOnlyEntries
	// MaxResolveCards bounds the cards of a resolve scope by ID. Each may be
	// guard-only, and each may have MaxDependsOn dependencies outside the scope
	// that are guarded too: 113 cards and 8 dependencies each need at most
	// 113 * 9 = 1,017 guard-only entries, within the table's 1,024. A row or
	// whole-table scope is counted by the manager after it reads.
	MaxResolveCards = 113
	// MaxScopeRows bounds the rows a row scope names.
	MaxScopeRows = 64
	// MaxDependsOn bounds the IDs one card's DEPENDS-ON names.
	MaxDependsOn = 8
	// MaxOutsideDependencies bounds the distinct card IDs one admission array
	// depends on that are not in the array: each is one guard-only entry. With
	// MaxChangedEntries admissions of MaxDependsOn dependencies each, the array
	// names at most 127 * 8 = 1,016 of them, so the bound holds by construction;
	// it is checked all the same.
	MaxOutsideDependencies = TableGuardOnlyEntries
	// MaxEvidenceRecordsPerCard bounds the records one evidence entry carries.
	MaxEvidenceRecordsPerCard = 16
	// MaxEvidenceRecords bounds the records of one evidence request: with 127
	// cards of up to 16 each the request could hold 2,032; 512 are allowed.
	MaxEvidenceRecords = 512
	// MaxPathGlobs bounds the globs one card's PATHS names.
	MaxPathGlobs = 8
	// MaxDepth bounds JSON nesting.
	MaxDepth = 8
)

// Byte bounds of the text a card and a request carry.
const (
	// MaxNameBytes bounds a table name and a row name.
	MaxNameBytes = 64
	// MaxIdentityBytes bounds an operation ID, an actor and an issuer.
	MaxIdentityBytes = 64
	// MaxRefBytes bounds an opaque reference: a source artifact identity and a
	// landing identity. A repository identity is bounded by MaxRepositoryBytes.
	MaxRefBytes = 128
	// MaxReasonBytes bounds a one-line reason.
	MaxReasonBytes = 256
	// MaxDetailBytes bounds the free text of a rejection or a named reason.
	MaxDetailBytes = 256
	// MaxNoteBytes bounds the one-line text of a notification: what the
	// notification changes about readiness.
	MaxNoteBytes = 160
	// MaxTitleBytes bounds a card's TITLE.
	MaxTitleBytes = 160
	// MaxDoorsBytes bounds a card's DOORS.
	MaxDoorsBytes = 200
	// MaxTestBytes bounds a card's TEST: `<package> <TestName>`, or `none <why>`.
	MaxTestBytes = 200
	// MaxGlobBytes bounds one PATHS glob.
	MaxGlobBytes = 120
	// MaxEntryBytes bounds a card's ENTRY.
	MaxEntryBytes = 128
	// MaxProseBytes bounds the prose values that stay in the file and appear in
	// the record only through the definition digest: DONE-WHEN and PROBES.
	MaxProseBytes = 2048
	// MaxCanonicalBytes bounds the canonical encoded request.
	MaxCanonicalBytes = TableManifestBytes
	// MaxInputBytes bounds the document Parse reads, before parsing: room for
	// indentation around a request whose canonical form fits MaxCanonicalBytes.
	MaxInputBytes = 4 << 20
	// MaxCounterDigits bounds a decimal counter (epoch, table revision, card
	// revision): the digits of the largest uint64.
	MaxCounterDigits = 20
	// MaxReceiptCards bounds each card list of a receipt.
	MaxReceiptCards = TableChangedEntries + TableGuardOnlyEntries
	// MaxOperationIDBytes bounds an operation ID, given or derived.
	MaxOperationIDBytes = 64
	// MaxAdmissionRecordBytes bounds one canonical admission record: the record a
	// card's admission stores. See the arithmetic below.
	MaxAdmissionRecordBytes = 6 << 10
)

// The table-fit arithmetic. A card request becomes one table manifest of at
// most TableManifestBytes, and the manager's own additions to each entry (the
// timestamps, counters, standing and logs it writes beside the request's data)
// are bounded per entry by the manager design. The largest encoded size of each
// entry, in bytes (the design's KiB figures, rounded up), is:
const (
	// ManifestEnvelopeBytes is the manifest outside its entries: table, epoch,
	// expected revision, operation ID, actor and the punctuation.
	ManifestEnvelopeBytes = 1024
	// ManifestOperationRecordBytes is the card operation record entry: the
	// receipt in four chunks of 60 KiB, and its fields (241 KiB).
	ManifestOperationRecordBytes = 246784
	// ManifestAdmitBytes is one admission entry, a create (2.2 KiB).
	ManifestAdmitBytes = 2253
	// ManifestPairBytes is one replacement pair, two entries (2.7 KiB).
	ManifestPairBytes = 2765
	// ManifestResolveBytes is one resolve move (0.3 KiB).
	ManifestResolveBytes = 308
	// ManifestInputBytes is the largest lifecycle input entry, a result, head,
	// rework or queue rejection with its standing, log and unsets (4.4 KiB).
	ManifestInputBytes = 4506
	// ManifestEvidenceEntryBytes is one evidence entry outside its records, with
	// a forced return (3.7 KiB).
	ManifestEvidenceEntryBytes = 3789
	// ManifestEvidenceRecordBytes is one evidence record: the field name, the
	// value of at most 519 bytes and its guard (0.6 KiB).
	ManifestEvidenceRecordBytes = 615
	// ManifestGuardBytes is one guard-only entry (0.25 KiB).
	ManifestGuardBytes = 256
)

// The worst-case manifests, one per operation, and their sums (each plus
// ManifestEnvelopeBytes and the operation record):
//
//	admit      127 * 2,253                            = 286,131 ->   532,915 + 1,024
//	replace     63 * 2,765                            = 174,195 ->   420,979 + 1,024
//	inputs     127 * 4,506                            = 572,262 ->   819,046 + 1,024
//	evidence   127 * 3,789 + 512 * 615 = 481,203 + 314,880 = 796,083 -> 1,042,867 + 1,024
//	resolve    113 * 308 + 1,017 * 256 = 34,804 + 260,352 = 295,156 ->  541,940 + 1,024
//
// The evidence request is the largest: 1,043,891 bytes against 1,048,576, a
// margin of 4,685 bytes. Every sum is asserted by the tests of the request
// package, which also build the worst-case request of each operation and check
// its entry counts against the table's 128 and 1,024 and its canonical size
// against MaxCanonicalBytes.
const (
	ManifestAdmitMax    = ManifestEnvelopeBytes + MaxChangedEntries*ManifestAdmitBytes + ManifestOperationRecordBytes
	ManifestReplaceMax  = ManifestEnvelopeBytes + MaxReplacementPairs*ManifestPairBytes + ManifestOperationRecordBytes
	ManifestInputsMax   = ManifestEnvelopeBytes + MaxChangedEntries*ManifestInputBytes + ManifestOperationRecordBytes
	ManifestEvidenceMax = ManifestEnvelopeBytes + MaxChangedEntries*ManifestEvidenceEntryBytes + MaxEvidenceRecords*ManifestEvidenceRecordBytes + ManifestOperationRecordBytes
	ManifestResolveMax  = ManifestEnvelopeBytes + MaxResolveCards*ManifestResolveBytes + MaxResolveCards*(1+MaxDependsOn)*ManifestGuardBytes + ManifestOperationRecordBytes
)
