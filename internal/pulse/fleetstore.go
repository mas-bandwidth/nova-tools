package pulse

// The fleet Redis, dialled the way every bench already reaches it: the ACL user `bench` on
// 100.115.99.19:6380, with the password handed in through the environment by
// `nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD`, never on a command line and never
// in a file this tool writes.
//
// THE PASSWORD IS READ FROM THE ENVIRONMENT AND NEVER PRINTED. It is taken from the named
// variable, put on the client options, and dropped; no error, receipt or refusal from this
// package carries it, and the variable's NAME is the only part that ever reaches a line.
// bin/bench-row and bin/sprint-table-redis do the same thing with REDISCLI_AUTH, and this
// is that pattern with one fewer process in it.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultStorePasswordEnv is the variable nova-secrets exec fills for the bench ACL user.
const DefaultStorePasswordEnv = "NOVA_REDIS_BENCH_PASSWORD"

// DefaultStoreUser is the ACL user a bench reads and writes `bench:*` as.
const DefaultStoreUser = "bench"

// StoreOptions is where the fleet store is and who this process is on it. Addr is the only
// required field; an empty User or PasswordEnv takes the defaults, and an unset password
// variable dials with no credentials at all -- which is what a miniredis in a test wants,
// and what a store with no ACL would want.
type StoreOptions struct {
	Addr        string
	User        string
	PasswordEnv string
	// Timeout bounds the dial and every command on the returned client. Zero takes five
	// seconds: a row pusher on a one-second interval must never block past its next tick.
	Timeout time.Duration
}

// DialStore names the fleet store and sends nothing (#3277): the caller's first command
// dials and authenticates, so no verb pays a PING round trip before its first batch. The
// timeouts bound that first command. A refusal here (no address) names the flag; it never
// names the password or its value.
func DialStore(ctx context.Context, o StoreOptions) (*redis.Client, error) {
	addr := strings.TrimSpace(o.Addr)
	if addr == "" {
		return nil, fmt.Errorf("no store address; --store wants host:port of the fleet Redis")
	}
	user := strings.TrimSpace(o.User)
	if user == "" {
		user = DefaultStoreUser
	}
	env := strings.TrimSpace(o.PasswordEnv)
	if env == "" {
		env = DefaultStorePasswordEnv
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	opts := &redis.Options{
		Addr:         addr,
		DialTimeout:  timeout,
		ReadTimeout:  timeout,
		WriteTimeout: timeout,
	}
	// An empty password means no AUTH at all: go-redis sends AUTH only when a password is
	// set, and sending a username with an empty password would be an ACL failure rather
	// than the anonymous dial a test or an open instance wants.
	if pw := os.Getenv(env); pw != "" {
		opts.Username = user
		opts.Password = pw
	}
	return redis.NewClient(opts), nil
}
