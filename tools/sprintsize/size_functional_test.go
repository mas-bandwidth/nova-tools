//go:build functional

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
	"github.com/redis/go-redis/v9"
)

// THE LAYER 1 SIZE RUN, IN THE CONTAINER (L1-CONTRACT section 10, design 1.8).
//
// tsetRunL1Sizes is the size run: it loads the tset profile (composed where the tree has Layer 2's log
// fragment, Layer 1 alone otherwise), fills the
// requested number of cards through the table's own Step, and judges the rows
// L1-1 to L1-21 against their limits, over the direct route and through a
// 128 ms each-way proxy (testredis.Far at 128 ms: the delay holds what the
// client sends, so a round trip is 256 ms). This test gives it the store the
// rule demands: a fresh standalone Redis 8.10.2 of the image, started by the
// test as its own child, with SLOWLOG at threshold zero, and gone with the
// container. Nothing else is measured against and no store outside the
// container is reached.
//
// One size per run, chosen by SPRINTSIZE_CARDS (100000 or 1000000). With it
// unset, and no file named sprintsize_cards.txt beside this one, the test skips
// by name, so the functional set is untouched. functionalrun passes no
// environment into its container, so the size may also be the only content of
// that file (the tree is mounted read-only, and the file is written before the
// run and never committed).
//
// THE OUTPUT. go test prints a passing test's output nowhere, and the table is
// the product, so the test reports it as a failure. It is honest: the tool ends
// every run with the error "L1 size gate incomplete until every row is run and
// section 10 post-GC/retained-Lua memory evidence is collected", and this test
// treats that error, and any row not INSIDE, as red.

const sizeCardsFile = "sizecards.txt"

// sizeRunCards is the card count asked for, or 0 for none.
func sizeRunCards(t *testing.T) int {
	t.Helper()
	text := strings.TrimSpace(os.Getenv("SPRINTSIZE_CARDS"))
	if text == "" {
		raw, err := os.ReadFile(sizeCardsFile)
		if err != nil {
			return 0
		}
		text = strings.TrimSpace(string(raw))
	}
	cards, err := strconv.Atoi(text)
	if err != nil || (cards != 100_000 && cards != 1_000_000) {
		t.Fatalf("SPRINTSIZE_CARDS wants 100000 or 1000000, got %q", text)
	}
	return cards
}

func TestSprintsizeL1SizeRun(t *testing.T) {
	t.Parallel()
	cards := sizeRunCards(t)
	if cards == 0 {
		t.Skip("SPRINTSIZE_CARDS is unset: the L1 size run is opt-in")
	}
	// SLOWLOG at zero with room: the sampler matches each FCALL by its record
	// and never changes the server's configuration.
	addr := testredis.Start(t, "--slowlog-log-slower-than", "0", "--slowlog-max-len", "1024")
	proxy := testredis.Far(t, addr, 128*time.Millisecond)
	cfg := tsetSizeConfig{redisAddr: addr, proxyAddr: proxy, space: "dev-l1size:", owned: true}

	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	// The composed profile is Layer 1 plus Layer 2's log fragment. Where that
	// fragment is not in the tree, the run measures Layer 1's own standalone
	// profile and says so in the header, never as the composed one.
	profile := fn.TSetComposed
	if _, err := fn.TSetSource(profile); err != nil {
		profile = fn.TSetStandalone
	}
	var table bytes.Buffer
	start := time.Now()
	err := tsetRunL1Sizes(ctx, cfg, []int{cards},
		func(ctx context.Context, client *redis.Client) error {
			if err := tsetCheckIsolatedServer(ctx, client); err != nil {
				return err
			}
			return fn.LoadTSet(ctx, client, profile)
		},
		func(addr, clientName string) (tsetOwnedStore, error) {
			return tset.NewRedis(addr, "", "", tset.WithClientName(clientName))
		}, &table)
	wall := time.Since(start)

	direct := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	defer direct.Close()
	memory, memErr := direct.Info(context.Background(), "memory").Result()
	line := "INFO memory after the run (both routes resident): "
	if memErr != nil {
		line += memErr.Error()
	} else {
		for _, name := range []string{"used_memory", "used_memory_dataset", "used_memory_peak", "used_memory_rss", "used_memory_vm_functions", "used_memory_vm_eval"} {
			line += fmt.Sprintf("%s=%s ", name, tsetInfoField(memory, name))
		}
	}
	t.Errorf("SIZE RUN cards=%d profile=%s wall=%.1fs err=%v\n%s\n%s", cards, profile, wall.Seconds(), err, table.String(), line)
}
