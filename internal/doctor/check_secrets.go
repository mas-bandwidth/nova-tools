package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// secretsDoc is the documented step a secrets fix line names
// (docs/SETUP.md, the dep-secrets-bb.w4 section).
const secretsDoc = "docs/SETUP.md, dep-secrets-bb.w4"

func init() {
	Default.Register(Check{
		Name:       "secrets",
		Dependency: "the secrets store and this machine's key",
		Fleet:      true,
		Run:        checkSecrets,
	})
}

// checkSecrets holds this machine's side of the nova-secrets store
// (docs/SPEC-SECRETS.md): sops is on PATH, this machine's age key is readable
// at the safe modes the whole boundary rests on (0600 in a 0700 directory),
// the store is a git working copy with its sops rules, and every secret name
// the seat's loop records require is in the store. Names only, through
// nova-secrets' own listing: the check takes no key, starts no decrypt and
// prints no value. It is fleet-scoped: a single machine's setup has none of
// these, so nova-doctor --local skips it.
func checkSecrets(ctx context.Context, env Env) Result {
	// 1. sops runs as a program and is found on PATH.
	sops := sopsOn(env)
	if sops == "" {
		return Result{Status: Fail,
			Evidence: "sops is not on PATH and NOVA_SECRETS_SOPS names no executable there",
			Fix:      "install sops (https://github.com/getsops/sops) and put it on PATH, or set NOVA_SECRETS_SOPS to it (" + secretsDoc + ")"}
	}

	// 2. this machine's age key: readable, its public half present, and the
	// modes that are the boundary itself.
	key := env.Getenv("NOVA_SECRETS_KEY")
	if key == "" {
		return Result{Status: Fail,
			Evidence: "NOVA_SECRETS_KEY is not set, so this machine has no age key to open the store with",
			Fix:      "set NOVA_SECRETS_KEY to this machine's age private key; nova-up --local writes it into seat.env (" + secretsDoc + ")"}
	}
	if _, err := env.ReadFile(key); err != nil {
		return Result{Status: Fail,
			Evidence: "the age key " + key + " is not readable",
			Fix:      "run nova-secrets keygen --as <seat> --key " + key + " --age-keygen <age-keygen> (" + secretsDoc + ")"}
	}
	if !keyCarriesPublicHalf(env, key) {
		return Result{Status: Fail,
			Evidence: "the age key " + key + " carries no `# public key:` line",
			Fix:      "run age-keygen -y " + key + " and restore its output as the key file's `# public key:` line (" + secretsDoc + ")"}
	}
	if mode, ok := filePerm(env, key); !ok || mode != 0o600 {
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("the age key %s is mode %04o, want 0600", key, mode),
			Fix:      "chmod 600 " + key + " (" + secretsDoc + ")"}
	}
	keyDir := filepath.Dir(key)
	if mode, ok := filePerm(env, keyDir); !ok || mode != 0o700 {
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("the age key's directory %s is mode %04o, want 0700", keyDir, mode),
			Fix:      "chmod 700 " + keyDir + " (" + secretsDoc + ")"}
	}

	// 3. the store is the strong working copy the store-reading verbs demand:
	// a git working copy that holds its own sops rules.
	store := env.Getenv("NOVA_SECRETS_STORE")
	if store == "" {
		return Result{Status: Fail,
			Evidence: "NOVA_SECRETS_STORE is not set, so this machine has no store to read",
			Fix:      "set NOVA_SECRETS_STORE to the secrets store's git working copy; nova-up --local writes it into seat.env (" + secretsDoc + ")"}
	}
	if _, err := env.ReadFile(filepath.Join(store, ".git", "HEAD")); err != nil {
		return Result{Status: Fail,
			Evidence: "the secrets store " + store + " is not a git working copy",
			Fix:      "git clone <store-url> " + store + " (" + secretsDoc + ")"}
	}
	if _, err := env.ReadFile(filepath.Join(store, ".sops.yaml")); err != nil {
		return Result{Status: Fail,
			Evidence: "the secrets store " + store + " holds no .sops.yaml",
			Fix:      "clone the store, or run nova-up --local to write the seat's rules (" + secretsDoc + ")"}
	}
	seat := env.Getenv("NOVA_SECRETS_SEAT")
	if seat == "" {
		return Result{Status: Fail,
			Evidence: "NOVA_SECRETS_SEAT is not set, so the seat's file is not named",
			Fix:      "set NOVA_SECRETS_SEAT to this machine's seat; nova-up --local writes it into seat.env (" + secretsDoc + ")"}
	}

	// 4. the store's names, through nova-secrets' own listing: no --key, no
	// --sops, no decrypt and no value.
	have, err := listedNames(ctx, env, store, seat)
	if err != nil {
		return Result{Status: Fail,
			Evidence: "the seat's secrets could not be listed: " + err.Error(),
			Fix:      "nova-secrets names --store " + store + " --as " + seat + " (a missing file is made by nova-secrets seat add) (" + secretsDoc + ")"}
	}

	// 5. every name the seat's loop records require is in that listing.
	required, err := requiredNames(ctx, env, seat)
	if err != nil {
		return Result{Status: Fail,
			Evidence: "the seat's loop records could not be read: " + err.Error(),
			Fix:      "nova-config loop list --json (run nova-config apply first) (" + secretsDoc + ")"}
	}
	var missing []string
	for _, name := range required {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		word := "names"
		if len(missing) == 1 {
			word = "name"
		}
		return Result{Status: Fail,
			Evidence: fmt.Sprintf("%d %s the seat's loops require are not in the store: %s", len(missing), word, strings.Join(missing, ", ")),
			Fix:      "nova-secrets seal --store " + store + " --as " + seat + " --name " + missing[0] + " (" + secretsDoc + ")"}
	}
	return Result{Status: OK,
		Evidence: fmt.Sprintf("sops at %s, key %s 0600 in 0700 %s, store %s, seat %s, %d name(s) listed, %d required present",
			sops, key, keyDir, store, seat, len(have), len(required))}
}

// sopsOn is the sops the check runs: the first executable `sops` on PATH, as
// the shell would resolve it, else the executable NOVA_SECRETS_SOPS names
// (pkg/seatcred reads the same variable).
func sopsOn(env Env) string {
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
			// a symlinked sops (a package manager) is a candidate.
			if info, err := e.Info(); err != nil || (info.Mode()&0o111 == 0 && e.Type()&os.ModeSymlink == 0) {
				continue
			}
			return filepath.Join(dir, "sops")
		}
	}
	if p := env.Getenv("NOVA_SECRETS_SOPS"); p != "" {
		if mode, ok := filePerm(env, p); ok && mode&0o111 != 0 {
			return p
		}
	}
	return ""
}

// keyCarriesPublicHalf reports whether the identity file at path carries the
// `# public key: age1…` comment age-keygen writes, the one source a tool that
// links no cryptography has (docs/SPEC-SECRETS.md).
func keyCarriesPublicHalf(env Env, path string) bool {
	b, err := env.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "# public key: ") {
			return true
		}
	}
	return false
}

// filePerm is the permission bits of the entry at path, and whether it is
// there. Env carries no Stat, so the entry is read from its directory's
// listing; the link's own mode is read, never the target's.
func filePerm(env Env, path string) (fs.FileMode, bool) {
	entries, err := env.ReadDir(filepath.Dir(path))
	if err != nil {
		return 0, false
	}
	base := filepath.Base(path)
	for _, e := range entries {
		if e.Name() != base {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return 0, false
		}
		return info.Mode().Perm(), true
	}
	return 0, false
}

// listedNames runs `nova-secrets names` and reads the key names off its
// `SECRETS NAME key=<NAME>` lines: the store's own listing, names only.
func listedNames(ctx context.Context, env Env, store, seat string) (map[string]bool, error) {
	out, err := env.Exec(ctx, "nova-secrets", "names", "--store", store, "--as", seat, "--max", "0")
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	listed := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "SECRETS NAME ") {
			if name := lineField(line, "key"); name != "" {
				have[name] = true
				listed = true
			}
			continue
		}
		if strings.HasPrefix(line, "SECRETS NAMES OK ") {
			listed = true
		}
	}
	if !listed {
		return nil, fmt.Errorf("nova-secrets names printed no listing for seat %s", seat)
	}
	return have, nil
}

// requiredNames is the names the seat's loop records require, read from
// `nova-config loop list --json`: every name in the `keys` field of a loop
// row whose `seat` is this machine's seat and whose unit is enabled. The
// union is sorted, so a report is byte-stable.
func requiredNames(ctx context.Context, env Env, seat string) ([]string, error) {
	out, err := env.Exec(ctx, "nova-config", "loop", "list", "--json")
	if err != nil {
		return nil, err
	}
	var wire struct {
		Items []struct {
			Kind   string `json:"kind"`
			Fields struct {
				Seat    string `json:"seat"`
				Keys    string `json:"keys"`
				Enabled string `json:"enabled"`
			} `json:"fields"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &wire); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, it := range wire.Items {
		if it.Kind != "" && it.Kind != "loop" {
			continue
		}
		f := it.Fields
		if f.Seat != seat || f.Keys == "" || f.Enabled == "false" {
			continue
		}
		for _, name := range strings.Split(f.Keys, ",") {
			if name = strings.TrimSpace(name); name != "" {
				seen[name] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

// lineField is the `key=value` token of a one-line record, or "".
func lineField(line, key string) string {
	for _, word := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(word, key+"="); ok {
			return v
		}
	}
	return ""
}
