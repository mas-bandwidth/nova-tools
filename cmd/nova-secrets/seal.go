// seal.go holds the seal verb: its flags, its run and the helpers only it uses.

package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

func runSealCLI(args []string, s streams) int {
	fs := flag.NewFlagSet("seal", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", asUse)
	keyFlag := fs.String("key", "", keyUse)
	sopsFlag := fs.String("sops", "", sopsUse)
	nameFlag := fs.String("name", "", "the key `NAME` to seal: capitals, digits and _, as GH_TOKEN (required)")
	ghFlag := fs.String("gh", "gh", ghUse)
	gitFlag := fs.String("git", "git", gitUse)
	stdinFlag := fs.Bool("stdin", false, "read the value from standard input instead of a hidden terminal prompt; never put it in an argument")
	noPRFlag := fs.Bool("no-pr", false, noPRUse)
	dryRunFlag := fs.Bool("dry-run", false, dryRunHelp)
	if err := parseFlags(fs, args); err != nil {
		return s.refuse("seal", 2, err)
	}

	// A hidden prompt needs a terminal: only the process's own stdin can be one.
	stdinIsTerminal := false
	if f, ok := s.stdin.(*os.File); ok {
		fi, statErr := f.Stat()
		stdinIsTerminal = statErr == nil && fi.Mode()&os.ModeCharDevice != 0
	}

	line, err := secrets.RunSeal(secrets.SealOptions{
		StoreDir:        *storeFlag,
		AsName:          *asFlag,
		KeyPath:         *keyFlag,
		SopsPath:        *sopsFlag,
		Name:            *nameFlag,
		GHPath:          *ghFlag,
		GitPath:         *gitFlag,
		NoPR:            *noPRFlag,
		DryRun:          *dryRunFlag,
		UseStdin:        *stdinFlag,
		Stdin:           s.stdin,
		StdinIsTerminal: stdinIsTerminal,
		Progress:        s.stderr,
	})
	if err != nil {
		return s.refuse("seal", 2, err)
	}

	fmt.Fprintln(s.stdout, line)
	return 0
}
