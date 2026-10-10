package sprint

import "context"

// LandCycleSync is the land round's dev sync (docs/SPEC-SPRINT.md, "Dev sync every
// cycle"): when one is due on the snapshot the round read (DevSyncDue), it merges the
// development branch into the base in the round's clone (RunDevSync), and due is true. The
// round then records the facts in one step whose plan is DevSynced(s, facts, req.Streams)
// on that step's own snapshot, before it lands any batch: a conflict stops every stream, so
// the batches after it are refused as any stopped stream's are. Not due: no git runs.
// The land command calls it only with --dev-sync, which is off by default.
func LandCycleSync(ctx context.Context, s *Snapshot, req DevSyncReq) (facts DevSyncFacts, due bool, err error) {
	if due, _ := DevSyncDue(s); !due {
		return DevSyncFacts{}, false, nil
	}
	facts, err = RunDevSync(ctx, req)
	return facts, true, err
}
