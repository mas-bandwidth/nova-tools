// nova-decide makes one typed decision per call through TypeSafe Jev.
//
// It reads a questions file and a state text, asks the provider once, and
// prints exactly one line. Exit 0 when every answer is at or above the floor,
// 3 when any answer is below it (a suggestion, never an authorization: the
// caller keeps today's behaviour as the fallback), 2 on refusal (no key, bad
// questions, provider error). The key comes only from the environment and is
// never printed.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-decide: one typed decision per call through TypeSafe Jev (see docs/CLI.md)

usage:
  nova-decide --questions <json file> [--state <file>|stdin] [--floor 0.9]
              [--base-url <url>] [--key-env JEV_API_KEY] [--prefix DECIDE]

  --questions <file>  JSON object of name to question: {"type": "choice"|"score"|"noul",
                      "instructions": <text>, "criteria": {<option>: <description>} for
                      choice, [<level texts>] for score, absent for noul} (required;
                      {"questions": {...}} also accepted)
  --state <file>      state text the decision is about; stdin when absent or "-" (default stdin)
  --floor <f>         confidence floor; answers below it are a suggestion (default 0.9)
  --base-url <url>    Jev endpoint (default https://api.typesafe.ai/v1/systemone)
  --key-env <name>    environment variable holding the key (default JEV_API_KEY,
                      TYPESAFE_API_KEY also accepted); never a file, never argv
  --prefix <word>     first token of the one line printed (default DECIDE)

exit codes: 0 every answer at or above the floor, 3 any answer below it (a
suggestion), 2 refusal: no key, bad questions, provider error.

example:
  nova-decide --questions ./questions.json --state ./state.md --floor 0.9
`

// version is empty in every ordinary build and is the one override: a release
// stamps it with -ldflags "-X main.version=<tag>".
var version string

// stdin is a var so tests can replace it; production reads the real stdin.
var stdin io.Reader = os.Stdin

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version":
			if len(args) > 1 {
				return refuse(stderr, "DECIDE", "bad-flags", fmt.Sprintf("version takes no arguments, got %d", len(args)-1))
			}
			fmt.Fprintln(stdout, buildinfo.Line("nova-decide", version))
			return 0
		case "help", "-h", "--help":
			fmt.Fprint(stdout, usage)
			return 0
		}
	}
	fs := flag.NewFlagSet("nova-decide", flag.ContinueOnError)
	questions := fs.String("questions", "", "JSON file of typed questions (required)")
	stateFile := fs.String("state", "", "file holding the state text; stdin when absent or -")
	floor := fs.Float64("floor", 0.9, "confidence floor; answers below it are a suggestion")
	baseURL := fs.String("base-url", decide.DefaultBaseURL, "Jev endpoint")
	keyEnv := fs.String("key-env", decide.DefaultKeyEnv, "environment variable holding the key")
	prefix := fs.String("prefix", "DECIDE", "first token of the one line printed")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "DECIDE", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, *prefix, "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*prefix) == "" {
		return refuse(stderr, "DECIDE", "bad-flags", "--prefix must not be empty")
	}
	if *questions == "" {
		return refuse(stderr, *prefix, "bad-questions", "--questions is required; refusing to guess")
	}
	if *floor < 0 || *floor > 1 {
		return refuse(stderr, *prefix, "bad-floor", fmt.Sprintf("floor must be between 0 and 1 (got %g)", *floor))
	}
	raw, err := os.ReadFile(*questions)
	if err != nil {
		return refuse(stderr, *prefix, "bad-questions", fmt.Sprintf("cannot read questions: %s", oneline.Err(err)))
	}
	qs, err := decide.ParseQuestions(raw)
	if err != nil {
		return refuse(stderr, *prefix, "bad-questions", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	var state string
	switch {
	case *stateFile == "" || *stateFile == "-":
		b, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
		if err != nil {
			return refuse(stderr, *prefix, "bad-state", fmt.Sprintf("cannot read stdin: %s", oneline.Err(err)))
		}
		state = string(b)
	default:
		b, err := os.ReadFile(*stateFile)
		if err != nil {
			return refuse(stderr, *prefix, "bad-state", fmt.Sprintf("cannot read state: %s", oneline.Err(err)))
		}
		state = string(b)
	}
	client, err := decide.New(*baseURL, *keyEnv)
	if err != nil {
		return refuse(stderr, *prefix, "no-key", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	answers, _, err := client.Decide(context.Background(), state, qs)
	if err != nil {
		return refuse(stderr, *prefix, "provider-error", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	fmt.Fprintln(stdout, decide.Line(*prefix, answers, *floor))
	for _, a := range answers {
		if a.Confidence < *floor {
			return 3
		}
	}
	return 0
}

// refuse prints the one refusal line: the prefix, REFUSED, a one-word reason
// and the detail. It goes to stderr; the key is never printed.
func refuse(stderr io.Writer, prefix, reason, detail string) int {
	fmt.Fprintf(stderr, "%s REFUSED reason=%s %s\n", oneline.Field(prefix), oneline.Field(reason), oneline.Escape(oneline.Cap(detail, oneline.TailBytes)))
	return 2
}
