package batchmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

// NonBatchKind is a fixed model action, never caller-supplied TLA source.
// Each action is paired with independently captured Redis observations.
type NonBatchKind string

const (
	AdvanceEpoch         NonBatchKind = "BatchAdvance"
	RefreshEpoch         NonBatchKind = "SecondRefresh"
	BindSecondEpoch      NonBatchKind = "SecondBind"
	OrdinaryRemoveMember NonBatchKind = "OrdinaryRemove"
)

type NonBatchStep struct {
	Index                   uint64
	Kind                    NonBatchKind
	Before, After           Snapshot
	BeforeModel, AfterModel ModelState
}

type MixedStep struct {
	Batch    *Step
	NonBatch *NonBatchStep
}

// RenderMixedHarness ties every physical observation to an existing action in
// BatchMemberTable. The model, rather than a Go transition oracle, decides
// whether the four-step epoch history is possible.
func RenderMixedHarness(steps []MixedStep) (string, error) {
	if len(steps) == 0 {
		return "", errors.New("empty mixed trace")
	}
	observed := make([]string, 0, len(steps)+1)
	cases := make([]string, 0, len(steps)+1)
	var previousImage map[string][]byte
	var previousModel ModelState
	for i, item := range steps {
		if (item.Batch == nil) == (item.NonBatch == nil) {
			return "", fmt.Errorf("step %d has zero or two actions", i)
		}
		var before, after Snapshot
		var beforeModel, afterModel ModelState
		var action string
		if item.Batch != nil {
			s := item.Batch
			if s.Index != uint64(i) {
				return "", fmt.Errorf("step %d batch index differs", i)
			}
			if err := ValidateStep(*s); err != nil {
				return "", fmt.Errorf("step %d: %w", i, err)
			}
			if err := validateStepActionIdentity(*s); err != nil {
				return "", fmt.Errorf("step %d: %w", i, err)
			}
			var err error
			action, err = s.Action.TLA()
			if err != nil {
				return "", err
			}
			before, after, beforeModel, afterModel = s.Before, s.After, s.BeforeModel, s.AfterModel
		} else {
			s := item.NonBatch
			if s.Index != uint64(i) {
				return "", fmt.Errorf("step %d non-batch index differs", i)
			}
			switch s.Kind {
			case AdvanceEpoch, RefreshEpoch, BindSecondEpoch, OrdinaryRemoveMember:
				action = string(s.Kind)
			default:
				return "", fmt.Errorf("unsupported model action %q", s.Kind)
			}
			before, after, beforeModel, afterModel = s.Before, s.After, s.BeforeModel, s.AfterModel
			if before.Image == nil || after.Image == nil {
				return "", fmt.Errorf("step %d lacks independent complete image", i)
			}
			allowed, err := nonBatchWriteKeys(*s)
			if err != nil {
				return "", err
			}
			if err := CheckImageDelta(before.Image, after.Image, allowed); err != nil {
				return "", fmt.Errorf("step %d complete image: %w", i, err)
			}
			if err := checkNonBatchMetadata(*s); err != nil {
				return "", fmt.Errorf("step %d: %w", i, err)
			}
			if s.Kind == RefreshEpoch && !SameImage(before.Image, after.Image) {
				return "", fmt.Errorf("epoch read changed complete store")
			}
			if s.Kind == AdvanceEpoch && (before.Epoch != "1" || after.Epoch != "2") {
				return "", fmt.Errorf("epoch advance observation differs")
			}
			if s.Kind == BindSecondEpoch && (before.Epoch != "2" || after.Epoch != "2") {
				return "", fmt.Errorf("second bind observation has wrong epoch")
			}
		}
		if i > 0 && (!SameImage(previousImage, before.Image) || !reflect.DeepEqual(previousModel, beforeModel)) {
			return "", fmt.Errorf("step %d is not continuous", i)
		}
		pre, err := beforeModel.TLA()
		if err != nil {
			return "", fmt.Errorf("step %d prestate: %w", i, err)
		}
		post, err := afterModel.TLA()
		if err != nil {
			return "", fmt.Errorf("step %d poststate: %w", i, err)
		}
		if i == 0 {
			observed = append(observed, pre)
		}
		observed = append(observed, post)
		prefix := "  [] "
		if i == 0 {
			prefix = "CASE "
		}
		cases = append(cases, prefix+"step="+strconv.Itoa(i)+" -> "+action)
		previousImage, previousModel = after.Image, afterModel
	}
	cases = append(cases, "  [] OTHER -> UNCHANGED bvars")
	return "---------------- MODULE BatchReceiptReplay ----------------\n" +
		"EXTENDS MCBatchMemberTable, TLC, Sequences\n" +
		"Observed == <<\n" + strings.Join(observed, ",\n") + "\n>>\n" +
		"ActualState == BatchReplayState\n" +
		"MatchesExecution == ActualState=Observed[step+1]\n" +
		"ReplayNext ==\n" + strings.Join(cases, "\n") + "\n" +
		"ReplaySpec == BatchReplayInit /\\ [][ReplayNext]_bvars /\\ WF_bvars(ReplayNext)\n" +
		"=================================================================\n", nil
}

// RenderSecondConfig preserves the exact checked second-epoch constants and
// all its invariants/properties, replacing only the spec name.
func RenderSecondConfig(modelConfig string, steps int) (string, error) {
	if steps != 4 || strings.Count(modelConfig, "SPECIFICATION SecondSpec") != 1 || strings.Count(modelConfig, "MaxSteps = 4") != 1 {
		return "", errors.New("unexpected second-epoch model config shape")
	}
	return strings.Replace(modelConfig, "SPECIFICATION SecondSpec", "SPECIFICATION ReplaySpec", 1) + "\nINVARIANT MatchesExecution\n", nil
}

func RenderOrdinaryConfig(modelConfig string, steps int) (string, error) {
	if steps != 2 || strings.Count(modelConfig, "SPECIFICATION BatchSpec") != 1 || strings.Count(modelConfig, "MaxSteps = 3") != 1 {
		return "", errors.New("unexpected ordinary batch model config shape")
	}
	cfg := strings.Replace(modelConfig, "SPECIFICATION BatchSpec", "SPECIFICATION ReplaySpec", 1)
	cfg = strings.Replace(cfg, "MaxSteps = 3", "MaxSteps = 2", 1)
	return cfg + "\nINVARIANT MatchesExecution\n", nil
}

// WriteMixedBundle writes a private, immutable input for a caller-owned TLC
// execution. The evidence includes the full lossless Redis image at every
// boundary; this function never invokes Java.
func WriteMixedBundle(models, output string, steps []MixedStep) (Bundle, error) {
	return writeMixedBundle(models, output, steps, "MCBatchSecondEpoch.cfg", RenderSecondConfig)
}

func WriteOrdinaryBundle(models, output string, steps []MixedStep) (Bundle, error) {
	return writeMixedBundle(models, output, steps, "MCBatchMemberTable.cfg", RenderOrdinaryConfig)
}

func writeMixedBundle(models, output string, steps []MixedStep, configName string, renderConfig func(string, int) (string, error)) (Bundle, error) {
	var b Bundle
	source, err := RenderMixedHarness(steps)
	if err != nil {
		return b, err
	}
	base, err := os.ReadFile(filepath.Join(models, configName))
	if err != nil {
		return b, err
	}
	config, err := renderConfig(string(base), len(steps))
	if err != nil {
		return b, err
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return b, err
	}
	work := filepath.Join(output, "work")
	if err := tlc.CopyModels(models, work); err != nil {
		return b, err
	}
	b = Bundle{Work: work, Module: filepath.Join(work, "BatchReceiptReplay.tla"), Config: filepath.Join(work, "BatchReceiptReplay.cfg"), Evidence: filepath.Join(output, "evidence.json")}
	if err := os.WriteFile(b.Module, []byte(source), 0o644); err != nil {
		return Bundle{}, err
	}
	if err := os.WriteFile(b.Config, []byte(config), 0o644); err != nil {
		return Bundle{}, err
	}
	type observation struct {
		Index       uint64            `json:"index"`
		Action      string            `json:"action"`
		Canonical   []byte            `json:"canonical,omitempty"`
		Digest      string            `json:"digest,omitempty"`
		Before      map[string][]byte `json:"before"`
		After       map[string][]byte `json:"after"`
		Receipt     *Receipt          `json:"receipt,omitempty"`
		BeforeModel string            `json:"before_model"`
		AfterModel  string            `json:"after_model"`
	}
	evidence := make([]observation, len(steps))
	for i, item := range steps {
		var pre, post ModelState
		if item.Batch != nil {
			s := item.Batch
			evidence[i] = observation{Index: uint64(i), Action: "Apply", Canonical: s.Request.Canonical, Digest: s.Request.Digest, Before: s.Before.Image, After: s.After.Image, Receipt: s.Receipt}
			pre, post = s.BeforeModel, s.AfterModel
		} else {
			s := item.NonBatch
			evidence[i] = observation{Index: uint64(i), Action: string(s.Kind), Before: s.Before.Image, After: s.After.Image}
			pre, post = s.BeforeModel, s.AfterModel
		}
		evidence[i].BeforeModel, err = pre.TLA()
		if err != nil {
			return Bundle{}, err
		}
		evidence[i].AfterModel, err = post.TLA()
		if err != nil {
			return Bundle{}, err
		}
	}
	raw, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return Bundle{}, err
	}
	if err := os.WriteFile(b.Evidence, raw, 0o600); err != nil {
		return Bundle{}, err
	}
	sh, ch := sha256.Sum256([]byte(source)), sha256.Sum256([]byte(config))
	b.SourceSHA256, b.ConfigSHA256 = hex.EncodeToString(sh[:]), hex.EncodeToString(ch[:])
	return b, nil
}
