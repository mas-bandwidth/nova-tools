package up

import (
	"fmt"
	"os"
	"strings"
)

// The secrets step: a nova-secrets store under the root for the
// coordinator's seat: the seat's age key (nova-secrets keygen), the store as
// a git working copy on main tracking a bare upstream beside it, and the
// store's sops rules naming the seat's public key. Every password a later
// step makes is sealed into it and is never printed (docs/SPEC-UP.md
// "Steps", 5; docs/SPEC-SECRETS.md).
func init() { Register(Step{Name: "secrets", Order: 50, Plan: planSecrets, Apply: applySecrets}) }

// pubKey is the public key the identity file at path names in its
// `# public key: age1…` line (docs/SPEC-SECRETS.md), or "".
func pubKey(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if k, ok := strings.CutPrefix(l, "# public key: "); ok {
			return strings.TrimSpace(k)
		}
	}
	return ""
}

func sopsRules(pub string) []byte {
	return fmt.Appendf(nil, "creation_rules:\n  - path_regex: ^%s\\.yaml$\n    age: %s\n", Seat, pub)
}

// secretsWants is each part of the store and whether it is as the step wants it.
func secretsWants(e *Env) []struct {
	part string
	ok   bool
} {
	pub := pubKey(e.Path(KeyFile()))
	return []struct {
		part string
		ok   bool
	}{
		{"key", pub != ""},
		{"upstream", exists(e.Path(SecretsGit))},
		{"store", exists(e.Path(SecretsDir, ".git"))},
		{"rules", pub != "" && fileState(e.Path(SecretsDir, ".sops.yaml"), sopsRules(pub)) == OK},
	}
}

func planSecrets(e *Env) Finding {
	var todo []string
	for _, w := range secretsWants(e) {
		if !w.ok {
			todo = append(todo, w.part)
		}
	}
	where := e.Path(SecretsDir) + " seat=" + Seat
	switch {
	case len(todo) == 4:
		return Finding{Create, where}
	case len(todo) > 0:
		return Finding{Change, where + ": " + strings.Join(todo, " ")}
	}
	return Finding{OK, where}
}

func applySecrets(e *Env) error {
	store, git := e.Path(SecretsDir), e.path("git")
	if pubKey(e.Path(KeyFile())) == "" {
		if _, err := e.Run(Cmd{Name: e.path("nova-secrets"), Args: []string{"keygen", "--as", Seat, "--key", e.Path(KeyFile()),
			"--age-keygen", e.path("age-keygen")}}); err != nil {
			return err
		}
	}
	pub := pubKey(e.Path(KeyFile()))
	if pub == "" {
		return fmt.Errorf("nova-secrets keygen left no public key line in %s", e.Path(KeyFile()))
	}
	runs := []Cmd{}
	if !exists(e.Path(SecretsGit)) {
		runs = append(runs, Cmd{Name: git, Args: []string{"init", "-q", "--bare", "-b", "main", e.Path(SecretsGit)}})
	}
	if !exists(e.Path(SecretsDir, ".git")) {
		runs = append(runs, Cmd{Name: git, Args: []string{"init", "-q", "-b", "main", store}},
			Cmd{Name: git, Args: []string{"-C", store, "remote", "add", "origin", e.Path(SecretsGit)}})
	}
	for _, c := range runs {
		if _, err := e.Run(c); err != nil {
			return err
		}
	}
	if fileState(e.Path(SecretsDir, ".sops.yaml"), sopsRules(pub)) == OK {
		return nil
	}
	if err := writeFile(e.Path(SecretsDir, ".sops.yaml"), sopsRules(pub), 0o644); err != nil {
		return err
	}
	return commitAndPush(e, store, "nova-up: the store's rules for the seat "+Seat, ".sops.yaml")
}

// commitAndPush commits paths in the store and pushes main to its upstream,
// which nova-secrets check and exec read (docs/SPEC-SECRETS.md).
func commitAndPush(e *Env, store, msg string, paths ...string) error {
	git := e.path("git")
	for _, c := range []Cmd{
		{Name: git, Args: append([]string{"-C", store, "add", "--"}, paths...)},
		{Name: git, Args: []string{"-C", store, "-c", "user.name=nova-up", "-c", "user.email=nova-up@example.invalid", "commit", "-q", "-m", msg}},
		{Name: git, Args: []string{"-C", store, "push", "-q", "-u", "origin", "main"}},
	} {
		if _, err := e.Run(c); err != nil {
			return err
		}
	}
	return nil
}
