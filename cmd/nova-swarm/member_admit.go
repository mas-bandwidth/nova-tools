package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// admitMemberLaunch reads the owner and claim again after the start gap, just
// before creating a native child. A disconnected server, old queue protocol,
// or changed claim cannot authorize a cached packet to launch.
func admitMemberLaunch(run func(...string) (int, []byte), as string, p member.Packet) error {
	code, out := run("queue", "--as", as, "--json", "--packets", "0")
	if code != 0 {
		return fmt.Errorf("launch admission queue unavailable (exit %d): %s", code, oneline.Escape(strings.TrimSpace(string(out))))
	}
	var q struct {
		Epoch   uint64 `json:"epoch"`
		Machine string `json:"machine"`
		Cards   []struct {
			ID      string `json:"id"`
			Col     string `json:"col"`
			Gen     int    `json:"gen"`
			Attempt int    `json:"attempt"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(out, &q); err != nil {
		return fmt.Errorf("launch admission queue invalid: %w", err)
	}
	if q.Machine != "RUNNING" || q.Epoch != p.Epoch {
		return fmt.Errorf("launch admission refused: machine %q epoch %d, packet epoch %d", oneline.Field(q.Machine), q.Epoch, p.Epoch)
	}
	for _, c := range q.Cards {
		if c.ID != p.Card {
			continue
		}
		if p.Kind == "read" {
			if c.Col == "reading" && max(c.Gen, 1) == max(p.Gen, 1) && c.Attempt == p.Attempt {
				return nil
			}
		} else if c.Col == "working" && c.Gen == p.Gen {
			return nil
		}
		return fmt.Errorf("launch admission refused: claim %s changed", oneline.Field(p.Card))
	}
	return fmt.Errorf("launch admission refused: claim %s absent", oneline.Field(p.Card))
}
