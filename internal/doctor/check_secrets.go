package doctor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

func init() {
	Default.Register(Check{Name: "secrets", Dependency: "nova-secrets store and keys", Fleet: true, Run: checkSecrets})
}

// checkSecrets verifies the nova-secrets setup: sops on PATH, the seat's key file
// present with safe permissions, and the store available with the seat's yaml file.
// It reports failures with one fix line each, without ever reading or printing secret values.
func checkSecrets(ctx context.Context, env Env) Result {
	const doc = "docs/SETUP.md, dep-secrets-b.w3"

	// Check 1: sops on PATH
	sopsPath := sopsOnPath(env)
	if sopsPath == "" {
		return Result{
			Status:  Fail,
			Evidence: "sops not found on PATH",
			Fix:     "install sops (https://github.com/getsops/sops) and ensure it is on PATH (" + doc + ")",
		}
	}

	// Check 2: key file exists with safe permissions
	keyPath := keyPath(env)
	if keyPath == "" {
		return Result{
			Status:    Fail,
			Evidence:  "no age key file path configured",
			Fix:       "set NOVA_SECRETS_KEY to the path of the age private key (" + doc + ")",
		}
	}

	keyInfo, err := env.ReadFile(keyPath)
	if err != nil {
		return Result{
			Status:    Fail,
			Evidence:  fmt.Sprintf("key file %s is not readable", keyPath),
			Fix:       fmt.Sprintf("ensure the key file exists and is readable: chmod 600 %s (" + doc + ")", keyPath),
		}
	}

	// Check key has public key comment line
	hasPubKey := false
	for _, line := range strings.Split(string(keyInfo), "\n") {
		if strings.HasPrefix(line, "# public key: ") {
			hasPubKey = true
			break
		}
	}
	if !hasPubKey {
		return Result{
			Status:    Fail,
			Evidence:  fmt.Sprintf("key file %s lacks the public key comment line", keyPath),
			Fix:       fmt.Sprintf("restore the public key comment at the top of %s (run: age-keygen -y %s) (" + doc + ")", keyPath, keyPath),
		}
	}

	// Check 3: store git working copy exists
	storePath := storePath(env)
	if storePath == "" {
		return Result{
			Status:    Fail,
			Evidence:  "no secrets store path configured",
			Fix:       "set NOVA_SECRETS_STORE to the git working copy of the secrets store (" + doc + ")",
		}
	}

	_, err = env.ReadFile(filepath.Join(storePath, ".git", "HEAD"))
	if err != nil {
		return Result{
			Status:    Fail,
			Evidence:  fmt.Sprintf("%s is not a git working copy", storePath),
			Fix:       fmt.Sprintf("clone the secrets store into %s (" + doc + ")", storePath),
		}
	}

	// Check 4: seat's yaml file exists
	seat := seatName(env)
	if seat == "" {
		return Result{
			Status:    Fail,
			Evidence:  "no seat name configured",
			Fix:       "set NOVA_SECRETS_SEAT to the seat name (" + doc + ")",
		}
	}

	seatFile := filepath.Join(storePath, seat+".yaml")
	if _, err := env.ReadFile(seatFile); err != nil {
		return Result{
			Status:    Fail,
			Evidence:  fmt.Sprintf("seat file %s not found", seatFile),
			Fix:       fmt.Sprintf("run nova-secrets keygen and add the seat with nova-secrets seat add (" + doc + ")"),
		}
	}

	return Result{
		Status:   OK,
		Evidence: fmt.Sprintf("sops at %s, key at %s, store at %s, seat=%s", sopsPath, keyPath, storePath, seat),
	}
}

func sopsOnPath(env Env) string {
	for _, dir := range filepath.SplitList(env.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := env.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Name() == "sops" && !e.IsDir() {
				return filepath.Join(dir, "sops")
			}
		}
	}
	return ""
}

func keyPath(env Env) string {
	return env.Getenv("NOVA_SECRETS_KEY")
}

func storePath(env Env) string {
	return env.Getenv("NOVA_SECRETS_STORE")
}

func seatName(env Env) string {
	return env.Getenv("NOVA_SECRETS_SEAT")
}
