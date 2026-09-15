package tokens

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/records"
)

// Refusal rule constants for package-level validation.
const (
	RuleSymlinkForbidden         = "symlink_forbidden"
	RuleTemporaryFilePresent     = "temporary_file_present"
	RulePackageStrayFile         = "package_stray_file"
	RuleMarkerMissing            = "marker_missing"
	RuleMarkerExists             = "marker_exists"
	RuleEnvelopeMalformed        = "envelope_malformed"
	RuleObservationMalformed     = "observation_malformed"
	RuleRecordPathOriginMismatch = "record_path_origin_mismatch"
	RuleMappingClosureViolation  = "mapping_closure_violation"
	RuleShardCountMismatch       = "shard_count_mismatch"
	RuleDigestMismatch           = "digest_mismatch"
	RuleInventoryMismatch        = "inventory_mismatch"
	RuleDestinationExists        = "destination_exists"
	RulePermissionsInvalid       = "permissions_invalid"
	RuleCountEquationMismatch    = "count_equation_mismatch"

	// Granular rule aliases aligning with the proposal specifications and test suites.
	RulePackageOrphanMapping         = RuleMappingClosureViolation
	RulePackageMissingMapping        = RuleMappingClosureViolation
	RuleObservationUndeclaredMapping = RuleMappingClosureViolation
	RuleCoverageUnusedMapping        = RuleMappingClosureViolation
	RuleInventoryEncoding            = RuleInventoryMismatch
	RuleCoverageGapsInvariant        = RuleCountEquationMismatch
	RuleCountMismatch                = RuleCountEquationMismatch
	RulePackageFileMode              = RulePermissionsInvalid
)

// RefusalExitCode is the exit status (2) mandated for all package refusals.
const RefusalExitCode = 2

// PackageRefusal is the error type returned whenever package directory structure,
// envelopes, mappings, inventories, or counts fail validation.
type PackageRefusal struct {
	Rule   string
	Path   string
	Detail string
}

func (r *PackageRefusal) Error() string {
	rule := cleanRefusalStr(r.Rule, 64)
	p := cleanRefusalStr(r.Path, 256)
	d := cleanRefusalStr(r.Detail, 256)
	if d == "" {
		return fmt.Sprintf("refused: %s at %s", rule, p)
	}
	return fmt.Sprintf("refused: %s at %s (%s)", rule, p, d)
}

func (r *PackageRefusal) RefusalExitCode() int {
	return RefusalExitCode
}

func cleanRefusalStr(s string, max int) string {
	var b strings.Builder
	truncated := false
	for _, r := range s {
		switch {
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f, r == 0x2028, r == 0x2029:
			continue
		}
		if b.Len()+utf8.RuneLen(r) > max {
			truncated = true
			break
		}
		b.WriteRune(r)
	}
	if truncated {
		return b.String() + "..."
	}
	return b.String()
}

func isContentID(s string) bool {
	return strings.HasPrefix(s, "sha256:") && len(s) == 71 && isLowercaseHex(s[7:])
}

func isLowercaseHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// FsyncHook is an optional hook invoked during directory and file syncs, allowing tests
// to trace and verify durability sync ordering and fail-stop behavior.
var FsyncHook func(path string) error

// FsyncDirectory flushes the directory dentry to stable storage.
func FsyncDirectory(dirPath string) error {
	if FsyncHook != nil {
		if err := FsyncHook(dirPath); err != nil {
			return err
		}
	}
	d, err := os.Open(dirPath)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// EnsureDestinationFresh verifies that dir does not exist prior to collection.
// If dir exists, it returns a PackageRefusal with RuleDestinationExists.
func EnsureDestinationFresh(dir string) error {
	if _, err := os.Lstat(dir); err == nil {
		return &PackageRefusal{
			Rule:   RuleDestinationExists,
			Path:   dir,
			Detail: "destination directory already exists",
		}
	}
	return nil
}

// AtomicNoReplaceRename renames tmpPath to targetPath without replacing targetPath.
// On POSIX platforms, this implements the portable link/unlink no-replace idiom:
// os.Link atomically fails with EEXIST if targetPath already exists, ensuring no
// pre-existing marker is ever clobbered.
// If targetPath already exists, it atomically fails and returns a PackageRefusal with RuleMarkerExists.
func AtomicNoReplaceRename(tmpPath, targetPath string) error {
	err := os.Link(tmpPath, targetPath)
	if err != nil {
		if os.IsExist(err) || errors.Is(err, fs.ErrExist) {
			return &PackageRefusal{
				Rule:   RuleMarkerExists,
				Path:   targetPath,
				Detail: "destination marker already exists",
			}
		}
		return &PackageRefusal{
			Rule:   RulePermissionsInvalid,
			Path:   targetPath,
			Detail: fmt.Sprintf("cannot link marker: %v", err),
		}
	}
	if err := syscall.Unlink(tmpPath); err != nil {
		return &PackageRefusal{
			Rule:   RuleTemporaryFilePresent,
			Path:   tmpPath,
			Detail: fmt.Sprintf("cannot unlink temporary marker: %v", err),
		}
	}
	return nil
}

// StageMarker writes candidateBatchBytes to filepath.Join(dir, markerTempName), syncs and closes it.
func StageMarker(dir string, markerTempName string, candidateBatchBytes []byte) error {
	if filepath.Base(markerTempName) != markerTempName || !strings.HasSuffix(markerTempName, ".tmp") {
		return &PackageRefusal{
			Rule:   RulePackageStrayFile,
			Path:   filepath.Join(dir, markerTempName),
			Detail: "marker temp name must be a simple filename ending in .tmp",
		}
	}
	markerPath := filepath.Join(dir, markerTempName)
	f, err := os.OpenFile(markerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) || errors.Is(err, fs.ErrExist) {
			return &PackageRefusal{
				Rule:   RuleTemporaryFilePresent,
				Path:   markerPath,
				Detail: "temporary marker file already exists",
			}
		}
		return err
	}
	if _, err := f.Write(candidateBatchBytes); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if FsyncHook != nil {
		if err := FsyncHook(markerPath); err != nil {
			return err
		}
	}
	return nil
}

// CommitMarker atomically renames markerTempName to batch.json using atomic no-replace,
// and syncs the directory root.
func CommitMarker(dir string, markerTempName string) error {
	if filepath.Base(markerTempName) != markerTempName || !strings.HasSuffix(markerTempName, ".tmp") {
		return &PackageRefusal{
			Rule:   RulePackageStrayFile,
			Path:   filepath.Join(dir, markerTempName),
			Detail: "marker temp name must be a simple filename ending in .tmp",
		}
	}
	tmpPath := filepath.Join(dir, markerTempName)
	targetPath := filepath.Join(dir, "batch.json")
	if err := AtomicNoReplaceRename(tmpPath, targetPath); err != nil {
		return err
	}
	return FsyncDirectory(dir)
}

// SyncDirectoryTree walks all subdirectories of dir and fsyncs them bottom-up
// (deepest leaf directories first, up to dir itself).
func SyncDirectoryTree(dir string) error {
	var dirs []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return err
	}

	sort.Slice(dirs, func(i, j int) bool {
		relI, _ := filepath.Rel(dir, dirs[i])
		relJ, _ := filepath.Rel(dir, dirs[j])
		depthI := 0
		if relI != "." {
			depthI = len(strings.Split(relI, string(filepath.Separator)))
		}
		depthJ := 0
		if relJ != "." {
			depthJ = len(strings.Split(relJ, string(filepath.Separator)))
		}
		if depthI != depthJ {
			return depthI > depthJ
		}
		return dirs[i] > dirs[j]
	})

	for _, d := range dirs {
		if err := FsyncDirectory(d); err != nil {
			return err
		}
	}
	return nil
}

// ValidateCandidateDirectory verifies a prepared batch directory BEFORE batch.json is installed.
func ValidateCandidateDirectory(dir string, ownedMarkerTempName string, candidateBatchBytes []byte) error {
	return validatePackage(dir, true, ownedMarkerTempName, candidateBatchBytes)
}

// ValidateInstalledDirectory verifies a completed batch directory containing batch.json.
func ValidateInstalledDirectory(dir string) error {
	return validatePackage(dir, false, "", nil)
}

func validatePackage(dir string, isCandidate bool, ownedMarkerTempName string, candidateBatchBytes []byte) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return &PackageRefusal{Rule: RuleSymlinkForbidden, Path: dir, Detail: "package root is a symlink"}
	}
	if !fi.IsDir() {
		return &PackageRefusal{Rule: RulePackageStrayFile, Path: dir, Detail: "package root must be a directory"}
	}
	if fi.Mode().Perm() != 0755 {
		return &PackageRefusal{Rule: RulePermissionsInvalid, Path: dir, Detail: fmt.Sprintf("expected dir permissions 0755, got 0%o", fi.Mode().Perm())}
	}

	batchPath := filepath.Join(dir, "batch.json")
	batchFi, batchErr := os.Lstat(batchPath)
	if isCandidate {
		if batchErr == nil {
			return &PackageRefusal{
				Rule:   RuleMarkerExists,
				Path:   batchPath,
				Detail: "batch.json must not exist in candidate directory",
			}
		}
	} else {
		if batchErr != nil {
			return &PackageRefusal{
				Rule:   RuleMarkerMissing,
				Path:   batchPath,
				Detail: "batch.json marker missing from installed directory",
			}
		}
		if batchFi.Mode()&os.ModeSymlink != 0 {
			return &PackageRefusal{Rule: RuleSymlinkForbidden, Path: batchPath, Detail: "batch.json must not be a symlink"}
		}
		if !batchFi.Mode().IsRegular() {
			return &PackageRefusal{Rule: RulePackageStrayFile, Path: batchPath, Detail: "batch.json must be a regular file"}
		}
		if batchFi.Mode().Perm() != 0644 {
			return &PackageRefusal{Rule: RulePermissionsInvalid, Path: batchPath, Detail: fmt.Sprintf("expected file permissions 0644, got 0%o", batchFi.Mode().Perm())}
		}
		raw, err := os.ReadFile(batchPath)
		if err != nil {
			return err
		}
		candidateBatchBytes = raw
	}

	if isCandidate && ownedMarkerTempName != "" {
		ownedPath := filepath.Join(dir, ownedMarkerTempName)
		if ownedFi, err := os.Lstat(ownedPath); err == nil {
			if ownedFi.Mode()&os.ModeSymlink != 0 {
				return &PackageRefusal{Rule: RuleSymlinkForbidden, Path: ownedPath, Detail: "owned marker temp must not be a symlink"}
			}
			if !ownedFi.Mode().IsRegular() {
				return &PackageRefusal{Rule: RulePackageStrayFile, Path: ownedPath, Detail: "owned marker temp must be a regular file"}
			}
			if ownedFi.Mode().Perm() != 0644 {
				return &PackageRefusal{Rule: RulePermissionsInvalid, Path: ownedPath, Detail: fmt.Sprintf("expected file permissions 0644, got 0%o", ownedFi.Mode().Perm())}
			}
		}
	}

	// Walk directory tree to enforce permissions, no symlinks, and no temporary files.
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(dir, path)
		if relPath == "." {
			return nil
		}

		entryFi, err := os.Lstat(path)
		if err != nil {
			return err
		}

		if strings.HasPrefix(entryFi.Name(), ".") {
			return &PackageRefusal{Rule: RulePackageStrayFile, Path: relPath, Detail: "hidden entries are forbidden in package: " + relPath}
		}

		if entryFi.Mode()&os.ModeSymlink != 0 {
			return &PackageRefusal{Rule: RuleSymlinkForbidden, Path: relPath, Detail: "symlinks are forbidden in package"}
		}

		if strings.HasSuffix(entryFi.Name(), ".tmp") {
			if isCandidate && ownedMarkerTempName != "" && relPath == ownedMarkerTempName {
				// Permitted owned marker temp in candidate directory
			} else {
				return &PackageRefusal{Rule: RuleTemporaryFilePresent, Path: relPath, Detail: "temporary file is forbidden in package"}
			}
		}

		if entryFi.IsDir() {
			if entryFi.Mode().Perm() != 0755 {
				return &PackageRefusal{Rule: RulePermissionsInvalid, Path: relPath, Detail: fmt.Sprintf("expected dir permissions 0755, got 0%o", entryFi.Mode().Perm())}
			}
		} else {
			if entryFi.Mode().Perm() != 0644 {
				return &PackageRefusal{Rule: RulePermissionsInvalid, Path: relPath, Detail: fmt.Sprintf("expected file permissions 0644, got 0%o", entryFi.Mode().Perm())}
			}
		}

		// Top-level strict boundaries
		if filepath.Dir(relPath) == "." {
			switch entryFi.Name() {
			case "records", "mappings":
				if !entryFi.IsDir() {
					return &PackageRefusal{Rule: RulePackageStrayFile, Path: relPath, Detail: entryFi.Name() + " must be a directory"}
				}
			case "inventories":
				if !entryFi.IsDir() {
					return &PackageRefusal{Rule: RulePackageStrayFile, Path: relPath, Detail: "inventories must be a directory"}
				}
			case "batch.json":
				if isCandidate {
					return &PackageRefusal{Rule: RuleMarkerExists, Path: relPath, Detail: "batch.json must not exist in candidate directory"}
				}
			default:
				if isCandidate && ownedMarkerTempName != "" && entryFi.Name() == ownedMarkerTempName {
					// allowed
				} else if entryFi.Name() == "coverage" {
					return &PackageRefusal{Rule: RulePackageStrayFile, Path: relPath, Detail: "coverage replica directory is forbidden in package"}
				} else {
					return &PackageRefusal{Rule: RulePackageStrayFile, Path: relPath, Detail: "unexpected entry in package: " + relPath}
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Check that required directories records and mappings exist
	for _, req := range []string{"records", "mappings"} {
		reqPath := filepath.Join(dir, req)
		reqFi, err := os.Lstat(reqPath)
		if err != nil || !reqFi.IsDir() {
			return &PackageRefusal{Rule: RulePackageStrayFile, Path: req, Detail: req + " directory is required in package"}
		}
	}

	// Validate coverage envelope
	covValidator := records.NewValidator(records.Allowlists{})
	env, err := covValidator.ValidateEnvelope(candidateBatchBytes)
	if err != nil {
		var ref *records.Refusal
		if errors.As(err, &ref) {
			if ref.Field == "body.counts.gaps" || strings.HasPrefix(ref.Field, "body.counts.") {
				return &PackageRefusal{Rule: RuleCountEquationMismatch, Path: "batch.json", Detail: ref.Error()}
			}
			if ref.Rule == records.RuleShardReference {
				return &PackageRefusal{Rule: RuleShardCountMismatch, Path: "batch.json", Detail: ref.Error()}
			}
			return &PackageRefusal{Rule: RuleEnvelopeMalformed, Path: "batch.json", Detail: ref.Error()}
		}
		return &PackageRefusal{Rule: RuleEnvelopeMalformed, Path: "batch.json", Detail: err.Error()}
	}
	if env.Coverage == nil || env.Coverage.Schema != records.SchemaCoverage {
		return &PackageRefusal{Rule: RuleEnvelopeMalformed, Path: "batch.json", Detail: "envelope is not nova.tokens.coverage/2"}
	}
	cov := env.Coverage

	// Validate mappings in mappings/
	mappingsDir := filepath.Join(dir, "mappings")
	mEntries, err := os.ReadDir(mappingsDir)
	if err != nil {
		return err
	}
	packagedMappings := make(map[string]*records.Envelope)
	for _, e := range mEntries {
		mRelPath := filepath.Join("mappings", e.Name())
		mFullPath := filepath.Join(mappingsDir, e.Name())
		mFi, err := os.Lstat(mFullPath)
		if err != nil {
			return err
		}
		if strings.HasPrefix(e.Name(), ".") {
			return &PackageRefusal{Rule: RulePackageStrayFile, Path: mRelPath, Detail: "hidden entries forbidden in mappings"}
		}
		if mFi.Mode()&os.ModeSymlink != 0 {
			return &PackageRefusal{Rule: RuleSymlinkForbidden, Path: mRelPath, Detail: "mapping file must not be a symlink"}
		}
		if mFi.IsDir() {
			return &PackageRefusal{Rule: RulePackageStrayFile, Path: mRelPath, Detail: "subdirectories forbidden in mappings"}
		}
		if !strings.HasSuffix(e.Name(), ".json") {
			return &PackageRefusal{Rule: RulePackageStrayFile, Path: mRelPath, Detail: "mapping file must end with .json"}
		}
		mHex := strings.TrimSuffix(e.Name(), ".json")
		if len(mHex) != 64 || !isLowercaseHex(mHex) {
			return &PackageRefusal{Rule: RulePackageStrayFile, Path: mRelPath, Detail: "mapping filename must be 64-hex digits"}
		}
		rawMapping, err := os.ReadFile(mFullPath)
		if err != nil {
			return err
		}
		mEnv, err := covValidator.ValidateEnvelope(rawMapping)
		if err != nil {
			var ref *records.Refusal
			if errors.As(err, &ref) {
				return &PackageRefusal{Rule: RuleEnvelopeMalformed, Path: mRelPath, Detail: ref.Error()}
			}
			return &PackageRefusal{Rule: RuleEnvelopeMalformed, Path: mRelPath, Detail: err.Error()}
		}
		if mEnv.Mapping == nil || mEnv.Mapping.Schema != records.SchemaMapping {
			return &PackageRefusal{Rule: RuleEnvelopeMalformed, Path: mRelPath, Detail: "envelope is not nova.tokens.mapping/2"}
		}
		wantHex := strings.TrimPrefix(mEnv.ID, "sha256:")
		if mHex != wantHex {
			return &PackageRefusal{Rule: RuleDigestMismatch, Path: mRelPath, Detail: fmt.Sprintf("filename hex %s does not match mapping CID hex %s", mHex, wantHex)}
		}
		packagedMappings[mEnv.ID] = mEnv
	}

	// Tripartite closure check part 1: packaged mappings vs coverage mapping_ids
	covMappingIDs := make(map[string]bool, len(cov.MappingIDs))
	for _, mid := range cov.MappingIDs {
		covMappingIDs[mid] = true
	}
	for pid := range packagedMappings {
		if !covMappingIDs[pid] {
			return &PackageRefusal{
				Rule:   RuleMappingClosureViolation,
				Path:   filepath.Join("mappings", strings.TrimPrefix(pid, "sha256:")+".json"),
				Detail: "orphan mapping in package: " + pid,
			}
		}
	}
	for _, mid := range cov.MappingIDs {
		if _, ok := packagedMappings[mid]; !ok {
			return &PackageRefusal{
				Rule:   RuleMappingClosureViolation,
				Path:   filepath.Join("mappings", strings.TrimPrefix(mid, "sha256:")+".json"),
				Detail: "missing mapping declared in coverage: " + mid,
			}
		}
	}

	// Validate records/ layout and collect shard files
	recordsDir := filepath.Join(dir, "records")
	foundShards := make(map[string]string) // shardID -> relPath
	dirsFound := make(map[string]bool)

	err = filepath.WalkDir(recordsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relFromRecords, _ := filepath.Rel(recordsDir, path)
		if relFromRecords == "." {
			return nil
		}
		relPath := filepath.Join("records", relFromRecords)
		if strings.HasPrefix(d.Name(), ".") {
			return &PackageRefusal{Rule: RulePackageStrayFile, Path: relPath, Detail: "hidden entries forbidden in records tree"}
		}
		parts := strings.Split(relFromRecords, string(filepath.Separator))
		entryFi, err := os.Lstat(path)
		if err != nil {
			return err
		}

		if entryFi.IsDir() {
			dirsFound[relFromRecords] = true
			if len(parts) >= 4 {
				return &PackageRefusal{Rule: RulePackageStrayFile, Path: relPath, Detail: "subdirectories forbidden in shard day level"}
			}
		} else {
			if len(parts) < 4 {
				return &PackageRefusal{Rule: RulePackageStrayFile, Path: relPath, Detail: "stray file in records before day level"}
			} else if len(parts) == 4 {
				if !strings.HasSuffix(entryFi.Name(), ".jsonl") {
					return &PackageRefusal{Rule: RulePackageStrayFile, Path: relPath, Detail: "shard file must end with .jsonl"}
				}
				shardHex := strings.TrimSuffix(entryFi.Name(), ".jsonl")
				if len(shardHex) != 64 || !isLowercaseHex(shardHex) {
					return &PackageRefusal{Rule: RulePackageStrayFile, Path: relPath, Detail: "shard filename must be 64-hex digits"}
				}
				shardID := "sha256:" + shardHex
				if _, exists := foundShards[shardID]; exists {
					return &PackageRefusal{Rule: RulePackageStrayFile, Path: relPath, Detail: "duplicate shard file on disk: " + shardID}
				}
				foundShards[shardID] = relPath
			} else {
				return &PackageRefusal{Rule: RulePackageStrayFile, Path: relPath, Detail: "records directory exceeds maximum depth"}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Verify all directories found under records/ correspond to valid directory prefixes of foundShards
	validRecordsDirs := make(map[string]bool)
	for _, relPath := range foundShards {
		shardRelFromRecords, _ := filepath.Rel("records", relPath)
		pParts := strings.Split(filepath.Dir(shardRelFromRecords), string(filepath.Separator))
		if len(pParts) == 3 {
			validRecordsDirs[pParts[0]] = true
			validRecordsDirs[filepath.Join(pParts[0], pParts[1])] = true
			validRecordsDirs[filepath.Join(pParts[0], pParts[1], pParts[2])] = true
		}
	}
	for dRel := range dirsFound {
		if !validRecordsDirs[dRel] {
			return &PackageRefusal{
				Rule:   RulePackageStrayFile,
				Path:   filepath.Join("records", dRel),
				Detail: "undeclared or empty directory in records tree: " + dRel,
			}
		}
	}

	// Check shard reference completeness and orphan shard detection
	covShardsMap := make(map[string]records.ShardRef, len(cov.Shards))
	for _, s := range cov.Shards {
		covShardsMap[s.ShardID] = s
	}
	for sid, relPath := range foundShards {
		if _, ok := covShardsMap[sid]; !ok {
			return &PackageRefusal{
				Rule:   RulePackageStrayFile,
				Path:   relPath,
				Detail: "orphan shard file not referenced in coverage: " + sid,
			}
		}
	}
	for _, s := range cov.Shards {
		if _, ok := foundShards[s.ShardID]; !ok {
			return &PackageRefusal{
				Rule:   RulePackageStrayFile,
				Path:   "records",
				Detail: "referenced shard missing on disk: " + s.ShardID,
			}
		}
	}

	// Validate shards, observations, origins, and inventory sets
	allObservationIDs := make(map[string]bool)
	observedMappingIDs := make(map[string]bool)
	shardObsIDsMap := make(map[string][]string)
	spendKeyObsMap := make(map[string]map[string]bool)
	totalShardRecords := 0

	for _, sRef := range cov.Shards {
		shardRelPath := foundShards[sRef.ShardID]
		shardFullPath := filepath.Join(dir, shardRelPath)
		parts := strings.Split(shardRelPath, string(filepath.Separator))
		dirFriend := parts[1]
		dirBench := parts[2]
		dirDay := parts[3]

		rawBytes, err := os.ReadFile(shardFullPath)
		if err != nil {
			return err
		}

		// Byte SHA256 matches shard_id
		sum := sha256.Sum256(rawBytes)
		fileHex := hex.EncodeToString(sum[:])
		expectedHex := strings.TrimPrefix(sRef.ShardID, "sha256:")
		if fileHex != expectedHex {
			return &PackageRefusal{
				Rule:   RuleDigestMismatch,
				Path:   shardRelPath,
				Detail: fmt.Sprintf("shard byte sha256 %s does not match shard_id %s", fileHex, expectedHex),
			}
		}

		// Check newline termination and non-empty lines
		if len(rawBytes) == 0 || rawBytes[len(rawBytes)-1] != '\n' {
			return &PackageRefusal{
				Rule:   RuleObservationMalformed,
				Path:   shardRelPath,
				Detail: "shard file must end in newline",
			}
		}
		rawLines := bytes.Split(rawBytes[:len(rawBytes)-1], []byte("\n"))
		for i, l := range rawLines {
			if len(l) == 0 {
				return &PackageRefusal{
					Rule:   RuleObservationMalformed,
					Path:   shardRelPath,
					Detail: fmt.Sprintf("shard line %d is empty", i+1),
				}
			}
		}

		wantCount, err := strconv.Atoi(sRef.RecordCount)
		if err != nil {
			return &PackageRefusal{
				Rule:   RuleShardCountMismatch,
				Path:   shardRelPath,
				Detail: "invalid record_count in shard reference",
			}
		}
		if len(rawLines) != wantCount {
			return &PackageRefusal{
				Rule:   RuleShardCountMismatch,
				Path:   shardRelPath,
				Detail: fmt.Sprintf("shard line count %d does not match record_count %d", len(rawLines), wantCount),
			}
		}
		totalShardRecords += len(rawLines)

		var thisShardObsIDs []string
		for lineIdx, line := range rawLines {
			lineNum := lineIdx + 1

			var probe struct {
				Body struct {
					MappingID string `json:"mapping_id"`
				} `json:"body"`
			}
			if err := json.Unmarshal(line, &probe); err != nil {
				return &PackageRefusal{
					Rule:   RuleObservationMalformed,
					Path:   shardRelPath,
					Detail: fmt.Sprintf("line %d is not valid JSON: %v", lineNum, err),
				}
			}
			mID := probe.Body.MappingID
			if mID == "" {
				return &PackageRefusal{
					Rule:   RuleObservationMalformed,
					Path:   shardRelPath,
					Detail: fmt.Sprintf("line %d missing mapping_id", lineNum),
				}
			}

			mEnv, ok := packagedMappings[mID]
			if !ok {
				return &PackageRefusal{
					Rule:   RuleMappingClosureViolation,
					Path:   shardRelPath,
					Detail: fmt.Sprintf("line %d observation references undeclared mapping: %s", lineNum, mID),
				}
			}

			var fields []string
			for f := range mEnv.Mapping.FieldRules {
				fields = append(fields, f)
			}
			sort.Strings(fields)
			obsValidator := records.NewValidator(records.Allowlists{
				RawUsageFields: fields,
				ReceiptFields:  mEnv.Mapping.IdentityRule.ReceiptFields,
			})

			obsEnv, err := obsValidator.ValidateEnvelope(line)
			if err != nil {
				return &PackageRefusal{
					Rule:   RuleObservationMalformed,
					Path:   shardRelPath,
					Detail: fmt.Sprintf("line %d: %v", lineNum, err),
				}
			}
			if obsEnv.Observation == nil || obsEnv.Observation.Schema != records.SchemaObservation {
				return &PackageRefusal{
					Rule:   RuleObservationMalformed,
					Path:   shardRelPath,
					Detail: fmt.Sprintf("line %d envelope is not nova.tokens.observation/2", lineNum),
				}
			}

			obs := obsEnv.Observation

			wantFriend := "_"
			if obs.Origin.Friend != nil && *obs.Origin.Friend != "" {
				wantFriend = *obs.Origin.Friend
			}
			wantBench := "_"
			if obs.Origin.Bench != nil && *obs.Origin.Bench != "" {
				wantBench = *obs.Origin.Bench
			}
			wantDay := CodexShardDay(obs.Time.OccurredAt)

			if dirFriend != wantFriend || dirBench != wantBench || dirDay != wantDay {
				return &PackageRefusal{
					Rule:   RuleRecordPathOriginMismatch,
					Path:   shardRelPath,
					Detail: fmt.Sprintf("line %d observation origin %s/%s/%s does not match shard directory %s/%s/%s", lineNum, wantFriend, wantBench, wantDay, dirFriend, dirBench, dirDay),
				}
			}

			allObservationIDs[obsEnv.ID] = true
			observedMappingIDs[obs.MappingID] = true
			thisShardObsIDs = append(thisShardObsIDs, obsEnv.ID)

			spendKey := obs.Source.Namespace + "\x00" + strings.Join(obs.Source.EventKey, "\x00")
			if spendKeyObsMap[spendKey] == nil {
				spendKeyObsMap[spendKey] = make(map[string]bool)
			}
			spendKeyObsMap[spendKey][obsEnv.ID] = true
		}

		shardObsIDsMap[sRef.ShardID] = thisShardObsIDs
	}

	// Tripartite closure check part 2: observations vs coverage mapping_ids
	for oid := range observedMappingIDs {
		if !covMappingIDs[oid] {
			return &PackageRefusal{
				Rule:   RuleMappingClosureViolation,
				Path:   "records",
				Detail: "observation references undeclared mapping: " + oid,
			}
		}
	}
	for _, mid := range cov.MappingIDs {
		if !observedMappingIDs[mid] {
			return &PackageRefusal{
				Rule:   RuleMappingClosureViolation,
				Path:   "batch.json",
				Detail: "coverage declares unused mapping: " + mid,
			}
		}
	}

	// Inventories validation
	referencedInventories := make(map[string]bool)
	inventoriesDir := filepath.Join(dir, "inventories")

	for _, sRef := range cov.Shards {
		thisObsIDs := append([]string(nil), shardObsIDsMap[sRef.ShardID]...)
		sort.Strings(thisObsIDs)

		if sRef.InventoryFile != nil {
			invID := *sRef.InventoryFile
			invHex := strings.TrimPrefix(invID, "sha256:")
			if len(invHex) != 64 || !isLowercaseHex(invHex) {
				return &PackageRefusal{Rule: RuleInventoryMismatch, Path: "batch.json", Detail: "invalid inventory_file CID: " + invID}
			}
			invRelPath := filepath.Join("inventories", invHex+".json")
			invFullPath := filepath.Join(dir, invRelPath)
			invFi, err := os.Lstat(invFullPath)
			if err != nil {
				return &PackageRefusal{Rule: RulePackageStrayFile, Path: invRelPath, Detail: "referenced inventory file missing on disk"}
			}
			if invFi.Mode()&os.ModeSymlink != 0 {
				return &PackageRefusal{Rule: RuleSymlinkForbidden, Path: invRelPath, Detail: "inventory file must not be a symlink"}
			}
			if !invFi.Mode().IsRegular() {
				return &PackageRefusal{Rule: RulePackageStrayFile, Path: invRelPath, Detail: "inventory must be a regular file"}
			}
			if invFi.Mode().Perm() != 0644 {
				return &PackageRefusal{Rule: RulePermissionsInvalid, Path: invRelPath, Detail: fmt.Sprintf("expected inventory file permissions 0644, got 0%o", invFi.Mode().Perm())}
			}

			invBytes, err := os.ReadFile(invFullPath)
			if err != nil {
				return err
			}
			if len(invBytes) == 0 || invBytes[len(invBytes)-1] != '\n' {
				return &PackageRefusal{Rule: RuleInventoryMismatch, Path: invRelPath, Detail: "inventory file must end in newline"}
			}
			invSum := sha256.Sum256(invBytes)
			if hex.EncodeToString(invSum[:]) != invHex {
				return &PackageRefusal{Rule: RuleDigestMismatch, Path: invRelPath, Detail: "inventory byte sha256 does not match filename"}
			}

			var invCIDs []string
			if err := json.Unmarshal(invBytes, &invCIDs); err != nil {
				return &PackageRefusal{Rule: RuleInventoryMismatch, Path: invRelPath, Detail: "inventory is not valid JSON array: " + err.Error()}
			}
			for _, cid := range invCIDs {
				if !isContentID(cid) {
					return &PackageRefusal{Rule: RuleInventoryMismatch, Path: invRelPath, Detail: "invalid CID in inventory: " + cid}
				}
			}

			sortedInv := append([]string(nil), invCIDs...)
			sort.Strings(sortedInv)

			if len(sortedInv) != len(thisObsIDs) {
				return &PackageRefusal{Rule: RuleInventoryMismatch, Path: invRelPath, Detail: fmt.Sprintf("inventory CID count %d != shard observation count %d", len(sortedInv), len(thisObsIDs))}
			}
			for i := range sortedInv {
				if sortedInv[i] != thisObsIDs[i] {
					return &PackageRefusal{Rule: RuleInventoryMismatch, Path: invRelPath, Detail: fmt.Sprintf("inventory CID %s != shard observation ID %s", sortedInv[i], thisObsIDs[i])}
				}
			}

			referencedInventories[invHex] = true
		} else {
			if len(sRef.InlineIDs) != len(thisObsIDs) {
				return &PackageRefusal{
					Rule:   RuleInventoryMismatch,
					Path:   foundShards[sRef.ShardID],
					Detail: fmt.Sprintf("inline_ids count %d != shard observation count %d", len(sRef.InlineIDs), len(thisObsIDs)),
				}
			}
			for i := range sRef.InlineIDs {
				if sRef.InlineIDs[i] != thisObsIDs[i] {
					return &PackageRefusal{
						Rule:   RuleInventoryMismatch,
						Path:   foundShards[sRef.ShardID],
						Detail: fmt.Sprintf("inline_ids[%d] %s != shard observation ID %s", i, sRef.InlineIDs[i], thisObsIDs[i]),
					}
				}
			}
		}
	}

	// Check for unreferenced inventory files
	if invFi, err := os.Lstat(inventoriesDir); err == nil && invFi.IsDir() {
		entries, err := os.ReadDir(inventoriesDir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			relInv := filepath.Join("inventories", e.Name())
			if strings.HasPrefix(e.Name(), ".") {
				return &PackageRefusal{Rule: RulePackageStrayFile, Path: relInv, Detail: "hidden entries forbidden in inventories"}
			}
			if e.IsDir() {
				return &PackageRefusal{Rule: RulePackageStrayFile, Path: relInv, Detail: "subdirectories forbidden in inventories"}
			}
			if !strings.HasSuffix(e.Name(), ".json") {
				return &PackageRefusal{Rule: RulePackageStrayFile, Path: relInv, Detail: "stray file in inventories"}
			}
			invHex := strings.TrimSuffix(e.Name(), ".json")
			if !referencedInventories[invHex] {
				return &PackageRefusal{Rule: RulePackageStrayFile, Path: relInv, Detail: "unreferenced inventory file: " + e.Name()}
			}
		}
	}

	// Validate count equations
	recordsEmitted, _ := strconv.Atoi(cov.Counts.RecordsEmitted)
	if recordsEmitted != totalShardRecords {
		return &PackageRefusal{
			Rule:   RuleCountEquationMismatch,
			Path:   "batch.json",
			Detail: fmt.Sprintf("records_emitted %d does not match sum of shard record counts %d", recordsEmitted, totalShardRecords),
		}
	}

	obsCount, _ := strconv.Atoi(cov.Counts.Observations)
	if obsCount != len(allObservationIDs) {
		return &PackageRefusal{
			Rule:   RuleCountEquationMismatch,
			Path:   "batch.json",
			Detail: fmt.Sprintf("observations %d does not match unique observation count %d", obsCount, len(allObservationIDs)),
		}
	}

	gapsCount, _ := strconv.Atoi(cov.Counts.Gaps)
	if gapsCount != len(cov.Reasons) {
		return &PackageRefusal{
			Rule:   RuleCountEquationMismatch,
			Path:   "batch.json",
			Detail: fmt.Sprintf("gaps %d does not match len(reasons) %d", gapsCount, len(cov.Reasons)),
		}
	}

	conflictsCount, _ := strconv.Atoi(cov.Counts.Conflicts)
	conflictingKeys := 0
	for _, idMap := range spendKeyObsMap {
		if len(idMap) > 1 {
			conflictingKeys++
		}
	}
	if conflictsCount != conflictingKeys {
		return &PackageRefusal{
			Rule:   RuleCountEquationMismatch,
			Path:   "batch.json",
			Detail: fmt.Sprintf("conflicts %d does not match conflicting spend keys %d", conflictsCount, conflictingKeys),
		}
	}

	return nil
}
