// providerkey.go holds the provider-key verb: its flags, its run and the helpers
// only it uses.

package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

// providerKeyAddUse is the flag help the verb's flags carry.
const providerKeyAddUse = "the provider key's `NAME`: the key_env of a nova-config provider row, as MINIMAX_API_KEY (required)"

// providerKeyConfigUse is the flag help of the nova-config program the verb reads
// the provider rows and the loop rows from.
const providerKeyConfigUse = "`path` of the nova-config program the provider rows and the loop rows are read from (default nova-config)"

// runProviderKeyCLI is `nova-secrets provider-key`: the one verb that adds a
// provider key to the fleet. Its only verb today is add.
func runProviderKeyCLI(args []string, s streams) int {
	if len(args) == 0 || args[0] != "add" {
		fmt.Fprintf(s.stderr, "SECRETS REFUSED: provider-key takes the verb add; run: nova-secrets provider-key add -h\n")
		return 2
	}
	return runProviderKeyAddCLI(args[1:], s)
}

// runProviderKeyAddCLI is `nova-secrets provider-key add <NAME>`: the owner enters
// the key and presses enter, and the rest happens (tla/ProviderKey.tla, OnePromptOnePR).
func runProviderKeyAddCLI(args []string, s streams) int {
	fs := flag.NewFlagSet("provider-key add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", "the source `seat` this machine opens, the seat the key is sealed into first (required)")
	keyFlag := fs.String("key", "", keyUse)
	sopsFlag := fs.String("sops", "", sopsUse)
	providerFlag := fs.String("provider", "", "the provider `word` whose key_env the NAME must be, as minimax; the row is read live and nothing is set")
	configFlag := fs.String("nova-config", "nova-config", providerKeyConfigUse)
	ghFlag := fs.String("gh", "gh", ghUse)
	gitFlag := fs.String("git", "git", gitUse)
	stdinFlag := fs.Bool("stdin", false, "read the value from standard input instead of a hidden terminal prompt; never put it in an argument")
	noPRFlag := fs.Bool("no-pr", false, noPRUse)
	if err := parseVerb(fs, args); err != nil {
		return s.refuse("provider-key add", 2, err)
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintf(s.stderr, "SECRETS REFUSED: provider-key add takes the key's NAME and nothing else; run: nova-secrets provider-key add -h\n")
		return 2
	}

	// A hidden prompt needs a terminal: only the process's own stdin can be one.
	stdinIsTerminal := false
	if f, ok := s.stdin.(*os.File); ok {
		fi, statErr := f.Stat()
		stdinIsTerminal = statErr == nil && fi.Mode()&os.ModeCharDevice != 0
	}

	line, err := secrets.RunProviderKeyAdd(secrets.ProviderKeyOptions{
		StoreDir:        *storeFlag,
		AsName:          *asFlag,
		Name:            rest[0],
		Provider:        *providerFlag,
		KeyPath:         *keyFlag,
		SopsPath:        *sopsFlag,
		GHPath:          *ghFlag,
		GitPath:         *gitFlag,
		ResolveGHEnv:    func() ([]string, error) { return seatcred.ForgeCommandEnv(nil, os.Getenv) },
		NoPR:            *noPRFlag,
		UseStdin:        *stdinFlag,
		Stdin:           s.stdin,
		StdinIsTerminal: stdinIsTerminal,
		Progress:        s.stderr,
		Config:          func() (secrets.ProviderKeyConfig, error) { return secrets.ProviderKeyConfigFromNovaConfig(*configFlag) },
	})
	if err != nil {
		return s.refuse("provider-key add", 2, err)
	}

	fmt.Fprintln(s.stdout, oneline.Escape(line))
	return 0
}
