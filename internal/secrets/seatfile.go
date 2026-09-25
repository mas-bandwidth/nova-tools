package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SeatFile is one seat's file, opened. Values stay inside Secret: nothing here
// prints, logs or returns one as a string.
type SeatFile struct {
	Path    string            // <store>/<seat>.yaml
	HeadSHA string            // the store's HEAD the file was read at
	Secrets map[string]Secret // every key in the file, by name
}

// OpenSeatFile runs every check exec runs before it decrypts -- the store is a
// directory working copy with a .sops.yaml and the seat's file, invariant 8
// (HEAD equals its upstream, nothing uncommitted or untracked), invariant 1
// (recovery.pub and the creation rules), invariant 6 (the key file's mode) and
// the sops version -- then decrypts <store>/<seat>.yaml with sops isolated from
// the caller's identities and parses it. It is the one path from a seat to its
// values: `nova-secrets exec` takes it before it replaces itself with the
// command, and a nova tool given --seat takes it in its own process
// (internal/seatcred, nova-tools#4052), so no shell wrapper stands between the
// two and neither can check less than the other.
func OpenSeatFile(storeDir, asName, keyPath, sopsPath string) (SeatFile, error) {
	var missing []string
	for _, m := range []struct{ v, flag string }{{storeDir, "--store <dir>"}, {asName, "--as <name>"}, {keyPath, "--key <path>"}, {sopsPath, "--sops <path>"}} {
		if m.v == "" {
			missing = append(missing, m.flag)
		}
	}
	if len(missing) > 0 {
		return SeatFile{}, fmt.Errorf("missing: %s", strings.Join(missing, ", "))
	}
	if !IsValidAsName(asName) {
		return SeatFile{}, fmt.Errorf("invalid seat name %q: must match [A-Za-z0-9_-]+", asName)
	}

	// 1. Store filesystem checks
	sFi, err := os.Stat(storeDir)
	if err != nil || !sFi.IsDir() {
		return SeatFile{}, fmt.Errorf("store %s is not a directory", storeDir)
	}
	gitDir := filepath.Join(storeDir, ".git")
	gFi, err := os.Stat(gitDir)
	if err != nil || !gFi.IsDir() {
		return SeatFile{}, fmt.Errorf("store %s has no .git directory", storeDir)
	}
	sopsConfigPath := filepath.Join(storeDir, ".sops.yaml")
	if _, err := os.Stat(sopsConfigPath); err != nil {
		return SeatFile{}, fmt.Errorf("store %s carries no .sops.yaml", storeDir)
	}
	targetFile := filepath.Join(storeDir, asName+".yaml")
	if _, err := os.Stat(targetFile); err != nil {
		return SeatFile{}, fmt.Errorf("store file %s is absent", targetFile)
	}

	// 2. Invariant 8 git check and complete committed-artifact validation
	st, headBlobs, indexData, failures, refusal := ValidateAdmissibleStore(storeDir)
	if refusal != nil {
		return SeatFile{}, refusal
	}
	if len(failures) > 0 {
		return SeatFile{}, fmt.Errorf("store %s: %s", storeDir, failures[0].Reason)
	}
	headSHA := st.HeadSHA

	entries, err := os.ReadDir(storeDir)
	if err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".yaml") && e.Name() != ".sops.yaml" {
				if headBlobs != nil {
					if _, ok := headBlobs[e.Name()]; !ok {
						return SeatFile{}, fmt.Errorf("uncommitted or untracked yaml file in store root: %s", e.Name())
					}
				}
				if indexData != nil {
					if _, ok := indexData.Entries[e.Name()]; !ok {
						return SeatFile{}, fmt.Errorf("untracked yaml file in store root: %s", e.Name())
					}
				}
			}
		}
	}

	// 3. Invariant 1 shape check (recovery.pub & .sops.yaml rules)
	recKey, err := ReadRecoveryPub(storeDir)
	if err != nil {
		return SeatFile{}, fmt.Errorf("store %s: %w", storeDir, err)
	}
	sopsCfg, err := ParseSopsConfig(storeDir)
	if err != nil {
		return SeatFile{}, fmt.Errorf("store %s: %w", storeDir, err)
	}
	inv1Fails := CheckInvariant1(storeDir, sopsCfg, recKey)
	if len(inv1Fails) > 0 {
		return SeatFile{}, fmt.Errorf("store %s: %s", storeDir, inv1Fails[0].Reason)
	}

	// 4. Key file mode check
	if err := CheckInvariant6(keyPath); err != nil {
		return SeatFile{}, err
	}

	// 5. Sops binary check
	if _, err := CheckSopsVersion(sopsPath); err != nil {
		return SeatFile{}, err
	}

	// 6. Decrypt target file
	decData, err := DecryptFile(sopsPath, keyPath, targetFile)
	if err != nil {
		return SeatFile{}, err
	}

	// 7. Parse decrypted secrets
	secretsMap, _, err := ParseDecryptedSecrets(decData)
	if err != nil {
		return SeatFile{}, err
	}

	return SeatFile{Path: targetFile, HeadSHA: headSHA, Secrets: secretsMap}, nil
}
