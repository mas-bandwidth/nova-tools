// nova-post prepares, shows and releases the outward-facing payloads.
//
// The house rule is the spec's gate: draft, show, approve, send. This tool renders a
// draft, writes it under --drafts and shows it byte for byte; it sends only when a bus
// receipt from Glenn names the draft's exact hash and is under 24 hours old. There is no
// approve verb: approval is a receipt this tool reads and never issues.
//
// A credential reaches this tool only through `nova-secrets exec`; it reads the one
// environment variable its channel needs, never a file, never argv, and never prints a
// value or its length.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/post"
)

const usage = `nova-post: draft, show and send the outward payloads, behind Glenn's receipt (docs/SPEC-OUTBOUND.md)

usage:
  nova-post draft --channel <ghost|bsky|email|discord> --target <t> --file <body.md> \
                  --drafts <dir> --allowlist <file> [--title <t>] [--link <url>] \
                  [--digest <YYYY-MM-DD>] [--cairn <file>] [--fleet <file>]
  nova-post show  --draft <hash> --drafts <dir>
  nova-post send  --draft <hash> --approval <receipt-id> --drafts <dir> \
                  --bus <dir> --allowlist <file>
  nova-post version
  nova-post help

draft renders the payload once, writes <hash>.post (the bytes) and <hash>.meta under
--drafts, and prints one line. show writes the exact payload bytes to stdout and its
receipt to stderr. send refuses unless the receipt exists in --bus, is Glenn's, names
this draft's hash and is under 24 hours old; it sends the stored bytes and is idempotent.

exit codes: 0 sent or shown, 1 a refusal by the world (no approval, a stale or foreign
approval, a target not in --allowlist, a provider 429/5xx), 2 an invocation that could
not run (missing flag, unreadable file, a --drafts that is not a directory, a body that
names a secret, a credential variable absent from this process).

example:
  nova-post help
  nova-post version
`

// version is empty in every ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>".
var version string

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "POST", "no-arguments", "--channel, --target, --file and --drafts are required; run: nova-post help")
	}
	switch args[0] {
	case "version", "--version":
		if len(args) > 1 {
			return refuse(stderr, "POST", "bad-flags", fmt.Sprintf("version takes no arguments, got %d", len(args)-1))
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
	}
	return refuse(stderr, "POST", "unknown-verb", fmt.Sprintf("%q is not a verb; the verbs are draft, show, send, version, help", oneline.Field(args[0])))
}

func runDraft(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-post draft", flag.ContinueOnError)
	channel := fs.String("channel", "", "ghost, bsky, email or discord (required)")
	target := fs.String("target", "", "the allowlisted target (required)")
	file := fs.String("file", "", "body markdown (required unless --digest)")
	drafts := fs.String("drafts", "", "the draft store (required)")
	allow := fs.String("allowlist", "", "channel<TAB>target per line (required)")
	title := fs.String("title", "", "optional title")
	link := fs.String("link", "", "optional link")
	digest := fs.String("digest", "", "email digest day, YYYY-MM-DD")
	cairn := fs.String("cairn", "", "cairn file (with --digest)")
	fleet := fs.String("fleet", "", "fleet file (with --digest)")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "POST", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "POST", "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	ch, err := requireChannel(*channel)
	if err != nil {
		return report(stderr, "POST", err)
	}
	if strings.TrimSpace(*target) == "" {
		return refuse(stderr, "POST", "no-target", "--target is required; refusing to guess the address")
	}
	if *drafts == "" {
		return refuse(stderr, "POST", "no-drafts", "--drafts is required; refusing to guess the draft store")
	}
	if *allow == "" {
		return refuse(stderr, "POST", "no-allowlist", "--allowlist is required; refusing to guess the finite set")
	}
	if err := requireDraftDir(*drafts); err != nil {
		return report(stderr, "POST", err)
	}
	req := post.Request{Channel: ch, Target: *target, Title: *title, Link: *link, DigestDate: *digest}
	if *digest != "" {
		if *file != "" {
			return refuse(stderr, "POST", "digest-and-file", "--digest and --file are mutually exclusive; name one body")
		}
		if *cairn == "" || *fleet == "" {
			return refuse(stderr, "POST", "digest-no-source", "--digest requires --cairn and --fleet")
		}
		if req.CairnData, err = readFileFlag("--cairn", *cairn); err != nil {
			return report(stderr, "POST", err)
		}
		if req.FleetData, err = readFileFlag("--fleet", *fleet); err != nil {
			return report(stderr, "POST", err)
		}
	} else {
		if *file == "" {
			return refuse(stderr, "POST", "no-file", "--file is required (or --digest for an email digest); refusing to guess the body")
		}
		body, err := readFileFlag("--file", *file)
		if err != nil {
			return report(stderr, "POST", err)
		}
		req.Body = string(body)
	}
	allowed, err := post.Allowed(*allow, ch, *target)
	if err != nil {
		return report(stderr, "POST", err)
	}
	if !allowed {
		return refuse(stderr, "POST", "target-not-allowed", fmt.Sprintf("channel=%s target=%s is not in %s; add the line %s<TAB>%s", oneline.Field(string(ch)), oneline.Field(*target), oneline.Field(*allow), oneline.Field(string(ch)), oneline.Field(*target)))
	}
	if _, _, err := resolveCredential(ch, *target); err != nil {
		return report(stderr, "POST", err)
	}
	d, err := post.Render(req, time.Now().UTC())
	if err != nil {
		return report(stderr, "POST", err)
	}
	if err := post.Save(*drafts, d); err != nil {
		return report(stderr, "POST", err)
	}
	fmt.Fprintf(stdout, "POST DRAFT OK hash=%s channel=%s target=%s bytes=%d drafts=%s\n",
		oneline.Field(d.Hash), oneline.Field(string(d.Channel)), oneline.Field(d.Target), len(d.Bytes), oneline.Field(*drafts))
	return 0
}

func runShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-post show", flag.ContinueOnError)
	draft := fs.String("draft", "", "the draft hash (required)")
	drafts := fs.String("drafts", "", "the draft store (required)")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "POST", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "POST", "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if *draft == "" {
		return refuse(stderr, "POST", "no-draft", "--draft is required; refusing to guess")
	}
	if *drafts == "" {
		return refuse(stderr, "POST", "no-drafts", "--drafts is required; refusing to guess the draft store")
	}
	if err := requireDraftDir(*drafts); err != nil {
		return report(stderr, "POST", err)
	}
	d, err := post.Load(*drafts, *draft)
	if err != nil {
		return report(stderr, "POST", err)
	}
	if _, err := stdout.Write(d.Bytes); err != nil {
		return refuse(stderr, "POST", "write-error", oneline.Err(err))
	}
	fmt.Fprintf(stderr, "POST SHOW OK hash=%s channel=%s bytes=%d drafts=%s\n",
		oneline.Field(d.Hash), oneline.Field(string(d.Channel)), len(d.Bytes), oneline.Field(*drafts))
	return 0
}

func runSend(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-post send", flag.ContinueOnError)
	draft := fs.String("draft", "", "the draft hash (required)")
	approval := fs.String("approval", "", "the release receipt id (required)")
	drafts := fs.String("drafts", "", "the draft store (required)")
	busDir := fs.String("bus", "", "the bus holding the receipt (required)")
	allow := fs.String("allowlist", "", "channel<TAB>target per line (required)")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "POST", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "POST", "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	for _, r := range []struct{ v, name, wants string }{
		{*draft, "--draft", "the draft hash"},
		{*approval, "--approval", "the release receipt id"},
		{*drafts, "--drafts", "the draft store"},
		{*busDir, "--bus", "the bus holding the receipt"},
		{*allow, "--allowlist", "the finite set of targets"},
	} {
		if strings.TrimSpace(r.v) == "" {
			return refuse(stderr, "POST", "missing-flag", fmt.Sprintf("%s is required (%s); refusing to guess", r.name, r.wants))
		}
	}
	if err := requireDraftDir(*drafts); err != nil {
		return report(stderr, "POST", err)
	}
	d, err := post.Load(*drafts, *draft)
	if err != nil {
		return report(stderr, "POST", err)
	}
	endpoint, apiKey, err := resolveCredential(d.Channel, d.Target)
	if err != nil {
		return report(stderr, "POST", err)
	}
	res, err := post.Send(post.SendInput{
		Drafts:    *drafts,
		Hash:      *draft,
		Approval:  *approval,
		BusDir:    *busDir,
		Allowlist: *allow,
		Endpoint:  endpoint,
		APIKey:    apiKey,
		Now:       time.Now().UTC(),
	})
	if err != nil {
		return report(stderr, "POST", err)
	}
	fmt.Fprintln(stdout, res.Line)
	return 0
}

// requireChannel parses --channel through the package that owns the vocabulary.
func requireChannel(s string) (post.Channel, error) {
	if strings.TrimSpace(s) == "" {
		return "", &post.RefusalError{Code: 2, Reason: "no-channel", Detail: "--channel is required; the channels are ghost, bsky, email, discord"}
	}
	return post.ParseChannel(s)
}

// requireDraftDir refuses a --drafts that does not exist or is not a directory. The tool
// creates no directory.
func requireDraftDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return &post.RefusalError{Code: 2, Reason: "no-drafts", Detail: "--drafts " + oneline.Field(dir) + " is not a directory: " + oneline.Err(err)}
	}
	if !info.IsDir() {
		return &post.RefusalError{Code: 2, Reason: "no-drafts", Detail: "--drafts " + oneline.Field(dir) + " is not a directory"}
	}
	return nil
}

func readFileFlag(name, path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, &post.RefusalError{Code: 2, Reason: "unreadable-file", Detail: "cannot read " + name + " " + oneline.Field(path) + ": " + oneline.Err(err)}
	}
	return raw, nil
}

// resolveCredential reads the one variable the channel needs and names the exec line when
// it is absent. It never falls back to a file or the Keychain.
func resolveCredential(ch post.Channel, target string) (endpoint, apiKey string, err error) {
	switch ch {
	case post.Ghost:
		key := os.Getenv("GHOST_ADMIN_KEY")
		if key == "" {
			return "", "", noCredential("GHOST_ADMIN_KEY")
		}
		return envOr("NOVA_POST_GHOST_URL", "https://"+target+"/ghost/api/admin/posts/?source=html"), key, nil
	case post.Bsky:
		key := os.Getenv("BSKY_APP_PASSWORD")
		if key == "" {
			return "", "", noCredential("BSKY_APP_PASSWORD")
		}
		return envOr("NOVA_POST_BSKY_URL", "https://bsky.social/xrpc/com.atproto.repo.createRecord"), key, nil
	case post.Email:
		key := os.Getenv("SMTP_PASSWORD")
		if key == "" {
			key = os.Getenv("SMTP_PASSWORD_BACKUP")
		}
		if key == "" {
			return "", "", noCredential("SMTP_PASSWORD")
		}
		return envOr("NOVA_POST_EMAIL_URL", "https://smtp.invalid/send"), key, nil
	case post.Discord:
		if os.Getenv("DISCORD_BOT_TOKEN") != "" && os.Getenv("DISCORD_FLEET_WEBHOOK") == "" && os.Getenv("DISCORD_FRIENDS_WEBHOOK") == "" {
			return "", "", &post.RefusalError{Code: 2, Reason: "discord-bot-token",
				Detail: "a DISCORD_BOT_TOKEN is present and no webhook is; this verb will not guess a second route, seal a webhook instead"}
		}
		name := "DISCORD_FRIENDS_WEBHOOK"
		if target == "fleet" {
			name = "DISCORD_FLEET_WEBHOOK"
		}
		url := os.Getenv(name)
		if url == "" {
			return "", "", noCredential(name)
		}
		return url, "", nil
	}
	return "", "", &post.RefusalError{Code: 2, Reason: "bad-channel", Detail: "unknown channel"}
}

func noCredential(name string) *post.RefusalError {
	return &post.RefusalError{Code: 2, Reason: "no-credential",
		Detail: name + " is absent from this process; launch it as: nova-secrets exec --only " + name + " -- nova-post ..."}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func refuse(stderr io.Writer, verb, reason, detail string) int {
	fmt.Fprintf(stderr, "%s REFUSED reason=%s %s\n", oneline.Field(verb), oneline.Field(reason), oneline.Escape(detail))
	return 2
}

// report prints a refusal carried by the package that raised it and returns its exit code.
func report(stderr io.Writer, verb string, err error) int {
	var re *post.RefusalError
	if errors.As(err, &re) {
		fmt.Fprintf(stderr, "%s REFUSED reason=%s %s\n", oneline.Field(verb), oneline.Field(re.Reason), oneline.Escape(re.Detail))
		return re.Code
	}
	fmt.Fprintf(stderr, "%s REFUSED reason=error %s\n", oneline.Field(verb), oneline.Escape(oneline.Err(err)))
	return 2
}
