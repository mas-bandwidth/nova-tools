// nova-table batch: batch operations on table properties
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// batchManifest represents the batch manifest with prop_set and prop_unset.
type batchManifest struct {
	PropSet   map[string]string // properties to set
	PropUnset []string          // properties to unset
}

// batchTable handles the nova-table batch command.
func (app *application) batchTable(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("batch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		redisAddr = fs.String("redis", "", "the Redis address")
		epoch     = fs.Uint64("epoch", 0, "the observed epoch")
		actor     = fs.String("actor", "", "the actor")
		fence     = fs.String("fence", "", "the fence")
		idem      = fs.String("idem", "", "the idem token")
		receipt   = fs.Bool("receipt", true, "print the receipt")
		setProps  = fs.String("prop-set", "", "properties to set (json)")
		unsetProps = fs.String("prop-unset", "", "properties to unset (comma-separated)")
	)
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, "batch", err.Error())
	}
	pos := fs.Args()
	if len(pos) != 1 {
		return refuse(stderr, "batch", "usage: nova-table batch <table>")
	}
	table := pos[0]

	// Parse prop-unset.
	var unset []string
	if *unsetProps != "" {
		unset = strings.Split(*unsetProps, ",")
		for i := range unset {
			unset[i] = strings.TrimSpace(unset[i])
		}
	}

	// Connect to Redis.
	conn, client, code := app.client(ctx, "batch", *redisAddr, stderr)
	if code != 0 {
		return code
	}
	defer conn.Close()

	// Read current properties.
	propsKey := "table:" + table + ":props"
	current, err := client.HGetAll(ctx, propsKey).Result()
	if err != nil {
		return app.refusal(stderr, "batch", err)
	}

	// Check for conflicts: properties both set and unset.
	seen := make(map[string]bool)
	for _, p := range unset {
		seen[p] = true
	}
	for p := range current {
		if seen[p] {
			return refused(stderr, "batch", fmt.Sprintf("property %s is both set and unset", p))
		}
	}

	// Build the operations.
	var ops []redis.HKeyVal
	for p, v := range current {
		ops = append(ops, redis.HKeyVal{Key: p, Value: v})
	}

	// Apply the batch.
	if len(ops) > 0 {
		var args []any
		for _, op := range ops {
			args = append(args, op.Key, op.Value)
		}
		if err := client.HMSet(ctx, propsKey, args...).Err(); err != nil {
			return app.refusal(stderr, "batch", err)
		}
	}

	fmt.Fprintf(stdout, "TABLE BATCH table=%s\n", table)
	return 0
}
