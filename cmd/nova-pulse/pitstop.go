package main

// The pitstop verb's CLI: the second of two ways the fleet Redis key `pitstop` is read and
// written. The CLI parses flags by hand because the verb takes a positional argument under
// `check` -- `check <verb>` -- and Go's flag package stops at the first non-flag word, so a
// `check launch --store X` would read `--store` as a positional argument nobody parsed.
// The library does the work; the library's seams are filled here from the same flags event
// and fold and wait already use -- --store / --user / --password-env -- so a card that calls
// `nova-pulse pitstop check launch` reaches the fleet Redis through the same dial.
//
// The walker is one pass over `rest`, advancing `i` itself. A --flag with an inline value
// (`--flag=val`) advances by 1; a --flag with the next arg as its value advances by 2 and
// skips the value at the top of the next iteration. A token that is not a flag under `set`
// is a refusal; under `check` it is the verb name, exactly one.

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdPitstop(args []string, stdout, stderr io.Writer) int {
	// pitstop is decision-then-exit; a `--` after the subcommand would be a runner mistake.
	head, cmd := splitAtDoubleDash(args)
	if len(cmd) > 0 {
		return refuse(stderr, " pitstop", "a bare -- with a command is not a pitstop invocation; pitstop makes its decision and exits")
	}
	if len(head) == 0 {
		return refuse(stderr, " pitstop", "a subcommand is required; pitstop takes set or check")
	}
	sub := strings.TrimSpace(head[0])
	rest := head[1:]

	var (
		state     string
		exceptRaw string
		verb      string
		store     string
		user      = pulse.DefaultStoreUser
		passEnv   = pulse.DefaultStorePasswordEnv
		key       string
		// positionals collects every token that was not a flag under its subcommand.
		// Under `set` any non-flag is a refusal; under `check` the first non-flag is the
		// verb name and a second non-flag is a refusal.
		positionals []string
		problems    []string
	)
	refuse := func(format string, a ...any) {
		problems = append(problems, fmt.Sprintf(format, a...))
	}

	for i := 0; i < len(rest); {
		arg := rest[i]
		name, inline, hasInline := splitFlag(arg)
		switch name {
		case "state":
			value, advance, ok := takePitstopValue(rest, i, inline, hasInline)
			if !ok {
				refuse("--state wants a value")
				i++
				continue
			}
			state = value
			i += advance
		case "except":
			value, advance, ok := takePitstopValue(rest, i, inline, hasInline)
			if !ok {
				refuse("--except wants a value")
				i++
				continue
			}
			exceptRaw = value
			i += advance
		case "verb":
			value, advance, ok := takePitstopValue(rest, i, inline, hasInline)
			if !ok {
				refuse("--verb wants a value")
				i++
				continue
			}
			verb = value
			i += advance
		case "store":
			value, advance, ok := takePitstopValue(rest, i, inline, hasInline)
			if !ok {
				refuse("--store wants a value")
				i++
				continue
			}
			store = value
			i += advance
		case "user", "store-user":
			value, advance, ok := takePitstopValue(rest, i, inline, hasInline)
			if !ok {
				refuse("--%s wants a value", name)
				i++
				continue
			}
			user = value
			i += advance
		case "password-env":
			value, advance, ok := takePitstopValue(rest, i, inline, hasInline)
			if !ok {
				refuse("--password-env wants a value")
				i++
				continue
			}
			passEnv = value
			i += advance
		case "key":
			value, advance, ok := takePitstopValue(rest, i, inline, hasInline)
			if !ok {
				refuse("--key wants a value")
				i++
				continue
			}
			key = value
			i += advance
		case "":
			// A token that is not a flag under set is a refusal; under check it is the
			// verb name. The subcommand check below decides which.
			positionals = append(positionals, arg)
			i++
		default:
			refuse("unknown flag --%s; pitstop takes --state, --except, --verb, --store, --user, --password-env and --key", name)
			i++
		}
	}

	var except []string
	if exceptRaw != "" {
		for _, e := range strings.Split(exceptRaw, ",") {
			if s := strings.TrimSpace(e); s != "" {
				except = append(except, s)
			}
		}
	}

	switch sub {
	case "set":
		if strings.TrimSpace(state) == "" {
			refuse("--state is required with set; it wants pause or resume")
		}
		if strings.TrimSpace(verb) != "" {
			refuse("--verb is a check flag, not a set flag; drop it")
		}
		if len(positionals) > 0 {
			refuse("%s is not a flag and pitstop takes no positional arguments under set; the verb name follows the check subcommand, and the flags follow the verb name", oneline.Field(positionals[0]))
		}
	case "check":
		if len(positionals) == 0 {
			refuse("check wants a verb name as a positional argument, not a flag; usage: pitstop check <verb>")
		} else if len(positionals) > 1 {
			refuse("check takes one verb name; got %d positionals: %s", len(positionals), oneline.Field(strings.Join(positionals, " ")))
		} else {
			verb = positionals[0]
		}
		if strings.TrimSpace(state) != "" {
			refuse("--state is a set flag, not a check flag; drop it")
		}
		if len(except) > 0 {
			refuse("--except is a set flag, not a check flag; drop it")
		}
	default:
		refuse("%s is not a subcommand; pitstop takes set or check", oneline.Field(sub))
	}

	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(stderr, "nova-pulse pitstop: %s\n", p)
		}
		return 2
	}

	return pulse.Pitstop(pulse.PitstopInput{
		Sub:    sub,
		State:  state,
		Except: except,
		Verb:   verb,
		Store: pulse.StoreOptions{
			Addr:        store,
			User:        user,
			PasswordEnv: passEnv,
			Timeout:     5 * time.Second,
		},
		Key:    key,
		Stdout: stdout,
		Stderr: stderr,
	})
}

// takePitstopValue reads the value of a --flag. The flag's value is either inline
// (`--flag=val`, advance=1) or the next argument (`--flag val`, advance=2). ok=false means
// the caller should refuse: the value was missing, or the next argument is itself a flag
// (a value that begins with a dash would have been taken as the next flag and not a value).
func takePitstopValue(args []string, i int, inline string, hasInline bool) (string, int, bool) {
	if hasInline {
		return inline, 1, true
	}
	if i+1 >= len(args) {
		return "", 1, false
	}
	if strings.HasPrefix(args[i+1], "-") {
		return "", 1, false
	}
	return args[i+1], 2, true
}
