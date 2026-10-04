package recovery

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Evidence contains explicit coordinator/provider observations absent from a sprint
// table. Zero values hold preparation; this adapter does not discover artifacts.
type Evidence struct {
	SoundBrief             bool
	TargetedFlashRetry     bool
	CapabilityFinding      string
	Fix                    string
	ArtifactsInspected     bool
	Artifacts              []string
	Diff                   string
	ProviderOutcomeUnknown bool
}

// Observe projects only the requested cohort from an existing loaded snapshot.
// SPEC-SPRINT-RECOVERY: never broaden selection to a stream or infer a failed
// checkpoint from a report. Libraries considered: existing sprint/cardhdr adapters
// plus strings for the existing provider cause record, no provider subprocess.
func Observe(s *sprint.Snapshot, cohort string, ids []string, limits Limits, evidence map[string]Evidence) (Plan, error) {
	if s == nil || s.Work == nil || s.Fleet == nil {
		return Plan{}, fmt.Errorf("recovery needs loaded work and fleet tables; load an exact cohort snapshot")
	}
	in := Input{Epoch: s.Epoch, Cohort: cohort, Limits: limits}
	for _, id := range ids {
		pr := s.Work.Card(id)
		if pr == nil {
			return Plan{}, fmt.Errorf("recovery card %s is absent; refresh the exact cohort", id)
		}
		wc := s.Fleet.Card(pr.F("work"))
		if wc == nil || wc.F(sprint.PrimaryField) != id || wc.Int("attempt") != pr.Int("attempt") {
			return Plan{}, fmt.Errorf("recovery card %s has no matching current work attempt; refresh the exact cohort", id)
		}
		if wc.Int("gen") <= 0 || pr.Int("attempt") <= 0 {
			return Plan{}, fmt.Errorf("recovery card %s has an invalid generation/attempt; refresh its exact identity", id)
		}
		model, why := cardhdr.ReadModel(pr.F("brief"))
		if why != "" {
			return Plan{}, fmt.Errorf("recovery card %s model header: %s", id, why)
		}
		tier, _ := sprint.CardTiers(pr)
		e := evidence[id]
		a := Attempt{ID: id, Stream: pr.Row, WorkID: wc.ID, Revision: pr.Rev, Generation: uint64(wc.Int("gen")), Number: uint64(pr.Int("attempt")), State: pr.Col, Failed: pr.F("result") == "failed", Head: wc.F("head"), Tier: tier, PinnedModel: model.Pin, Failure: pr.F("failure"), SoundBrief: e.SoundBrief, TargetedFlashRetry: e.TargetedFlashRetry, CapabilityFinding: e.CapabilityFinding, Fix: e.Fix, ArtifactsInspected: e.ArtifactsInspected, Artifacts: e.Artifacts, Diff: e.Diff, ProviderOutcomeUnknown: e.ProviderOutcomeUnknown}
		for _, field := range strings.Fields(wc.F(sprint.FieldProviderError)) {
			if value, ok := strings.CutPrefix(field, "class="); ok {
				a.ProviderClass = value
				break
			}
		}
		in.Attempts = append(in.Attempts, a)
	}
	return Build(in)
}
