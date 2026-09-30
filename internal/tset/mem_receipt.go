package tset

import (
	"crypto/sha1"
	"encoding/hex"
	"strconv"
	"strings"
)

// memReceipt is the complete persisted receipt. In particular it does not
// retain the request, intent, observations, counters, or per-member deltas.
// Its namespace is the request epoch, including for an advance.
type memReceipt struct {
	IntentDigest string  `json:"intent_digest"`
	Status       string  `json:"status"`
	EpochBefore  Decimal `json:"epoch_before"`
	EpochAfter   Decimal `json:"epoch_after"`
	FirstSeq     Decimal `json:"first_seq"`
	LastSeq      Decimal `json:"last_seq"`
	Changed      int     `json:"changed"`
	Result       string  `json:"result"`
}

const maxReceiptBytes = 32 << 10

// encodeMemReceipt follows the char2escape table in Redis's bundled
// deps/lua/src/lua_cjson.c. CJSON escapes slash and DEL but leaves HTML
// characters and U+2028/U+2029 as their original UTF-8 bytes. Field order
// does not matter to the stored object or its byte length. Receipt admission,
// planned HSET bytes, and done-read fetched bytes all use this encoding.
func encodeMemReceipt(r memReceipt) []byte {
	b := make([]byte, 0, len(r.Result)+192)
	b = append(b, `{"intent_digest":`...)
	b = appendCJSONString(b, r.IntentDigest)
	b = append(b, `,"status":`...)
	b = appendCJSONString(b, r.Status)
	b = append(b, `,"epoch_before":`...)
	b = appendCJSONString(b, string(r.EpochBefore))
	b = append(b, `,"epoch_after":`...)
	b = appendCJSONString(b, string(r.EpochAfter))
	b = append(b, `,"first_seq":`...)
	b = appendCJSONString(b, string(r.FirstSeq))
	b = append(b, `,"last_seq":`...)
	b = appendCJSONString(b, string(r.LastSeq))
	b = append(b, `,"changed":`...)
	b = strconv.AppendInt(b, int64(r.Changed), 10)
	b = append(b, `,"result":`...)
	b = appendCJSONString(b, r.Result)
	return append(b, '}')
}

func appendCJSONString(b []byte, s string) []byte {
	const digits = "0123456789abcdef"
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\b':
			b = append(b, '\\', 'b')
		case '\t':
			b = append(b, '\\', 't')
		case '\n':
			b = append(b, '\\', 'n')
		case '\f':
			b = append(b, '\\', 'f')
		case '\r':
			b = append(b, '\\', 'r')
		case '"', '/', '\\':
			b = append(b, '\\', c)
		case 0x7f:
			b = append(b, '\\', 'u', '0', '0', '7', 'f')
		default:
			if c < 0x20 {
				b = append(b, '\\', 'u', '0', '0', digits[c>>4], digits[c&0xf])
			} else {
				b = append(b, c)
			}
		}
	}
	return append(b, '"')
}

func intentDigest(intent string) string {
	sum := sha1.Sum([]byte(intent))
	return hex.EncodeToString(sum[:])
}

func memReceiptForReply(step Step, reply Reply) memReceipt {
	return memReceipt{
		IntentDigest: intentDigest(*step.Intent),
		Status:       reply.Status,
		EpochBefore:  reply.EpochBefore,
		EpochAfter:   reply.EpochAfter,
		FirstSeq:     reply.FirstSeq,
		LastSeq:      reply.LastSeq,
		Changed:      reply.Changed,
		Result:       reply.Result,
	}
}

func (r memReceipt) replayReply() Reply {
	return Reply{
		Status:      r.Status,
		EpochBefore: r.EpochBefore,
		EpochAfter:  r.EpochAfter,
		FirstSeq:    r.FirstSeq,
		LastSeq:     r.LastSeq,
		Changed:     r.Changed,
		Result:      r.Result,
		Replay:      true,
	}
}

// checkReceipt runs after static request and namespace validation but before
// any current-epoch, member, row, or log guard. The original request epoch is
// looked up even when the active epoch has advanced twice or more.
func (m *Mem) checkReceipt(space *memSpace, step Step) (Reply, bool, error) {
	if step.Op == nil {
		return Reply{}, false, nil
	}
	byEpoch := space.receipts[step.Epoch]
	r, ok := byEpoch[*step.Op]
	if !ok {
		return Reply{}, false, nil
	}
	if step.Intent == nil || r.IntentDigest != intentDigest(*step.Intent) {
		return Reply{}, true, &Refusal{
			Status:  "refused",
			Code:    "OPCONFLICT",
			Detail:  RefusalDetail{IDs: []string{}, Cells: []string{}, Rows: []string{}},
			Message: "operation identity has a different intent; nothing was changed",
		}
	}
	return r.replayReply(), true, nil
}

// saveReceipt is called against the cloned space only after successful plan
// and before that clone is published. The 32 KiB bound is on the actual encoded
// object, matching the Redis hash value stored by the Lua implementation.
func (m *Mem) saveReceipt(space *memSpace, step Step, reply Reply) error {
	if step.Op == nil {
		return nil
	}
	if step.Intent == nil {
		return &Refusal{Status: "refused", Code: "REQUEST", Message: "operation has no intent; nothing was changed"}
	}
	r := memReceiptForReply(step, reply)
	encoded := encodeMemReceipt(r)
	if len(encoded) > maxReceiptBytes {
		return &Refusal{Status: "refused", Code: "LIMIT", Message: "receipt exceeds limit; nothing was changed"}
	}
	if space.receipts == nil {
		space.receipts = make(map[Decimal]map[string]memReceipt)
	}
	if space.receipts[step.Epoch] == nil {
		space.receipts[step.Epoch] = make(map[string]memReceipt)
	}
	space.receipts[step.Epoch][*step.Op] = r
	return nil
}

// doneLookup returns one slot for each submitted identity, preserving order and
// keeping a conflict local to its slot. No epoch scan occurs.
func doneLookup(space *memSpace, identities []DoneIdentity) []DoneSlot {
	slots := make([]DoneSlot, len(identities))
	for i, identity := range identities {
		r, ok := space.receipts[identity.Epoch][identity.Op]
		if !ok {
			slots[i] = DoneSlot{Status: "absent"}
			continue
		}
		if r.IntentDigest != identity.IntentDigest {
			slots[i] = DoneSlot{Status: "conflict", IntentDigest: r.IntentDigest}
			continue
		}
		compact := &DoneReceipt{Status: r.Status, EpochBefore: r.EpochBefore,
			EpochAfter: r.EpochAfter, FirstSeq: r.FirstSeq, LastSeq: r.LastSeq,
			Changed: r.Changed, Result: r.Result}
		slots[i] = DoneSlot{Status: "match", IntentDigest: r.IntentDigest, Receipt: compact}
	}
	return slots
}

// readDoneQuery accounts for every requested receipt namespace, including
// absent slots and original epochs different from the surrounding read epoch.
// The outer Read call makes this a single atomic snapshot and withholds all
// answers when any later slot exhausts the shared read budget.
func (m *Mem) readDoneQuery(space *memSpace, q ReadQuery, budget *readBudget) (ReadAnswer, error) {
	if len(q.Ops) == 0 || len(q.Ops) > 2000 {
		return ReadAnswer{}, memRefusal("REQUEST", RefusalDetail{})
	}
	seen := make(map[string]bool, len(q.Ops))
	for _, identity := range q.Ops {
		if !memValidDecimal(identity.Epoch) || !validID(identity.Op) ||
			len(identity.IntentDigest) != 40 || strings.Trim(identity.IntentDigest, "0123456789abcdef") != "" {
			return ReadAnswer{}, memRefusal("REQUEST", RefusalDetail{})
		}
		key := string(identity.Epoch) + "\x00" + identity.Op + "\x00" + identity.IntentDigest
		if seen[key] {
			return ReadAnswer{}, memRefusal("REQUEST", RefusalDetail{})
		}
		seen[key] = true
	}
	checkedEpochs := make(map[Decimal]bool)
	for _, identity := range q.Ops {
		if memCompareDecimal(identity.Epoch, space.active) > 0 {
			return ReadAnswer{}, memRefusal("EPOCHAHEAD", RefusalDetail{ActiveEpoch: space.active})
		}
		if space.epochs[identity.Epoch] == nil {
			return ReadAnswer{}, memRefusal("EPOCHGONE", RefusalDetail{ActiveEpoch: space.active})
		}
		if identity.Epoch != space.active && !checkedEpochs[identity.Epoch] {
			if err := budget.chargeProbe(2); err != nil {
				return ReadAnswer{}, err
			}
			if err := budget.chargeRaw(int64(len("tset/1") + len(identity.Epoch))); err != nil {
				return ReadAnswer{}, err
			}
			checkedEpochs[identity.Epoch] = true
		}
		if err := budget.chargeProbe(1); err != nil {
			return ReadAnswer{}, err
		}
		if receipt, ok := space.receipts[identity.Epoch][identity.Op]; ok {
			wire := encodeMemReceipt(receipt)
			if err := budget.chargeRaw(int64(len(wire))); err != nil {
				return ReadAnswer{}, err
			}
		}
	}
	return ReadAnswer{Kind: "done", Done: doneLookup(space, q.Ops)}, nil
}
