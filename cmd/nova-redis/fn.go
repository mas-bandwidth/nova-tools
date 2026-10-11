package main

// The fn verbs put the nova_sprint Redis function library (the Lua that
// nova-table and nova-config call with FCALL) on a store, and say whether a
// store holds it. The library is the one this binary embeds
// (pkg/nsprint/fn's lua/, described by fn.Spec) and the machinery is
// pkg/redisfn:
//
//   - fn load is redisfn's Ensure, the deployer's load: nothing is written
//     when the store holds this exact code, and other code under the name is
//     replaced with one FUNCTION LOAD REPLACE. One receipt line:
//     LOADED|UNCHANGED|REPLACED nova_sprint sha=<digest> [was=<digest>] store=<addr>.
//   - fn check is redisfn's Check and changes nothing: OK (exit 0), STALE or
//     MISSING (exit 1): OK|STALE|MISSING nova_sprint sha=<digest>
//     loaded=<digest|none> want=<digest> store=<addr>.
//
// A failure of either is one FAILED line on stderr, with the remedy for its
// cause: exit 1 when the store answered with a refusal, exit 2 when no
// answer came or the login was refused (redisconn.Classify, as for every
// nova-redis verb). Every line holds one sha=, this binary's digest.
//
// Both log in as --user (default NOVA_REDIS_USER) with the password in the
// variable --password-env names, and open the store through connect
// (pkg/redisconn), as every nova-redis verb does.
//
// A tool preparing for an FCALL uses redisfn.LoadMissing, which loads the
// library only when it is absent so it does not overwrite a deployed library.
// fn load is the explicit deployment operation.
//
// The fn verbs print their own lines (the receipt's bare head, LOADED
// nova_sprint sha=..., is a line the skeleton cannot render), so they are
// Prints verbs.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
	"github.com/mas-bandwidth/nova-tools/pkg/redisfn"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// library is the function library the fn verbs load and check.
func library() redisfn.Library {
	lib := fn.Spec()
	lib.Remedy = "nova-redis fn load --addr <host:port>"
	return lib
}

// fnLoadVerb is the fn load verb: a store write of this binary's library.
func fnLoadVerb(d deps) tool.Verb {
	return tool.Verb{
		Name:    "fn load",
		Usage:   "fn load --redis <host:port> [--user <name>] [--password-env <NAME>]",
		Example: "",
		Effect:  tool.LocalWrite,
		Flags: func(f *tool.Flags) {
			f.Prints()
			loginFlags(f)
		},
		Run: func(c *tool.Call) *tool.Out { return fnRun(c, d, "load") },
	}
}

// fnCheckVerb is the fn check verb: an inspection of the store's library.
func fnCheckVerb(d deps) tool.Verb {
	return tool.Verb{
		Name:    "fn check",
		Usage:   "fn check --redis <host:port> [--user <name>] [--password-env <NAME>]",
		Example: "",
		Effect:  tool.Inspection,
		Flags: func(f *tool.Flags) {
			f.Prints()
			loginFlags(f)
		},
		Run: func(c *tool.Call) *tool.Out { return fnRun(c, d, "check") },
	}
}

// fnRun is fn load and fn check over the store d.fnOpen (or connect) opens.
func fnRun(c *tool.Call, d deps, sub string) *tool.Out {
	store := loginFrom(c)
	if err := store.check(d); err != nil {
		return tool.Refuse(err.Error())
	}
	// after prints the one NOTE a run that spelled --addr carries, after the
	// verb's own outcome line: the status word leads the line the reader acts
	// on, and the alias note follows it as pkg/tool renders its own
	// notes. A failure is the report, and prints nothing more.
	alias := store.aliasNote()
	after := func() {
		if alias != "" {
			note(c.Stdout, alias)
		}
	}
	lib := library()
	want, err := lib.Digest()
	if err != nil {
		return tool.Refuse(fmt.Sprintf("this binary's library does not build, so nothing was sent to the store: %s; fix the Lua and rebuild", err))
	}
	at := *store.addr
	name := oneline.Field(lib.Name)
	ctx := context.Background()
	failed := func(err error) *tool.Out {
		cause := oneline.Err(err)
		var collision *redisfn.CollisionError
		if errors.As(err, &collision) {
			// The collision's error ends in its own remedy; the line keeps one,
			// remedy=, which names the holder and the command.
			cause, _, _ = strings.Cut(cause, "; remedy: ")
		}
		// redisconn's line ends in its next step; the line keeps one remedy,
		// this verb's, which names the command to run again.
		cause, _, _ = strings.Cut(cause, "; next: ")
		line(c.Stderr, "FAILED "+name, "sha", want, "store", at, "err", free(cause), "remedy", quoted(remedy(sub, err, store)))
		if answered(err) {
			return tool.Exit(1)
		}
		return tool.Exit(2)
	}
	open := d.fnOpen
	if open == nil {
		open = func(ctx context.Context, store login) (redis.UniversalClient, func() error, error) {
			conn, err := connect(ctx, store, d)
			if err != nil {
				return nil, nil, err
			}
			return conn.Client(), conn.Close, nil
		}
	}
	client, closeStore, err := open(ctx, store)
	if err != nil {
		return failed(err)
	}
	// ignored: a deferred close after the verb's answer is printed; the answer is the report
	defer func() { _ = closeStore() }()

	if sub == "load" {
		r, err := lib.Ensure(ctx, client)
		if err != nil {
			return failed(err)
		}
		// The receipt's own line (redisfn.Receipt.String) after its outcome word.
		word := r.Outcome.String()
		line(c.Stdout, word, "library", bare{strings.TrimPrefix(r.String(), word+" "), map[string]string{"name": r.Library, "sha": r.Digest, "was": r.Was, "why": r.Why}}, "store", at)
		after()
		return tool.Exit(0)
	}

	state, err := lib.Check(ctx, client)
	loaded := "none"
	var mismatch *redisfn.MismatchError
	if errors.As(err, &mismatch) && mismatch.Loaded != "" {
		loaded = mismatch.Loaded
	}
	switch state {
	case redisfn.Same:
		line(c.Stdout, "OK "+name, "sha", want, "loaded", want, "want", want, "store", at)
		after()
		return tool.Exit(0)
	case redisfn.Different, redisfn.Absent:
		word := "STALE"
		if state == redisfn.Absent {
			word = "MISSING"
		}
		line(c.Stdout, word+" "+name, "sha", want, "loaded", loaded, "want", want, "store", at,
			"remedy", quoted("nova-redis fn load "+store.flags()+" puts this binary's library on the store"))
		after()
		return tool.Exit(1)
	}
	return failed(err)
}

// answered is true when the store itself replied to the command that failed
// with a refusal a caller mends by changing the command or the library
// (NOPERM, a library it would not take, a function name another library
// holds). It is false when no answer came (the store could not be reached,
// or the wait ended) and when the store refused the login: those are
// redisconn's Unreachable and AuthRefused, which exit 2 in every nova-redis
// verb. A refusal exits 1.
func answered(err error) bool {
	var collision *redisfn.CollisionError
	if errors.As(err, &collision) {
		return true
	}
	if redisconn.Classify(err) != redisconn.Other {
		return false
	}
	var reply redis.Error
	return errors.As(err, &reply)
}

// remedy is the one next step for a failure, by its cause.
func remedy(sub string, err error, store login) string {
	var collision *redisfn.CollisionError
	if errors.As(err, &collision) {
		var held []string
		for _, h := range collision.Held {
			if h.Holder != "" {
				held = append(held, "library "+h.Holder+" registers "+h.Function)
			} else {
				held = append(held, "another library registers "+h.Function)
			}
		}
		return "a function name belongs to one library and " + strings.Join(held, ", ") +
			": load the version of that library that no longer registers it, or delete it, then nova-redis fn load " + store.flags()
	}
	needs := "FUNCTION LIST"
	if sub == "load" {
		needs = "FUNCTION LIST and FUNCTION LOAD"
	}
	login := "log in as a user that may run " + needs + ": check --user (" + UserEnv + ") and the password in " + *store.passwordEnv + ", then nova-redis fn " + sub + " " + store.flags()
	if redisconn.Classify(err) == redisconn.AuthRefused {
		return login
	}
	var reply redis.Error
	if errors.As(err, &reply) {
		if strings.HasPrefix(reply.Error(), "NOPERM") {
			return login
		}
		if sub == "load" {
			return "the store would not take this binary's library; err names the file and line: fix the Lua and rebuild, then nova-redis fn load " + store.flags()
		}
		return "the store refused " + needs + "; read err, then nova-redis fn check " + store.flags()
	}
	if sub == "load" {
		return "no answer, so the store may hold either library: check that the store at " + *store.addr + " is up and --redis is right, then nova-redis fn check " + store.flags()
	}
	return "no answer: check that the store at " + *store.addr + " is up and --redis is right, then nova-redis fn check " + store.flags()
}

// line prints one typed line to w: its leading words, then key, value pairs.
// It is the Prints verbs' own printer, kept because the skeleton cannot render
// a bare head (LOADED nova_sprint sha=...) or a multi-word kind.
func line(w io.Writer, head string, kv ...any) {
	var b strings.Builder
	b.WriteString(head)
	for i := 0; i+1 < len(kv); i += 2 {
		k := kv[i].(string)
		switch x := kv[i+1].(type) {
		case quoted:
			fmt.Fprintf(&b, " %s=%q", k, string(x))
		case free:
			fmt.Fprintf(&b, " %s=%s", k, string(x))
		case bare:
			if x.text != "" {
				b.WriteString(" " + x.text)
			}
		case why:
			b.WriteString(": " + string(x))
		case next:
			b.WriteString("; run: " + string(x))
		case string:
			fmt.Fprintf(&b, " %s=%s", k, oneline.Field(x))
		default:
			fmt.Fprintf(&b, " %s=%v", k, x)
		}
	}
	fmt.Fprintln(w, b.String())
}

// note prints one NOTE line to w.
func note(w io.Writer, text string) {
	fmt.Fprintln(w, "NOTE "+text)
}
