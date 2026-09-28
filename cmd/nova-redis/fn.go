package main

// The fn verbs put the nova_sprint Redis function library (the Lua that
// nova-table and nova-config call with FCALL) on a store, and say whether a
// store holds it. The library is the one this binary embeds
// (internal/nsprint/fn's lua/, described by fn.Spec) and the machinery is
// internal/redisfn:
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
// (internal/redisconn), as every nova-redis verb does.
//
// A tool on its way to an FCALL never calls these: it calls redisfn's
// LoadMissing, which never replaces a library (nova-tools #3620). fn load is
// for the one place that deploys.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/redisfn"
)

// library is the function library the fn verbs load and check.
func library() redisfn.Library {
	lib := fn.Spec()
	lib.Remedy = "nova-redis fn load --addr <host:port>"
	return lib
}

func cmdFn(args []string, stdout, stderr io.Writer, d deps) int {
	return fnVerb(args, stdout, stderr, d, func(ctx context.Context, store login) (redis.UniversalClient, func() error, error) {
		conn, err := connect(ctx, store, d)
		if err != nil {
			return nil, nil, err
		}
		return conn.Client(), conn.Close, nil
	})
}

// opener opens the store for a login check accepted. cmdFn's is connect;
// the unit tests hand a store of their own that answers FUNCTION, which
// miniredis does not.
type opener func(ctx context.Context, store login) (redis.UniversalClient, func() error, error)

// fnVerb is fn load and fn check over the store open opens.
func fnVerb(args []string, stdout, stderr io.Writer, d deps, open opener) int {
	if len(args) == 0 {
		return refuse(stderr, " fn", "no subverb given; load puts this binary's function library on the store, check compares the store's with it")
	}
	sub := args[0]
	if sub != "load" && sub != "check" {
		return refuse(stderr, " fn", fmt.Sprintf("unknown subverb %q; want load or check", sub))
	}
	fs := flag.NewFlagSet("fn "+sub, flag.ContinueOnError)
	store := loginFlags(fs)
	if !parse(fs, args[1:], stderr, "addr") {
		return 2
	}
	if err := store.check(d); err != nil {
		return refuse(stderr, " fn "+sub, err.Error())
	}
	lib := library()
	want, err := lib.Digest()
	if err != nil {
		return refuse(stderr, " fn "+sub, fmt.Sprintf("this binary's library does not build, so nothing was sent to the store: %s; fix the Lua and rebuild", err))
	}
	at := oneline.Field(*store.addr)
	ctx := context.Background()
	failed := func(err error) int {
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
		fmt.Fprintf(stderr, "FAILED %s sha=%s store=%s err=%s remedy=%q\n", oneline.Field(lib.Name), want, at, cause, remedy(sub, err, store))
		if answered(err) {
			return 1
		}
		return 2
	}
	client, closeStore, err := open(ctx, store)
	if err != nil {
		return failed(err)
	}
	defer func() { _ = closeStore() }()

	if sub == "load" {
		r, err := lib.Ensure(ctx, client)
		if err != nil {
			return failed(err)
		}
		fmt.Fprintf(stdout, "%s store=%s\n", r, at)
		return 0
	}

	state, err := lib.Check(ctx, client)
	loaded := "none"
	var mismatch *redisfn.MismatchError
	if errors.As(err, &mismatch) && mismatch.Loaded != "" {
		loaded = mismatch.Loaded
	}
	switch state {
	case redisfn.Same:
		fmt.Fprintf(stdout, "OK %s sha=%s loaded=%s want=%s store=%s\n", oneline.Field(lib.Name), want, want, want, at)
		return 0
	case redisfn.Different, redisfn.Absent:
		word := "STALE"
		if state == redisfn.Absent {
			word = "MISSING"
		}
		fmt.Fprintf(stdout, "%s %s sha=%s loaded=%s want=%s store=%s remedy=%q\n", word, oneline.Field(lib.Name), want, loaded, want, at,
			"nova-redis fn load "+store.flags()+" puts this binary's library on the store")
		return 1
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
		return "no answer, so the store may hold either library: check that the store at " + *store.addr + " is up and --addr is right, then nova-redis fn check " + store.flags()
	}
	return "no answer: check that the store at " + *store.addr + " is up and --addr is right, then nova-redis fn check " + store.flags()
}
