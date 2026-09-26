package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/redis/go-redis/v9"
)

// redis is the rare raw command (nova-tools#4330): one Redis command sent by
// this process over the store dial every verb uses, as the seat's login, with
// no redis-cli and no wrapper; the password never leaves this process's
// memory. The reply prints the way redis-cli prints to a pipe: one line per
// value, an array or map flattened in order (a map's keys sorted), a nil as
// an empty line. The receipt is one line on stderr, and a refusal -- Redis's
// (NOPERM, WRONGTYPE, NOAUTH, unreachable) or ours -- is printed there too.
// Exit 0 the command ran, 1 Redis refused it, 2 could not run.
func init() {
	register(Verb{
		Name:    "redis",
		Summary: "[--seat <name>] [--redis <addr>] [--] <cmd...>: one raw Redis command as the seat, reply on stdout, every refusal on stderr",
		Run:     cmdRedisRaw,
	})
}

const redisRawWants = "wants [--seat <name>] [--redis <host:port>] [--] <redis command...>, for example: nova-sprint --seat coordinator redis ZCARD sprint:S:cards"

func cmdRedisRaw(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("redis")
	addr := fs.String("redis", rawAddrDefault(), "redis address (the seat's row, else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR)")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "redis", redisRawWants)
	}
	cmdArgs := fs.Args()
	if len(cmdArgs) == 0 {
		return refuse(stderr, "redis", "no command; "+redisRawWants)
	}
	if *addr == "" {
		return refuse(stderr, "redis", "no address: pass --seat <name> with a seats.tsv row, --redis <host:port>, or set NOVA_SPRINT_REDIS; "+redisRawWants)
	}
	who := "seat=none"
	if c, ok, err := seatcred.Active(); ok {
		if err != nil {
			return refuse(stderr, "redis", err.Error())
		}
		who = c.String()
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuse(stderr, "redis", err.Error())
	}
	defer func() { _ = st.Close() }()
	argv := make([]any, len(cmdArgs))
	for i, a := range cmdArgs {
		argv[i] = a
	}
	reply, err := st.Client().Do(ctx, argv...).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		fmt.Fprintf(stderr, "REDIS REFUSED %s addr=%s cmd=%s why=%s\n", who, oneline.Field(*addr), oneline.Field(cmdArgs[0]), oneline.Escape(err.Error()))
		return 1
	}
	printRedisReply(stdout, reply)
	fmt.Fprintf(stderr, "REDIS %s addr=%s cmd=%s exit=0\n", who, oneline.Field(*addr), oneline.Field(cmdArgs[0]))
	return 0
}

// rawAddrDefault is the seat's row address, else the environment's, without
// the loopback fallback: a raw command never goes to a Redis nobody named.
func rawAddrDefault() string {
	if a := seatcred.Addr(); a != "" {
		return a
	}
	for _, k := range seatAddrEnvs {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// printRedisReply prints v as redis-cli does to a pipe.
func printRedisReply(w io.Writer, v any) {
	switch x := v.(type) {
	case nil:
		fmt.Fprintln(w)
	case string:
		fmt.Fprintln(w, x)
	case []byte:
		fmt.Fprintln(w, string(x))
	case int64:
		fmt.Fprintln(w, x)
	case float64:
		fmt.Fprintln(w, strconv.FormatFloat(x, 'f', -1, 64))
	case bool:
		if x {
			fmt.Fprintln(w, 1)
		} else {
			fmt.Fprintln(w, 0)
		}
	case []any:
		for _, e := range x {
			printRedisReply(w, e)
		}
	case map[any]any:
		keys := make([]any, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
		for _, k := range keys {
			printRedisReply(w, k)
			printRedisReply(w, x[k])
		}
	case error:
		fmt.Fprintln(w, x.Error())
	default:
		fmt.Fprintln(w, x)
	}
}
