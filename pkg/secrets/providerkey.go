package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// THE MODEL. tla/ProviderKey.tla is the design this verb carries: Prompt (the one
// hidden read), Seal (one commit on one branch into the source seat and every route
// seat), OpenPR (the one pull request), Merge (the forge's action, which the verb
// waits for), Fetch and Pull (the first step of every store verb). The invariant
// the verb holds is OnePromptOnePR: one key add makes one prompt and one pull
// request, whatever the number of seats it touches.

// ProviderKeyConfig is what `provider-key add` reads from nova-config before it
// touches the store: the provider rows (a name and the key_env it holds) and the
// loop rows (the seat and the argv). It is read live, and the loop rows are the
// only source of the route seats (tla/ProviderKey.tla, routeSeats).
type ProviderKeyConfig struct {
	Providers []ProviderRow
	Loops     []LoopRow
}

// ProviderRow is one nova-config provider row: its name and its key_env, the NAME
// of the environment variable its key is in, never the value.
type ProviderRow struct {
	Name   string
	KeyEnv string
}

// LoopRow is one nova-config loop row: the seat it runs on and the argv it runs.
type LoopRow struct {
	Seat string
	Argv []string
}

// RouteSeats are the seats of the loop rows that run a nova-swarm member or a
// reader, de-duplicated and in name order. It is derived from the loop rows and
// never from a hand list (tla/ProviderKey.tla, routeSeats: the "handlist" witness
// is the defect this derivation cannot have).
func (c ProviderKeyConfig) RouteSeats() []string {
	seen := map[string]bool{}
	var seats []string
	for _, l := range c.Loops {
		if l.Seat == "" || seen[l.Seat] || !runsRoute(l.Argv) {
			continue
		}
		seen[l.Seat] = true
		seats = append(seats, l.Seat)
	}
	sort.Strings(seats)
	return seats
}

// runsRoute reports whether argv runs a nova-swarm member or a reader: the programs
// that open a provider route's key.
func runsRoute(argv []string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if filepath.Base(argv[i]) != "nova-swarm" {
			continue
		}
		if argv[i+1] == "member" || argv[i+1] == "reader" {
			return true
		}
	}
	return false
}

// ProviderKeyOptions carries one `provider-key add` request and the test seams
// around it.
//
// The value travels from the one hidden prompt (or --stdin) to the encrypt child's
// stdin and nowhere else: not argv, not a file in the clear, not an output line
// (tla/ProviderKey.tla, NoValueOutsideTheStore).
type ProviderKeyOptions struct {
	StoreDir string
	AsName   string // the source seat: the seat this machine opens
	Name     string // the key: a provider row's key_env
	Provider string // the provider word, when the caller names one

	KeyPath      string
	SopsPath     string
	GHPath       string
	GitPath      string
	GHEnv        []string // child environment for the seat-owned forge calls
	ResolveGHEnv func() ([]string, error)

	NoPR bool
	// UseStdin reads the value from stdin instead of the controlling terminal.
	UseStdin bool
	// Stdin is where the value comes from when it is not a terminal.
	Stdin io.Reader
	// StdinIsTerminal says whether stdin is the person's terminal.
	StdinIsTerminal bool

	// Progress receives one short line per step that can take time (nil = silent).
	// It never carries a value: step names and public facts only.
	Progress io.Writer

	Now   func() time.Time
	Sleep func(time.Duration)

	// Config reads the live nova-config rows: the provider rows and the loop rows.
	Config func() (ProviderKeyConfig, error)

	// MergeWait bounds the wait for the pull request's merge; MergePoll is how
	// often the forge is asked and MergeSay how often the wait says it is waiting.
	MergeWait time.Duration
	MergePoll time.Duration
	MergeSay  time.Duration

	// Exec replaces the os/exec child process, as it does for seal.
	Exec execCommand
}

func (o ProviderKeyOptions) say(format string, a ...interface{}) {
	if o.Progress != nil {
		// ignored: the progress line is best effort; the verb's result still carries the outcome
		_, _ = fmt.Fprintf(o.Progress, "provider-key add: "+format+"\n", a...)
	}
}

func (o ProviderKeyOptions) exec() execCommand {
	if o.Exec != nil {
		return o.Exec
	}
	return realExecCommand
}

// RunProviderKeyAdd seals one provider key into the fleet's source seat and into
// every seat that runs a provider route, in one commit and one pull request, waits
// for that pull request's merge and fast-forwards the checkout to the merged main.
//
// The value is read once, at a hidden prompt, and reaches nothing but the encrypt
// child's stdin (tla/ProviderKey.tla, Prompt and NoValueOutsideTheStore).
func RunProviderKeyAdd(opts ProviderKeyOptions) (string, error) {
	if err := preflight(opts.StoreDir, need{opts.StoreDir, "--store <dir>", false},
		need{opts.AsName, "--as <seat> (the source seat this machine opens)", true},
		need{opts.Name, "<NAME> (the provider key's name)", true},
		need{opts.KeyPath, "--key <path>", false},
		need{opts.SopsPath, "--sops <path>", false}); err != nil {
		return "", err
	}
	if !IsValidEnvVar(opts.Name) {
		return "", fmt.Errorf("invalid key name %q: must match [A-Z][A-Z0-9_]*", opts.Name)
	}
	if opts.GHPath == "" {
		opts.GHPath = "gh"
	}
	if opts.GitPath == "" {
		opts.GitPath = "git"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Sleep == nil {
		opts.Sleep = time.Sleep
	}
	if opts.Config == nil {
		return "", fmt.Errorf("no nova-config rows to read: the verb reads the provider rows and the loop rows live; run: nova-secrets provider-key add -h")
	}
	if !opts.NoPR && opts.ResolveGHEnv != nil {
		var err error
		opts.GHEnv, err = opts.ResolveGHEnv()
		if err != nil {
			return "", err
		}
	}

	// The name must be a provider row's key_env, read live: the configuration says
	// nothing about a value (tla/ProviderKey.tla, keyOf).
	cfg, err := opts.Config()
	if err != nil {
		return "", err
	}
	if err := providerKeyNameOK(cfg, opts.Name, opts.Provider); err != nil {
		return "", err
	}

	run := opts.exec()
	if err := CheckInvariant6(opts.KeyPath); err != nil {
		return "", err
	}
	if _, err := CheckSopsVersion(run, opts.SopsPath); err != nil {
		return "", err
	}

	// The store verb's first step: the checkout is fetched and fast-forwarded, so a
	// checkout that is merely behind never refuses (tla/ProviderKey.tla, Fetch and
	// Pull; the "stalerefuses" witness is the defect).
	opts.say("fetching and fast-forwarding the store")
	if err := FetchFirst(run, opts.StoreDir, opts.GitPath); err != nil {
		return "", err
	}

	// The one input: the value, hidden, asked once (tla/ProviderKey.tla, Prompt).
	// The read is seal's own read, so a provider key is read exactly as a seal is.
	value, err := readSealValue(SealOptions{
		Stdin:           opts.Stdin,
		StdinIsTerminal: opts.StdinIsTerminal,
		UseStdin:        opts.UseStdin,
	})
	if err != nil {
		return "", err
	}

	recoveryKey, err := ReadRecoveryPub(opts.StoreDir)
	if err != nil {
		return "", fmt.Errorf("store %s: %w", opts.StoreDir, err)
	}

	sourceFile := opts.AsName + ".yaml"
	sourcePath := filepath.Join(opts.StoreDir, sourceFile)
	if _, err := os.Stat(sourcePath); err != nil {
		return "", fmt.Errorf("%s does not exist in the store; the source seat is given its first values by seat add, never by provider-key add (SPEC-SECRETS rule 12)", sourceFile)
	}

	// The source seat's own document: the value under its name, every other name it
	// held kept (sealApply is the one fold a seal makes).
	opts.say("reading %s", sourceFile)
	sourcePlaintext, err := sealDecrypt(run, opts.SopsPath, opts.KeyPath, sourcePath)
	if err != nil {
		return "", err
	}
	sourceDoc := sealApply(sourcePlaintext, opts.Name, value)

	opts.say("encrypting to the source seat's recipients")
	sourceCipher, rules, err := sealEncrypt(run, opts.SopsPath, opts.KeyPath, opts.StoreDir, sourceFile, ProviderKeyVerb, sourceDoc)
	if err != nil {
		return "", err
	}

	// The fan-out: every seat that runs a provider route carries the key too, in
	// the same commit (tla/ProviderKey.tla, Seal).
	files := []carryFile{{name: sourceFile, content: sourceCipher}}
	routeSeats := cfg.RouteSeats()
	var already []string
	for _, seat := range routeSeats {
		if seat == opts.AsName {
			continue
		}
		seatFile := seat + ".yaml"
		seatPath := filepath.Join(opts.StoreDir, seatFile)
		if _, err := os.Stat(seatPath); err != nil {
			return "", fmt.Errorf("route seat %s carries no seat file in the store (%s is absent); a route seat that runs a provider route needs its own file; run: nova-secrets seat add --store %s --as %s ...", seat, seatFile, opts.StoreDir, seat)
		}
		held, err := seatInjectTarget(opts.StoreDir, seatFile, recoveryKey)
		if err != nil {
			return "", err
		}
		if slicesContains(held.names, opts.Name) {
			// A seat already carrying the key is left as it is (tla/ProviderKey.tla,
			// Seal: the branch holds the seats the pull request carries the key to).
			already = append(already, seat)
			continue
		}
		doc, err := seatInjectCompose(sourceDoc, held, []string{opts.Name}, opts.AsName, seat, opts.StoreDir)
		if err != nil {
			return "", err
		}
		cipher, seatRules, err := sealEncrypt(run, opts.SopsPath, opts.KeyPath, opts.StoreDir, seatFile, ProviderKeyVerb, doc)
		if err != nil {
			return "", err
		}
		if seatRules != nil && rules == nil {
			rules = seatRules
		}
		files = append(files, carryFile{name: seatFile, content: cipher})
	}

	branch := fmt.Sprintf("seal/%s-%s-%s", opts.AsName, opts.Name, opts.Now().UTC().Format("20060102-150405"))
	commitMsg := fmt.Sprintf("seal %s into %s", opts.Name, strings.Join(seatsOf(files), ", "))
	carry := sealCarry{
		run:          run,
		storeDir:     opts.StoreDir,
		gitPath:      opts.GitPath,
		ghPath:       opts.GHPath,
		ghEnv:        opts.GHEnv,
		seatFile:     sourceFile,
		files:        files,
		rules:        rules,
		branch:       branch,
		message:      commitMsg,
		title:        commitMsg,
		body:         fmt.Sprintf("Sealed with nova-secrets provider-key add. The value was read once at a hidden prompt and was never written to a file in the clear, to argv, or to output. It is sealed into %s.", strings.Join(seatsOf(files), ", ")),
		noPR:         opts.NoPR,
		say:          opts.say,
		waitForMerge: !opts.NoPR,
		mergeWait:    opts.MergeWait,
		mergePoll:    opts.MergePoll,
		mergeSay:     opts.MergeSay,
		resumeName:   opts.Name,
		now:          opts.Now,
		sleep:        opts.Sleep,
		check: func() error {
			return providerKeyOnEverySeat(opts.StoreDir, opts.Name, seatsOf(files))
		},
	}

	prNum, merged, err := carry.carry(sourceCipher)
	if err != nil {
		return "", err
	}
	switch {
	case opts.NoPR:
		return fmt.Sprintf("SECRETS PROVIDER-KEY ADD OK name=%s seats=%d committed branch=%s\n"+
			"SECRETS PROVIDER-KEY ADD NOTE no pull request was opened (--no-pr), so the key is not yet on main; next: git -C %s push -u origin %s, then open and merge its pull request",
			oneline.Field(opts.Name), len(files), oneline.Field(branch), oneline.Field(opts.StoreDir), oneline.Field(branch)), nil
	case !merged:
		return fmt.Sprintf("SECRETS PROVIDER-KEY ADD OK name=%s seats=%d pr=#%s open", oneline.Field(opts.Name), len(files), prNum), nil
	}
	return fmt.Sprintf("SECRETS PROVIDER-KEY ADD OK name=%s seats=%d pr=#%s merged", oneline.Field(opts.Name), len(files), prNum), nil
}

// ProviderKeyVerb is the mark a provider-key add writes into the seat file it
// writes (SPEC-SECRETS "gate", the mark).
const ProviderKeyVerb = "provider-key add"

// seatsOf are the seat names the files of one commit carry, in name order.
func seatsOf(files []carryFile) []string {
	var seats []string
	for _, f := range files {
		seats = append(seats, strings.TrimSuffix(f.name, ".yaml"))
	}
	sort.Strings(seats)
	return seats
}

func slicesContains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// providerKeyNameOK holds the name to the configuration: the name must be the
// key_env of a provider row (tla/ProviderKey.tla, keyOf). With a provider named,
// the row of that provider is the one that holds it; nothing is set when the name
// does not match.
func providerKeyNameOK(cfg ProviderKeyConfig, name, provider string) error {
	if provider != "" {
		for _, p := range cfg.Providers {
			if p.Name != provider {
				continue
			}
			if p.KeyEnv == "" {
				return fmt.Errorf("provider %s holds no key_env, so there is no key to seal; run: nova-config provider set %s --key_env %s", oneline.Field(provider), provider, name)
			}
			if p.KeyEnv != name {
				return fmt.Errorf("provider %s holds key_env %s, not %s; run: nova-secrets provider-key add %s", oneline.Field(provider), oneline.Field(p.KeyEnv), oneline.Field(name), p.KeyEnv)
			}
			return nil
		}
		return fmt.Errorf("no provider row named %s; run: nova-config provider list", oneline.Field(provider))
	}
	for _, p := range cfg.Providers {
		if p.KeyEnv == name {
			return nil
		}
	}
	var rows []string
	for _, p := range cfg.Providers {
		rows = append(rows, fmt.Sprintf("%s key_env=%s", p.Name, dashIfEmpty(p.KeyEnv)))
	}
	return fmt.Errorf("%s is not the key_env of any provider row; the rows are: %s; run: nova-config provider set <provider> --key_env %s",
		oneline.Field(name), strings.Join(rows, ", "), name)
}

// providerKeyOnEverySeat is the check a merged provider key ends on: every seat the
// commit touched carries the key's name on main. The value is never read: the name
// is what the file says (tla/ProviderKey.tla, EnabledOnlyWhenEverySeatCarries).
func providerKeyOnEverySeat(storeDir, name string, seats []string) error {
	for _, seat := range seats {
		file := filepath.Join(storeDir, seat+".yaml")
		keys, _, _, err := ParseStoreFileWithoutDecrypting(file)
		if err != nil {
			return fmt.Errorf("seat %s: %w", seat, err)
		}
		found := false
		for _, k := range keys {
			if k.Name == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("seat %s does not carry %s on main yet", seat, name)
		}
	}
	return nil
}

// ProviderKeyConfigFromNovaConfig reads the live nova-config rows the verb needs:
// the provider rows (a name and its key_env) and the loop rows (the seat and the
// argv). The rows are read from the nova-config program, which owns the store's
// login; no row is read from a file the caller names, and no value is in them.
func ProviderKeyConfigFromNovaConfig(path string) (ProviderKeyConfig, error) {
	providers, err := providerKeyRows(path, "provider")
	if err != nil {
		return ProviderKeyConfig{}, err
	}
	loops, err := providerKeyRows(path, "loop")
	if err != nil {
		return ProviderKeyConfig{}, err
	}
	cfg := ProviderKeyConfig{}
	for _, row := range providers {
		cfg.Providers = append(cfg.Providers, ProviderRow{Name: row["name"], KeyEnv: row["key_env"]})
	}
	for _, row := range loops {
		argv, err := providerKeyArgv(row["argv"])
		if err != nil {
			return ProviderKeyConfig{}, err
		}
		cfg.Loops = append(cfg.Loops, LoopRow{Seat: row["seat"], Argv: argv})
	}
	return cfg, nil
}

// providerKeyRows are the fields of every row of one kind, read from the
// nova-config program's own JSON answer.
func providerKeyRows(path, kind string) ([]map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{kind, "list", "--json"}
	// The one door a child process goes through: a named deadline and a WaitDelay,
	// so a killed child cannot hang its caller on a pipe (pkg/subproc).
	cmd, cancel := subproc.CommandFor(ctx, subproc.BudgetOf(path, args), path, args...)
	defer cancel()
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("nova-config %s list could not be read: %w; the provider rows and the loop rows are read live", kind, err)
	}
	var envelope struct {
		Items []struct {
			Kind   string            `json:"kind"`
			Fields map[string]string `json:"fields"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return nil, fmt.Errorf("nova-config %s list answered something this verb cannot read: %w", kind, err)
	}
	rows := make([]map[string]string, 0, len(envelope.Items))
	for _, item := range envelope.Items {
		rows = append(rows, item.Fields)
	}
	return rows, nil
}

// providerKeyArgv is the argv of a loop row: the JSON array the row holds.
func providerKeyArgv(canonical string) ([]string, error) {
	if canonical == "" {
		return nil, nil
	}
	var argv []string
	if err := json.Unmarshal([]byte(canonical), &argv); err != nil {
		return nil, fmt.Errorf("a loop row's argv is not a JSON array of strings: %w", err)
	}
	return argv, nil
}
