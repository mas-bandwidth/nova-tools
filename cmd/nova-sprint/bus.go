// The bus verbs (nova-tools #3865, rowan-new specs/bus-redis.md): post,
// read, pending, tail, ls and reply over the Redis streams bus:<to> and
// bus:sent:<from> (internal/nsprint/bus). They replace the rowan-stella git
// bus: a friend posts with `bus post`, Rowan keeps `bus tail --as rowan`
// armed. Exit 0 done, 1 refused with the remedy named, 2 usage.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/bus"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "bus",
		Summary: "friends talk to Rowan over Redis streams: post, read, pending, tail, ls, reply",
		Run:     runBus,
	})
}

const busUsage = "want post, read, pending, tail, ls or reply"

func runBus(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "bus", busUsage)
	}
	switch args[0] {
	case "post":
		return runBusPost(ctx, args[1:], out, errOut, false)
	case "reply":
		return runBusPost(ctx, args[1:], out, errOut, true)
	case "read":
		return runBusRead(ctx, args[1:], out, errOut)
	case "pending":
		return runBusPending(ctx, args[1:], out, errOut)
	case "tail":
		sig, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runBusTail(sig, args[1:], out, errOut)
	case "ls":
		return runBusLs(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "bus", "unknown subverb "+args[0]+"; "+busUsage)
	}
}

// busRefused prints the one refusal line and exits 1.
func busRefused(errOut io.Writer, sub string, err error) int {
	var r *bus.Refusal
	if errors.As(err, &r) {
		fmt.Fprintf(errOut, "REFUSED bus %s: %s; remedy: %s\n", sub, oneline.Escape(r.Why), oneline.Escape(r.Remedy))
		return 1
	}
	fmt.Fprintf(errOut, "REFUSED bus %s: %s; remedy: check the store address (--redis, NOVA_SPRINT_REDIS) and the seat, then rerun\n", sub, oneline.Err(err))
	return 1
}

func busOpen(ctx context.Context, addr string, single bool) (*store.Store, error) {
	a := taskAddr(addr)
	if single {
		return store.OpenSingle(ctx, a)
	}
	return store.Open(ctx, a)
}

func runBusPost(ctx context.Context, args []string, out, errOut io.Writer, reply bool) int {
	sub := "post"
	if reply {
		sub = "reply"
	}
	fs, addr := lifeFlags("bus " + sub)
	as := fs.String("as", "", "")
	to := fs.String("to", "", "")
	kind := fs.String("kind", "", "")
	subject := fs.String("subject", "", "")
	body := fs.String("body", "", "")
	bodyFile := fs.String("body-file", "", "")
	ref := fs.String("ref", "", "")
	re := fs.String("re", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "bus "+sub, err.Error())
	}
	bodySet, fileSet := false, false
	fs.Visit(func(f *flag.Flag) {
		bodySet = bodySet || f.Name == "body"
		fileSet = fileSet || f.Name == "body-file"
	})
	if fs.NArg() != 0 || *as == "" || bodySet == fileSet {
		return refuse(errOut, "bus "+sub, "usage: bus "+sub+" --as <me> "+map[bool]string{false: "--to <who> --kind <k> --subject <text>", true: "--re <xid> [--kind <k>] [--subject <text>]"}[reply]+" (--body <text> | --body-file <f>) [--ref <ref>]")
	}
	if reply && (*re == "" || *to != "") {
		return refuse(errOut, "bus reply", "reply takes --re <xid> and no --to: it answers the entry's sender")
	}
	if !reply && (*to == "" || *kind == "" || *subject == "") {
		return refuse(errOut, "bus post", "post needs --to, --kind and --subject")
	}
	text := *body
	if fileSet {
		b, err := os.ReadFile(*bodyFile)
		if err != nil {
			return refuse(errOut, "bus "+sub, err.Error())
		}
		text = string(b)
	}
	m := bus.Message{From: *as, To: *to, Kind: *kind, Subject: *subject, Body: text, Ref: *ref, Re: *re}
	if err := bus.CheckReader(*as); err != nil {
		return busRefused(errOut, sub, err)
	}
	st, err := busOpen(ctx, *addr, false)
	if err != nil {
		return busRefused(errOut, sub, err)
	}
	defer func() { _ = st.Close() }()
	if reply {
		orig, err := bus.Lookup(ctx, st.Client(), *as, *re)
		if err != nil {
			return busRefused(errOut, sub, err)
		}
		m.To = orig.From
		if m.Kind == "" {
			m.Kind = "answer"
		}
		if m.Subject == "" {
			m.Subject = "re: " + strings.TrimPrefix(orig.Subject, "re: ")
		}
	}
	id, err := bus.Post(ctx, st.Client(), m)
	if err != nil {
		return busRefused(errOut, sub, err)
	}
	fmt.Fprintf(out, "POSTED to=%s id=%s\n", m.To, id)
	return 0
}

func busReaderFlags(sub string, args []string, errOut io.Writer) (as, addr *string, n *int, all *bool, code int) {
	fs, a := lifeFlags("bus " + sub)
	as = fs.String("as", "", "")
	n = fs.Int("n", 20, "")
	all = fs.Bool("all", false, "")
	if err := fs.Parse(args); err != nil {
		return nil, nil, nil, nil, refuse(errOut, "bus "+sub, err.Error())
	}
	if fs.NArg() != 0 || *as == "" || *n <= 0 {
		return nil, nil, nil, nil, refuse(errOut, "bus "+sub, "usage: bus "+sub+" --as <me>"+map[bool]string{true: " [--n 20] [--all]", false: ""}[sub == "read"])
	}
	if sub != "read" && (*all || *n != 20) {
		return nil, nil, nil, nil, refuse(errOut, "bus "+sub, "--n and --all belong to bus read")
	}
	return as, a, n, all, -1
}

func runBusRead(ctx context.Context, args []string, out, errOut io.Writer) int {
	as, addr, n, all, code := busReaderFlags("read", args, errOut)
	if code >= 0 {
		return code
	}
	if err := bus.CheckReader(*as); err != nil {
		return busRefused(errOut, "read", err)
	}
	st, err := busOpen(ctx, *addr, false)
	if err != nil {
		return busRefused(errOut, "read", err)
	}
	defer func() { _ = st.Close() }()
	es, err := bus.Fetch(ctx, st.Client(), *as, *n, *all)
	if err != nil {
		return busRefused(errOut, "read", err)
	}
	for _, e := range es {
		fmt.Fprintln(out, e.Line())
		writeBody(out, e.Body)
	}
	if err := bus.Ack(ctx, st.Client(), *as, es); err != nil {
		return busRefused(errOut, "read", err)
	}
	fmt.Fprintf(out, "READ as=%s n=%d\n", *as, len(es))
	return 0
}

// writeBody prints the body under its header, every line indented two
// spaces, so no body line can pass for a header or a receipt.
func writeBody(out io.Writer, body string) {
	if body == "" {
		return
	}
	for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		fmt.Fprintln(out, "  "+oneline.Escape(l))
	}
}

func runBusPending(ctx context.Context, args []string, out, errOut io.Writer) int {
	as, addr, _, _, code := busReaderFlags("pending", args, errOut)
	if code >= 0 {
		return code
	}
	if err := bus.CheckReader(*as); err != nil {
		return busRefused(errOut, "pending", err)
	}
	st, err := busOpen(ctx, *addr, false)
	if err != nil {
		return busRefused(errOut, "pending", err)
	}
	defer func() { _ = st.Close() }()
	ps, err := bus.Pending(ctx, st.Client(), *as)
	if err != nil {
		return busRefused(errOut, "pending", err)
	}
	for _, p := range ps {
		fmt.Fprintf(out, "%s %s idle=%dms deliveries=%d\n", p.ID, p.Stream, p.Idle.Milliseconds(), p.Deliveries)
	}
	fmt.Fprintf(out, "PENDING as=%s n=%d\n", *as, len(ps))
	return 0
}

// busTailBlock is one blocking XREADGROUP of bus tail. A new entry ends the
// wait at once; the bound is how long a signal waits for the loop to see it
// (the store client does not abort a blocked read on its context).
var busTailBlock = time.Second

func runBusTail(ctx context.Context, args []string, out, errOut io.Writer) int {
	as, addr, _, _, code := busReaderFlags("tail", args, errOut)
	if code >= 0 {
		return code
	}
	if err := bus.CheckReader(*as); err != nil {
		return busRefused(errOut, "tail", err)
	}
	st, err := busOpen(ctx, *addr, true)
	if err != nil {
		return busRefused(errOut, "tail", err)
	}
	defer func() { _ = st.Close() }()
	var mu sync.Mutex
	err = bus.Tail(ctx, st.Client(), *as, busTailBlock, func(e bus.Entry) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintln(out, e.Line()+" body="+oneline.Quote(e.Body))
	})
	if err != nil {
		return busRefused(errOut, "tail", err)
	}
	return 0
}

func runBusLs(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("bus ls")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return refuse(errOut, "bus ls", "usage: bus ls [--redis <addr>]")
	}
	st, err := busOpen(ctx, *addr, false)
	if err != nil {
		return busRefused(errOut, "ls", err)
	}
	defer func() { _ = st.Close() }()
	ins, err := bus.Ls(ctx, st.Client())
	if err != nil {
		return busRefused(errOut, "ls", err)
	}
	var total int64
	for _, in := range ins {
		if in.Name != bus.All {
			fmt.Fprintf(out, "%s len=%d unread=%d\n", bus.InboxKey(in.Name), in.Len, in.Unread[in.Name])
			total += in.Unread[in.Name]
			continue
		}
		var parts []string
		for _, p := range bus.People {
			if u, ok := in.Unread[p]; ok {
				parts = append(parts, fmt.Sprintf("%s:%d", p, u))
			}
		}
		fmt.Fprintf(out, "%s len=%d unread=%s\n", bus.InboxKey(in.Name), in.Len, busList(parts))
	}
	fmt.Fprintf(out, "BUS inboxes=%d unread=%d\n", len(ins), total)
	return 0
}

func busList(parts []string) string {
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}
