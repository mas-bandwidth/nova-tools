// nova-post drafts, shows and sends outward posts behind Glenn's approval. It
// prepares and renders a draft freely; it releases one only on a bus receipt
// that names the payload's hash. See docs/SPEC-OUTBOUND.md.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/post"
)

const usage = `nova-post: draft, show and send outward posts behind Glenn's approval (see docs/SPEC-OUTBOUND.md)

usage:
  nova-post draft --channel <ghost|bsky|email|discord> --target <t> --file <body.md>
                  --drafts <dir> --allowlist <file> [--title <t>] [--link <url>]
                  [--digest <YYYY-MM-DD>] [--cairn <file>] [--fleet <file>]
  nova-post show  --draft <hash> --drafts <dir>
  nova-post send  --draft <hash> --approval <receipt-id> --drafts <dir>
                  --bus <dir> --allowlist <file>
  nova-post version
  nova-post help

  --channel <c>     ghost, bsky, email or discord (required)
  --target <t>      the allowlisted target: a Ghost site, a bsky handle, an
                    email list, or fleet|friends (required)
  --file <body.md>  body markdown; --digest is email-only and exclusive with it
  --drafts <dir>    the draft store; required on every verb, created by you
  --allowlist <f>   one channel<TAB>target per line; required on draft and send
  --title <t>       optional title or subject
  --link <url>      optional link, rendered as a bsky link card
  --digest <day>    email digest for YYYY-MM-DD; needs --cairn and --fleet
  --cairn <file>    the day's cairn beats (with --digest)
  --fleet <file>    the day's fleet numbers (with --digest)
  --draft <hash>    the payload hash the draft verb printed
  --approval <id>   the bus receipt id carrying APPROVE nova-post sha256=<hash>
  --bus <dir>       the bus the receipt is read from; required on send

exit codes: 0 the verb ran, 1 the gate or a provider said NO, 2 the invocation
could not run. A credential is read only from the environment nova-secrets exec
delivers, and is never printed.

example:
  nova-post version
  nova-post help
`

// version is empty in every ordinary build and is the one override: a release
// stamps it with -ldflags "-X main.version=<tag>".
var version string

// transport is the engine's seam: production leaves it at DefaultTransport,
// tests point the channels at their fakes.
var transport = post.DefaultTransport()

// now is the injected clock the approval window is read from; there is no
// --now flag, because a caller who can name the time can forge freshness.
var now = time.Now

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuseLine(stderr, "no-arguments", "give one of draft, show, send, version or help; run: nova-post help", 2)
	}
	switch args[0] {
	case "version", "--version":
		if len(args) > 1 {
			return refuseLine(stderr, "bad-flags", "version takes no arguments; run: nova-post help", 2)
		}
		fmt.Fprintln(stdout, buildinfo.Line("nova-post", version))
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "draft":
		return runDraft(args[1:], stdout, stderr)
	case "show":
		return runShow(args[1:], stdout, stderr)
	case "send":
		return runSend(args[1:], stdout, stderr)
	default:
		return refuseLine(stderr, "bad-verb", fmt.Sprintf("unknown verb %q; run: nova-post help", oneline.Field(args[0])), 2)
	}
}

func runDraft(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-post draft", flag.ContinueOnError)
	channel := fs.String("channel", "", "ghost, bsky, email or discord")
	target := fs.String("target", "", "the allowlisted target")
	file := fs.String("file", "", "body markdown")
	drafts := fs.String("drafts", "", "the draft store")
	allow := fs.String("allowlist", "", "the allowlist file")
	title := fs.String("title", "", "title or subject")
	link := fs.String("link", "", "link for a bsky card")
	digest := fs.String("digest", "", "email digest day")
	cairn := fs.String("cairn", "", "cairn file for the digest")
	fleet := fs.String("fleet", "", "fleet file for the digest")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuseLine(stderr, "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes), 2)
	}
	if fs.NArg() > 0 {
		return refuseLine(stderr, "bad-flags", fmt.Sprintf("unexpected argument %q; run: nova-post help", oneline.Field(fs.Arg(0))), 2)
	}
	for _, req := range []struct{ name, value string }{
		{"--channel", *channel}, {"--target", *target}, {"--drafts", *drafts}, {"--allowlist", *allow},
	} {
		if req.value == "" {
			return refuseLine(stderr, "missing-flag", req.name+" is required; refusing to guess, run: nova-post help", 2)
		}
	}
	c, err := post.ParseChannel(*channel)
	if err != nil {
		return fail(stderr, err)
	}
	switch {
	case *digest != "" && *file != "":
		return refuseLine(stderr, "digest-with-file", "--digest and --file are mutually exclusive; give one, run: nova-post help", 2)
	case *digest == "" && *file == "":
		return refuseLine(stderr, "missing-file", "either --file or --digest is required; refusing to guess, run: nova-post help", 2)
	case *digest != "" && (*cairn == "" || *fleet == ""):
		return refuseLine(stderr, "digest-missing-inputs", "--digest needs both --cairn and --fleet; give them, run: nova-post help", 2)
	case c == post.Discord && (*title != "" || *link != ""):
		return refuseLine(stderr, "discord-no-title-link", "--title and --link do not belong to discord; drop them, run: nova-post help", 2)
	}
	res, err := post.Draft(post.Options{
		Channel: c, Target: *target, Drafts: *drafts, Allowlist: *allow,
		File: *file, Title: *title, Link: *link, Digest: *digest,
		Cairn: *cairn, Fleet: *fleet, Now: now(), Transport: transport,
	})
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "POST DRAFT OK hash=%s channel=%s target=%s bytes=%d drafts=%s\n",
		oneline.Field(res.Hash), oneline.Field(string(c)), oneline.Field(res.Meta.Target),
		res.Meta.Bytes, oneline.Field(*drafts))
	return 0
}

func runShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-post show", flag.ContinueOnError)
	draft := fs.String("draft", "", "the payload hash")
	drafts := fs.String("drafts", "", "the draft store")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuseLine(stderr, "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes), 2)
	}
	if fs.NArg() > 0 {
		return refuseLine(stderr, "bad-flags", fmt.Sprintf("unexpected argument %q; run: nova-post help", oneline.Field(fs.Arg(0))), 2)
	}
	if *draft == "" {
		return refuseLine(stderr, "missing-flag", "--draft is required; refusing to guess, run: nova-post help", 2)
	}
	if *drafts == "" {
		return refuseLine(stderr, "missing-flag", "--drafts is required; refusing to guess, run: nova-post help", 2)
	}
	res, err := post.Show(*drafts, *draft)
	if err != nil {
		return fail(stderr, err)
	}
	stdout.Write(res.Payload)
	fmt.Fprintf(stderr, "POST SHOW OK hash=%s channel=%s bytes=%d drafts=%s\n",
		oneline.Field(*draft), oneline.Field(res.Meta.Channel), len(res.Payload), oneline.Field(*drafts))
	return 0
}

func runSend(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-post send", flag.ContinueOnError)
	draft := fs.String("draft", "", "the payload hash")
	approval := fs.String("approval", "", "the bus receipt id")
	drafts := fs.String("drafts", "", "the draft store")
	busDir := fs.String("bus", "", "the bus directory")
	allow := fs.String("allowlist", "", "the allowlist file")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuseLine(stderr, "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes), 2)
	}
	if fs.NArg() > 0 {
		return refuseLine(stderr, "bad-flags", fmt.Sprintf("unexpected argument %q; run: nova-post help", oneline.Field(fs.Arg(0))), 2)
	}
	for _, req := range []struct{ name, value string }{
		{"--draft", *draft}, {"--approval", *approval}, {"--drafts", *drafts}, {"--bus", *busDir}, {"--allowlist", *allow},
	} {
		if req.value == "" {
			return refuseLine(stderr, "missing-flag", req.name+" is required; refusing to guess, run: nova-post help", 2)
		}
	}
	res, err := post.Send(post.Options{
		Draft: *draft, Approval: *approval, Drafts: *drafts, Bus: *busDir,
		Allowlist: *allow, Now: now(), Transport: transport,
	})
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, res.Line)
	return 0
}

// refuseLine prints one refusal line on stderr: the token, the reason and the
// remedy. It never prints a credential.
func refuseLine(stderr io.Writer, reason, remedy string, code int) int {
	fmt.Fprintf(stderr, "POST REFUSED reason=%s %s\n",
		oneline.Field(reason), oneline.Cap(oneline.Escape(remedy), oneline.TailBytes))
	return code
}

// fail maps an engine error to its refusal line and exit code.
func fail(stderr io.Writer, err error) int {
	var r *post.Refusal
	if errors.As(err, &r) {
		return refuseLine(stderr, r.Reason, r.Remedy, r.Exit)
	}
	return refuseLine(stderr, "internal-error", oneline.Err(err), 2)
}
