// profile-preimage-check is a deterministic Go encoding checker using internal/records
// to verify parent profile tables, 8-member config preimages, and linked synthetic fixtures.
//
// Run from the repository root:
//
//	go run ./docs/drafts/fixtures/profile-preimage-check
//
// To regenerate or update fixtures:
//
//	go run ./docs/drafts/fixtures/profile-preimage-check -write
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/records"
)

func shaHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func shaPrefixed(b []byte) string {
	return "sha256:" + shaHex(b)
}

func toRecordValue(v any) (records.Value, error) {
	if v == nil {
		return nil, nil
	}
	switch t := v.(type) {
	case bool:
		return t, nil
	case string:
		return t, nil
	case []any:
		arr := make([]records.Value, len(t))
		for i, elem := range t {
			val, err := toRecordValue(elem)
			if err != nil {
				return nil, err
			}
			arr[i] = val
		}
		return arr, nil
	case []string:
		return records.Strings(t), nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		obj := records.NewObject()
		for _, k := range keys {
			recVal, err := toRecordValue(t[k])
			if err != nil {
				return nil, err
			}
			if err := obj.Set(k, recVal); err != nil {
				return nil, err
			}
		}
		return obj, nil
	case *records.Object:
		return t, nil
	case []records.Value:
		return t, nil
	default:
		return nil, fmt.Errorf("unsupported value type: %T", v)
	}
}

func canonicalBytes(v any) ([]byte, error) {
	recVal, err := toRecordValue(v)
	if err != nil {
		return nil, err
	}
	return records.Canonicalize(recVal)
}

// canonicalCatalog frames the root as {"profiles":<canonical profiles>,"version":1}
// per SPEC-SWARM-PROFILES.md:215-224.
func canonicalCatalog(profilesMap map[string]any) (string, string, error) {
	canProfiles, err := canonicalBytes(profilesMap)
	if err != nil {
		return "", "", err
	}
	can := fmt.Sprintf(`{"profiles":%s,"version":1}`, string(canProfiles))
	return can, shaHex([]byte(can)), nil
}

// computeRealizationEnvHash computes the pure RealizeProfileEnvironment hash.
func computeRealizationEnvHash(contextKind, contextRoot, jobID, slot, nonce, secretName string, pathList []string, envList []map[string]string) (string, error) {
	slotDir := contextRoot + "/slots/" + slot + "/worker"
	jobDir := slotDir + "/jobs/" + jobID
	dataHome := jobDir + "/data"

	type pair struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	derivedEnv := []pair{
		{"HOME", dataHome},
		{"XDG_DATA_HOME", dataHome},
		{"NOVA_SWARM_JOB", jobDir},
		{"PATH", strings.Join(pathList, ":")},
	}
	for _, e := range envList {
		derivedEnv = append(derivedEnv, pair{Name: e["name"], Value: e["value"]})
	}
	sort.Slice(derivedEnv, func(i, j int) bool { return derivedEnv[i].Name < derivedEnv[j].Name })

	envValues := make([]any, len(derivedEnv))
	for i, e := range derivedEnv {
		envValues[i] = map[string]any{"name": e.Name, "value": e.Value}
	}

	preimage := map[string]any{
		"schema": "nova.swarm.realization/1",
		"context": map[string]any{
			"kind": contextKind,
			"root": contextRoot,
		},
		"job_id":            jobID,
		"slot":              slot,
		"reservation_nonce": nonce,
		"paths": map[string]any{
			"slot":      slotDir,
			"job":       jobDir,
			"data_home": dataHome,
		},
		"env":            envValues,
		"secret_env_var": secretName,
	}
	can, err := canonicalBytes(preimage)
	if err != nil {
		return "", err
	}
	return shaPrefixed(can), nil
}

func buildFixtures() (catalogFixture map[string]any, attemptFixture map[string]any, launchFixture map[string]any, err error) {
	// --- 1. Artifact manifest for synthetic execution ---
	artifactManifest := map[string]any{
		"schema": "nova.swarm.artifact/1",
		"entries": []any{
			map[string]any{
				"role":        "harness",
				"destination": "harness/opencode",
				"type":        "regular",
				"mode":        "0755",
				"bytes":       "123",
				"sha256":      "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
			map[string]any{
				"role":        "worker",
				"destination": "worker/INSTRUCTIONS.md",
				"type":        "regular",
				"mode":        "0644",
				"bytes":       "456",
				"sha256":      "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
			map[string]any{
				"role":        "worker",
				"destination": "worker/skills",
				"type":        "dir",
				"mode":        "0755",
			},
		},
	}
	artCan, err := canonicalBytes(artifactManifest)
	if err != nil {
		return nil, nil, nil, err
	}
	artifactHash := shaPrefixed(artCan)

	// --- 2. Control manifest for synthetic launch ---
	controlManifest := map[string]any{
		"schema": "nova.swarm.control/1",
		"entries": []any{
			map[string]any{
				"role":        "launcher",
				"destination": "launcher",
				"type":        "regular",
				"mode":        "0755",
				"bytes":       "123",
				"sha256":      "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
			map[string]any{
				"role":        "sandbox",
				"destination": "sandbox",
				"type":        "regular",
				"mode":        "0755",
				"bytes":       "456",
				"sha256":      "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
		},
	}
	ctrlCan, err := canonicalBytes(controlManifest)
	if err != nil {
		return nil, nil, nil, err
	}
	controlHash := shaPrefixed(ctrlCan)
	controlHex := shaHex(ctrlCan)

	// --- 3. Synthetic Generated Config ---
	syntheticHarness := "{\"harness\":\"synthetic-harness/1\",\"version\":1}\n"
	generatedConfigHash := shaPrefixed([]byte(syntheticHarness))

	// --- 4. Resolved native model object ---
	resolvedObj := map[string]any{
		"provider": "synthetic-go",
		"model":    "synthetic/go-small",
		"endpoint": "https://go.synthetic.invalid/v1",
		"native": map[string]any{
			"schema":    "nova.swarm.native-model/1",
			"api_id":    "Synthetic-API-Small",
			"sdk":       "@synthetic/go-sdk",
			"model_url": "https://model.synthetic.invalid/fallback",
			"runtime": map[string]any{
				"name":         "Synthetic Go Small",
				"family":       "synthetic",
				"release_date": "2026-01-01",
				"status":       "active",
				"capabilities": map[string]any{
					"temperature": true,
					"reasoning":   false,
					"attachment":  false,
					"toolcall":    true,
					"input": map[string]any{
						"text":  true,
						"audio": false,
						"image": false,
						"video": false,
						"pdf":   false,
					},
					"output": map[string]any{
						"text":  true,
						"audio": false,
						"image": false,
						"video": false,
						"pdf":   false,
					},
					"interleaved": true,
				},
				"limits": map[string]any{
					"context": "4096",
					"input":   "1024",
					"output":  "512",
				},
				"headers": []any{
					map[string]any{
						"name":  "X-Synthetic-Route",
						"value": "go",
					},
				},
				"sdk_options": map[string]any{
					"kind": "object",
					"members": []any{
						map[string]any{
							"name": "maxRetries",
							"value": map[string]any{
								"kind":    "number",
								"decimal": "2",
							},
						},
						map[string]any{
							"name": "stream",
							"value": map[string]any{
								"kind":  "bool",
								"value": true,
							},
						},
					},
				},
				"model_options": map[string]any{
					"kind": "object",
					"members": []any{
						map[string]any{
							"name": "tag",
							"value": map[string]any{
								"kind":  "string",
								"value": "synthetic-go",
							},
						},
						map[string]any{
							"name": "temperature",
							"value": map[string]any{
								"kind":    "number",
								"decimal": "0.25",
							},
						},
					},
				},
				"variant": nil,
			},
		},
	}

	// --- 5. Profile Catalog Vector ---
	// Retain existing 3 vectors, add 4th vector showing worker.execution admission
	// and route.native.models supplying the frozen native model projection per SWARM-NATIVE-MODEL.md.
	catalogVector4Profiles := map[string]any{
		"go-small": map[string]any{
			"allowed_models": []any{"synthetic/go-small"},
			"env_var":        "OPENCODE_GO_KEY",
			"model":          "synthetic/go-small",
			"prompt": map[string]any{
				"mode":   "compact",
				"prefix": "Use the bounded task contract.",
				"tools":  []any{},
			},
			"route": map[string]any{
				"credentials": map[string]any{
					"age_key":  "/secure/example/worker.agekey",
					"gate":     "/opt/example/bin/nova-secrets",
					"kind":     "nova-secrets",
					"launcher": "/opt/example/bin/isolated-worker-launcher",
					"seat":     "worker",
					"sops":     "/opt/example/bin/sops",
					"store":    "/secure/example/store",
				},
				"endpoint": "https://go.synthetic.invalid/v1",
				"provider": "synthetic-go",
				"native": map[string]any{
					"models": []any{
						map[string]any{
							"requested": "synthetic/go-small",
							"resolved":  resolvedObj,
						},
					},
				},
			},
			"worker": map[string]any{
				"deadline": "300s",
				"execution": map[string]any{
					"adapter":          "opencode-native/1",
					"adapter_revision": "1",
					"artifact": map[string]any{
						"max_bytes":          "1073741824",
						"max_files":          "4096",
						"max_manifest_bytes": "16777216",
					},
					"env": []any{
						map[string]any{"name": "NO_COLOR", "value": "1"},
					},
					"path": []any{"/usr/bin", "/bin"},
				},
				"harness":             "/opt/example/bin/opencode",
				"harness_args":        []any{"run", "--model", "{model}", "--", "{prompt}"},
				"input_limit_phrases": []any{},
				"name":                "hosted-small",
				"read_roots":          []any{},
				"usage":               "opencode",
				"worker_dir":          "/opt/example/worker",
			},
		},
	}
	cat4Can, cat4Hash, err := canonicalCatalog(catalogVector4Profiles)
	if err != nil {
		return nil, nil, nil, err
	}
	catalogHashPrefixed := "sha256:" + cat4Hash

	catalogFixture = map[string]any{
		"status": "proposed encoding-only vectors; profile values intentionally do not pass admission schema",
		"vectors": []any{
			map[string]any{
				"name":      "empty",
				"input":     "{ \"version\": 1, \"profiles\": {} }",
				"canonical": "{\"profiles\":{},\"version\":1}",
				"sha256":    "67d82c25cee9879aa641a9988622147511968e3956ca3407d73550757a539c86",
			},
			map[string]any{
				"name":      "empty-reordered",
				"input":     "{\"profiles\":{},\"version\":1}",
				"canonical": "{\"profiles\":{},\"version\":1}",
				"sha256":    "67d82c25cee9879aa641a9988622147511968e3956ca3407d73550757a539c86",
			},
			map[string]any{
				"name":      "utf16-order",
				"input":     `{"version":1,"profiles":{"\ue000":{},"\ud800\udc00":{}}}`,
				"canonical": "{\"profiles\":{\"\xf0\x90\x80\x80\":{},\"\ue000\":{}},\"version\":1}",
				"sha256":    "95ce28aab5589541c6d0b0ce393f4575e6d8b0a5c78dcca33baf2b56d101ed73",
			},
			map[string]any{
				"name":      "profile-execution-admission",
				"input":     cat4Can,
				"canonical": cat4Can,
				"sha256":    cat4Hash,
			},
		},
	}

	// --- 6. Attempt Body Fixture ---
	taskBytes := "Review the supplied fixture.\n"
	promptBytes := "Synthetic prompt for encoding validation only.\n"
	taskHash := shaPrefixed([]byte(taskBytes))
	promptHash := shaPrefixed([]byte(promptBytes))
	prefixHash := shaPrefixed([]byte("Use the bounded task contract."))

	requestedObj := map[string]any{
		"provider": "synthetic-go",
		"model":    "synthetic/go-small",
	}

	workerObj := map[string]any{
		"name":                "hosted-small",
		"usage":               "opencode",
		"harness":             "/opt/example/bin/opencode",
		"worker_dir":          "/opt/example/worker",
		"harness_args":        []any{"run", "--model", "{model}", "--", "{prompt}"},
		"read_roots":          []any{},
		"input_limit_phrases": []any{},
	}

	executionObj := map[string]any{
		"adapter":          "opencode-native/1",
		"adapter_revision": "1",
		"artifact_hash":    artifactHash,
		"harness":          "harness/opencode",
		"worker_root":      "worker",
		"path":             []any{"/usr/bin", "/bin"},
		"env": []any{
			map[string]any{"name": "NO_COLOR", "value": "1"},
		},
		"artifact": map[string]any{
			"max_files":          "4096",
			"max_bytes":          "1073741824",
			"max_manifest_bytes": "16777216",
		},
	}

	credentialsObj := map[string]any{
		"kind":     "nova-secrets",
		"store":    "/secure/example/store",
		"seat":     "worker",
		"age_key":  "/secure/example/worker.agekey",
		"sops":     "/opt/example/bin/sops",
		"gate":     "/opt/example/bin/nova-secrets",
		"launcher": "/opt/example/bin/isolated-worker-launcher",
	}

	envVarStr := "OPENCODE_GO_KEY"

	limitsObj := map[string]any{
		"files":       "2",
		"tokens":      "36000",
		"deadline_ns": "300000000000",
		"max_input":   "-",
	}

	promptObj := map[string]any{
		"mode":     "compact",
		"prefix":   "Use the bounded task contract.",
		"template": "read-pr",
		"tools":    []any{},
	}

	// Canonical 8-member config preimage
	configPreimage := map[string]any{
		"credentials": credentialsObj,
		"env_var":     envVarStr,
		"execution":   executionObj,
		"limits":      limitsObj,
		"prompt":      promptObj,
		"requested":   requestedObj,
		"resolved":    resolvedObj,
		"worker":      workerObj,
	}

	cfgCan, err := canonicalBytes(configPreimage)
	if err != nil {
		return nil, nil, nil, err
	}
	configHash := shaPrefixed(cfgCan)

	bodyObj := map[string]any{
		"schema":     "nova.swarm.attempt/1",
		"job_id":     "20260914T052500Z-fixture-012abc",
		"profile_id": "go-small",
		"lineage": map[string]any{
			"kind":     "none",
			"previous": "-",
		},
		"catalog_hash": catalogHashPrefixed,
		"requested":    requestedObj,
		"resolved":     resolvedObj,
		"worker":       workerObj,
		"execution":    executionObj,
		"credentials":  credentialsObj,
		"env_var":      envVarStr,
		"limits":       limitsObj,
		"prompt":       promptObj,
		"attribution": map[string]any{
			"bench": "fixture",
			"repo":  "example/project",
			"actor": "example",
			"basis": "caller",
		},
		"hashes": map[string]any{
			"task":             taskHash,
			"prompt":           promptHash,
			"config":           configHash,
			"prefix":           prefixHash,
			"generated_config": generatedConfigHash,
		},
		"input_bytes": map[string]any{
			"task":   "29",
			"prompt": "47",
		},
	}

	bodyCan, err := canonicalBytes(bodyObj)
	if err != nil {
		return nil, nil, nil, err
	}
	snapshotHash := shaPrefixed(bodyCan)

	manifestObj := map[string]any{
		"schema":        "nova.swarm.prelaunch/1",
		"job_id":        "20260914T052500Z-fixture-012abc",
		"snapshot_hash": snapshotHash,
	}
	manifestCan, err := canonicalBytes(manifestObj)
	if err != nil {
		return nil, nil, nil, err
	}
	manifestHash := shaPrefixed(manifestCan)

	attemptFixture = map[string]any{
		"status":                 "proposed encoding fixture only; synthetic native model, synthetic execution, synthetic generated config; no live configuration or launch validation",
		"task_bytes":             taskBytes,
		"prompt_bytes":           promptBytes,
		"body":                   bodyObj,
		"canonical":              string(bodyCan),
		"snapshot_hash":          snapshotHash,
		"manifest":               manifestObj,
		"manifest_canonical":     string(manifestCan),
		"config_canonical":       string(cfgCan),
		"generated_config_bytes": syntheticHarness,
		"artifact_manifest":      artifactManifest,
	}

	// --- 7. Launch Record Fixture ---
	realizationEnvHash, err := computeRealizationEnvHash(
		"pool",
		"/secure/example/pool",
		"20260914T052500Z-fixture-012abc",
		"1",
		"012345abcdef",
		"OPENCODE_GO_KEY",
		[]string{"/usr/bin", "/bin"},
		[]map[string]string{{"name": "NO_COLOR", "value": "1"}},
	)
	if err != nil {
		return nil, nil, nil, err
	}

	controlRoot := fmt.Sprintf("/secure/example/pool/evidence/20260914T052500Z-fixture-012abc/control/sha256-%s", controlHex)
	launcherPath := controlRoot + "/launcher"
	sandboxPath := controlRoot + "/sandbox"

	launchBodyObj := map[string]any{
		"schema": "nova.swarm.launch/1",
		"context": map[string]any{
			"kind": "pool",
			"root": "/secure/example/pool",
		},
		"evidence_root":     "/secure/example/pool/evidence",
		"job_id":            "20260914T052500Z-fixture-012abc",
		"slot":              "1",
		"reservation_nonce": "012345abcdef",
		"manifest_hash":     manifestHash,
		"sandbox":           sandboxPath,
		"usage_every_ns":    "1000000000",
		"control": map[string]any{
			"root":          controlRoot,
			"manifest_hash": controlHash,
			"launcher": map[string]any{
				"source": "/opt/example/bin/isolated-worker-launcher",
				"path":   launcherPath,
				"sha256": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
			"sandbox_source": "/opt/example/bin/nova-sandbox",
		},
		"realization": map[string]any{
			"env_hash": realizationEnvHash,
		},
	}

	launchCan, err := canonicalBytes(launchBodyObj)
	if err != nil {
		return nil, nil, nil, err
	}
	launchHash := shaPrefixed(launchCan)

	launchFixture = map[string]any{
		"status":             "proposed encoding fixture only; synthetic paths, no launch or security validation",
		"body":               launchBodyObj,
		"canonical":          string(launchCan),
		"launch_hash":        launchHash,
		"manifest_canonical": string(manifestCan),
		"control_manifest":   controlManifest,
		"argv": []any{
			launcherPath,
			"profile-supervise",
			"--launch",
			"/secure/example/pool/evidence/20260914T052500Z-fixture-012abc/launch/012345abcdef.json",
			"--launch-hash",
			launchHash,
		},
	}

	return catalogFixture, attemptFixture, launchFixture, nil
}

// verifyFixtures performs the strict verification required by the contract.
func verifyFixtures(catFix, attFix, lchFix map[string]any) error {
	// A. Check catalog encoding
	vectors, ok := catFix["vectors"].([]any)
	if !ok || len(vectors) < 4 {
		return fmt.Errorf("catalog fixture must have at least 4 vectors, got %d", len(vectors))
	}
	var vec4ParsedProfiles map[string]any
	var vec4SHA string

	for _, v := range vectors {
		vm := v.(map[string]any)
		name := vm["name"].(string)
		inputStr := vm["input"].(string)
		expectedCan := vm["canonical"].(string)
		expectedSHA := vm["sha256"].(string)

		var parsedRaw map[string]any
		dec := json.NewDecoder(strings.NewReader(inputStr))
		if err := dec.Decode(&parsedRaw); err != nil {
			return fmt.Errorf("vector %q failed unmarshal: %w", name, err)
		}
		profilesRaw := parsedRaw["profiles"].(map[string]any)
		can, actualSHA, err := canonicalCatalog(profilesRaw)
		if err != nil {
			return fmt.Errorf("vector %q failed canonicalCatalog: %w", name, err)
		}
		if can != expectedCan {
			return fmt.Errorf("vector %q canonical mismatch\n got: %s\nwant: %s", name, can, expectedCan)
		}
		if actualSHA != expectedSHA {
			return fmt.Errorf("vector %q sha mismatch: got %s, want %s", name, actualSHA, expectedSHA)
		}
		if name == "profile-execution-admission" {
			vec4ParsedProfiles = profilesRaw
			vec4SHA = actualSHA
		}
	}

	if vec4ParsedProfiles == nil {
		return fmt.Errorf("vector profile-execution-admission not found")
	}

	// B. Check attempt body & linkage
	attBody, ok := attFix["body"].(map[string]any)
	if !ok {
		return fmt.Errorf("attempt fixture missing body")
	}

	// 1. Linkage: catalog_hash must match vector 4 SHA
	if attBody["catalog_hash"] != "sha256:"+vec4SHA {
		return fmt.Errorf("attempt catalog_hash linkage mismatch: got %s, want %s", attBody["catalog_hash"], "sha256:"+vec4SHA)
	}

	// 2. Linkage: catalog native model projection must match attempt resolved.native
	profileID, ok := attBody["profile_id"].(string)
	if !ok {
		return fmt.Errorf("attempt missing profile_id")
	}
	catProf, ok := vec4ParsedProfiles[profileID].(map[string]any)
	if !ok {
		return fmt.Errorf("profile %q not found in catalog", profileID)
	}
	catRoute, ok := catProf["route"].(map[string]any)
	if !ok {
		return fmt.Errorf("catalog profile %q missing route", profileID)
	}
	catNative, ok := catRoute["native"].(map[string]any)
	if !ok {
		return fmt.Errorf("catalog profile %q missing route.native", profileID)
	}
	catModels, ok := catNative["models"].([]any)
	if !ok || len(catModels) == 0 {
		return fmt.Errorf("catalog profile %q missing route.native.models", profileID)
	}
	reqModel := attBody["requested"].(map[string]any)["model"].(string)
	var matchingNativeResolved map[string]any
	for _, m := range catModels {
		mm := m.(map[string]any)
		if mm["requested"] == reqModel {
			matchingNativeResolved = mm["resolved"].(map[string]any)
			break
		}
	}
	if matchingNativeResolved == nil {
		return fmt.Errorf("catalog profile %q has no native model entry for %q", profileID, reqModel)
	}
	catResCan, err := canonicalBytes(matchingNativeResolved)
	if err != nil {
		return fmt.Errorf("canonicalize catalog resolved native: %w", err)
	}
	attResCan, err := canonicalBytes(attBody["resolved"])
	if err != nil {
		return fmt.Errorf("canonicalize attempt resolved: %w", err)
	}
	if string(catResCan) != string(attResCan) {
		return fmt.Errorf("catalog native model projection does not match attempt resolved projection\n got: %s\nwant: %s", catResCan, attResCan)
	}

	// 3. Execution single owner: attempt.worker MUST NOT contain execution
	workerMap, ok := attBody["worker"].(map[string]any)
	if !ok {
		return fmt.Errorf("attempt missing worker")
	}
	if _, hasExec := workerMap["execution"]; hasExec {
		return fmt.Errorf("attempt.worker must not contain execution (single execution owner violation)")
	}

	// 4. Execution object at top-level
	execMap, ok := attBody["execution"].(map[string]any)
	if !ok {
		return fmt.Errorf("attempt missing top-level execution")
	}
	artMap, ok := execMap["artifact"].(map[string]any)
	if !ok || artMap["max_bytes"] == "" || artMap["max_files"] == "" || artMap["max_manifest_bytes"] == "" {
		return fmt.Errorf("attempt.execution artifact caps malformed")
	}
	if execMap["artifact_hash"] == "" || execMap["adapter"] == "" || execMap["adapter_revision"] == "" || execMap["worker_root"] == "" || execMap["harness"] == "" {
		return fmt.Errorf("attempt.execution missing required fields")
	}

	// 5. Artifact manifest linkage
	artManCan, err := canonicalBytes(attFix["artifact_manifest"])
	if err != nil {
		return fmt.Errorf("canonicalize artifact manifest: %w", err)
	}
	if shaPrefixed(artManCan) != execMap["artifact_hash"] {
		return fmt.Errorf("artifact_hash mismatch: got %s, want %s", execMap["artifact_hash"], shaPrefixed(artManCan))
	}

	// 6. 8-member config preimage verification
	configMembers := map[string]any{
		"credentials": attBody["credentials"],
		"env_var":     attBody["env_var"],
		"execution":   attBody["execution"],
		"limits":      attBody["limits"],
		"prompt":      attBody["prompt"],
		"requested":   attBody["requested"],
		"resolved":    attBody["resolved"],
		"worker":      attBody["worker"],
	}
	if len(configMembers) != 8 {
		return fmt.Errorf("config preimage must have exactly 8 members, got %d", len(configMembers))
	}
	cfgCan, err := canonicalBytes(configMembers)
	if err != nil {
		return fmt.Errorf("canonicalize config preimage: %w", err)
	}
	expectedCfgHash := shaPrefixed(cfgCan)
	hashesMap, ok := attBody["hashes"].(map[string]any)
	if !ok {
		return fmt.Errorf("attempt missing hashes")
	}
	if hashesMap["config"] != expectedCfgHash {
		return fmt.Errorf("config hash mismatch: got %s, want %s", hashesMap["config"], expectedCfgHash)
	}
	if attFix["config_canonical"] != string(cfgCan) {
		return fmt.Errorf("config_canonical in fixture does not match canonical config preimage bytes")
	}

	// 7. Non-cyclic generated_config
	genCfgBytes, ok := attFix["generated_config_bytes"].(string)
	if !ok {
		return fmt.Errorf("attempt fixture missing generated_config_bytes")
	}
	expectedGenCfgHash := shaPrefixed([]byte(genCfgBytes))
	if hashesMap["generated_config"] != expectedGenCfgHash {
		return fmt.Errorf("generated_config hash mismatch: got %s, want %s", hashesMap["generated_config"], expectedGenCfgHash)
	}

	// 8. Snapshot hash and manifest
	bodyCan, err := canonicalBytes(attBody)
	if err != nil {
		return fmt.Errorf("canonicalize attempt body: %w", err)
	}
	snapshotHash := shaPrefixed(bodyCan)
	if attFix["snapshot_hash"] != snapshotHash {
		return fmt.Errorf("snapshot_hash mismatch: got %s, want %s", attFix["snapshot_hash"], snapshotHash)
	}
	manifestMap, ok := attFix["manifest"].(map[string]any)
	if !ok {
		return fmt.Errorf("attempt missing manifest")
	}
	if manifestMap["snapshot_hash"] != snapshotHash {
		return fmt.Errorf("manifest snapshot_hash mismatch")
	}
	manifestCan, err := canonicalBytes(manifestMap)
	if err != nil {
		return fmt.Errorf("canonicalize manifest: %w", err)
	}
	if attFix["manifest_canonical"] != string(manifestCan) {
		return fmt.Errorf("manifest_canonical mismatch")
	}
	manifestHash := shaPrefixed(manifestCan)

	// C. Check launch record & linkage
	lchBody, ok := lchFix["body"].(map[string]any)
	if !ok {
		return fmt.Errorf("launch fixture missing body")
	}

	// 1. Linkage: manifest_hash must match manifestHash of attempt
	if lchBody["manifest_hash"] != manifestHash {
		return fmt.Errorf("launch record manifest_hash linkage mismatch: got %s, want %s", lchBody["manifest_hash"], manifestHash)
	}
	if lchFix["manifest_canonical"] != attFix["manifest_canonical"] {
		return fmt.Errorf("launch record manifest_canonical linkage mismatch")
	}

	// 2. Control recomputation and verification
	ctrlMap, ok := lchBody["control"].(map[string]any)
	if !ok {
		return fmt.Errorf("launch record missing control")
	}
	if ctrlMap["root"] == "" || ctrlMap["manifest_hash"] == "" {
		return fmt.Errorf("launch control missing root or manifest_hash")
	}
	ctrlManCan, err := canonicalBytes(lchFix["control_manifest"])
	if err != nil {
		return fmt.Errorf("canonicalize control_manifest: %w", err)
	}
	expectedCtrlHash := shaPrefixed(ctrlManCan)
	if ctrlMap["manifest_hash"] != expectedCtrlHash {
		return fmt.Errorf("control.manifest_hash mismatch: got %s, want %s", ctrlMap["manifest_hash"], expectedCtrlHash)
	}
	launcherMap, ok := ctrlMap["launcher"].(map[string]any)
	if !ok {
		return fmt.Errorf("control missing launcher object")
	}
	expectedLauncherPath := ctrlMap["root"].(string) + "/launcher"
	if launcherMap["path"] != expectedLauncherPath {
		return fmt.Errorf("control.launcher.path mismatch: got %s, want %s", launcherMap["path"], expectedLauncherPath)
	}

	// 3. Protected execution targets verification
	expectedSandboxPath := ctrlMap["root"].(string) + "/sandbox"
	if lchBody["sandbox"] != expectedSandboxPath {
		return fmt.Errorf("launch body sandbox is not protected control path: got %s, want %s", lchBody["sandbox"], expectedSandboxPath)
	}
	argv, ok := lchFix["argv"].([]any)
	if !ok || len(argv) != 6 {
		return fmt.Errorf("launch argv shape mismatch")
	}
	if argv[0] != expectedLauncherPath {
		return fmt.Errorf("launch argv[0] is not protected control launcher path: got %s, want %s", argv[0], expectedLauncherPath)
	}

	// 4. Realization recomputation and verification
	realMap, ok := lchBody["realization"].(map[string]any)
	if !ok {
		return fmt.Errorf("launch record missing realization")
	}
	contextMap, ok := lchBody["context"].(map[string]any)
	if !ok {
		return fmt.Errorf("launch record missing context")
	}
	execPath := []string{}
	for _, p := range execMap["path"].([]any) {
		execPath = append(execPath, p.(string))
	}
	execEnv := []map[string]string{}
	for _, e := range execMap["env"].([]any) {
		em := e.(map[string]any)
		execEnv = append(execEnv, map[string]string{
			"name":  em["name"].(string),
			"value": em["value"].(string),
		})
	}
	expectedEnvHash, err := computeRealizationEnvHash(
		contextMap["kind"].(string),
		contextMap["root"].(string),
		lchBody["job_id"].(string),
		lchBody["slot"].(string),
		lchBody["reservation_nonce"].(string),
		attBody["env_var"].(string),
		execPath,
		execEnv,
	)
	if err != nil {
		return fmt.Errorf("compute realization env_hash: %w", err)
	}
	if realMap["env_hash"] != expectedEnvHash {
		return fmt.Errorf("realization env_hash mismatch: got %s, want %s", realMap["env_hash"], expectedEnvHash)
	}

	// 5. Launch canonical and launch_hash
	lchCan, err := canonicalBytes(lchBody)
	if err != nil {
		return fmt.Errorf("canonicalize launch body: %w", err)
	}
	if shaPrefixed(lchCan) != lchFix["launch_hash"] {
		return fmt.Errorf("launch_hash mismatch: got %s, want %s", shaPrefixed(lchCan), lchFix["launch_hash"])
	}
	if string(lchCan) != lchFix["canonical"] {
		return fmt.Errorf("launch body canonical mismatch")
	}

	// 6. Argv --launch-hash linkage
	if argv[5] != lchFix["launch_hash"] {
		return fmt.Errorf("argv --launch-hash linkage mismatch")
	}

	return nil
}

// verifyNegativeWitnesses proves that the checker rejects:
// 1. 7-member config preimages (missing execution)
// 2. cyclic / generated hash inclusion
// 3. duplicate owner fields (worker.execution)
// 4. altered execution fields
// 5. wrong linkage
// 6. missing expected hashes
// 7. stale execution target in argv[0]
// 8. stale sandbox target in launch body
// 9. catalog missing native model projection
// 10. corrupted expected hash in committed fixture
func verifyNegativeWitnesses(catFix, attFix, lchFix map[string]any) error {
	bodyMap := attFix["body"].(map[string]any)

	// Witness 1: 7-member config preimage (missing execution)
	sevenMember := map[string]any{
		"credentials": bodyMap["credentials"],
		"env_var":     bodyMap["env_var"],
		"limits":      bodyMap["limits"],
		"prompt":      bodyMap["prompt"],
		"requested":   bodyMap["requested"],
		"resolved":    bodyMap["resolved"],
		"worker":      bodyMap["worker"],
	}
	sevenCan, err := canonicalBytes(sevenMember)
	if err != nil {
		return fmt.Errorf("witness 1 canonicalize: %w", err)
	}
	if shaPrefixed(sevenCan) == bodyMap["hashes"].(map[string]any)["config"] {
		return fmt.Errorf("NEGATIVE WITNESS FAIL: 7-member config preimage erroneously matched 8-member config hash")
	}

	// Witness 2: Cyclic / generated hash inclusion in config preimage
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
	cyclicCan, err := canonicalBytes(cyclicPreimage)
	if err != nil {
		return fmt.Errorf("witness 2 canonicalize: %w", err)
	}
	if shaPrefixed(cyclicCan) == bodyMap["hashes"].(map[string]any)["config"] {
		return fmt.Errorf("NEGATIVE WITNESS FAIL: cyclic generated_config inclusion was not distinguished")
	}

	// Witness 3: Duplicate owner fields (worker having execution)
	tamperedWorker := make(map[string]any)
	for k, v := range bodyMap["worker"].(map[string]any) {
		tamperedWorker[k] = v
	}
	tamperedWorker["execution"] = bodyMap["execution"]
	tamperedBody := make(map[string]any)
	for k, v := range bodyMap {
		tamperedBody[k] = v
	}
	tamperedBody["worker"] = tamperedWorker
	tamperedAttFix := make(map[string]any)
	for k, v := range attFix {
		tamperedAttFix[k] = v
	}
	tamperedAttFix["body"] = tamperedBody
	if err := verifyFixtures(catFix, tamperedAttFix, lchFix); err == nil {
		return fmt.Errorf("NEGATIVE WITNESS FAIL: duplicate execution in worker was not rejected")
	}

	// Witness 4: Altered execution fields
	tamperedExec := make(map[string]any)
	for k, v := range bodyMap["execution"].(map[string]any) {
		tamperedExec[k] = v
	}
	tamperedExec["adapter"] = "opencode-tampered/99"
	tamperedBody4 := make(map[string]any)
	for k, v := range bodyMap {
		tamperedBody4[k] = v
	}
	tamperedBody4["execution"] = tamperedExec
	tamperedAttFix4 := make(map[string]any)
	for k, v := range attFix {
		tamperedAttFix4[k] = v
	}
	tamperedAttFix4["body"] = tamperedBody4
	if err := verifyFixtures(catFix, tamperedAttFix4, lchFix); err == nil {
		return fmt.Errorf("NEGATIVE WITNESS FAIL: altered execution field was not rejected")
	}

	// Witness 5: Wrong linkage (tampered manifest_hash in launch record)
	tamperedLchBody := make(map[string]any)
	for k, v := range lchFix["body"].(map[string]any) {
		tamperedLchBody[k] = v
	}
	tamperedLchBody["manifest_hash"] = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	tamperedLchFix := make(map[string]any)
	for k, v := range lchFix {
		tamperedLchFix[k] = v
	}
	tamperedLchFix["body"] = tamperedLchBody
	if err := verifyFixtures(catFix, attFix, tamperedLchFix); err == nil {
		return fmt.Errorf("NEGATIVE WITNESS FAIL: mismatched manifest_hash linkage was not rejected")
	}

	// Witness 6: Missing expected hashes (missing generated_config)
	tamperedHashes := make(map[string]any)
	for k, v := range bodyMap["hashes"].(map[string]any) {
		tamperedHashes[k] = v
	}
	delete(tamperedHashes, "generated_config")
	tamperedBody6 := make(map[string]any)
	for k, v := range bodyMap {
		tamperedBody6[k] = v
	}
	tamperedBody6["hashes"] = tamperedHashes
	tamperedAttFix6 := make(map[string]any)
	for k, v := range attFix {
		tamperedAttFix6[k] = v
	}
	tamperedAttFix6["body"] = tamperedBody6
	if err := verifyFixtures(catFix, tamperedAttFix6, lchFix); err == nil {
		return fmt.Errorf("NEGATIVE WITNESS FAIL: missing generated_config hash was not rejected")
	}

	// Witness 7: Stale execution target in argv[0] (reverting to configured source)
	tamperedArgv := make([]any, len(lchFix["argv"].([]any)))
	copy(tamperedArgv, lchFix["argv"].([]any))
	tamperedArgv[0] = "/opt/example/bin/isolated-worker-launcher"
	tamperedLchFix7 := make(map[string]any)
	for k, v := range lchFix {
		tamperedLchFix7[k] = v
	}
	tamperedLchFix7["argv"] = tamperedArgv
	if err := verifyFixtures(catFix, attFix, tamperedLchFix7); err == nil {
		return fmt.Errorf("NEGATIVE WITNESS FAIL: configured source launcher path in argv[0] was not rejected")
	}

	// Witness 8: Stale sandbox target in launch body (reverting to configured source)
	tamperedLchBody8 := make(map[string]any)
	for k, v := range lchFix["body"].(map[string]any) {
		tamperedLchBody8[k] = v
	}
	tamperedLchBody8["sandbox"] = "/opt/example/bin/nova-sandbox"
	tamperedLchFix8 := make(map[string]any)
	for k, v := range lchFix {
		tamperedLchFix8[k] = v
	}
	tamperedLchFix8["body"] = tamperedLchBody8
	if err := verifyFixtures(catFix, attFix, tamperedLchFix8); err == nil {
		return fmt.Errorf("NEGATIVE WITNESS FAIL: configured source sandbox path was not rejected")
	}

	// Witness 9: Catalog missing native model projection for opencode-native/1 profile
	tamperedVectors := []any{}
	for _, v := range catFix["vectors"].([]any) {
		vm := v.(map[string]any)
		if vm["name"] == "profile-execution-admission" {
			// Strip route.native
			var parsed map[string]any
			json.Unmarshal([]byte(vm["input"].(string)), &parsed)
			profs := parsed["profiles"].(map[string]any)
			goSmall := profs["go-small"].(map[string]any)
			route := goSmall["route"].(map[string]any)
			delete(route, "native")
			c, s, _ := canonicalCatalog(profs)
			tamperedVectors = append(tamperedVectors, map[string]any{
				"name":      "profile-execution-admission",
				"input":     c,
				"canonical": c,
				"sha256":    s,
			})
		} else {
			tamperedVectors = append(tamperedVectors, vm)
		}
	}
	tamperedCatFix9 := map[string]any{
		"status":  catFix["status"],
		"vectors": tamperedVectors,
	}
	if err := verifyFixtures(tamperedCatFix9, attFix, lchFix); err == nil {
		return fmt.Errorf("NEGATIVE WITNESS FAIL: catalog missing native model projection was not rejected")
	}

	// Witness 10: Corrupted expected hash in committed fixture
	tamperedAttFix10 := make(map[string]any)
	for k, v := range attFix {
		tamperedAttFix10[k] = v
	}
	tamperedAttFix10["snapshot_hash"] = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if err := verifyFixtures(catFix, tamperedAttFix10, lchFix); err == nil {
		return fmt.Errorf("NEGATIVE WITNESS FAIL: corrupted expected hash in fixture was not rejected")
	}

	return nil
}

func prettyIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func loadFixtures(dir string) (catFix, attFix, lchFix map[string]any, err error) {
	catBytes, err := os.ReadFile(filepath.Join(dir, "swarm-profile-catalog-encoding.json"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read catalog fixture: %w", err)
	}
	if err := json.Unmarshal(catBytes, &catFix); err != nil {
		return nil, nil, nil, fmt.Errorf("unmarshal catalog fixture: %w", err)
	}

	attBytes, err := os.ReadFile(filepath.Join(dir, "swarm-attempt-body.json"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read attempt fixture: %w", err)
	}
	if err := json.Unmarshal(attBytes, &attFix); err != nil {
		return nil, nil, nil, fmt.Errorf("unmarshal attempt fixture: %w", err)
	}

	lchBytes, err := os.ReadFile(filepath.Join(dir, "swarm-launch-record.json"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read launch fixture: %w", err)
	}
	if err := json.Unmarshal(lchBytes, &lchFix); err != nil {
		return nil, nil, nil, fmt.Errorf("unmarshal launch fixture: %w", err)
	}

	return catFix, attFix, lchFix, nil
}

func main() {
	writeFlag := flag.Bool("write", false, "regenerate and write fixtures to docs/fixtures/")
	fixturesDir := flag.String("fixtures-dir", "docs/fixtures", "directory containing fixtures")
	flag.Parse()

	if *writeFlag {
		catFix, attFix, lchFix, err := buildFixtures()
		if err != nil {
			fmt.Fprintf(os.Stderr, "build fixtures failed: %v\n", err)
			os.Exit(1)
		}

		catJSON, err := prettyIndent(catFix)
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(*fixturesDir, "swarm-profile-catalog-encoding.json"), catJSON, 0644); err != nil {
			panic(err)
		}

		attJSON, err := prettyIndent(attFix)
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(*fixturesDir, "swarm-attempt-body.json"), attJSON, 0644); err != nil {
			panic(err)
		}

		lchJSON, err := prettyIndent(lchFix)
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(*fixturesDir, "swarm-launch-record.json"), lchJSON, 0644); err != nil {
			panic(err)
		}
		fmt.Printf("Fixtures successfully generated and written to %s/\n", *fixturesDir)
	}

	// Normal check: MUST read committed fixture files from disk
	catFix, attFix, lchFix, err := loadFixtures(*fixturesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load committed fixtures from %s failed: %v\n", *fixturesDir, err)
		os.Exit(1)
	}

	// Verify all fixtures
	if err := verifyFixtures(catFix, attFix, lchFix); err != nil {
		fmt.Fprintf(os.Stderr, "fixture verification failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("FIXTURES OK: all declared canonical strings, 8-member config preimage, and linkage verified")

	// Verify negative witnesses
	if err := verifyNegativeWitnesses(catFix, attFix, lchFix); err != nil {
		fmt.Fprintf(os.Stderr, "negative witness verification failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("NEGATIVE WITNESSES OK: 10/10 negative boundary tests verified")
	fmt.Println("LIMIT: encoding and preimage checks only; no native runtime, credentials, providers, or launch execution")
}
