// exec.go holds the exec verb: its flags, its run and the helpers only it uses.

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// sopsIdentityEnv is every variable in sops' documented age identity lookup. None of
// them reaches the command exec starts.
var sopsIdentityEnv = []string{
	"SOPS_AGE_KEY_FILE",
	"SOPS_AGE_KEY",
	"SOPS_AGE_KEY_CMD",
	"SOPS_AGE_SSH_PRIVATE_KEY_FILE",
	"SOPS_KEYSERVICE",
}



func runExecCLI(args []string, s streams) int {
	// The flags stand before '--' and the command after it. With no '--' every argument
	// is a flag, and the missing command is named with the missing flags, in one line.
	flagArgs, cmdArgs := args, []string(nil)
	if i := slices.Index(args, "--"); i >= 0 {
		flagArgs, cmdArgs = args[:i], args[i+1:]
	}

	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", asUse)
	keyFlag := fs.String("key", "", keyUse)
	sopsFlag := fs.String("sops", "", sopsUse)
	onlyFlag := fs.String("only", "", "the key `names` to put in the command's environment, comma separated (GH_TOKEN,API_KEY), or all (required)")
	var requireFlags stringSlice
	fs.Var(&requireFlags, "require", "a key `NAME` that must be in the seat's file and in --only, or exec refuses; repeatable")

	// -h among the flags, before the --, is the verb's help; after it, the command's.
	if len(flagArgs) > 0 && flagArgs[0] == "help" {
		panic(verbflag.Help{FS: fs})
	}
	if err := parseVerb(fs, flagArgs); err != nil {
		return s.refuse("exec", 125, err)
	}



	if len(fs.Args()) > 0 {
		return s.refuse("exec", 125, fmt.Errorf("unexpected argument %s before '--'; the command goes after '--': nova-secrets exec <flags> -- <cmd> [args...]", oneline.Quote(fs.Args()[0])))
	}

	// sops' identity lookup is defeated for the command as well as for the sops child:
	// every variable in it is dropped from this process before anything runs, so the
	// command, which this process becomes, never inherits a route to another key
	// (SPEC-SECRETS test 2). The sops child's environment is built, not edited, inside
	// the package.
	for _, name := range sopsIdentityEnv {
		// ignored: os.Unsetenv fails only on a name the platform cannot hold, and these names are fixed constants
		_ = os.Unsetenv(name)
	}

	code, err := secrets.RunExec(*storeFlag, *asFlag, *keyFlag, *sopsFlag, *onlyFlag, requireFlags, cmdArgs)
	if err != nil {
		return s.refuse("exec", code, err)
	}
	return code
}
