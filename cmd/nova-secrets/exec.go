// exec.go holds the exec verb: its flags, its run and the helpers only it uses.

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
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

// redisWidthWriteVerbs are the redis-cli spellings that write or remove a
// string key. redis takes its command names in any case; the width key is a
// string, so the hash and list writes are not this set.
var redisWidthWriteVerbs = map[string]bool{
	"set": true, "setnx": true, "setex": true, "psetex": true, "getset": true,
	"mset": true, "msetnx": true, "del": true, "unlink": true,
}

// friendWidthName reports the friend a token names as its working column,
// friend:<name>:width -- the sprint table's key -- or "" when it is not that
// key (nova-tools#2676).
func friendWidthName(tok string) string {
	if !strings.HasPrefix(tok, "friend:") || !strings.HasSuffix(tok, ":width") {
		return ""
	}
	name := strings.TrimSuffix(strings.TrimPrefix(tok, "friend:"), ":width")
	if !secrets.IsValidAsName(name) {
		return ""
	}
	return name
}

// refusedWidthHandWrite is the one command exec refuses for a reason that is
// another tool's law (nova-tools#2676): redis-cli writing
// friend:<name>:width, the sprint table's working column. Since #3447 that
// column is the friend row's working count, written by the friend row loop
// from the friend's leased tasks; no beat writes the
// row (the retired nova-wake beat refused it), so the remedy is taking work
// through the queue, never a beat and never a hand-write (nova-tools#3807). The hand-write reached the
// store only because the store held REDISCLI_AUTH. Reads of the key still run.
func refusedWidthHandWrite(cmdArgs []string) error {
	base := filepath.Base(cmdArgs[0])
	if base != "redis-cli" && base != "redis-cli.exe" {
		return nil
	}
	writes, name := false, ""
	for i := 1; i < len(cmdArgs); i++ {
		tok := cmdArgs[i]
		if redisWidthWriteVerbs[strings.ToLower(tok)] {
			writes = true
		}
		if n := friendWidthName(tok); n != "" {
			name = n
		}
	}
	if !writes || name == "" {
		return nil
	}
	return fmt.Errorf("redis-cli writing friend:%s:width by hand through nova-secrets exec is refused; that count is the friend row's (friend:%s working), written only by the friend row loop from the friend's leased tasks, never a redis-cli line (nova-tools #3447)", name, name)
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

	// The sprint table's working column is the friend's own tool's to write, never a
	// redis-cli line through this exec; the hand-write of it went through because the
	// store held REDISCLI_AUTH.
	if len(cmdArgs) > 0 {
		if err := refusedWidthHandWrite(cmdArgs); err != nil {
			return s.refuse("exec", 125, err)
		}
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
