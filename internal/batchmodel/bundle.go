package batchmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

// Bundle is a private, deterministic TLC input and its independently captured
// Redis evidence. Writing it does not run Java; callers supply the source
// models directory and a scratch output directory.
type Bundle struct{ Work, Module, Config, Evidence, SourceSHA256, ConfigSHA256 string }

func WriteBundle(models, output string, steps []Step) (Bundle, error) {
	var b Bundle
	if len(steps) == 0 {
		return b, fmt.Errorf("empty receipt trace")
	}
	source, err := RenderHarness(steps)
	if err != nil {
		return b, err
	}
	base, err := os.ReadFile(filepath.Join(models, "MCBatchMemberTable.cfg"))
	if err != nil {
		return b, err
	}
	config, err := RenderConfig(string(base), len(steps))
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
	type imageEvidence struct {
		Index       uint64            `json:"index"`
		Canonical   []byte            `json:"canonical"`
		Digest      string            `json:"digest"`
		Before      map[string][]byte `json:"before"`
		After       map[string][]byte `json:"after"`
		Receipt     *Receipt          `json:"receipt"`
		Prior       *Receipt          `json:"prior,omitempty"`
		BeforeModel string            `json:"before_model"`
		AfterModel  string            `json:"after_model"`
	}
	evidence := make([]imageEvidence, len(steps))
	for i, s := range steps {
		pre, err := s.BeforeModel.TLA()
		if err != nil {
			return Bundle{}, err
		}
		post, err := s.AfterModel.TLA()
		if err != nil {
			return Bundle{}, err
		}
		evidence[i] = imageEvidence{Index: s.Index, Canonical: s.Request.Canonical, Digest: s.Request.Digest, Before: s.Before.Image, After: s.After.Image, Receipt: s.Receipt, Prior: s.PriorReceipt, BeforeModel: pre, AfterModel: post}
	}
	raw, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return Bundle{}, err
	}
	if err := os.WriteFile(b.Evidence, raw, 0o600); err != nil {
		return Bundle{}, err
	}
	sh := sha256.Sum256([]byte(source))
	ch := sha256.Sum256([]byte(config))
	b.SourceSHA256 = hex.EncodeToString(sh[:])
	b.ConfigSHA256 = hex.EncodeToString(ch[:])
	return b, nil
}
