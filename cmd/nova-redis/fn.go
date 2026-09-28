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
//     MISSING (exit 1), each with the store's digest and this binary's, or
//     FAILED when the store could not be read (exit 2).
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

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisfn"
)

// library is the function library the fn verbs load and check.
func library() redisfn.Library {
	lib := fn.Spec()
	lib.Remedy = "nova-redis fn load --addr <host:port>"
	return lib
}

func cmdFn(args []string, stdout, stderr io.Writer, d deps) int {
	if len(args) == 0 {
		return refuse(stderr, " fn", "no subverb given; load puts this binary's function library on the store, check compares the store's with it")
	}
	sub := args[0]
	if sub != "load" && sub != "check" {
		return refuse(stderr, " fn", fmt.Sprintf("unknown subverb %q; want load or check", sub))
	}
	fs := flag.NewFlagSet("fn "+sub, flag.ContinueOnError)
	addr := fs.String("addr", "", "store address")
	if !parse(fs, args[1:], stderr, "addr") {
		return 2
	}
	if err := validAddr(*addr); err != nil {
		return refuse(stderr, " fn "+sub, err.Error())
	}
	lib := library()
	want, err := lib.Digest()
	if err != nil {
		return refuse(stderr, " fn "+sub, fmt.Sprintf("this binary's library does not build, so nothing was sent to the store: %s; fix the Lua and rebuild", err))
	}
	client := d.dial(*addr, d.getenv(PasswordEnv))
	defer func() { _ = client.Close() }()
	store := oneline.Field(*addr)
	ctx := context.Background()

	if sub == "load" {
		r, err := lib.Ensure(ctx, client)
		if err != nil {
			fmt.Fprintf(stderr, "FAILED %s sha=%s store=%s err=%s remedy=%q\n", oneline.Field(lib.Name), want, store, oneline.Err(err),
				"check --addr and "+PasswordEnv+", and that the store's user may FUNCTION LIST and FUNCTION LOAD; then nova-redis fn check --addr "+*addr)
			return 1
		}
		fmt.Fprintf(stdout, "%s store=%s\n", r, store)
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
		fmt.Fprintf(stdout, "OK %s loaded=%s want=%s store=%s\n", oneline.Field(lib.Name), want, want, store)
		return 0
	case redisfn.Different, redisfn.Absent:
		word := "STALE"
		if state == redisfn.Absent {
			word = "MISSING"
		}
		fmt.Fprintf(stdout, "%s %s loaded=%s want=%s store=%s remedy=%q\n", word, oneline.Field(lib.Name), loaded, want, store,
			"nova-redis fn load --addr "+*addr+" puts this binary's library on the store")
		return 1
	}
	fmt.Fprintf(stderr, "FAILED %s want=%s store=%s err=%s remedy=%q\n", oneline.Field(lib.Name), want, store, oneline.Err(err),
		"check --addr and "+PasswordEnv+", and that the store's user may FUNCTION LIST")
	return 2
}
