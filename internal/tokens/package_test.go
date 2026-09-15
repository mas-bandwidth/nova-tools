package tokens

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/records"
)

func strPtr(s string) *string {
	return &s
}

type testBatch struct {
	Dir            string
	Coverage       records.Coverage
	CoverageBytes  []byte
	CoverageID     string
	ShardFile      string
	ShardHex       string
	MappingFile    string
	MappingHex     string
	ObservationIDs []string
	ObsLines       [][]byte
}

func newTestBatch(t *testing.T) *testBatch {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	mapBytes, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tokens", "codex", "mapping.json"))
	if err != nil {
		t.Fatalf("failed to read mapping fixture: %v", err)
	}
	mEnv, err := records.NewValidator(records.Allowlists{}).ValidateEnvelope(mapBytes)
	if err != nil {
		t.Fatalf("failed to validate mapping fixture: %v", err)
	}
	mapHex := strings.TrimPrefix(mEnv.ID, "sha256:")

	mappingsDir := filepath.Join(dir, "mappings")
	if err := os.MkdirAll(mappingsDir, 0755); err != nil {
		t.Fatal(err)
	}
	mapFile := filepath.Join(mappingsDir, mapHex+".json")
	if err := os.WriteFile(mapFile, mapBytes, 0644); err != nil {
		t.Fatal(err)
	}

	recBytes, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tokens", "codex", "expected_records.jsonl"))
	if err != nil {
		t.Fatalf("failed to read records fixture: %v", err)
	}
	lines := bytes.Split(recBytes, []byte("\n"))
	var obsLines [][]byte
	var obsIDs []string
	for _, l := range lines {
		if len(bytes.TrimSpace(l)) == 0 {
			continue
		}
		var env records.Envelope
		if err := json.Unmarshal(l, &env); err != nil {
			t.Fatal(err)
		}
		obsLines = append(obsLines, l)
		obsIDs = append(obsIDs, env.ID)
		if len(obsLines) == 2 {
			break
		}
	}
	if len(obsLines) != 2 {
		t.Fatalf("expected 2 records, got %d", len(obsLines))
	}

	shardContent := append(append([]byte(nil), obsLines[0]...), '\n')
	shardContent = append(shardContent, append(obsLines[1], '\n')...)
	shardSum := sha256.Sum256(shardContent)
	shardHex := hex.EncodeToString(shardSum[:])

	recordsDayDir := filepath.Join(dir, "records", "rowan", "studio", "2026-09-12")
	if err := os.MkdirAll(recordsDayDir, 0755); err != nil {
		t.Fatal(err)
	}
	shardFile := filepath.Join(recordsDayDir, shardHex+".jsonl")
	if err := os.WriteFile(shardFile, shardContent, 0644); err != nil {
		t.Fatal(err)
	}

	cov := records.Coverage{
		Schema:         records.SchemaCoverage,
		ScopeID:        "nova.codex-desktop.responses",
		SourceIDs:      []string{"nova.codex-desktop.responses"},
		Interval:       records.CoverageInterval{Start: "2026-09-12T00:00:00Z", End: "2026-09-13T00:00:00Z"},
		Status:         "complete_within_scope",
		Reasons:        []records.CoverageReason{},
		CollectedAt:    "2026-09-13T01:00:00Z",
		CollectorBuild: "codex-desktop@0.154.0 build=abc123",
		MappingIDs:     []string{mEnv.ID},
		Shards: []records.ShardRef{
			{
				ShardID:     "sha256:" + shardHex,
				RecordCount: "2",
				InlineIDs:   obsIDs,
			},
		},
		Predecessors: []string{},
		Counts: records.CoverageCounts{
			SourceCandidates: "2",
			RecordsEmitted:   "2",
			Observations:     "2",
			Conflicts:        "0",
			Gaps:             "0",
		},
	}

	covBytes, covID, err := records.SealCoverage(cov)
	if err != nil {
		t.Fatalf("failed to seal coverage: %v", err)
	}

	return &testBatch{
		Dir:            dir,
		Coverage:       cov,
		CoverageBytes:  covBytes,
		CoverageID:     covID,
		ShardFile:      shardFile,
		ShardHex:       shardHex,
		MappingFile:    mapFile,
		MappingHex:     mapHex,
		ObservationIDs: obsIDs,
		ObsLines:       obsLines,
	}
}

func newTestBatchWithInventory(t *testing.T) (*testBatch, string) {
	t.Helper()
	b := newTestBatch(t)

	invJSON, err := json.Marshal(b.ObservationIDs)
	if err != nil {
		t.Fatal(err)
	}
	invContent := append(invJSON, '\n')
	invSum := sha256.Sum256(invContent)
	invHex := hex.EncodeToString(invSum[:])
	invID := "sha256:" + invHex

	invDir := filepath.Join(b.Dir, "inventories")
	if err := os.MkdirAll(invDir, 0755); err != nil {
		t.Fatal(err)
	}
	invFile := filepath.Join(invDir, invHex+".json")
	if err := os.WriteFile(invFile, invContent, 0644); err != nil {
		t.Fatal(err)
	}

	b.Coverage.Shards[0].InlineIDs = nil
	b.Coverage.Shards[0].InventoryFile = &invID

	covBytes, covID, err := records.SealCoverage(b.Coverage)
	if err != nil {
		t.Fatalf("failed to seal coverage: %v", err)
	}
	b.CoverageBytes = covBytes
	b.CoverageID = covID

	return b, invFile
}

func installBatch(t *testing.T, b *testBatch) {
	t.Helper()
	batchPath := filepath.Join(b.Dir, "batch.json")
	if err := os.WriteFile(batchPath, b.CoverageBytes, 0644); err != nil {
		t.Fatalf("failed to install batch.json: %v", err)
	}
}

func checkRefusal(t *testing.T, err error, wantRules ...string) *PackageRefusal {
	t.Helper()
	if err == nil {
		t.Fatalf("expected refusal with rule in %v, got nil", wantRules)
	}
	var ref *PackageRefusal
	if !errors.As(err, &ref) {
		t.Fatalf("expected *PackageRefusal, got %T: %v", err, err)
	}
	matched := false
	for _, want := range wantRules {
		if ref.Rule == want {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("refusal rule = %q, want one of %v (detail: %s)", ref.Rule, wantRules, ref.Detail)
	}
	if ref.RefusalExitCode() != RefusalExitCode {
		t.Fatalf("exit code = %d, want %d", ref.RefusalExitCode(), RefusalExitCode)
	}
	return ref
}

// TC-VAL-01: candidate_validation_success
func TestTC_VAL_01_CandidateValidationSuccess(t *testing.T) {
	b := newTestBatch(t)
	if err := ValidateCandidateDirectory(b.Dir, "", b.CoverageBytes); err != nil {
		t.Fatalf("ValidateCandidateDirectory failed: %v", err)
	}
}

// TC-VAL-02: candidate_validation_refuses_marker
func TestTC_VAL_02_CandidateValidationRefusesMarker(t *testing.T) {
	b := newTestBatch(t)
	installBatch(t, b)
	err := ValidateCandidateDirectory(b.Dir, "", b.CoverageBytes)
	checkRefusal(t, err, RuleMarkerExists)
}

// TC-VAL-03: candidate_validation_owned_marker_temp_checked
func TestTC_VAL_03_CandidateValidationOwnedMarkerTempChecked(t *testing.T) {
	b := newTestBatch(t)
	tmpPath := filepath.Join(b.Dir, "batch.json.tmp")
	if err := os.WriteFile(tmpPath, b.CoverageBytes, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCandidateDirectory(b.Dir, "batch.json.tmp", b.CoverageBytes); err != nil {
		t.Fatalf("ValidateCandidateDirectory failed with owned marker: %v", err)
	}
}

// TC-VAL-04: candidate_validation_stray_temp_rejected
func TestTC_VAL_04_CandidateValidationStrayTempRejected(t *testing.T) {
	// Subcase 1: stray.tmp present alongside owned marker
	b := newTestBatch(t)
	if err := os.WriteFile(filepath.Join(b.Dir, "batch.json.tmp"), b.CoverageBytes, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.Dir, "stray.tmp"), []byte("stray"), 0644); err != nil {
		t.Fatal(err)
	}
	err := ValidateCandidateDirectory(b.Dir, "batch.json.tmp", b.CoverageBytes)
	checkRefusal(t, err, RuleTemporaryFilePresent)

	// Subcase 2: pre-existing batch.json.tmp when ownedMarkerTempName is empty
	b2 := newTestBatch(t)
	if err := os.WriteFile(filepath.Join(b2.Dir, "batch.json.tmp"), b2.CoverageBytes, 0644); err != nil {
		t.Fatal(err)
	}
	err2 := ValidateCandidateDirectory(b2.Dir, "", b2.CoverageBytes)
	checkRefusal(t, err2, RuleTemporaryFilePresent)
}

// TC-VAL-05: installed_validation_success
func TestTC_VAL_05_InstalledValidationSuccess(t *testing.T) {
	b := newTestBatch(t)
	installBatch(t, b)
	if err := ValidateInstalledDirectory(b.Dir); err != nil {
		t.Fatalf("ValidateInstalledDirectory failed: %v", err)
	}
}

// TC-VAL-06: installed_validation_forbids_tmp_and_extra_files
func TestTC_VAL_06_InstalledValidationForbidsTmpAndExtraFiles(t *testing.T) {
	// (a) lingering batch.json.tmp
	b := newTestBatch(t)
	installBatch(t, b)
	if err := os.WriteFile(filepath.Join(b.Dir, "batch.json.tmp"), []byte("leftover"), 0644); err != nil {
		t.Fatal(err)
	}
	err := ValidateInstalledDirectory(b.Dir)
	checkRefusal(t, err, RuleTemporaryFilePresent)

	// (b) unexpected extra file
	b2 := newTestBatch(t)
	installBatch(t, b2)
	if err := os.WriteFile(filepath.Join(b2.Dir, "extra.txt"), []byte("extra"), 0644); err != nil {
		t.Fatal(err)
	}
	err2 := ValidateInstalledDirectory(b2.Dir)
	checkRefusal(t, err2, RulePackageStrayFile)
}

// TC-VAL-07: no_clobber_destination_exists
func TestTC_VAL_07_NoClobberDestinationExists(t *testing.T) {
	b := newTestBatch(t)
	// Verify EnsureDestinationFresh refuses existing destination directory
	errFresh := EnsureDestinationFresh(b.Dir)
	checkRefusal(t, errFresh, RuleDestinationExists)

	// Verify EnsureDestinationFresh succeeds on non-existent directory
	nonExistent := filepath.Join(b.Dir, "does-not-exist")
	if err := EnsureDestinationFresh(nonExistent); err != nil {
		t.Errorf("expected nil for fresh destination, got %v", err)
	}
}

// TC-VAL-08: strict_symlink_rejection
func TestTC_VAL_08_StrictSymlinkRejection(t *testing.T) {
	b := newTestBatch(t)
	symPath := filepath.Join(b.Dir, "records", "rowan", "studio", "2026-09-12", "link.jsonl")
	if err := os.Symlink(b.ShardFile, symPath); err != nil {
		t.Fatal(err)
	}
	err := ValidateCandidateDirectory(b.Dir, "", b.CoverageBytes)
	checkRefusal(t, err, RuleSymlinkForbidden)
}

// TestOrphanShardRejected: orphan shard file not referenced in coverage is rejected
func TestOrphanShardRejected(t *testing.T) {
	b := newTestBatch(t)
	installBatch(t, b)

	orphanHex := strings.Repeat("f", 64)
	orphanFile := filepath.Join(b.Dir, "records", "rowan", "studio", "2026-09-12", orphanHex+".jsonl")
	if err := os.WriteFile(orphanFile, []byte("{\"id\":\"sha256:...\"}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := ValidateInstalledDirectory(b.Dir)
	checkRefusal(t, err, RulePackageStrayFile)
}

// TC-VAL-09: crash_recovery_incomplete_batch
func TestTC_VAL_09_CrashRecoveryIncompleteBatch(t *testing.T) {
	b := newTestBatch(t)
	// Crash during staging: leaves batch.json.tmp on disk without batch.json
	if err := os.WriteFile(filepath.Join(b.Dir, "batch.json.tmp"), b.CoverageBytes, 0644); err != nil {
		t.Fatal(err)
	}
	err := ValidateInstalledDirectory(b.Dir)
	checkRefusal(t, err, RuleMarkerMissing)
}

// TC-VAL-10: atomic_rename_competing_marker_race
func TestTC_VAL_10_AtomicRenameCompetingMarkerRace(t *testing.T) {
	b := newTestBatch(t)
	markerTemp := "batch.json.tmp"
	if err := StageMarker(b.Dir, markerTemp, b.CoverageBytes); err != nil {
		t.Fatal(err)
	}

	competingBytes := []byte("{\"competing\":true}")
	targetPath := filepath.Join(b.Dir, "batch.json")
	if err := os.WriteFile(targetPath, competingBytes, 0644); err != nil {
		t.Fatal(err)
	}

	err := CommitMarker(b.Dir, markerTemp)
	checkRefusal(t, err, RuleMarkerExists)

	// Verify existing marker was preserved byte-identical
	haveBytes, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(haveBytes, competingBytes) {
		t.Errorf("competing marker was corrupted or clobbered")
	}

	// Verify temporary marker was preserved and NOT unlinked on link failure
	if _, err := os.Stat(filepath.Join(b.Dir, markerTemp)); err != nil {
		t.Fatalf("temporary marker must be preserved on link failure, got err: %v", err)
	}

	// Discriminating witness: foreign and unrelated files cannot be targeted or unlinked
	parentDir := filepath.Dir(b.Dir)
	foreignFile := filepath.Join(parentDir, "foreign.tmp")
	foreignBytes := []byte("foreign data")
	if err := os.WriteFile(foreignFile, foreignBytes, 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(foreignFile)

	// 1. Directory traversal attempt via CommitMarker is refused; foreign file is untouched
	err = CommitMarker(b.Dir, "../foreign.tmp")
	checkRefusal(t, err, RulePackageStrayFile)
	if content, err := os.ReadFile(foreignFile); err != nil || !bytes.Equal(content, foreignBytes) {
		t.Fatalf("foreign file must remain untouched, err: %v", err)
	}

	// 2. Unrelated non-.tmp file in batch dir cannot be targeted or unlinked
	unrelatedFile := filepath.Join(b.Dir, "unrelated.txt")
	unrelatedBytes := []byte("unrelated data")
	if err := os.WriteFile(unrelatedFile, unrelatedBytes, 0644); err != nil {
		t.Fatal(err)
	}
	err = CommitMarker(b.Dir, "unrelated.txt")
	checkRefusal(t, err, RulePackageStrayFile)
	if content, err := os.ReadFile(unrelatedFile); err != nil || !bytes.Equal(content, unrelatedBytes) {
		t.Fatalf("unrelated file must remain untouched, err: %v", err)
	}

	// 3. StageMarker also rejects directory traversal and non-.tmp files
	err = StageMarker(b.Dir, "../foreign.tmp", b.CoverageBytes)
	checkRefusal(t, err, RulePackageStrayFile)
	err = StageMarker(b.Dir, "unrelated.txt", b.CoverageBytes)
	checkRefusal(t, err, RulePackageStrayFile)
}

// TC-VAL-11: durability_fsync_ancestor_directories_trace
func TestTC_VAL_11_DurabilityFsyncAncestorDirectoriesTrace(t *testing.T) {
	b := newTestBatch(t)

	var trace []string
	FsyncHook = func(path string) error {
		trace = append(trace, path)
		return nil
	}
	defer func() { FsyncHook = nil }()

	// 1. SyncDirectoryTree: bottom-up trace syncing day, bench, friend, records, mappings, up to root
	if err := SyncDirectoryTree(b.Dir); err != nil {
		t.Fatalf("SyncDirectoryTree failed: %v", err)
	}

	if len(trace) < 5 {
		t.Fatalf("expected at least 5 directories synced in tree, got %d: %v", len(trace), trace)
	}

	// Root dir must be the final directory synced in SyncDirectoryTree
	if trace[len(trace)-1] != b.Dir {
		t.Errorf("final synced dir must be root %s, got %s", b.Dir, trace[len(trace)-1])
	}

	// Deepest leaf (records/rowan/studio/2026-09-12) must appear before records/rowan/studio
	dayDir := filepath.Join(b.Dir, "records", "rowan", "studio", "2026-09-12")
	benchDir := filepath.Join(b.Dir, "records", "rowan", "studio")
	dayIdx, benchIdx := -1, -1
	for i, p := range trace {
		if p == dayDir {
			dayIdx = i
		}
		if p == benchDir {
			benchIdx = i
		}
	}
	if dayIdx == -1 || benchIdx == -1 || dayIdx > benchIdx {
		t.Errorf("bottom-up sync violation: dayIdx=%d, benchIdx=%d", dayIdx, benchIdx)
	}

	// 2. StageMarker: writes and fsyncs batch.json.tmp
	markerTemp := "batch.json.tmp"
	markerPath := filepath.Join(b.Dir, markerTemp)
	if err := StageMarker(b.Dir, markerTemp, b.CoverageBytes); err != nil {
		t.Fatalf("StageMarker failed: %v", err)
	}
	if trace[len(trace)-1] != markerPath {
		t.Errorf("expected StageMarker to sync %s, got %s", markerPath, trace[len(trace)-1])
	}

	// 3. CommitMarker: atomic no-replace rename, then fsync(root)
	if err := CommitMarker(b.Dir, markerTemp); err != nil {
		t.Fatalf("CommitMarker failed: %v", err)
	}
	if trace[len(trace)-1] != b.Dir {
		t.Errorf("expected CommitMarker to sync root %s after rename, got %s", b.Dir, trace[len(trace)-1])
	}

	// 4. Fail-stop behavior: error in FsyncHook immediately aborts
	FsyncHook = func(path string) error {
		return errors.New("simulated fsync failure")
	}
	if err := SyncDirectoryTree(b.Dir); err == nil {
		t.Fatal("expected fail-stop on simulated fsync error, got nil")
	}
}

// TC-VAL-12: shard_observation_malformed_envelope
func TestTC_VAL_12_ShardObservationMalformedEnvelope(t *testing.T) {
	// (a) Shard line contains invalid JSON
	b := newTestBatch(t)
	badJSON := []byte("{not valid json\n")
	shardSum := sha256.Sum256(badJSON)
	shardHex := hex.EncodeToString(shardSum[:])
	badFile := filepath.Join(b.Dir, "records", "rowan", "studio", "2026-09-12", shardHex+".jsonl")
	if err := os.WriteFile(badFile, badJSON, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(b.ShardFile); err != nil {
		t.Fatal(err)
	}
	b.Coverage.Shards[0].ShardID = "sha256:" + shardHex
	b.Coverage.Shards[0].RecordCount = "1"
	b.Coverage.Shards[0].InlineIDs = b.ObservationIDs[:1]
	b.Coverage.Counts.SourceCandidates = "1"
	b.Coverage.Counts.RecordsEmitted = "1"
	b.Coverage.Counts.Observations = "1"
	covBytes, _, err := records.SealCoverage(b.Coverage)
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateCandidateDirectory(b.Dir, "", covBytes)
	checkRefusal(t, err, RuleObservationMalformed)

	// (b) Shard line violates observation/2 schema (wrong schema)
	b2 := newTestBatch(t)
	badSchema := []byte(fmt.Sprintf("{\"schema\":\"bad_schema\",\"body\":{\"mapping_id\":\"%s\"}}\n", b2.Coverage.MappingIDs[0]))
	shardSum2 := sha256.Sum256(badSchema)
	shardHex2 := hex.EncodeToString(shardSum2[:])
	badFile2 := filepath.Join(b2.Dir, "records", "rowan", "studio", "2026-09-12", shardHex2+".jsonl")
	if err := os.WriteFile(badFile2, badSchema, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(b2.ShardFile); err != nil {
		t.Fatal(err)
	}
	b2.Coverage.Shards[0].ShardID = "sha256:" + shardHex2
	b2.Coverage.Shards[0].RecordCount = "1"
	b2.Coverage.Shards[0].InlineIDs = b2.ObservationIDs[:1]
	b2.Coverage.Counts.SourceCandidates = "1"
	b2.Coverage.Counts.RecordsEmitted = "1"
	b2.Coverage.Counts.Observations = "1"
	covBytes2, _, err := records.SealCoverage(b2.Coverage)
	if err != nil {
		t.Fatal(err)
	}
	err2 := ValidateCandidateDirectory(b2.Dir, "", covBytes2)
	checkRefusal(t, err2, RuleObservationMalformed)
}

// TC-VAL-13: shard_record_wrong_origin_path
func TestTC_VAL_13_ShardRecordWrongOriginPath(t *testing.T) {
	b := newTestBatch(t)

	// Move the shard directory to mismatched friend "alice"
	rowanDir := filepath.Join(b.Dir, "records", "rowan")
	aliceDir := filepath.Join(b.Dir, "records", "alice")
	if err := os.Rename(rowanDir, aliceDir); err != nil {
		t.Fatal(err)
	}

	err := ValidateCandidateDirectory(b.Dir, "", b.CoverageBytes)
	checkRefusal(t, err, RuleRecordPathOriginMismatch)
}

// TC-VAL-15: sorted_id_set_inventory_comparison
func TestTC_VAL_15_SortedIDSetInventoryComparison(t *testing.T) {
	b, invFile := newTestBatchWithInventory(t)

	// Reverse ID order in inventory JSON
	revIDs := []string{b.ObservationIDs[1], b.ObservationIDs[0]}
	revJSON, err := json.Marshal(revIDs)
	if err != nil {
		t.Fatal(err)
	}
	revContent := append(revJSON, '\n')
	revSum := sha256.Sum256(revContent)
	revHex := hex.EncodeToString(revSum[:])
	revID := "sha256:" + revHex

	if err := os.Remove(invFile); err != nil {
		t.Fatal(err)
	}
	newInvFile := filepath.Join(b.Dir, "inventories", revHex+".json")
	if err := os.WriteFile(newInvFile, revContent, 0644); err != nil {
		t.Fatal(err)
	}

	b.Coverage.Shards[0].InventoryFile = &revID
	covBytes, _, err := records.SealCoverage(b.Coverage)
	if err != nil {
		t.Fatal(err)
	}
	b.CoverageBytes = covBytes

	installBatch(t, b)
	if err := ValidateInstalledDirectory(b.Dir); err != nil {
		t.Fatalf("expected order-independent sorted ID set equality, got %v", err)
	}
}

// TC-VAL-16: link_unlink_fallback_crash_unpublishable
func TestTC_VAL_16_LinkUnlinkFallbackCrashUnpublishable(t *testing.T) {
	b := newTestBatch(t)
	installBatch(t, b)

	// Simulate crash between hard link and unlink leaving both files on disk
	tmpPath := filepath.Join(b.Dir, "batch.json.tmp")
	if err := os.WriteFile(tmpPath, b.CoverageBytes, 0644); err != nil {
		t.Fatal(err)
	}

	err := ValidateInstalledDirectory(b.Dir)
	checkRefusal(t, err, RuleTemporaryFilePresent)
}

// TC-VAL-17: retained_unreadable_turn_id_observation_valid (TC-VAL-20b)
func TestTC_VAL_17_RetainedUnsupportedSubfieldAccepted(t *testing.T) {
	m := codexMapping(t)
	// Decode an observation where turn_id is an invalid shape {"x":1}, triggering codexShapeNoTurnIDLexeme.
	lines := `{"type":"token_usage_record","response_id":"resp-t2","session_id":"t1","turn_id":{"x":1},"timestamp":"2026-09-12T00:00:00Z","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	d, err := DecodeCodexReaders(m, codexFixtureBinding, []io.Reader{strings.NewReader(lines)})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(d.Observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(d.Observations))
	}
	obs := d.Observations[0]
	if !obs.Spendable {
		t.Errorf("expected resp-t2 to be spendable, got false")
	}
	if n := d.Unsupported[codexShapeNoTurnIDLexeme]; n != 1 {
		t.Errorf("expected 1 unsupported turn_id count, got %d", n)
	}

	// Validate that receipt has turn_id omitted
	v := records.NewValidator(m.Allowlists())
	env, err := v.ValidateEnvelope(obs.Envelope)
	if err != nil {
		t.Fatalf("envelope validation failed: %v", err)
	}
	if _, hasTurnID := env.Observation.Receipt["turn_id"]; hasTurnID {
		t.Errorf("expected turn_id to be omitted from receipt, got %+v", env.Observation.Receipt)
	}

	// Build a complete batch around this single observation envelope
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mapBytes, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tokens", "codex", "mapping.json"))
	if err != nil {
		t.Fatal(err)
	}
	mEnv, err := records.NewValidator(records.Allowlists{}).ValidateEnvelope(mapBytes)
	if err != nil {
		t.Fatal(err)
	}
	mapHex := strings.TrimPrefix(mEnv.ID, "sha256:")
	mappingsDir := filepath.Join(dir, "mappings")
	os.MkdirAll(mappingsDir, 0755)
	os.WriteFile(filepath.Join(mappingsDir, mapHex+".json"), mapBytes, 0644)

	shardContent := append(append([]byte(nil), obs.Envelope...), '\n')
	shardSum := sha256.Sum256(shardContent)
	shardHex := hex.EncodeToString(shardSum[:])

	shardDir := filepath.Join(dir, "records", "rowan", "studio", "2026-09-12")
	os.MkdirAll(shardDir, 0755)
	os.WriteFile(filepath.Join(shardDir, shardHex+".jsonl"), shardContent, 0644)

	cov := records.Coverage{
		Schema:    records.SchemaCoverage,
		ScopeID:   "nova.codex-desktop.responses",
		SourceIDs: []string{"nova.codex-desktop.responses"},
		Interval:  records.CoverageInterval{Start: "2026-09-12T00:00:00Z", End: "2026-09-13T00:00:00Z"},
		Status:    "complete_within_scope",
		Reasons: []records.CoverageReason{
			{Code: "unsupported_rows", Source: strPtr("nova.codex-desktop.responses")},
		},
		CollectedAt:    "2026-09-13T01:00:00Z",
		CollectorBuild: "codex-desktop@0.154.0 build=abc123",
		MappingIDs:     []string{mEnv.ID},
		Shards: []records.ShardRef{
			{
				ShardID:       "sha256:" + shardHex,
				RecordCount:   "1",
				InlineIDs:     []string{env.ID},
				InventoryFile: nil,
			},
		},
		Counts: records.CoverageCounts{
			SourceCandidates: "1",
			RecordsEmitted:   "1",
			Observations:     "1",
			Conflicts:        "0",
			Gaps:             "1",
		},
	}
	covBytes, _, err := records.SealCoverage(cov)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "batch.json"), covBytes, 0644); err != nil {
		t.Fatal(err)
	}

	if err := ValidateInstalledDirectory(dir); err != nil {
		t.Fatalf("expected retained observation to validate in installed directory, got %v", err)
	}
}

// TC-PKG-01: package_inventory_file_valid
func TestTC_PKG_01_PackageInventoryFileValid(t *testing.T) {
	b, _ := newTestBatchWithInventory(t)
	installBatch(t, b)
	if err := ValidateInstalledDirectory(b.Dir); err != nil {
		t.Fatalf("ValidateInstalledDirectory failed: %v", err)
	}
}

// TC-PKG-02: package_unreferenced_inventory_file
func TestTC_PKG_02_PackageUnreferencedInventoryFile(t *testing.T) {
	b, _ := newTestBatchWithInventory(t)
	installBatch(t, b)

	unrefHex := strings.Repeat("c", 64)
	unrefFile := filepath.Join(b.Dir, "inventories", unrefHex+".json")
	if err := os.WriteFile(unrefFile, []byte("[\"sha256:1111111111111111111111111111111111111111111111111111111111111111\"]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := ValidateInstalledDirectory(b.Dir)
	checkRefusal(t, err, RulePackageStrayFile)
}

// TC-PKG-03: package_missing_referenced_inventory
func TestTC_PKG_03_PackageMissingReferencedInventory(t *testing.T) {
	b, invFile := newTestBatchWithInventory(t)
	installBatch(t, b)

	if err := os.Remove(invFile); err != nil {
		t.Fatal(err)
	}

	err := ValidateInstalledDirectory(b.Dir)
	checkRefusal(t, err, RulePackageStrayFile)
}

// TC-PKG-04: package_inventory_missing_trailing_lf
func TestTC_PKG_04_PackageInventoryMissingTrailingLF(t *testing.T) {
	b, invFile := newTestBatchWithInventory(t)

	raw, err := os.ReadFile(invFile)
	if err != nil {
		t.Fatal(err)
	}
	rawNoLF := bytes.TrimRight(raw, "\n")
	if err := os.WriteFile(invFile, rawNoLF, 0644); err != nil {
		t.Fatal(err)
	}

	installBatch(t, b)
	err = ValidateInstalledDirectory(b.Dir)
	checkRefusal(t, err, RuleInventoryMismatch, RuleDigestMismatch)
}

// TC-PKG-05: mapping_closure_exact_3way
func TestTC_PKG_05_MappingClosureExact3Way(t *testing.T) {
	b := newTestBatch(t)
	installBatch(t, b)
	if err := ValidateInstalledDirectory(b.Dir); err != nil {
		t.Fatalf("expected 3-way mapping closure to pass, got: %v", err)
	}
}

// TC-PKG-06: mapping_closure_orphan_package_file
func TestTC_PKG_06_MappingClosureOrphanPackageFile(t *testing.T) {
	b := newTestBatch(t)
	installBatch(t, b)

	antigravityBytes, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tokens", "antigravity", "mapping.json"))
	if err != nil {
		t.Fatal(err)
	}
	mEnv, err := records.NewValidator(records.Allowlists{}).ValidateEnvelope(antigravityBytes)
	if err != nil {
		t.Fatal(err)
	}
	extraHex := strings.TrimPrefix(mEnv.ID, "sha256:")
	extraFile := filepath.Join(b.Dir, "mappings", extraHex+".json")
	if err := os.WriteFile(extraFile, antigravityBytes, 0644); err != nil {
		t.Fatal(err)
	}

	err = ValidateInstalledDirectory(b.Dir)
	checkRefusal(t, err, RuleMappingClosureViolation)
}

// TC-PKG-07: mapping_closure_missing_package_file
func TestTC_PKG_07_MappingClosureMissingPackageFile(t *testing.T) {
	b := newTestBatch(t)

	missingID := "sha256:" + strings.Repeat("d", 64)
	b.Coverage.MappingIDs = append(b.Coverage.MappingIDs, missingID)
	covBytes, _, err := records.SealCoverage(b.Coverage)
	if err != nil {
		t.Fatal(err)
	}
	b.CoverageBytes = covBytes
	installBatch(t, b)

	err = ValidateInstalledDirectory(b.Dir)
	checkRefusal(t, err, RuleMappingClosureViolation)
}

// TC-PKG-08: mapping_closure_observation_undeclared
func TestTC_PKG_08_MappingClosureObservationUndeclared(t *testing.T) {
	b := newTestBatch(t)

	// Synthesize an observation referencing an undeclared mapping M3
	undeclaredMappingID := "sha256:" + strings.Repeat("3", 64)
	var obsEnv map[string]interface{}
	if err := json.Unmarshal(b.ObsLines[0], &obsEnv); err != nil {
		t.Fatal(err)
	}
	body := obsEnv["body"].(map[string]interface{})
	body["mapping_id"] = undeclaredMappingID

	newBodyBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	newBodySum := sha256.Sum256(newBodyBytes)
	obsEnv["id"] = "sha256:" + hex.EncodeToString(newBodySum[:])

	modLine, err := json.Marshal(obsEnv)
	if err != nil {
		t.Fatal(err)
	}
	modShard := append(modLine, '\n')
	shardSum := sha256.Sum256(modShard)
	shardHex := hex.EncodeToString(shardSum[:])

	os.Remove(b.ShardFile)
	newShardFile := filepath.Join(filepath.Dir(b.ShardFile), shardHex+".jsonl")
	if err := os.WriteFile(newShardFile, modShard, 0644); err != nil {
		t.Fatal(err)
	}

	b.Coverage.Shards[0].ShardID = "sha256:" + shardHex
	b.Coverage.Shards[0].RecordCount = "1"
	b.Coverage.Shards[0].InlineIDs = []string{obsEnv["id"].(string)}
	b.Coverage.Counts.RecordsEmitted = "1"
	b.Coverage.Counts.Observations = "1"

	covBytes, _, err := records.SealCoverage(b.Coverage)
	if err != nil {
		t.Fatal(err)
	}
	b.CoverageBytes = covBytes
	installBatch(t, b)

	err = ValidateInstalledDirectory(b.Dir)
	checkRefusal(t, err, RuleMappingClosureViolation)
}

// TC-PKG-09: mapping_closure_coverage_unused
func TestTC_PKG_09_MappingClosureCoverageUnused(t *testing.T) {
	b := newTestBatch(t)

	// Add second valid mapping to mappings/ AND to coverage.mapping_ids, but shard does not use it
	antigravityBytes, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tokens", "antigravity", "mapping.json"))
	if err != nil {
		t.Fatal(err)
	}
	mEnv, err := records.NewValidator(records.Allowlists{}).ValidateEnvelope(antigravityBytes)
	if err != nil {
		t.Fatal(err)
	}
	extraHex := strings.TrimPrefix(mEnv.ID, "sha256:")
	extraFile := filepath.Join(b.Dir, "mappings", extraHex+".json")
	if err := os.WriteFile(extraFile, antigravityBytes, 0644); err != nil {
		t.Fatal(err)
	}

	b.Coverage.MappingIDs = append(b.Coverage.MappingIDs, mEnv.ID)
	// Keep mapping IDs sorted
	if b.Coverage.MappingIDs[0] > b.Coverage.MappingIDs[1] {
		b.Coverage.MappingIDs[0], b.Coverage.MappingIDs[1] = b.Coverage.MappingIDs[1], b.Coverage.MappingIDs[0]
	}

	covBytes, _, err := records.SealCoverage(b.Coverage)
	if err != nil {
		t.Fatal(err)
	}
	b.CoverageBytes = covBytes
	installBatch(t, b)

	err = ValidateInstalledDirectory(b.Dir)
	checkRefusal(t, err, RuleMappingClosureViolation)
}

// TC-PKG-10: package_input_coverage_dir_forbidden
func TestTC_PKG_10_PackageInputCoverageDirForbidden(t *testing.T) {
	b := newTestBatch(t)
	covDir := filepath.Join(b.Dir, "coverage")
	if err := os.MkdirAll(covDir, 0755); err != nil {
		t.Fatal(err)
	}

	err := ValidateCandidateDirectory(b.Dir, "", b.CoverageBytes)
	checkRefusal(t, err, RulePackageStrayFile)
}

// TC-PKG-11: mapping_already_in_destination_ledger_packaged
func TestTC_PKG_11_MappingAlreadyInDestinationLedgerPackaged(t *testing.T) {
	b := newTestBatch(t)
	// Batch packages mapping even if destination already has an identical copy
	if err := ValidateCandidateDirectory(b.Dir, "", b.CoverageBytes); err != nil {
		t.Fatalf("ValidateCandidateDirectory failed: %v", err)
	}
}

// TC-VAL-14: shard_record_unallocated_and_underscore_paths
func TestTC_VAL_14_ShardRecordUnallocatedAndUnderscorePaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	mapBytes, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tokens", "codex", "mapping.json"))
	if err != nil {
		t.Fatal(err)
	}
	mEnv, err := records.NewValidator(records.Allowlists{}).ValidateEnvelope(mapBytes)
	if err != nil {
		t.Fatal(err)
	}
	mapHex := strings.TrimPrefix(mEnv.ID, "sha256:")

	mappingsDir := filepath.Join(dir, "mappings")
	os.MkdirAll(mappingsDir, 0755)
	os.WriteFile(filepath.Join(mappingsDir, mapHex+".json"), mapBytes, 0644)

	// Build an observation with nil friend, nil bench, and nil occurred_at
	rawPresent := func(val string) records.RawField {
		return records.RawField{Presence: "present", Value: &val, NumberKind: "integer", Unit: "tokens"}
	}
	rawAbsent := func(reason string) records.RawField {
		return records.RawField{Presence: "absent", Reason: &reason, NumberKind: "integer", Unit: "tokens"}
	}
	obs := records.Observation{
		Schema: records.SchemaObservation,
		Source: records.Source{
			Kind:      "codex_desktop",
			Namespace: "nova.codex-desktop.responses",
			SessionID: "sess-unallocated-test",
			EventKey:  []string{"resp-nil-origin"},
		},
		Kind: "turn",
		Revision: records.Revision{
			Native: nil,
			Basis:  "none",
		},
		Time: records.Times{
			OccurredAt: nil,
			Basis:      "unknown",
		},
		Origin: records.Origin{
			Friend:    nil,
			Bench:     nil,
			Basis:     "unknown",
			BindingID: nil,
		},
		Model: records.Model{
			ID:    nil,
			Basis: "unknown",
		},
		Repository: records.Repository{
			ID:       nil,
			Basis:    "unattributed",
			PolicyID: nil,
			Touched:  []string{},
		},
		RawUsage: map[string]records.RawField{
			"input_tokens":             rawPresent("10"),
			"output_tokens":            rawPresent("5"),
			"total_tokens":             rawPresent("15"),
			"cached_input_tokens":      rawAbsent("not_supplied"),
			"cache_write_input_tokens": rawAbsent("not_supplied"),
			"reasoning_output_tokens":  rawAbsent("not_supplied"),
		},
		ModelUsage: []records.ModelUsage{},
		MappingID:  mEnv.ID,
		Receipt:    map[string]string{},
	}

	v := records.NewValidator(records.Allowlists{
		RawUsageFields: []string{
			"input_tokens", "output_tokens", "total_tokens",
			"cached_input_tokens", "cache_write_input_tokens", "reasoning_output_tokens",
		},
		ReceiptFields: []string{"response_id", "turn_id"},
	})
	obsBytes, _, err := v.SealObservation(obs)
	if err != nil {
		t.Fatalf("seal observation: %v", err)
	}
	sealedEnv, err := v.ValidateEnvelope(obsBytes)
	if err != nil {
		t.Fatalf("validate sealed observation: %v", err)
	}

	shardContent := append(append([]byte(nil), obsBytes...), '\n')
	shardSum := sha256.Sum256(shardContent)
	shardHex := hex.EncodeToString(shardSum[:])

	// Shard must be located at records/_/_/unallocated/<shardHex>.jsonl
	unallocatedDir := filepath.Join(dir, "records", "_", "_", "unallocated")
	os.MkdirAll(unallocatedDir, 0755)
	shardFile := filepath.Join(unallocatedDir, shardHex+".jsonl")
	os.WriteFile(shardFile, shardContent, 0644)

	cov := records.Coverage{
		Schema:         records.SchemaCoverage,
		ScopeID:        "nova.codex-desktop.responses",
		SourceIDs:      []string{"nova.codex-desktop.responses"},
		Interval:       records.CoverageInterval{Start: "2026-09-12T00:00:00Z", End: "2026-09-13T00:00:00Z"},
		Status:         "complete_within_scope",
		Reasons:        []records.CoverageReason{},
		CollectedAt:    "2026-09-13T01:00:00Z",
		CollectorBuild: "codex-desktop@0.154.0 build=abc123",
		MappingIDs:     []string{mEnv.ID},
		Shards: []records.ShardRef{
			{
				ShardID:     "sha256:" + shardHex,
				RecordCount: "1",
				InlineIDs:   []string{sealedEnv.ID},
			},
		},
		Predecessors: []string{},
		Counts: records.CoverageCounts{
			SourceCandidates: "1",
			RecordsEmitted:   "1",
			Observations:     "1",
			Conflicts:        "0",
			Gaps:             "0",
		},
	}

	covBytes, _, err := records.SealCoverage(cov)
	if err != nil {
		t.Fatal(err)
	}

	// Validation passes: valid origin-path match on _/_/unallocated
	if err := ValidateCandidateDirectory(dir, "", covBytes); err != nil {
		t.Fatalf("expected candidate validation to pass on _/_/unallocated, got %v", err)
	}

	// Negative control: moving directory to mismatched friend returns RuleRecordPathOriginMismatch
	mismatchedDir := filepath.Join(dir, "records", "alice", "_", "unallocated")
	os.MkdirAll(filepath.Dir(mismatchedDir), 0755)
	if err := os.Rename(unallocatedDir, mismatchedDir); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(dir, "records", "_"))
	errBad := ValidateCandidateDirectory(dir, "", covBytes)
	checkRefusal(t, errBad, RuleRecordPathOriginMismatch)
}

// TestUndeclaredDirectoryUnderRecordsRejected: empty, undeclared, or hidden directories under records/ are rejected
func TestUndeclaredDirectoryUnderRecordsRejected(t *testing.T) {
	// (a) Empty records/bogus/ directory rejected
	b := newTestBatch(t)
	bogusDir := filepath.Join(b.Dir, "records", "bogus")
	if err := os.Mkdir(bogusDir, 0755); err != nil {
		t.Fatal(err)
	}
	err := ValidateCandidateDirectory(b.Dir, "", b.CoverageBytes)
	checkRefusal(t, err, RulePackageStrayFile)

	// (b) Hidden directory records/.hidden/ rejected
	b2 := newTestBatch(t)
	hiddenDir := filepath.Join(b2.Dir, "records", ".hidden")
	if err := os.Mkdir(hiddenDir, 0755); err != nil {
		t.Fatal(err)
	}
	err2 := ValidateCandidateDirectory(b2.Dir, "", b2.CoverageBytes)
	checkRefusal(t, err2, RulePackageStrayFile)

	// (c) Subdirectory at day level rejected
	b3 := newTestBatch(t)
	subDayDir := filepath.Join(b3.Dir, "records", "rowan", "studio", "2026-09-12", "extra_dir")
	if err := os.Mkdir(subDayDir, 0755); err != nil {
		t.Fatal(err)
	}
	err3 := ValidateCandidateDirectory(b3.Dir, "", b3.CoverageBytes)
	checkRefusal(t, err3, RulePackageStrayFile)
}

// TestPermissionsEnforcement: files must be 0644, directories 0755
func TestPermissionsEnforcement(t *testing.T) {
	b := newTestBatch(t)
	// Invalidate file mode
	if err := os.Chmod(b.ShardFile, 0777); err != nil {
		t.Fatal(err)
	}
	err := ValidateCandidateDirectory(b.Dir, "", b.CoverageBytes)
	checkRefusal(t, err, RulePermissionsInvalid)

	// Restore file mode and invalidate directory mode
	os.Chmod(b.ShardFile, 0644)
	shardDir := filepath.Dir(b.ShardFile)
	if err := os.Chmod(shardDir, 0700); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(shardDir, 0755)

	err2 := ValidateCandidateDirectory(b.Dir, "", b.CoverageBytes)
	checkRefusal(t, err2, RulePermissionsInvalid)
}

// TestCountEquations: records_emitted, observations, gaps, and conflicts equations
func TestCountEquations(t *testing.T) {
	// 1. records_emitted mismatch
	b := newTestBatch(t)
	b.Coverage.Counts.RecordsEmitted = "99"
	covBytes, _, err := records.SealCoverage(b.Coverage)
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateCandidateDirectory(b.Dir, "", covBytes)
	checkRefusal(t, err, RuleCountEquationMismatch)

	// 2. observations mismatch
	b2 := newTestBatch(t)
	b2.Coverage.Counts.Observations = "99"
	covBytes2, _, err := records.SealCoverage(b2.Coverage)
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateCandidateDirectory(b2.Dir, "", covBytes2)
	checkRefusal(t, err, RuleCountEquationMismatch)

	// 3. conflicts mismatch
	b3 := newTestBatch(t)
	b3.Coverage.Counts.Conflicts = "1"
	covBytes3, _, err := records.SealCoverage(b3.Coverage)
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateCandidateDirectory(b3.Dir, "", covBytes3)
	checkRefusal(t, err, RuleCountEquationMismatch)
}

// TestEnvelopeMalformedRefused asserts RuleEnvelopeMalformed when envelope json is malformed
// or envelope schema is incorrect for batch.json / candidate coverage or mappings.
func TestEnvelopeMalformedRefused(t *testing.T) {
	b := newTestBatch(t)

	// 1. Candidate coverage bytes: malformed JSON
	err := ValidateCandidateDirectory(b.Dir, "", []byte("{not json"))
	checkRefusal(t, err, RuleEnvelopeMalformed)

	// 2. Candidate coverage bytes: wrong schema (observation instead of coverage)
	obsBytes := []byte(`{"schema":"nova.tokens.observation/2","data":{}}`)
	err = ValidateCandidateDirectory(b.Dir, "", obsBytes)
	checkRefusal(t, err, RuleEnvelopeMalformed)

	// 3. Installed batch: malformed mapping envelope
	b2 := newTestBatch(t)
	installBatch(t, b2)
	mappingFile := filepath.Join(b2.Dir, "mappings", b2.MappingHex+".json")
	if err := os.WriteFile(mappingFile, []byte("invalid json"), 0644); err != nil {
		t.Fatal(err)
	}
	err = ValidateInstalledDirectory(b2.Dir)
	checkRefusal(t, err, RuleEnvelopeMalformed)

	// 4. Installed batch: mapping envelope with wrong schema
	b3 := newTestBatch(t)
	installBatch(t, b3)
	mappingFile3 := filepath.Join(b3.Dir, "mappings", b3.MappingHex+".json")
	if err := os.WriteFile(mappingFile3, []byte(`{"schema":"nova.tokens.coverage/2","data":{}}`), 0644); err != nil {
		t.Fatal(err)
	}
	err = ValidateInstalledDirectory(b3.Dir)
	checkRefusal(t, err, RuleEnvelopeMalformed)
}

// TestAtomicNoReplaceRenameLinkFailurePermissionsInvalid asserts that non-EEXIST link failures
// map to RulePermissionsInvalid.
func TestAtomicNoReplaceRenameLinkFailurePermissionsInvalid(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "test.tmp")
	if err := os.WriteFile(src, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tmpDir, "nonexistent-dir", "batch.json")
	err := AtomicNoReplaceRename(src, target)
	checkRefusal(t, err, RulePermissionsInvalid)
}
