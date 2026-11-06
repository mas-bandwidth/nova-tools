//go:build functional

package contract

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// real runs a command that is not one of ours (age-keygen) in the row's world.
func (e *Env) real(name string, args ...string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		e.skip("%s is not on PATH", name)
	}
	cmd := exec.Command(p, args...)
	cmd.Env, cmd.Dir = e.environ(true), e.Dir
	out, err := cmd.Output()
	if err != nil {
		e.t.Fatalf("%s %v: %v", name, args, err)
	}
	return strings.TrimSpace(string(out))
}

// secretsInit makes a store with a recovery key; secretsStore adds the seat web,
// its key, and one secret, A, in it.
func secretsInit(e *Env) {
	for _, t := range []string{"sops", "git", "age-keygen"} {
		if _, err := exec.LookPath(t); err != nil {
			e.skip("%s is not on PATH", t)
		}
	}
	e.Set("GIT_AUTHOR_NAME", "t")
	e.Set("GIT_AUTHOR_EMAIL", "t@example.invalid")
	e.Set("GIT_COMMITTER_NAME", "t")
	e.Set("GIT_COMMITTER_EMAIL", "t@example.invalid")
	e.Var("store", e.Dir+"/store")
	e.Var("key", e.Dir+"/web.key")
	e.Var("pins", e.Dir+"/pins") // outside the store and any work tree: grants and sets are sealed to them
	e.Set("NOVA_SECRETS_PINS", "{pins}")
	e.real("age-keygen", "-o", e.Dir+"/rec.key")
	e.real("age-keygen", "-o", e.Dir+"/web.key")
	e.Var("web", e.real("age-keygen", "-y", e.Dir+"/web.key"))
	if r := e.Exec("secrets", "", "init", "--store", "{store}", "--pins", "{pins}", "--recovery", e.real("age-keygen", "-y", e.Dir+"/rec.key")); r.code != 0 {
		e.t.Fatalf("init: %s", r.errs)
	}
}

func secretsStore(e *Env) {
	secretsInit(e)
	if r := e.Exec("secrets", "", "seat", "add", "web", "{web}", "--store", "{store}"); r.code != 0 {
		e.t.Fatalf("seat add: %s", r.errs)
	}
	if r := e.Exec("secrets", "val\n", "set", "A", "--as", "web", "--store", "{store}", "--key", "{key}"); r.code != 0 {
		e.t.Fatalf("set: %s", r.errs)
	}
}

func init() {
	ex := func(cmd ...string) []string {
		return append([]string{"exec", "--as", "web", "--store", "{store}", "--key", "{key}", "--only", "A", "--"}, cmd...)
	}
	set := []string{"set", "A", "--as", "web", "--store", "{store}", "--key", "{key}"}
	register(
		Fault{Tool: "secrets", Name: "names: no store", Argv: []string{"names"}, Want: Want{Reason: []string{"--store"}}},
		Fault{Tool: "secrets", Name: "exec: nothing given", Argv: []string{"exec"}, Want: Want{Reason: []string{"--only", "--store", "--key"}}},
		Fault{Tool: "secrets", Name: "names: store absent", Argv: []string{"names", "--store", "{dir}/nostore"},
			Want: Want{Exit: []int{2}, Reason: []string{"does not exist"}}},
		Fault{Tool: "secrets", Name: "init: recovery is not a public key", Argv: []string{"init", "--store", "{dir}/st", "--recovery", "bogus"},
			Want: Want{Exit: []int{2}, Reason: []string{"age public key"}}},
		Fault{Tool: "secrets", Name: "set: no rule for the seat", Setup: secretsInit, Stdin: "v\n", Argv: set,
			Want: Want{Exit: []int{2}, Reason: []string{"seat"}}},
		Fault{Tool: "secrets", Name: "set: key file absent", Setup: func(e *Env) { secretsStore(e); e.Var("key", e.Dir+"/nokey") }, Stdin: "v\n", Argv: set,
			Want: Want{Exit: []int{2}, Reason: []string{"nokey"}}, Known: "§7 key file"},
		Fault{Tool: "secrets", Name: "exec: name not in the seat file", Setup: secretsStore,
			Argv: []string{"exec", "--as", "web", "--store", "{store}", "--key", "{key}", "--only", "ZZ", "--", "true"},
			Want: Want{Exit: []int{125}, Reason: []string{"unknown secret"}}},
		Fault{Tool: "secrets", Name: "exec: seat has no file", Setup: secretsStore,
			Argv: []string{"exec", "--as", "nope", "--store", "{store}", "--key", "{key}", "--only", "A", "--", "true"},
			Want: Want{Exit: []int{125}, Reason: []string{"nope"}}, Known: "§8 refusal grammar"},
		Fault{Tool: "secrets", Name: "exec: wrong key for the seat",
			Setup: func(e *Env) {
				secretsStore(e)
				e.real("age-keygen", "-o", e.Dir+"/other.key")
				e.Var("key", e.Dir+"/other.key")
			},
			Argv: ex("true"), Want: Want{Exit: []int{125}, Reason: []string{"key"}}, Known: "§8 refusal grammar"},
		Fault{Tool: "secrets", Name: "exec: sops not found at --sops", Setup: secretsStore,
			Argv: append([]string{"exec", "--as", "web", "--store", "{store}", "--key", "{key}", "--sops", "{dir}/nosops", "--only", "A", "--"}, "true"),
			Want: Want{Reason: []string{"sops"}}, Known: "8"},
		Fault{Tool: "secrets", Name: "exec: sops not on PATH", Setup: func(e *Env) { secretsStore(e); e.Path(e.Tools("git", "age-keygen", "sh", "true")) },
			Argv: ex("true"), Want: Want{Reason: []string{"sops"}}, Known: "8"},
		Fault{Tool: "secrets", Name: "set: git not on PATH", Setup: func(e *Env) { secretsStore(e); e.Path(e.Tools("sops", "age-keygen", "sh")) },
			Stdin: "v\n", Argv: set, Want: Want{Reason: []string{"git", "not found"}}, Known: "8"},
		Fault{Tool: "secrets", Name: "set: store read-only", Setup: func(e *Env) { secretsStore(e); e.Chmod(e.Dir+"/store", 0o500) },
			Stdin: "w\n", Argv: set, Want: Want{Reason: []string{"permission denied"}}, Known: "§7 read-only store"},
		Fault{Tool: "secrets", Name: "exec: seat file corrupt",
			Setup: func(e *Env) { secretsStore(e); os.WriteFile(e.Dir+"/store/web.yaml", []byte("corrupt\n"), 0o644) },
			Argv:  ex("true"), Want: Want{Reason: []string{"sops"}}, Known: "8"},
		Fault{Tool: "secrets", Name: "exec: command killed by SIGKILL", Setup: secretsStore, Argv: ex("sh", "-c", "kill -9 $$"),
			Want: Want{Signal: 9, Info: regexp.MustCompile(`^SECRETS EXEC OK `)}},
	)
}
