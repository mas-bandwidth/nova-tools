package secrets

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE MODEL. tla/ProviderKey.tla is the design these tests hold: Prompt (the one
// hidden read), Seal (one commit on one branch into the source seat and every route
// seat), OpenPR (the one pull request), Merge (the forge's action, which the verb
// waits for), Fetch and Pull (every store verb's first step). The invariant is
// OnePromptOnePR: one key add makes one prompt and one pull request.

// pkCall is one child the fake ran: the program, its argv and its environment.
type pkCall struct {
	name string
	args []string
	env  []string
}

// pkFake is the fake store's world: the sops, the git and the gh the verb runs.
// Nothing here is a real program, a real store or a network: the seams are the
// Exec, GitPath, GHPath and SopsPath options.
type pkFake struct {
	mu    sync.Mutex
	calls []pkCall

	storeDir string
	// keyPath is the age key this machine opens the source seat with.
	keyPath string
	// sopsPath is the sops the verb runs.
	sopsPath string
	// recipients are the sops recipients each seat file is sealed to.
	recipients map[string][]string
	// behind is how many commits the checkout is behind its upstream.
	behind int
	// localCommits are the local commits the checkout carries; a checkout with them
	// cannot be fast-forwarded.
	localCommits []string
	// openPolls is how many polls the pull request is open before it is merged.
	openPolls int
	polls     int
	now       time.Time
}

// pkValue is the value the fake store is given, and the one string that must
// reach no argv, no environment and no output.
const pkValue = "sk-minimax-secret-value"

// pkRead is a stdin that counts the bytes it hands out: one prompt read is one
// line, and nothing after it.
type pkRead struct {
	mu    sync.Mutex
	read  int
	inner io.Reader
}

func (r *pkRead) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, err := r.inner.Read(p)
	r.read += n
	return n, err
}

func (r *pkRead) bytesRead() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.read
}

// newProviderKeyFake is a store with a source seat (rowan), three route seats
// (air, bee, cee) and one seat that runs no route (dan), and the fake sops, git
// and gh the verb runs.
func newProviderKeyFake(t *testing.T) *pkFake {
	t.Helper()
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	require.NoError(t, os.MkdirAll(filepath.Join(store, ".git"), 0o755))

	f := &pkFake{
		storeDir:   store,
		recipients: map[string][]string{},
		now:        time.Date(2026, 10, 20, 0, 5, 0, 0, time.UTC),
	}
	routeSeats := []string{"rowan", "air", "bee", "cee", "dan"}
	var rules strings.Builder
	rules.WriteString("creation_rules:\n")
	for _, seat := range routeSeats {
		pub := pubRowan
		if seat != "rowan" {
			pub = pubAir
		}
		f.recipients[seat+".yaml"] = []string{pub, pubRecovery}
		fmt.Fprintf(&rules, "  - path_regex: ^%s\\.yaml$\n    unencrypted_regex: ^(NOVA_SECRETS_WRITTEN_BY)$\n    age: %s,%s\n", seat, pub, pubRecovery)
	}
	mustWrite(t, filepath.Join(store, ".sops.yaml"), rules.String(), 0644)
	mustWrite(t, filepath.Join(store, "recovery.pub"), pubRecovery+"\n", 0644)
	// The source seat holds the values this machine can open.
	mustWrite(t, filepath.Join(store, "rowan.yaml"),
		fakeCipher([]string{pubRowan, pubRecovery}, "GH_TOKEN: ghp_carried\n"), 0644)
	// Every other seat holds one sealed name the source carries.
	for _, seat := range routeSeats[1:] {
		mustWrite(t, filepath.Join(store, seat+".yaml"),
			fakeCipher([]string{pubAir, pubRecovery}, "GH_TOKEN: ENC[AES256_GCM,data:x,iv:a,tag:b,type:str]\n"), 0644)
	}
	// The sops the verb runs: the file is there, and the Exec seam is what answers.
	f.sopsPath = filepath.Join(dir, "sops")
	mustWrite(t, f.sopsPath, "#!/bin/sh\nexit 0\n", 0755)
	keyDir := filepath.Join(dir, "keys")
	require.NoError(t, os.MkdirAll(keyDir, 0o700))
	f.keyPath = filepath.Join(keyDir, "rowan.key")
	mustWrite(t, f.keyPath, "AGE-SECRET-KEY-1ROWAN\n# public key: "+pubRowan+"\n", 0600)
	return f
}

func (f *pkFake) exec(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, pkCall{name: filepath.Base(name), args: append([]string(nil), args...), env: append([]string(nil), env...)})
	f.mu.Unlock()
	switch filepath.Base(name) {
	case "sops":
		return f.sops(stdin, args)
	case "git":
		return f.git(args)
	case "gh":
		return f.gh(args)
	}
	return nil, fmt.Errorf("fake: unknown program %s", name)
}

// sops is the fake sops: it decrypts a file the store wrote and encrypts the
// plaintext on stdin to the file's own recipients.
func (f *pkFake) sops(stdin io.Reader, args []string) ([]byte, error) {
	if len(args) > 0 && args[0] == "--version" {
		return []byte("sops 3.13.3\n"), nil
	}
	if len(args) > 0 && args[0] == "-d" {
		data, err := os.ReadFile(args[1])
		if err != nil {
			return nil, err
		}
		_, plaintext, ok := strings.Cut(string(data), "PLAINTEXT\n")
		if !ok {
			return nil, fmt.Errorf("fake sops: %s holds no plaintext", args[1])
		}
		return []byte(plaintext), nil
	}
	name := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--filename-override" {
			name = args[i+1]
		}
	}
	plaintext, err := io.ReadAll(stdin)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("sops:\n    age:\n")
	for _, r := range f.recipients[name] {
		b.WriteString("        - recipient: " + r + "\n")
	}
	b.WriteString("PLAINTEXT\n")
	b.Write(plaintext)
	return []byte(b.String()), nil
}

// git is the fake git: it says the store is on main, clean, and fast-forwardable
// unless the fixture says it carries local commits.
func (f *pkFake) git(args []string) ([]byte, error) {
	if len(args) == 0 {
		return nil, nil
	}
	switch args[0] {
	case "rev-parse":
		return []byte("main\n"), nil
	case "status":
		return nil, nil
	case "fetch":
		return nil, nil
	case "merge":
		if len(f.localCommits) > 0 {
			return nil, fmt.Errorf("fatal: Not possible to fast-forward, aborting.")
		}
		return nil, nil
	case "rev-list":
		return []byte(fmt.Sprintf("%d\n", f.behind)), nil
	case "log":
		return []byte(strings.Join(f.localCommits, "\n") + "\n"), nil
	}
	return nil, nil
}

// gh is the fake forge: it opens the one pull request and reports it merged after
// the polls the fixture says.
func (f *pkFake) gh(args []string) ([]byte, error) {
	if len(args) < 2 {
		return nil, nil
	}
	switch args[0] + " " + args[1] {
	case "pr create":
		return []byte("https://example.com/mas-bandwidth/secrets/pull/42\n"), nil
	case "pr view":
		f.polls++
		if f.polls <= f.openPolls {
			return []byte(`{"state":"OPEN"}`), nil
		}
		return []byte(`{"state":"MERGED"}`), nil
	}
	return nil, nil
}

// callsNamed are the calls the fake ran of one program.
func (f *pkFake) callsNamed(name string) []pkCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []pkCall
	for _, c := range f.calls {
		if c.name == name {
			out = append(out, c)
		}
	}
	return out
}

// gitArgs are the arguments of every git call, one call per line.
func (f *pkFake) gitArgs() []string {
	var out []string
	for _, c := range f.callsNamed("git") {
		out = append(out, strings.Join(c.args, " "))
	}
	return out
}

// ghArgs are the arguments of every gh call, one call per line.
func (f *pkFake) ghArgs() []string {
	var out []string
	for _, c := range f.callsNamed("gh") {
		out = append(out, strings.Join(c.args, " "))
	}
	return out
}

// pkConfig is the fake nova-config: three route-running loop seats and one loop
// that runs no route.
func pkConfig() ProviderKeyConfig {
	return ProviderKeyConfig{
		Providers: []ProviderRow{{Name: "openai", KeyEnv: "OPENAI_API_KEY"}, {Name: "minimax", KeyEnv: "MINIMAX_API_KEY"}},
		Loops: []LoopRow{
			{Seat: "air", Argv: []string{"/usr/local/bin/nova-swarm", "member", "--beat", "10"}},
			{Seat: "bee", Argv: []string{"/usr/local/bin/nova-swarm", "reader", "--lanes", "2"}},
			{Seat: "cee", Argv: []string{"nova-swarm", "member"}},
			{Seat: "dan", Argv: []string{"/usr/local/bin/nova-swarm", "where"}},
			{Seat: "air", Argv: []string{"/usr/local/bin/nova-swarm", "member"}},
		},
	}
}

// pkOptions are the verb's options over the fake store and the fake config.
func pkOptions(f *pkFake, name string, stdin io.Reader) ProviderKeyOptions {
	return ProviderKeyOptions{
		StoreDir: f.storeDir,
		AsName:   "rowan",
		Name:     name,
		KeyPath:  f.keyPath,
		SopsPath: f.sopsPath,
		GitPath:  "git",
		GHPath:   "gh",
		Stdin:    stdin,
		Now:      func() time.Time { return f.now },
		Sleep:    func(d time.Duration) { f.now = f.now.Add(d) },
		Config:   func() (ProviderKeyConfig, error) { return pkConfig(), nil },
		Exec:     f.exec,
	}
}

// TestOneProviderKeyAddIsOnePromptOneCommitEverySeat is the owner's bar: "I should
// have to only enter the key and press enter. Rest just happens." One prompt, one
// commit touching the source seat and every route seat, one pull request, the wait
// ends on the merge, the checkout is pulled, and the value reaches no argv, no
// environment and no output.
func TestOneProviderKeyAddIsOnePromptOneCommitEverySeat(t *testing.T) {
	t.Parallel()

	f := newProviderKeyFake(t)
	f.openPolls = 1 // the pull request is open once, then the forge merges it.
	stdin := &pkRead{inner: strings.NewReader(pkValue + "\n")}
	opts := pkOptions(f, "MINIMAX_API_KEY", stdin)
	var progress strings.Builder
	opts.Progress = &progress

	line, err := RunProviderKeyAdd(opts)
	require.NoError(t, err, "RunProviderKeyAdd: %v", err)

	// One prompt read: the value's line and nothing after it.
	assert.Equal(t, len(pkValue)+1, stdin.bytesRead(), "the value was read more than once")

	// One commit, touching exactly the source seat and the three route seats.
	var added []string
	commits := 0
	for _, call := range f.callsNamed("git") {
		switch {
		case len(call.args) >= 2 && call.args[0] == "add":
			added = append(added, call.args[1])
		case len(call.args) >= 1 && call.args[0] == "commit":
			commits++
		}
	}
	assert.Equal(t, 1, commits, "the key add made more than one commit: %v", f.gitArgs())
	assert.ElementsMatch(t, []string{"rowan.yaml", "air.yaml", "bee.yaml", "cee.yaml"}, added,
		"the commit touched other files than the source seat and the three route seats: %v", added)

	// One pull request, and the wait ends on the merge: the first poll is open, the
	// second is merged.
	creates := 0
	for _, call := range f.callsNamed("gh") {
		if len(call.args) >= 2 && call.args[0] == "pr" && call.args[1] == "create" {
			creates++
		}
	}
	assert.Equal(t, 1, creates, "the key add opened more than one pull request: %v", f.ghArgs())
	assert.Equal(t, 2, f.polls, "the wait did not end on the fake merge")

	// The checkout is pulled to the merged main.
	var pulled bool
	for _, args := range f.gitArgs() {
		if strings.HasPrefix(args, "pull") {
			pulled = true
		}
	}
	assert.True(t, pulled, "the checkout was not pulled: %v", f.gitArgs())

	// The value reaches no argv, no environment and no output.
	for _, call := range f.calls {
		assert.NotContains(t, strings.Join(call.args, " "), pkValue, "the value reached an argv: %v", call.args)
		assert.NotContains(t, strings.Join(call.env, " "), pkValue, "the value reached a child's environment")
	}
	assert.NotContains(t, line, pkValue, "the value reached the result line:\n%s", line)
	assert.NotContains(t, progress.String(), pkValue, "the value reached the progress output:\n%s", progress.String())
	assert.NotContains(t, strings.Join(f.ghArgs(), " "), pkValue, "the value reached the forge's argv")

	// The result names the key and the seats it reached.
	assert.Contains(t, line, "MINIMAX_API_KEY", "the result does not name the key:\n%s", line)
	assert.Contains(t, line, "seats=4", "the result does not name the four seats:\n%s", line)
	assert.Contains(t, line, "merged", "the result does not say the pull request merged:\n%s", line)

	// The seat that runs no route is untouched.
	assert.NotContains(t, strings.Join(added, " "), "dan.yaml", "the seat that runs no route was touched")
}

// TestAStaleCheckoutIsFastForwardedNotRefused_FetchFirst is the fetch-first rule: a
// checkout that is merely behind is brought forward and never refuses, and a
// checkout with local commits is refused naming them.
func TestAStaleCheckoutIsFastForwardedNotRefused_FetchFirst(t *testing.T) {
	t.Parallel()

	t.Run("a checkout behind its upstream is fast-forwarded", func(t *testing.T) {
		t.Parallel()

		f := newProviderKeyFake(t)
		f.behind = 3
		stdin := &pkRead{inner: strings.NewReader(pkValue + "\n")}
		opts := pkOptions(f, "MINIMAX_API_KEY", stdin)

		_, err := RunProviderKeyAdd(opts)
		require.NoError(t, err, "a checkout merely behind must never refuse: %v", err)

		var fetched, fastForwarded bool
		for _, args := range f.gitArgs() {
			if args == "fetch" {
				fetched = true
			}
			if strings.HasPrefix(args, "merge --ff-only") {
				fastForwarded = true
			}
		}
		assert.True(t, fetched, "the store was not fetched: %v", f.gitArgs())
		assert.True(t, fastForwarded, "the store was not fast-forwarded: %v", f.gitArgs())
	})

	t.Run("a checkout with local commits is refused naming them", func(t *testing.T) {
		t.Parallel()

		f := newProviderKeyFake(t)
		f.behind = 2
		f.localCommits = []string{"abc1234 a local commit nobody reviewed"}
		stdin := &pkRead{inner: strings.NewReader(pkValue + "\n")}
		opts := pkOptions(f, "MINIMAX_API_KEY", stdin)

		_, err := RunProviderKeyAdd(opts)
		require.Error(t, err, "a checkout with local commits must be refused")
		assert.Contains(t, err.Error(), "abc1234", "the refusal does not name the local commits: %v", err)
		assert.Equal(t, 0, stdin.bytesRead(), "the value was read although the store refused")
	})
}

// TestAnUnknownKeyNameIsRefused_ProviderKeyAdd is the rule that keeps the
// configuration and the store in step: a name that is no provider row's key_env is
// refused before anything is read or written, and a provider named with another
// provider's key says which row to set.
func TestAnUnknownKeyNameIsRefused_ProviderKeyAdd(t *testing.T) {
	t.Parallel()

	t.Run("a name no provider row holds is refused", func(t *testing.T) {
		t.Parallel()

		f := newProviderKeyFake(t)
		stdin := &pkRead{inner: strings.NewReader(pkValue + "\n")}
		opts := pkOptions(f, "NOT_A_PROVIDER_KEY", stdin)

		_, err := RunProviderKeyAdd(opts)
		require.Error(t, err, "an unknown key name must be refused")
		assert.Contains(t, err.Error(), "key_env", "the refusal does not say what the name must be: %v", err)
		assert.Contains(t, err.Error(), "minimax", "the refusal does not name the provider rows: %v", err)
		assert.Equal(t, 0, stdin.bytesRead(), "the value was read although the name was refused")
		assert.Empty(t, f.callsNamed("git"), "the store was touched although the name was refused")
		assert.Empty(t, f.callsNamed("gh"), "the forge was called although the name was refused")
	})

	t.Run("a provider named with another provider's key says which row to set", func(t *testing.T) {
		t.Parallel()

		f := newProviderKeyFake(t)
		stdin := &pkRead{inner: strings.NewReader(pkValue + "\n")}
		opts := pkOptions(f, "OPENAI_API_KEY", stdin)
		opts.Provider = "minimax"

		_, err := RunProviderKeyAdd(opts)
		require.Error(t, err, "a provider whose key_env is another name must be refused")
		assert.Contains(t, err.Error(), "MINIMAX_API_KEY", "the refusal does not say which row to set: %v", err)
		assert.Equal(t, 0, stdin.bytesRead(), "the value was read although the name was refused")
		assert.Empty(t, f.callsNamed("git"), "the store was touched although the name was refused")
	})
}
