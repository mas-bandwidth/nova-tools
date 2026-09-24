package land

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// ComputeInputID computes sha256(from_tip, ordered member heads, class, selection graph ids, policy_id, runner_id) (spec 5.5).
func ComputeInputID(fromTip string, memberHeads []string, class string, selGraphIDs []string, policyID, runnerID string) string {
	h := sha256.New()
	h.Write([]byte(fromTip))
	h.Write([]byte("\n"))
	h.Write([]byte(strings.Join(memberHeads, ",")))
	h.Write([]byte("\n"))
	h.Write([]byte(class))
	h.Write([]byte("\n"))
	sortedSel := make([]string, len(selGraphIDs))
	copy(sortedSel, selGraphIDs)
	sort.Strings(sortedSel)
	h.Write([]byte(strings.Join(sortedSel, ",")))
	h.Write([]byte("\n"))
	h.Write([]byte(policyID))
	h.Write([]byte("\n"))
	h.Write([]byte(runnerID))
	return hex.EncodeToString(h.Sum(nil))
}

// ComputePolicyID hashes fleet/land/<repo>.yml and the required set definition (spec 5.5).
func ComputePolicyID(policyYAML, requiredSet string) string {
	h := sha256.New()
	h.Write([]byte(policyYAML))
	h.Write([]byte("\n"))
	h.Write([]byte(requiredSet))
	return hex.EncodeToString(h.Sum(nil))
}

// ComputeRunnerID hashes nova-tools version, go version, and lisp implementation version (spec 5.5).
func ComputeRunnerID(novaToolsVersion, goVersion, lispVersion string) string {
	h := sha256.New()
	h.Write([]byte(novaToolsVersion))
	h.Write([]byte("\n"))
	h.Write([]byte(goVersion))
	h.Write([]byte("\n"))
	h.Write([]byte(lispVersion))
	return hex.EncodeToString(h.Sum(nil))
}

// SelGraphID names one selection graph by the identity it is cached under
// (land:<repo>:sel:<tree>:<goos>:<cfg>, spec 5.4), so the same tree, platform
// and configuration always give the same id.
func SelGraphID(tree, goos, cfg string) string {
	return tree + ":" + goos + ":" + cfg
}

// GateInputID is the input identity the gate worker computes and receipts
// (spec 5.5): from_tip, the ordered member heads, the class, the base and train
// selection graph ids, policy_id and runner_id. A GREEN receipt is indexed by
// it (land:<repo>:receipt_by_input:<id>), so a later plan of the same inputs
// reuses the receipt and a changed policy, runner or tree re-gates (L31).
func GateInputID(fromTip string, memberHeads []string, class, baseTree, trainTree, goos, cfg, policyID, runnerID string) string {
	return ComputeInputID(fromTip, memberHeads, class,
		[]string{SelGraphID(baseTree, goos, cfg), SelGraphID(trainTree, goos, cfg)},
		policyID, runnerID)
}
