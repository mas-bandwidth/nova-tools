package tokens

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/records"
)

// The retained-token decoder for Codex Desktop cumulative `token_count` snapshots: two
// top-level namespace groups (info.total_token_usage and info.last_token_usage) preserved
// raw and never added, subtracted, or repriced as provider spend.
//
// WHOSE DECISIONS THESE ARE. Every cell this file applies was proposed by the source
// owner in docs/PROPOSAL-TOKENS-CODEX-SNAPSHOTS.md for issue #154, and the executable form
// is the two sealed manifests at testdata/tokens/codex-snapshots/mapping-total.json and
// mapping-last.json. This decoder READS those manifests, exactly as the response decoder
// reads its own, so a decision lives in one place. The four wire literals the two manifests
// do not carry rules for (repository unattributed, origin owner_binding/unknown) are the
// same literals the response mapping fixed and are named here for the same reason.
//
// WHAT IT NEVER DOES. It opens no real Codex session store; the caller names the files.
// A snapshot event keeps its two groups raw: `zero_semantics` is `unknown`, `spend_role` is
// `non_spend`, and neither group enters normalized spend. It never adds successive
// snapshots, subtracts them, fills a missing response's spend from them, or applies the
// response mapping's input+output=total arithmetic. `model_context_window` is capacity
// metadata, extracted from the sibling `info` member and never counted.

const (
	// The two group namespaces the proposal fixes, the suffix of the two manifests'
	// identity_rule.namespace. The snapshot decoder reads its two mappings by these.
	codexSnapshotGroupTotal = "total"
	codexSnapshotGroupLast  = "last"

	// The fixed shape vocabulary this decoder counts but does not map.
	codexSnapshotShapeMeta      = "session_meta"
	codexSnapshotShapeEvent     = "event_msg"
	codexSnapshotShapeTokenCnt  = "token_count"
	codexSnapshotShapeNoInfo    = "token_count_without_info"
	codexSnapshotShapeNoOrdinal = "token_count_without_ordinal"
	codexSnapshotShapeOther     = "other_snapshot_shape"
)

// CodexSnapshotBinding is the trusted source identity/binding for one snapshot segment:
// the physical original rollout UUID and the owner-supplied origin. The original rollout
// UUID MUST come from a trusted source identity/binding and is preserved through copying;
// the current path and logical thread ID cannot substitute for it. The origin is the same
// CodexBinding the response decoder uses: friend, bench and binding ID supplied together or
// not at all.
type CodexSnapshotBinding struct {
	OriginalRolloutID string
	Origin            CodexBinding
}

// CodexSnapshotDecoding is what one snapshot decode run produced.
type CodexSnapshotDecoding struct {
	// Observations are the sealed records, in source order across the two group namespaces.
	Observations []CodexObservation
	// Refusals are the records the boundary refused, named by rule and field.
	Refusals []CodexRefusal
	// Unsupported counts the shapes these mappings do not cover, by a fixed vocabulary.
	Unsupported map[string]int
}

// ReadCodexSnapshotMapping reads one of the two proposed snapshot manifests, checking that
// its identity is the snapshot's own: event key [original_rollout_id, ordinal_as_string],
// receipt [ordinal], and no normalized spend.
func ReadCodexSnapshotMapping(path string) (*CodexMapping, error) {
	m, err := readCodexManifest(path)
	if err != nil {
		return nil, err
	}
	if len(m.EventKey) != 2 || m.EventKey[0] != "original_rollout_id" || m.EventKey[1] != "ordinal_as_string" {
		return nil, errors.New("the snapshot mapping manifest's event key is not [original_rollout_id, ordinal_as_string]")
	}
	if strings.Join(m.ReceiptFields, ",") != "ordinal" {
		return nil, errors.New("the snapshot mapping manifest's receipt allowlist is not [ordinal]")
	}
	if m.NormalizedSpendSupported || m.ObservationKind != "snapshot" {
		return nil, errors.New("the snapshot mapping manifest is not raw-only")
	}
	for _, r := range m.Fields {
		if r.SpendRole != "non_spend" {
			return nil, errors.New("the snapshot mapping declares a spend role")
		}
	}
	return m, nil
}

// codexSnapshotLine is the paginated rollout line. Identity members stay raw so a native
// ordinal keeps its exact decimal lexeme.
type codexSnapshotLine struct {
	Ordinal   json.RawMessage `json:"ordinal"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp *string         `json:"timestamp"`
	Type      string          `json:"type"`
}

// codexSnapshotMeta is the session_meta payload: the physical segment id (session
// provenance) and the ancestor reference.
type codexSnapshotMeta struct {
	HistoryBase *struct {
		ThreadID string `json:"thread_id"`
	} `json:"history_base"`
	ID string `json:"id"`
}

// codexSnapshotInfo is the event_msg payload.info: the two cumulative groups and the
// sibling capacity metadata.
type codexSnapshotInfo struct {
	LastTokenUsage     map[string]json.RawMessage `json:"last_token_usage"`
	TotalTokenUsage    map[string]json.RawMessage `json:"total_token_usage"`
	ModelContextWindow json.RawMessage            `json:"model_context_window"`
}

// codexSnapshotEvent is the event_msg payload.
type codexSnapshotEvent struct {
	Info *codexSnapshotInfo `json:"info"`
	Type string             `json:"type"`
}

// DecodeCodexSnapshotRollout decodes the named snapshot files, one binding each, into the
// two group namespaces. bindings[i] names paths[i].
func DecodeCodexSnapshotRollout(mTotal, mLast *CodexMapping, bindings []CodexSnapshotBinding, paths []string) (*CodexSnapshotDecoding, error) {
	readers := make([]io.Reader, 0, len(paths))
	closers := make([]io.Closer, 0, len(paths))
	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}()
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		closers = append(closers, f)
		readers = append(readers, f)
	}
	return DecodeCodexSnapshotReaders(mTotal, mLast, bindings, readers)
}

// DecodeCodexSnapshotReaders decodes snapshot lines from readers, in order, one binding per
// reader.
func DecodeCodexSnapshotReaders(mTotal, mLast *CodexMapping, bindings []CodexSnapshotBinding, readers []io.Reader) (*CodexSnapshotDecoding, error) {
	if mTotal == nil || mLast == nil {
		return nil, errors.New("a snapshot decode needs both sealed snapshot manifests")
	}
	d := &CodexSnapshotDecoding{Unsupported: map[string]int{}}
	vTotal := records.NewValidator(mTotal.Allowlists())
	vLast := records.NewValidator(mLast.Allowlists())
	for at, r := range readers {
		var b CodexSnapshotBinding
		if at < len(bindings) {
			b = bindings[at]
		}
		if b.OriginalRolloutID == "" {
			return nil, errors.New("a snapshot decode needs a trusted original rollout ID per source")
		}
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		sessionID := ""
		for n := 1; sc.Scan(); n++ {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			if err := d.readSnapshotLine(mTotal, mLast, vTotal, vLast, b, &sessionID, line); err != nil {
				return nil, errors.New("source " + strconv.Itoa(at) + " line " + strconv.Itoa(n) + ": " + err.Error())
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (d *CodexSnapshotDecoding) readSnapshotLine(mTotal, mLast *CodexMapping, vTotal, vLast *records.Validator, b CodexSnapshotBinding, sessionID *string, line []byte) error {
	var rec codexSnapshotLine
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if err := dec.Decode(&rec); err != nil {
		return errors.New("the snapshot line is not a JSON object of the shape these mappings read")
	}
	if dec.More() {
		return errors.New("the snapshot line carries more than one JSON value")
	}
	switch rec.Type {
	case codexSnapshotShapeMeta:
		var meta codexSnapshotMeta
		if err := json.Unmarshal(rec.Payload, &meta); err != nil || meta.ID == "" {
			return errors.New("a session_meta line carries no physical segment id")
		}
		*sessionID = meta.ID
		return nil
	case codexSnapshotShapeEvent:
		var ev codexSnapshotEvent
		if err := json.Unmarshal(rec.Payload, &ev); err != nil {
			return errors.New("an event_msg line is not the payload shape these mappings read")
		}
		if ev.Type != codexSnapshotShapeTokenCnt {
			d.Unsupported[codexSnapshotShapeOther]++
			return nil
		}
		ordinal, ok := codexIdentity(rec.Ordinal)
		if !ok {
			d.Unsupported[codexSnapshotShapeNoOrdinal]++
			return nil
		}
		if ev.Info == nil {
			d.Unsupported[codexSnapshotShapeNoInfo]++
			return nil
		}
		for _, g := range []codexSnapshotGroup{mTotal, mLast} {
			if err := d.sealSnapshot(mTotal, mLast, g, vTotal, vLast, b, *sessionID, ordinal, ev.Info, rec.Timestamp); err != nil {
				return err
			}
		}
		return nil
	default:
		d.Unsupported[codexSnapshotShapeOther]++
		return nil
	}
}

// codexSnapshotGroup is one of the two mappings, tagged with which sibling group it maps.
type codexSnapshotGroup = *CodexMapping

func (d *CodexSnapshotDecoding) sealSnapshot(mTotal, mLast *CodexMapping, g *CodexMapping, vTotal, vLast *records.Validator, b CodexSnapshotBinding, sessionID, ordinal string, info *codexSnapshotInfo, timestamp *string) error {
	groupUsage := info.TotalTokenUsage
	v := vTotal
	if g == mLast {
		groupUsage = info.LastTokenUsage
		v = vLast
	}
	merged := make(map[string]json.RawMessage, len(groupUsage)+1)
	for k, raw := range groupUsage {
		merged[k] = raw
	}
	merged["model_context_window"] = info.ModelContextWindow

	obs := records.Observation{
		Schema: records.SchemaObservation,
		Source: records.Source{
			Kind:            g.SourceKind,
			ProducerVersion: nil,
			Namespace:       g.Namespace,
			SessionID:       sessionID,
			EventKey:        []string{b.OriginalRolloutID, ordinal},
		},
		Kind:       g.ObservationKind,
		Revision:   records.Revision{Basis: g.RevisionBasis},
		Repository: records.Repository{Basis: codexRepositoryBasis},
		MappingID:  g.ID,
		Receipt:    map[string]string{"ordinal": ordinal},
	}
	if timestamp != nil {
		obs.Time = records.Times{OccurredAt: timestamp, Basis: g.TimeBasis}
	} else {
		obs.Time = records.Times{Basis: g.TimeBasisMissing}
	}
	obs.Model = records.Model{Basis: g.ModelBasisAbsent}
	if b.Origin.Supplied() {
		friend, bench, id := b.Origin.Friend, b.Origin.Bench, b.Origin.ID
		obs.Origin = records.Origin{Friend: &friend, Bench: &bench, Basis: codexOriginBound, BindingID: &id}
	} else {
		obs.Origin = records.Origin{Basis: codexOriginUnknown}
	}
	obs.RawUsage = codexRawUsage(g, merged)
	obs.ModelUsage = nil

	raw, id, err := v.SealObservation(obs)
	if err != nil {
		ref := CodexRefusal{ResponseID: ordinal}
		var r *records.Refusal
		if errors.As(err, &r) {
			ref.Rule, ref.Field = r.Rule, r.Field
		}
		d.Refusals = append(d.Refusals, ref)
		return nil
	}
	d.Observations = append(d.Observations, CodexObservation{
		ID:       id,
		Envelope: raw,
		SpendKey: g.Namespace + "\x00" + b.OriginalRolloutID + "\x00" + ordinal,
		Day:      CodexShardDay(obs.Time.OccurredAt),
	})
	return nil
}
