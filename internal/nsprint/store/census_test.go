package store

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestCensusOnePipeline(t *testing.T) {
	s := miniredis.RunT(t)
	s.SAdd("benches", "apple", "banana", "cherry")
	s.HSet("bench:apple", "host", "apple.local", "at", "2026")
	s.HSet("bench:banana", "host", "banana.local")
	// cherry is missing
	s.HSet("bench:missing", "host", "ghost")
	
	for i := 0; i < 1042; i++ {
		name := fmt.Sprintf("bench%d", i)
		s.SAdd("benches", name)
		s.HSet("bench:"+name, "host", name+".local", "at", "2026")
	}

	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()
	st := New(client)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out bytes.Buffer
	req := CensusRequest{
		Set:    "benches",
		Fields: []string{"host", "at"},
	}

	sum, err := RunCensus(ctx, st, req, &out)
	if err != nil {
		t.Fatalf("RunCensus: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1042+3+1 {
		t.Errorf("got %d lines, want %d", len(lines), 1042+3+1)
	}

	if sum.Keys != 1045 {
		t.Errorf("sum.Keys=%d", sum.Keys)
	}
	if sum.Missing != 1 {
		t.Errorf("sum.Missing=%d", sum.Missing)
	}
}
