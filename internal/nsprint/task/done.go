package task

import (
	"context"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// FileFollowUp is the hook to file a follow-up body via `file` (#3089).
var FileFollowUp = func(ctx context.Context, body string) error {
	return fmt.Errorf("file follow-up not wired")
}

// ProcessDoneEvidenceAndFollowUps validates evidence schema and files any follow-ups.
func ProcessDoneEvidenceAndFollowUps(ctx context.Context, st *store.Store, sprint, id string, evidence string, status DoneStatus) error {
	ev, err := ParseEvidence(evidence)
	if err != nil {
		return err
	}

	client := st.Client()
	taskKey := "s:" + sprint + ":task:" + id

	var followUps []string
	if status == DoneClosed {
		followUps = ev.FollowUps
		if len(followUps) > 0 {
			_ = client.HSet(ctx, taskKey, "followups_unfiled", strings.Join(followUps, "\n---\n")).Err()
		}
	} else if status == DoneRepeat {
		unfiled, err := client.HGet(ctx, taskKey, "followups_unfiled").Result()
		if err == nil && unfiled != "" {
			followUps = strings.Split(unfiled, "\n---\n")
		}
	}

	if len(followUps) == 0 {
		return nil
	}

	var remaining []string
	for _, fu := range followUps {
		fu = strings.TrimSpace(fu)
		if fu == "" {
			continue
		}
		if err := FileFollowUp(ctx, fu); err != nil {
			// If file.Main exited 1 (posted but read-back differs), never retry
			if strings.Contains(err.Error(), "differs") {
				continue
			}
			remaining = append(remaining, fu)
		}
	}

	if len(remaining) > 0 {
		_ = client.HSet(ctx, taskKey, "followups_unfiled", strings.Join(remaining, "\n---\n")).Err()
	} else {
		_ = client.HDel(ctx, taskKey, "followups_unfiled").Err()
	}

	return nil
}
