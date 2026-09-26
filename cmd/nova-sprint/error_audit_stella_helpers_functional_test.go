//go:build functional

package main

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// The fixtures Stella's error-audit probes name
// (error_audit_stella_functional_test.go, stella-e6353bf80360). Her review
// checkout carried them beside #4369's sentinel tests, which are not on dev;
// these are the same shapes, without t.Setenv so the probes stay parallel.

const sdSprint = "sd-sprint"

// sdStore is a throwaway store with the library loaded and one open sprint.
func sdStore(t *testing.T) (*redis.Client, *store.Store) {
	t.Helper()
	_, c := wstest.Start(t)
	ctx := context.Background()
	pipe := c.Pipeline()
	pipe.HSet(ctx, "s:"+sdSprint, "status", "open")
	pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sdSprint})
	pipe.SAdd(ctx, "sprints", sdSprint)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return c, store.New(c)
}

// sdCard pushes one stream card (ns_tcard_push): ready, or waiting on deps.
func sdCard(t *testing.T, c *redis.Client, id, stream, deps string) error {
	t.Helper()
	where := "ready"
	if deps != "" {
		where = "waiting"
	}
	_, err := taskcard.Push(context.Background(), c, taskcard.PushRequest{ID: id, Where: where, Stream: stream, Kind: "build",
		Title: "STREAM: " + stream + " | " + id, Repo: "mas-bandwidth/nova-tools", By: "test", DependsOn: deps})
	return err
}
