package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sandboxProbeURL is what the probe fetches from inside the sandbox: a public
// page that answers 200 when a bench's network reaches the internet.
const sandboxProbeURL = "https://models.opencode.ai/api.json"

func init() {
	register(verb{
		name:    "sandbox-probe",
		summary: "prove a bench's network the way a sandboxed card sees it",
		help: `ci sandbox-probe --slot <name>   (RUNNER_NAME and RUNNER_TEMP in the environment)

Rule R: nothing enters the loop untested, and the probe itself is no exception. A probe
on the host can pass while every sandboxed card dies. This proves the bench the way a
card sees it: it builds nova-sandbox from this checkout and fetches inside it, so a bench
enters the loop only after the SANDBOXED probe is green. Run it on Linux benches only,
where the sandbox backend is landlock.

Builds ./cmd/nova-sandbox, then runs curl under it with HOME, the read and write roots
and the working directory under $RUNNER_TEMP/np, and reads the HTTP status. Prints
"PROBE OK runner=<name> slot=<slot> sandboxed-net=200" or, on anything else,
"PROBE FAIL runner=<name> slot=<slot> sandboxed-net=<status>" and exits 1.

Exit 0 the sandboxed network answered 200, 1 it did not or the build failed, 2 bad usage.

example:
  go run ./tools/ci sandbox-probe --slot 1
`,
		do: func(e env, args []string) int { return sandboxProbeVerb(e, args, selRealHost()) },
	})
}

func sandboxProbeVerb(e env, args []string, h selHost) int {
	const name = "sandbox-probe"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	slot := fs.String("slot", "", "the probe slot's name")
	if code, done := selFlags(e, name, fs, args); done {
		return code
	}
	if fs.NArg() > 0 {
		return selRefuse(e, name, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	tmp := e.getenv("RUNNER_TEMP")
	if tmp == "" {
		fmt.Fprintln(e.stderr, "sandbox-probe: RUNNER_TEMP is not set")
		return 1
	}
	runner := e.getenv("RUNNER_NAME")
	root := selRoot(e)
	if code, err := h.stream(root, nil, e.stdout, e.stderr, "go", "build", "./cmd/nova-sandbox"); err != nil || code != 0 {
		fmt.Fprintf(e.stderr, "sandbox-probe: go build ./cmd/nova-sandbox failed: %s\n", selWhy("", code, err))
		return 1
	}
	np := filepath.Join(tmp, "np")
	home := filepath.Join(np, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		fmt.Fprintf(e.stderr, "sandbox-probe: %v\n", err)
		return 1
	}
	pwd, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(e.stderr, "sandbox-probe: %v\n", err)
		return 1
	}
	var out bytes.Buffer
	code, err := h.stream(root, []string{"HOME=" + home}, &out, e.stderr,
		"./nova-sandbox", "--read", pwd, "--write", np, "--cwd", np, "--",
		"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", sandboxProbeURL)
	status := strings.TrimSpace(out.String())
	if err != nil || code != 0 {
		status = fmt.Sprintf("%s (%s)", status, selWhy("", code, err))
		status = strings.TrimSpace(status)
	}
	if status != "200" {
		fmt.Fprintf(e.stdout, "PROBE FAIL runner=%s slot=%s sandboxed-net=%s\n", runner, *slot, status)
		return 1
	}
	fmt.Fprintf(e.stdout, "PROBE OK runner=%s slot=%s sandboxed-net=%s\n", runner, *slot, status)
	return 0
}
