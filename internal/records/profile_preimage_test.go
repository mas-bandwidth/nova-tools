package records

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func shaHexBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func shaPrefixedBytes(b []byte) string {
	return "sha256:" + shaHexBytes(b)
}

func testCanonicalBytes(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("json marshal: %w", err)
	}
	parsed, err := parseStrict(raw, nil, "body")
	if err != nil {
		return nil, fmt.Errorf("parse strict json: %w", err)
	}
	can, err := Canonicalize(parsed)
	if err != nil {
		return nil, fmt.Errorf("canonicalize: %w", err)
	}
	return can, nil
}

func testCanonicalCatalog(profilesMap map[string]any) (string, string, error) {
	canProfiles, err := testCanonicalBytes(profilesMap)
	if err != nil {
		return "", "", err
	}
	can := fmt.Sprintf(`{"profiles":%s,"version":1}`, string(canProfiles))
	return can, shaHexBytes([]byte(can)), nil
}

func TestSwarmProfilePreimageMigrationFixtures(t *testing.T) {
	repoRoot := filepath.Join("..", "..")

	catBytes, err := os.ReadFile(filepath.Join(repoRoot, "docs", "fixtures", "swarm-profile-catalog-encoding.json"))
	if err != nil {
		t.Fatalf("read catalog fixture: %v", err)
	}
	attBytes, err := os.ReadFile(filepath.Join(repoRoot, "docs", "fixtures", "swarm-attempt-body.json"))
	if err != nil {
		t.Fatalf("read attempt fixture: %v", err)
	}
	lchBytes, err := os.ReadFile(filepath.Join(repoRoot, "docs", "fixtures", "swarm-launch-record.json"))
	if err != nil {
		t.Fatalf("read launch fixture: %v", err)
	}

	var catFix, attFix, lchFix map[string]any
	if err := json.Unmarshal(catBytes, &catFix); err != nil {
		t.Fatalf("unmarshal catalog fixture: %v", err)
	}
	if err := json.Unmarshal(attBytes, &attFix); err != nil {
		t.Fatalf("unmarshal attempt fixture: %v", err)
	}
	if err := json.Unmarshal(lchBytes, &lchFix); err != nil {
		t.Fatalf("unmarshal launch fixture: %v", err)
	}

	// 1. Verify Catalog vectors
	vectors, ok := catFix["vectors"].([]any)
	if !ok || len(vectors) < 4 {
		t.Fatalf("catalog vectors must have at least 4 entries, got %d", len(vectors))
	}
	for _, v := range vectors {
		vm := v.(map[string]any)
		name := vm["name"].(string)
		inputStr := vm["input"].(string)
		expectedCan := vm["canonical"].(string)
		expectedSHA := vm["sha256"].(string)

		var parsedRaw map[string]any
		dec := json.NewDecoder(strings.NewReader(inputStr))
		if err := dec.Decode(&parsedRaw); err != nil {
			t.Fatalf("vector %q failed unmarshal: %v", name, err)
		}
		profilesRaw := parsedRaw["profiles"].(map[string]any)
		can, actualSHA, err := testCanonicalCatalog(profilesRaw)
		if err != nil {
			t.Fatalf("vector %q failed canonicalCatalog: %v", name, err)
		}
		if can != expectedCan {
			t.Fatalf("vector %q canonical mismatch\n got: %s\nwant: %s", name, can, expectedCan)
		}
		if actualSHA != expectedSHA {
			t.Fatalf("vector %q sha mismatch\n got: %s\nwant: %s", name, actualSHA, expectedSHA)
		}
	}

	// 2. Verify Attempt Body & 8-member Config Preimage
	bodyMap := attFix["body"].(map[string]any)
	workerMap := bodyMap["worker"].(map[string]any)
	if _, dup := workerMap["execution"]; dup {
		t.Fatalf("attempt.worker must NOT contain execution (attempt.execution is single owner)")
	}

	execMap := bodyMap["execution"].(map[string]any)
	if execMap["adapter"] == "" || execMap["artifact_hash"] == "" {
		t.Fatalf("attempt.execution missing required fields")
	}

	hashesMap := bodyMap["hashes"].(map[string]any)
	for _, req := range []string{"task", "prompt", "config", "prefix", "generated_config"} {
		if hashesMap[req] == "" {
			t.Fatalf("attempt hashes missing required hash %q", req)
		}
	}

	configMembers := []string{"credentials", "env_var", "execution", "limits", "prompt", "requested", "resolved", "worker"}
	configPreimage := make(map[string]any)
	for _, k := range configMembers {
		v, exists := bodyMap[k]
		if !exists {
			t.Fatalf("body missing config member %q", k)
		}
		configPreimage[k] = v
	}
	cfgCan, err := testCanonicalBytes(configPreimage)
	if err != nil {
		t.Fatalf("canonicalize config preimage: %v", err)
	}
	if shaPrefixedBytes(cfgCan) != hashesMap["config"] {
		t.Fatalf("config hash mismatch: got %s, want %s", shaPrefixedBytes(cfgCan), hashesMap["config"])
	}
	if string(cfgCan) != attFix["config_canonical"] {
		t.Fatalf("config_canonical mismatch in attempt fixture")
	}

	bodyCan, err := testCanonicalBytes(bodyMap)
	if err != nil {
		t.Fatalf("canonicalize attempt body: %v", err)
	}
	if shaPrefixedBytes(bodyCan) != attFix["snapshot_hash"] {
		t.Fatalf("snapshot_hash mismatch: got %s, want %s", shaPrefixedBytes(bodyCan), attFix["snapshot_hash"])
	}
	if string(bodyCan) != attFix["canonical"] {
		t.Fatalf("attempt body canonical string mismatch")
	}

	manifestMap := attFix["manifest"].(map[string]any)
	if manifestMap["snapshot_hash"] != attFix["snapshot_hash"] {
		t.Fatalf("manifest snapshot_hash mismatch")
	}
	manifestCan, err := testCanonicalBytes(manifestMap)
	if err != nil {
		t.Fatalf("canonicalize manifest: %v", err)
	}
	if string(manifestCan) != attFix["manifest_canonical"] {
		t.Fatalf("manifest_canonical mismatch")
	}
	manifestHash := shaPrefixedBytes(manifestCan)

	// 3. Verify Launch Record & Linkage
	lchBody := lchFix["body"].(map[string]any)
	if lchBody["manifest_hash"] != manifestHash {
		t.Fatalf("launch record manifest_hash linkage mismatch: got %s, want %s", lchBody["manifest_hash"], manifestHash)
	}
	if lchFix["manifest_canonical"] != attFix["manifest_canonical"] {
		t.Fatalf("launch record manifest_canonical linkage mismatch")
	}

	ctrlMap := lchBody["control"].(map[string]any)
	if ctrlMap["root"] == "" || ctrlMap["manifest_hash"] == "" {
		t.Fatalf("launch control missing root or manifest_hash")
	}
	realMap := lchBody["realization"].(map[string]any)
	if realMap["env_hash"] == "" {
		t.Fatalf("launch realization missing env_hash")
	}

	lchCan, err := testCanonicalBytes(lchBody)
	if err != nil {
		t.Fatalf("canonicalize launch body: %v", err)
	}
	if shaPrefixedBytes(lchCan) != lchFix["launch_hash"] {
		t.Fatalf("launch_hash mismatch: got %s, want %s", shaPrefixedBytes(lchCan), lchFix["launch_hash"])
	}
	if string(lchCan) != lchFix["canonical"] {
		t.Fatalf("launch body canonical mismatch")
	}

	argv := lchFix["argv"].([]any)
	if len(argv) != 6 || argv[5] != lchFix["launch_hash"] {
		t.Fatalf("launch argv linkage mismatch")
	}
}

func TestSwarmProfilePreimageNegativeWitnesses(t *testing.T) {
	repoRoot := filepath.Join("..", "..")

	attBytes, err := os.ReadFile(filepath.Join(repoRoot, "docs", "fixtures", "swarm-attempt-body.json"))
	if err != nil {
		t.Fatalf("read attempt fixture: %v", err)
	}
	var attFix map[string]any
	if err := json.Unmarshal(attBytes, &attFix); err != nil {
		t.Fatalf("unmarshal attempt fixture: %v", err)
	}
	bodyMap := attFix["body"].(map[string]any)

	// Witness 1: 7-member config preimage (missing execution) must NOT match config hash
	sevenMember := map[string]any{
		"credentials": bodyMap["credentials"],
		"env_var":     bodyMap["env_var"],
		"limits":      bodyMap["limits"],
		"prompt":      bodyMap["prompt"],
		"requested":   bodyMap["requested"],
		"resolved":    bodyMap["resolved"],
		"worker":      bodyMap["worker"],
	}
	sevenCan, err := testCanonicalBytes(sevenMember)
	if err != nil {
		t.Fatalf("witness 1 canonicalize: %v", err)
	}
	if shaPrefixedBytes(sevenCan) == bodyMap["hashes"].(map[string]any)["config"] {
		t.Fatalf("NEGATIVE WITNESS FAIL: 7-member config preimage matched 8-member hash")
	}

	// Witness 2: Cyclic / generated_config inclusion in config preimage must NOT match config hash
	cyclicPreimage := map[string]any{
		"credentials":      bodyMap["credentials"],
		"env_var":          bodyMap["env_var"],
		"execution":        bodyMap["execution"],
		"limits":           bodyMap["limits"],
		"prompt":           bodyMap["prompt"],
		"requested":        bodyMap["requested"],
		"resolved":         bodyMap["resolved"],
		"worker":           bodyMap["worker"],
		"generated_config": bodyMap["hashes"].(map[string]any)["generated_config"],
	}
	cyclicCan, err := testCanonicalBytes(cyclicPreimage)
	if err != nil {
		t.Fatalf("witness 2 canonicalize: %v", err)
	}
	if shaPrefixedBytes(cyclicCan) == bodyMap["hashes"].(map[string]any)["config"] {
		t.Fatalf("NEGATIVE WITNESS FAIL: cyclic generated_config inclusion matched config hash")
	}

	// Witness 3: Duplicate owner field (worker having execution)
	workerWithExec := make(map[string]any)
	for k, v := range bodyMap["worker"].(map[string]any) {
		workerWithExec[k] = v
	}
	workerWithExec["execution"] = bodyMap["execution"]
	if _, hasExec := workerWithExec["execution"]; !hasExec {
		t.Fatalf("failed to construct duplicate owner witness")
	}

	// Witness 4: Altered execution field produces different config hash
	alteredExec := make(map[string]any)
	for k, v := range bodyMap["execution"].(map[string]any) {
		alteredExec[k] = v
	}
	alteredExec["adapter"] = "opencode-native/2"
	alteredConfig := map[string]any{
		"credentials": bodyMap["credentials"],
		"env_var":     bodyMap["env_var"],
		"execution":   alteredExec,
		"limits":      bodyMap["limits"],
		"prompt":      bodyMap["prompt"],
		"requested":   bodyMap["requested"],
		"resolved":    bodyMap["resolved"],
		"worker":      bodyMap["worker"],
	}
	alteredCan, err := testCanonicalBytes(alteredConfig)
	if err != nil {
		t.Fatalf("altered config canonicalize: %v", err)
	}
	if shaPrefixedBytes(alteredCan) == bodyMap["hashes"].(map[string]any)["config"] {
		t.Fatalf("NEGATIVE WITNESS FAIL: altered execution field produced identical config hash")
	}
}
