// A recorded gh, for the tests of the issue backend. It is on PATH instead of the real
// one, so nothing here reaches the network — CONTRIBUTING: a test that touches the network
// wants a reason, and this one does not have one.
//
// It holds the thread in a file the test names, serves it back as two JSON pages so that
// the backend's pagination is exercised rather than assumed, and records every argv it was
// called with so a test can prove that an event's text never went on a command line.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	store := os.Getenv("NOVA_BOARD_FAKE_GH_STORE")
	if store == "" {
		fmt.Fprintln(os.Stderr, "the fake gh wants NOVA_BOARD_FAKE_GH_STORE")
		os.Exit(2)
	}
	if log := os.Getenv("NOVA_BOARD_FAKE_GH_ARGV"); log != "" {
		if f, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
			f.Close()
		}
	}
	args := os.Args[1:]
	if len(args) == 0 {
		os.Exit(2)
	}
	switch args[0] {
	case "api":
		raw, err := os.ReadFile(store)
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		type comment struct {
			Body string `json:"body"`
		}
		var all []comment
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) != "" {
				all = append(all, comment{Body: line})
			}
		}
		// Two pages, concatenated the way `gh api --paginate` concatenates them.
		half := len(all) / 2
		for _, page := range [][]comment{all[:half], all[half:]} {
			out, err := json.Marshal(page)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			if page == nil {
				out = []byte("[]")
			}
			fmt.Println(string(out))
		}
	case "issue":
		body, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if strings.TrimSpace(string(body)) == "" {
			fmt.Fprintln(os.Stderr, "the fake gh was given no body on stdin")
			os.Exit(1)
		}
		f, err := os.OpenFile(store, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		fmt.Fprint(f, string(body))
		fmt.Println("https://github.com/example/example/issues/1#issuecomment-1")
	default:
		fmt.Fprintf(os.Stderr, "the fake gh does not know %q\n", args[0])
		os.Exit(2)
	}
}
