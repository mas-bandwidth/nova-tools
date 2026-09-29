package batchmodel

import (
	"fmt"
	"reflect"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// CheckImageDelta rejects any changed key outside the action's exact physical
// write set. The values are lossless Redis DUMP bytes, so even foreign binary
// data and stream metadata are compared without JSON normalization.
func CheckImageDelta(before, after map[string][]byte, allowed map[string]bool) error {
	if before == nil || after == nil {
		return fmt.Errorf("missing complete store image")
	}
	for key, old := range before {
		value, ok := after[key]
		if !ok || !SameBytes(old, value) {
			if !allowed[key] {
				return fmt.Errorf("unrelated key %q changed", key)
			}
		}
	}
	for key := range after {
		if _, existed := before[key]; !existed && !allowed[key] {
			return fmt.Errorf("unrelated key %q appeared", key)
		}
	}
	return nil
}

func checkStreamPrefix(before, after Snapshot, appends int) error {
	if len(after.Events) != len(before.Events)+appends {
		return fmt.Errorf("prior change-stream event prefix changed")
	}
	for i, event := range before.Events {
		if !reflect.DeepEqual(after.Events[i], event) {
			return fmt.Errorf("prior change-stream event prefix changed")
		}
	}
	return nil
}

func sameHashExcept(before, after map[string]string, except ...string) bool {
	if before == nil || after == nil {
		return false
	}
	copyHash := func(h map[string]string) map[string]string {
		out := make(map[string]string, len(h))
		for k, v := range h {
			out[k] = v
		}
		for _, key := range except {
			delete(out, key)
		}
		return out
	}
	return reflect.DeepEqual(copyHash(before), copyHash(after))
}

func checkAcceptedMetadata(s Step) error {
	if !sameHashExcept(s.Before.EpochFields, s.After.EpochFields) {
		return fmt.Errorf("accepted batch changed active-epoch metadata")
	}
	if !sameHashExcept(s.Before.RevisionFields, s.After.RevisionFields, "n") {
		return fmt.Errorf("accepted batch changed unrelated revision metadata")
	}
	for _, epoch := range []string{"1", "2"} {
		if epoch == s.Request.Epoch {
			if !sameHashExcept(s.Before.Definitions[epoch], s.After.Definitions[epoch], "_revision", "_present") {
				return fmt.Errorf("accepted batch changed unrelated definition metadata in epoch %s", epoch)
			}
		} else if !sameHashExcept(s.Before.Definitions[epoch], s.After.Definitions[epoch]) {
			return fmt.Errorf("accepted batch changed old-epoch definition metadata")
		}
	}
	return nil
}

func checkNonBatchMetadata(s NonBatchStep) error {
	b, a := s.Before, s.After
	switch s.Kind {
	case AdvanceEpoch:
		if !sameHashExcept(b.EpochFields, a.EpochFields, "n") || !sameHashExcept(b.RevisionFields, a.RevisionFields) || !sameHashExcept(b.Definitions["1"], a.Definitions["1"]) || !sameHashExcept(b.Definitions["2"], a.Definitions["2"]) {
			return fmt.Errorf("epoch advance changed unrelated metadata")
		}
	case RefreshEpoch:
		if !sameHashExcept(b.EpochFields, a.EpochFields) || !sameHashExcept(b.RevisionFields, a.RevisionFields) || !sameHashExcept(b.Definitions["1"], a.Definitions["1"]) || !sameHashExcept(b.Definitions["2"], a.Definitions["2"]) {
			return fmt.Errorf("epoch refresh changed metadata")
		}
	case BindSecondEpoch:
		if !sameHashExcept(b.EpochFields, a.EpochFields) || !sameHashExcept(b.RevisionFields, a.RevisionFields, "n") || !sameHashExcept(b.Definitions["1"], a.Definitions["1"]) || len(b.Definitions["2"]) != 0 {
			return fmt.Errorf("second bind changed unrelated metadata")
		}
		if err := checkFiniteDefinition(a.Definitions["2"]); err != nil {
			return fmt.Errorf("second bind shape: %w", err)
		}
		for _, row := range []string{"r1", "r2"} {
			if len(a.RowFields["2"][row]) != 0 {
				return fmt.Errorf("second bind row %s has unmodelled metadata", row)
			}
		}
		if err := checkFiniteRows(a, "2"); err != nil {
			return err
		}
	case OrdinaryRemoveMember:
		if !sameHashExcept(b.EpochFields, a.EpochFields) || !sameHashExcept(b.RevisionFields, a.RevisionFields, "n") || !sameHashExcept(b.Definitions["1"], a.Definitions["1"], "_revision", "_present") || !sameHashExcept(b.Definitions["2"], a.Definitions["2"]) {
			return fmt.Errorf("ordinary remove changed unrelated metadata")
		}
	default:
		return fmt.Errorf("unknown non-batch metadata action %q", s.Kind)
	}
	if s.Kind == AdvanceEpoch || s.Kind == RefreshEpoch {
		if err := checkStreamPrefix(b, a, 0); err != nil {
			return err
		}
		if a.StreamInfo != b.StreamInfo {
			return fmt.Errorf("non-stream action changed hidden stream metadata")
		}
	} else {
		if err := checkStreamPrefix(b, a, 1); err != nil {
			return err
		}
		if a.StreamInfo.EntriesAdded != b.StreamInfo.EntriesAdded+1 || a.StreamInfo.MaxDeletedID != b.StreamInfo.MaxDeletedID || a.LastEvent == nil || a.StreamInfo.LastGeneratedID != a.LastEvent.ID {
			return fmt.Errorf("ordinary action changed hidden stream metadata")
		}
	}
	return nil
}

func acceptedWriteKeys(s Step) (map[string]bool, error) {
	epoch, err := decimal(s.Request.Epoch)
	if err != nil {
		return nil, err
	}
	table := s.Request.Table
	prefix := ntable.EpochPrefix(table, epoch)
	allowed := map[string]bool{
		ntable.RevisionKey(table):               true,
		ntable.ChangesKey(table):                true,
		prefix + ":definition":                  true,
		prefix + ":op:" + s.Request.OperationID: true,
	}
	for _, id := range s.Request.Selected {
		allowed[ntable.MemberKey(id)] = true
		for _, m := range []Member{s.Before.Members[id], s.After.Members[id]} {
			if m.Place == nil {
				continue
			}
			if m.Epoch != s.Request.Epoch {
				return nil, fmt.Errorf("selected member %s changes across epochs", id)
			}
			allowed[ntable.CellKeyAt(table, m.Place.Row, m.Place.Column, epoch)] = true
		}
	}
	return allowed, nil
}

func nonBatchWriteKeys(s NonBatchStep) (map[string]bool, error) {
	allowed := map[string]bool{}
	switch s.Kind {
	case AdvanceEpoch:
		allowed["replay:epoch"] = true
	case RefreshEpoch:
		// An ordinary read has no physical write set.
	case BindSecondEpoch:
		allowed[ntable.RevisionKey("t1")] = true
		allowed[ntable.ChangesKey("t1")] = true
		prefix := ntable.EpochPrefix("t1", 2)
		allowed[prefix+":definition"] = true
		allowed[ntable.RowsKeyAt("t1", 2)] = true
		for _, row := range []string{"r1", "r2"} {
			allowed[ntable.RowKeyAt("t1", row, 2)] = true
		}
	case OrdinaryRemoveMember:
		allowed[ntable.RevisionKey("t1")] = true
		allowed[ntable.ChangesKey("t1")] = true
		allowed[ntable.EpochPrefix("t1", 1)+":definition"] = true
		allowed[ntable.MemberKey("m1")] = true
		allowed[ntable.CellKeyAt("t1", "r1", "c1", 1)] = true
	default:
		return nil, fmt.Errorf("unsupported non-batch action %q", s.Kind)
	}
	return allowed, nil
}
