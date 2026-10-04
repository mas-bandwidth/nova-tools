// Package recovery plans bounded failed-card rework without changing sprint state.
package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Limits caps one retry, independently of the routing tier (SPEC-SPRINT-RECOVERY).
type Limits struct {
	Tokens          uint64 `json:"tokens"`
	DeadlineSeconds uint64 `json:"deadline_seconds"`
	MaxCostMicros   uint64 `json:"max_cost_micros"`
}

// Attempt is an exact observed failed attempt. ProviderClass comes from the existing
// swarm provider adapter; capability evidence is an explicit independent disposition.
// Artifacts are observations, never guessed job paths or inferred pushed commits.
type Attempt struct {
	ID                     string   `json:"id"`
	Stream                 string   `json:"stream"`
	WorkID                 string   `json:"work_id"`
	Revision               uint64   `json:"revision"`
	Generation             uint64   `json:"generation"`
	Number                 uint64   `json:"attempt"`
	State                  string   `json:"state"`
	Failed                 bool     `json:"failed"`
	Head                   string   `json:"head"`
	Tier                   string   `json:"tier"`
	PinnedModel            string   `json:"pinned_model,omitempty"`
	Failure                string   `json:"failure"`
	ProviderClass          string   `json:"provider_class,omitempty"`
	ProviderOutcomeUnknown bool     `json:"provider_outcome_unknown"`
	SoundBrief             bool     `json:"sound_brief"`
	TargetedFlashRetry     bool     `json:"targeted_flash_retry"`
	CapabilityFinding      string   `json:"capability_finding,omitempty"`
	Fix                    string   `json:"fix,omitempty"`
	ArtifactsInspected     bool     `json:"artifacts_inspected"`
	Artifacts              []string `json:"artifacts"`
	Diff                   string   `json:"diff"`
}

// Input is one exact cohort snapshot; sorted IDs make its identity independent of
// table delivery order. Epoch and every observed attempt field fence rework.
type Input struct {
	Epoch    uint64    `json:"epoch"`
	Cohort   string    `json:"cohort"`
	Limits   Limits    `json:"limits"`
	Attempts []Attempt `json:"attempts"`
}

type Action struct {
	Attempt      Attempt `json:"current"`
	Class        string  `json:"class"`
	ProposedTier string  `json:"proposed_tier"`
	Safe         bool    `json:"safe"`
	Reason       string  `json:"reason"`
}

type Plan struct {
	ID      string   `json:"id"`
	Input   Input    `json:"snapshot"`
	Actions []Action `json:"actions"`
}

var mechanicalSHA = regexp.MustCompile(`^step [0-9]+ not-done: the step line's commit [0-9a-fA-F]+ is no sha \(40 or 12 hex, or -\):`)

// Build implements Plan in SPEC-SPRINT-RECOVERY. No free-form semantic failure is
// an automatic capability decision; unknown provider outcomes never retry.
func Build(in Input) (Plan, error) {
	var p Plan
	if in.Cohort == "" || len(in.Attempts) == 0 || len(in.Attempts) > 2000 {
		return p, errors.New("recovery wants a named exact cohort of 1..2000 attempts; select the failed card IDs")
	}
	if in.Limits.Tokens == 0 || in.Limits.DeadlineSeconds == 0 || in.Limits.MaxCostMicros == 0 {
		return p, errors.New("recovery needs explicit positive token, deadline and cost caps; set retry limits")
	}
	in.Attempts = slices.Clone(in.Attempts)
	slices.SortFunc(in.Attempts, func(a, b Attempt) int { return strings.Compare(a.ID, b.ID) })
	for i, a := range in.Attempts {
		if a.ID == "" || a.WorkID == "" || a.Stream == "" || a.Revision == 0 || a.Generation == 0 || a.Number == 0 || (i > 0 && a.ID == in.Attempts[i-1].ID) {
			return p, fmt.Errorf("invalid or duplicate failed-attempt identity %q; refresh the exact cohort snapshot", a.ID)
		}
		size := len(a.ID) + len(a.Stream) + len(a.WorkID) + len(a.Head) + len(a.Tier) + len(a.PinnedModel) + len(a.Failure) + len(a.ProviderClass) + len(a.CapabilityFinding) + len(a.Fix) + len(a.Diff)
		for _, path := range a.Artifacts {
			size += len(path)
		}
		if size > 16384 || len(a.Artifacts) > 256 {
			return p, fmt.Errorf("recovery observations for %s exceed 16 KiB or 256 artifacts; keep logs in provider artifacts and project bounded evidence", a.ID)
		}
		// Own artifact slices: a provider caller cannot mutate a saved plan through aliases.
		in.Attempts[i].Artifacts = slices.Clone(a.Artifacts)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return p, fmt.Errorf("encode recovery snapshot: %w", err)
	}
	sum := sha256.Sum256(raw)
	p = Plan{ID: hex.EncodeToString(sum[:]), Input: in, Actions: make([]Action, 0, len(in.Attempts))}
	for _, a := range in.Attempts {
		p.Actions = append(p.Actions, classify(a))
	}
	return p, nil
}

// classify implements the conservative classification table in the recovery spec.
func classify(a Attempt) Action {
	r := Action{Attempt: a, Class: "unknown", ProposedTier: a.Tier, Reason: "failure needs a concrete independent finding and bounded fix"}
	switch {
	case !a.Failed || a.State != sprint.Review:
		r.Reason = "only completed failed work in review is eligible; do not interrupt a live or accepted attempt"
	case a.ProviderOutcomeUnknown:
		r.Class = "infrastructure"
		r.Reason = "provider may have accepted work; reconcile its outcome before retry"
	case a.ProviderClass != "":
		r.Class = "infrastructure"
		r.Reason = "repair provider availability/configuration first; a failure is not capability evidence"
	case mechanicalSHA.MatchString(a.Failure):
		r.Class = "mechanical"
		r.Reason = "repair the literal full40 RESULT commit identity; preserve tier"
	case a.CapabilityFinding != "":
		r.Class = "capability"
		r.Reason = "sound brief plus targeted flash retry and independent capability finding required"
		if a.SoundBrief && a.TargetedFlashRetry && a.Tier == "flash" && a.PinnedModel == "" {
			r.ProposedTier = "pro"
			r.Reason = "bounded pro rework after the independently diagnosed targeted flash retry"
		}
	}
	eligible := r.Class == "mechanical" || (r.Class == "capability" && a.Tier == "flash" && r.ProposedTier == "pro" && a.SoundBrief && a.TargetedFlashRetry && a.PinnedModel == "")
	if eligible && a.SoundBrief && a.Fix != "" && a.ArtifactsInspected && len(a.Artifacts) == 0 && a.Diff == "" {
		r.Safe = true
	} else if eligible && (!a.ArtifactsInspected || len(a.Artifacts) > 0 || a.Diff != "") {
		r.Reason += "; checkpoint inspection/preservation is required and is not implemented by this tracer"
	}
	return r
}

// Receipt is the durable operation identity returned by the existing fenced store.
// It is supplied only after that store committed; preparing a request is not success.
type Receipt struct {
	PlanID string `json:"plan_id"`
}

type Prepared struct {
	AlreadyApplied bool
	Requests       []sprint.ReworkReq
	Limits         Limits
}

// Prepare validates the entire immutable plan against a fresh cohort before returning
// existing Rework requests. The store must atomically check revision/epoch and record
// this plan ID with its commit; this function performs no store write or git action.
func Prepare(p Plan, current Input, receipt Receipt, who string) (Prepared, error) {
	var out Prepared
	saved, err := Build(p.Input)
	if err != nil {
		return out, err
	}
	// Protect action edits as well as the snapshot identity.
	if !reflect.DeepEqual(saved, p) {
		return out, errors.New("recovery plan was altered; regenerate it from the current cohort")
	}
	if receipt.PlanID == p.ID {
		out.AlreadyApplied = true
		return out, nil
	}
	fresh, err := Build(current)
	if err != nil {
		return out, err
	}
	if fresh.ID != p.ID {
		return out, errors.New("stale recovery plan; refresh generation, revision, head and cohort before apply")
	}
	out.Limits = p.Input.Limits
	for _, a := range p.Actions {
		if !a.Safe {
			return Prepared{}, fmt.Errorf("recovery of %s is held: %s", a.Attempt.ID, a.Reason)
		}
		tier := ""
		if a.ProposedTier != a.Attempt.Tier {
			tier = a.ProposedTier
		}
		out.Requests = append(out.Requests, sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{a.Attempt.ID}}, Fix: a.Attempt.Fix, Tier: tier, Who: who})
	}
	return out, nil
}
