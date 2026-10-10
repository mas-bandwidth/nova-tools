package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
// (pkg/seatcred), so no shell wrapper stands between the
// two and neither can check less than the other.
func OpenSeatFile(storeDir, asName, keyPath, sopsPath string) (SeatFile, error) {
	// 1. The invocation and the store's shape, every problem at once
	if err := preflight(storeDir, need{storeDir, "--store <dir>", false}, need{asName, "--as <name>", true}, need{keyPath, "--key <path>", false}, need{sopsPath, "--sops <path>", false}); err != nil {
		return SeatFile{}, err
	}
	targetFile := filepath.Join(storeDir, asName+".yaml")
	if _, err := os.Stat(targetFile); err != nil {
		return SeatFile{}, seatAbsent(storeDir, asName)
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
	if _, err := CheckSopsVersion(nil, sopsPath); err != nil {
		return SeatFile{}, err
	}

	// 6. Decrypt target file
	decData, err := DecryptFile(nil, sopsPath, keyPath, targetFile)
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

// need is one flag a verb requires: the value it was given, the flag spelled with what
// it wants ("--store <dir>"), and whether the value names a seat, which is then a path
// component and held to IsValidAsName.
type need struct {
	v, flag string
	seat    bool
}

// preflight is every problem a verb can name before it reads a store file, in one
// error, so a caller fixes the call once (STANDARD §2, ONBOARDING point 2): each
// required flag left empty, each seat name that is not one, and, when --store is
// given, each way it falls short of a store.
func preflight(storeDir string, needs ...need) error {
	var missing, problems []string
	for _, n := range needs {
		switch {
		case n.v == "":
			missing = append(missing, n.flag)
		case n.seat && !IsValidAsName(n.v):
			problems = append(problems, fmt.Sprintf("invalid seat name %q for %s: must match [A-Za-z0-9_-]+", n.v, strings.Fields(n.flag)[0]))
		}
	}
	if len(missing) > 0 {
		problems = append([]string{"missing " + strings.Join(missing, ", ")}, problems...)
	}
	if storeDir != "" {
		if err := storeShape(storeDir); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}

// storeShape names at once every way dir falls short of a store: a directory that is
// a git working copy (a .git directory, never a worktree's .git file) carrying a
// .sops.yaml. The remedy is the command that makes one.
func storeShape(dir string) error {
	remedy := fmt.Sprintf("run: git clone <store url> %s (a new store: git init it, then write its .sops.yaml from the rule block nova-secrets keygen prints)", dir)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("store %s is not a directory; %s", dir, remedy)
	}
	var lacks []string
	if fi, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		lacks = append(lacks, "no .git directory")
	} else if !fi.IsDir() {
		lacks = append(lacks, gitIsAFile)
		remedy = gitFileRemedy(dir)
	}
	if _, err := os.Stat(filepath.Join(dir, ".sops.yaml")); err != nil {
		lacks = append(lacks, "no .sops.yaml")
	}
	if len(lacks) == 0 {
		return nil
	}
	return fmt.Errorf("store %s has %s; %s", dir, strings.Join(lacks, " and "), remedy)
}

// gitIsAFile is what a store whose .git is a file has. A worktree's or a submodule's
// .git is a file pointing at a repository elsewhere, so the HEAD and tracking ref read
// here would not be the store's own.
const gitIsAFile = "a .git that is a file (a worktree or submodule), where a directory working copy is wanted"

// gitFileRemedy is the way on from a store whose .git is a file: the store's own
// working copy, which git names, or a clone of the store.
func gitFileRemedy(dir string) string {
	return fmt.Sprintf("pass --store the store's own working copy (for a worktree, the directory holding the .git this prints: git -C %s rev-parse --path-format=absolute --git-common-dir) or a clone of the store; run: git clone <store url> <new dir>", dir)
}

// seatAbsent refuses a seat with no file in the store, naming the seats the store does
// hold, or, when it holds none, the verb that writes a seat's first value.
func seatAbsent(storeDir, asName string) error {
	var seats []string
	if entries, err := os.ReadDir(storeDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".yaml") && e.Name() != ".sops.yaml" {
				seats = append(seats, strings.TrimSuffix(e.Name(), ".yaml"))
			}
		}
	}
	if len(seats) == 0 {
		return fmt.Errorf("seat file %s.yaml is absent: store %s holds no seat file yet; seal writes a seat's first value: run: nova-secrets seal --store %s --as %s --key <path> --sops <path> --name <NAME>", asName, storeDir, storeDir, asName)
	}
	sort.Strings(seats)
	return fmt.Errorf("seat file %s.yaml is absent in store %s; its seats are %s: pass --as one of them; seal starts a new seat: run: nova-secrets seal --store %s --as %s --key <path> --sops <path> --name <NAME>", asName, storeDir, strings.Join(seats, ", "), storeDir, asName)
}
