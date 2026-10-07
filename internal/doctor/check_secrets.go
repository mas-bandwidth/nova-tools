package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// secretsProbeTimeout bounds the one `nova-secrets names` listing the secrets
// check runs.
const secretsProbeTimeout = 10 * time.Second

func init() {
	Default.Register(Check{Name: "secrets", Dependency: "nova-secrets store and keys", Run: checkSecrets})
}

// requiredSecrets are the secret names the seat and the loop records require:
// the Redis passwords nova-up seals into the seat's file, one per ACL user
// (internal/up/redis.go RedisUsers and PasswordName, docs/SPEC-UP.md "Steps",
// 6). The check reads them back by name only, never a value.
func requiredSecrets() []string {
	users := []string{"coordinator", "bench", "ns-table", "ns-friend"}
	names := make([]string, 0, len(users))
	for _, u := range users {
		names = append(names, "NOVA_UP_REDIS_"+strings.ToUpper(strings.ReplaceAll(u, "-", "_"))+"_PASSWORD")
	}
	return names
}

// checkSecrets holds the nova-secrets setup where nova-up --local leaves it
// (docs/SETUP.md, dep-secrets-bb.w3): sops on PATH, the machine's age key file
// present with safe permissions, and every secret name the seat and the loop
// records require present in the store by nova-secrets' own listing. Names
// only: no value is read or printed (docs/SPEC-SECRETS.md, `names`).
func checkSecrets(ctx context.Context, env Env) Result {
	const doc = "docs/SETUP.md, dep-secrets-bb.w3"

	sopsPath := sopsOnPath(env)
	if sopsPath == "" {
		return Result{Status: Fail, Evidence: "sops not found on PATH",
			Fix: "install sops on this machine (nova-up --local step binaries names it) (" + doc + ")"}
	}

	keyPath := env.Getenv("NOVA_SECRETS_KEY")
	if keyPath == "" {
		return Result{Status: Fail, Evidence: "NOVA_SECRETS_KEY names no age key file",
			Fix: "run nova-secrets keygen --as <seat> --key <path> --age-keygen <path> (" + doc + ")"}
	}
	raw, err := env.ReadFile(keyPath)
	if err != nil {
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("key file %s is not readable", keyPath),
			Fix:      fmt.Sprintf("run nova-secrets keygen --as <seat> --key %s --age-keygen <path>, or restore the key file with mode 0600 (%s)", keyPath, doc)}
	}
	if perm, ok := filePerm(env, keyPath); !ok || perm != 0o600 {
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("key file %s is not mode 0600", keyPath),
			Fix:      fmt.Sprintf("run chmod 600 %s, so only this user reads the key (%s)", keyPath, doc)}
	}
	if perm, ok := dirPerm(env, filepath.Dir(keyPath)); !ok || perm != 0o700 {
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("the directory holding %s is not mode 0700", keyPath),
			Fix:      fmt.Sprintf("run chmod 700 %s, so only this user reaches the key (%s)", filepath.Dir(keyPath), doc)}
	}
	if !hasPublicKeyLine(string(raw)) {
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("key file %s carries no public key comment line", keyPath),
			Fix:      fmt.Sprintf("run age-keygen -y %s and restore its output as the # public key: line (%s)", keyPath, doc)}
	}

	store := env.Getenv("NOVA_SECRETS_STORE")
	if store == "" {
		return Result{Status: Fail, Evidence: "NOVA_SECRETS_STORE names no secrets store",
			Fix: "clone the secrets store and set NOVA_SECRETS_STORE to its working copy (nova-up --local step secrets) (" + doc + ")"}
	}
	seat := env.Getenv("NOVA_SECRETS_SEAT")
	if seat == "" {
		return Result{Status: Fail, Evidence: "NOVA_SECRETS_SEAT names no seat",
			Fix: "set NOVA_SECRETS_SEAT to this machine's seat, e.g. coordinator (nova-up --local step seat) (" + doc + ")"}
	}

	have, err := secretNames(ctx, env, store, seat)
	if err != nil {
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("nova-secrets names --store %s --as %s did not answer", store, seat),
			Fix:      fmt.Sprintf("run nova-secrets names --store %s --as %s --max 0; a refusal there names the store or seat to fix (%s)", store, seat, doc)}
	}
	var missing []string
	for _, n := range requiredSecrets() {
		if !have[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("seat %s lacks the secrets the seat and loops require: %s", seat, strings.Join(missing, ", ")),
			Fix:      fmt.Sprintf("run nova-up --local step redis, or seal each name with nova-secrets seal --store %s --as %s --name <NAME> (%s)", store, seat, doc)}
	}
	return Result{Status: OK,
		Evidence: fmt.Sprintf("sops at %s, key at %s mode 0600, seat %s holds the %d secrets the seat and loops require", sopsPath, keyPath, seat, len(requiredSecrets()))}
}

// sopsOnPath is the `sops` PATH resolves, the first of a name winning as the
// shell resolves it, or "".
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
			if e.Name() != "sops" || e.IsDir() {
				continue
			}
			if info, err := e.Info(); err != nil || (info.Mode()&0o111 == 0 && e.Type()&os.ModeSymlink == 0) {
				continue
			}
			return filepath.Join(dir, "sops")
		}
	}
	return ""
}

// filePerm is the permission bits of the file at path, read through the
// directory listing, so the check stays behind Env.
func filePerm(env Env, path string) (uint32, bool) {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	entries, err := env.ReadDir(filepath.Clean(dir))
	if err != nil {
		return 0, false
	}
	for _, e := range entries {
		if e.Name() != base {
			continue
		}
		info, err := e.Info()
		if err != nil || e.IsDir() {
			return 0, false
		}
		return uint32(info.Mode().Perm()), true
	}
	return 0, false
}

// dirPerm is the permission bits of dir, read through its parent's listing.
func dirPerm(env Env, dir string) (uint32, bool) {
	dir = filepath.Clean(dir)
	if dir == "." || dir == "" {
		return 0o700, true
	}
	parent, base := filepath.Split(dir)
	if parent == "" {
		parent = "."
	}
	entries, err := env.ReadDir(filepath.Clean(parent))
	if err != nil {
		return 0, false
	}
	for _, e := range entries {
		if e.Name() != base {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return 0, false
		}
		return uint32(info.Mode().Perm()), true
	}
	return 0, false
}

// hasPublicKeyLine reports whether the key file carries the `# public key:
// age1…` comment age-keygen writes at the top of the identity file, the only
// source of the public half to a tool that links no cryptography
// (docs/SPEC-SECRETS.md, "Where \"finds this key's public half\" comes from").
func hasPublicKeyLine(raw string) bool {
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "# public key: ") {
			return true
		}
	}
	return false
}

// secretNames is the key names `nova-secrets names` lists for the seat's file:
// one `SECRETS NAME key=<NAME>` line each, never a value.
func secretNames(ctx context.Context, env Env, store, seat string) (map[string]bool, error) {
	cctx, cancel := context.WithTimeout(ctx, secretsProbeTimeout)
	defer cancel()
	out, err := env.Exec(cctx, "nova-secrets", "names", "--store", store, "--as", seat, "--max", "0")
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "SECRETS NAME ") {
			continue
		}
		for _, f := range strings.Fields(l) {
			if n, ok := strings.CutPrefix(f, "key="); ok && n != "" {
				have[n] = true
			}
		}
	}
	return have, nil
}
