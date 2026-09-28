package prereview

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// TaskCardReader reads a card from a Redis task hash (nova-tools #3392).
// It is a variable so tests swap in a stub and dial nothing.
var TaskCardReader = func(ctx context.Context, addr, taskID string) (Card, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	title, err := rdb.HGet(ctx, "task:"+taskID, "title").Result()
	if err != nil {
		return Card{}, fmt.Errorf("HGET task:%s title: %w", taskID, err)
	}

	return ParseTaskCard(taskID, title), nil
}

// ParseTaskCard reads a task hash title string for the card metadata
// (PATHS, DONE-WHEN, STREAM, WHO) and returns a Card with PathsFrom="task".
func ParseTaskCard(taskID, title string) Card {
	c := Card{
		Path:       "task:" + taskID,
		PathsFrom:  "task",
		SymbolFrom: "none",
	}

	for _, line := range strings.Split(title, "\n") {
		m := cardKeyRE.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		key, value := m[1], strings.TrimSpace(m[2])
		switch key {
		case "PATHS":
			if value == "" || strings.EqualFold(value, "none") {
				continue
			}
			for _, g := range strings.Split(value, ",") {
				if g = strings.TrimSpace(g); g != "" {
					c.Paths = append(c.Paths, g)
				}
			}
			if len(c.Paths) > 0 {
				c.PathsFrom = "task"
			}
		case "DONE-WHEN":
			c.DoneWhen = value
		}
	}

	// If RESULT section exists, capture it
	if i := strings.Index(title, "RESULT"); i >= 0 {
		c.Result = title[i:]
	}

	return c
}
