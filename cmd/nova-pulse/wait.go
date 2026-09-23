package main

// `nova-pulse wait` at the command line (nova-tools #2546).
//
// THE ARGUMENTS ARE PARSED BY HAND, and that is deliberate. --until takes a condition AND
// its arguments -- `--until file-has ./harvest.log 'LANDED #[0-9]+' --every 5s` -- and Go's
// flag package stops at the first non-flag word, so `./harvest.log` would end the flags and
// --every would arrive as a positional argument nobody read. A verb that silently ignores
// the interval it was given is the same class of bug the zsh one-liners had. So: the line is
// split at the first bare `--` (everything after it is the command to run), and what is left
// is walked once, with an unknown flag refused by name rather than swallowed.

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdWait(args []string, stdout, stderr io.Writer) int {
	head, cmd := splitAtDoubleDash(args)

	var (
		until    []string
		everyRaw string
		timeRaw  string
		busDir   string
		store    string
		user     = pulse.DefaultStoreUser
		passEnv  = pulse.DefaultStorePasswordEnv
		problems []string
	)
	refuseWait := func(format string, a ...any) {
		problems = append(problems, fmt.Sprintf(format, a...))
	}

	i := 0
	for i < len(head) {
		arg := head[i]
		name, inline, hasInline := splitFlag(arg)
		switch name {
		case "until":
			if hasInline {
				until = append(until, inline)
				i++
			} else {
				i++
			}
			// Everything up to the next flag is this condition's arguments. A value
			// that begins with a dash is taken as a flag, which is why no condition
			// argument in the vocabulary starts with one.
			for i < len(head) && !strings.HasPrefix(head[i], "-") {
				until = append(until, head[i])
				i++
			}
		case "every", "timeout", "bus", "store", "store-user", "password-env":
			value := inline
			if !hasInline {
				if i+1 >= len(head) {
					refuseWait("--%s wants a value", name)
					i++
					continue
				}
				value = head[i+1]
				i += 2
			} else {
				i++
			}
			switch name {
			case "every":
				everyRaw = value
			case "timeout":
				timeRaw = value
			case "bus":
				busDir = value
			case "store":
				store = value
			case "store-user":
				user = value
			case "password-env":
				passEnv = value
			}
		case "":
			refuseWait("%s is not a flag and wait takes no positional arguments; the condition's arguments follow --until, and the command to run follows a bare --", oneline.Field(arg))
			i++
		default:
			refuseWait("unknown flag --%s; wait takes --until, --every, --timeout, --bus, --store, --store-user and --password-env", oneline.Field(name))
			i++
		}
	}

	every := pulse.DefaultWaitEvery
	if strings.TrimSpace(everyRaw) != "" {
		d, err := pulse.ParsePollInterval(everyRaw)
		if err != nil {
			refuseWait("--every %s", err)
		} else {
			every = d
		}
	}
	timeout := pulse.DefaultWaitTimeout
	if strings.TrimSpace(timeRaw) != "" {
		d, err := pulse.ParsePollInterval(timeRaw)
		if err != nil {
			refuseWait("--timeout %s", err)
		} else {
			timeout = d
		}
	}
	if len(cmd) == 0 && hasBareDoubleDash(args) {
		refuseWait("a bare -- with nothing after it names no command to run; drop it, or give the command")
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(stderr, "nova-pulse wait: %s\n", p)
		}
		return 2
	}

	return pulse.Wait(pulse.WaitInput{
		Until:   until,
		Every:   every,
		Timeout: timeout,
		Cmd:     cmd,
		Bus:     busDir,
		Store:   pulse.StoreOptions{Addr: store, User: user, PasswordEnv: passEnv},
		Stdout:  stdout,
		Stderr:  stderr,
		// No Now: this verb's clock has to MOVE. Every other verb is handed main's one
		// fixed instant, and a wait whose spent= never grew would never time out, so
		// pulse.Wait's default -- time.Now().UTC() read each tick -- is the right clock
		// here and `now` is deliberately unused.
	})
}

// splitAtDoubleDash splits on the first bare `--`. Everything after it is the command,
// verbatim, including its own flags: that is the whole point of the separator.
func splitAtDoubleDash(args []string) (head, cmd []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

func hasBareDoubleDash(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return true
		}
	}
	return false
}

// splitFlag reads one argument as a flag: `--every`, `-every`, `--every=5s`. A word that is
// not a flag comes back with an empty name.
func splitFlag(arg string) (name, value string, hasValue bool) {
	if !strings.HasPrefix(arg, "-") || arg == "-" || arg == "--" {
		return "", "", false
	}
	trimmed := strings.TrimLeft(arg, "-")
	if eq := strings.IndexByte(trimmed, '='); eq >= 0 {
		return trimmed[:eq], trimmed[eq+1:], true
	}
	return trimmed, "", false
}
