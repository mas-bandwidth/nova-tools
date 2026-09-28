package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// friendConsumer reads --as friend:<f> (card owner); a bench or a bare name
// is refused with the one spelling named.
func friendConsumer(as string) (taskcard.Consumer, error) {
	k, err := taskcard.ParseConsumer(as)
	if err != nil || k.Kind != "friend" {
		return taskcard.Consumer{}, fmt.Errorf("--as wants friend:<f>, got %q", as)
	}
	return k, nil
}

// friendActor is the by of a friend's move: the seat (NOVA_FRIEND) when
// set, which must be the friend; else the friend's name.
func friendActor(friend string) (string, error) {
	if seat := os.Getenv(seatEnv); seat != "" && seat != friend {
		return "", fmt.Errorf("--as friend:%s is not the seat (%s=%s)", friend, seatEnv, seat)
	}
	return friend, nil
}

// quoteField keeps a stream name with spaces ("swarm: cards") one field.
func quoteField(s string) string {
	if strings.ContainsAny(s, " \t\"") {
		return strconv.Quote(s)
	}
	return s
}

// parseInterspersed parses fs over args, allowing positionals before, between
// or after the flags (Go's FlagSet alone stops at the first positional, so a
// published name-first order such as `fleet release <sha> --as <a>` would
// leave every flag unparsed). A bare "--" ends flag parsing as usual.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		if len(args) > len(rest) && args[len(args)-len(rest)-1] == "--" {
			return append(pos, rest...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}
