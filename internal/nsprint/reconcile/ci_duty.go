package reconcile

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

type GHJobClient interface {
	JobLog(ctx context.Context, repo string, jobID int64) (string, error)
	RerunFailedJob(ctx context.Context, repo string, jobID int64) error
}

type CIDuty struct {
	Store  *store.Store
	GitHub GHJobClient
}

func (d *CIDuty) Pass(ctx context.Context, instance string) (Counts, []string, error) {
	if d.Store == nil || d.GitHub == nil {
		return Counts{}, nil, errors.New("ci duty: store and github are required")
	}

	client := d.Store.Client()
	err := client.XGroupCreateMkStream(ctx, ghevent.Stream, "ci", "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return Counts{}, nil, fmt.Errorf("ci group: %w", err)
	}

	streams, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    "ci",
		Consumer: instance,
		Streams:  []string{ghevent.Stream, ">"},
		Count:    100,
		Block:    time.Millisecond,
	}).Result()

	if errors.Is(err, redis.Nil) {
		return Counts{}, nil, nil
	}
	if err != nil {
		return Counts{}, nil, fmt.Errorf("ci read: %w", err)
	}

	var counts Counts
	var lines []string
	var ack []string

	sprints, err := client.SMembers(ctx, "sprints").Result()
	if err != nil {
		return Counts{}, nil, err
	}

	for _, stream := range streams {
		for _, msg := range stream.Messages {
			ack = append(ack, msg.ID)
			
			event, _ := msg.Values["event"].(string)
			payload, _ := msg.Values["payload"].(string)
			if event == "" || payload == "" {
				continue
			}
			
			e, err := ghevent.Decode(event, []byte(payload))
			if err != nil || e.Kind != "check_run" || e.Action != "completed" || e.Conclusion != "failure" {
				continue
			}
			
			if e.Number == "" || e.Head == "" || e.Check == "" || e.CheckRunID == "" {
				continue
			}
			
			// Is this a PR record's head?
			isPRHead := false
			for _, s := range sprints {
				head, err := client.HGet(ctx, "s:"+s+":pr:"+e.Repo+":"+e.Number, "head").Result()
				if err == nil && head == e.Head {
					isPRHead = true
					break
				}
			}
			if !isPRHead {
				continue
			}
			
			// Second failure is red
			rerunKey := "ci:infra:rerun:" + e.Repo + ":" + e.Head + ":" + e.Check
			exists, err := client.Exists(ctx, rerunKey).Result()
			if err != nil {
				continue
			}
			if exists > 0 {
				continue
			}
			
			jobID, err := strconv.ParseInt(e.CheckRunID, 10, 64)
			if err != nil {
				continue
			}
			
			log, err := d.GitHub.JobLog(ctx, e.Repo, jobID)
			if err != nil {
				continue
			}
			
			reason, isInfra := isInfraLog(log)
			if !isInfra {
				continue
			}
			
			err = d.GitHub.RerunFailedJob(ctx, e.Repo, jobID)
			if err != nil {
				continue
			}
			
			client.Set(ctx, rerunKey, "1", 30*24*time.Hour)
			lines = append(lines, fmt.Sprintf("ci:: rerun=1 reason=%s", reason))
			counts.Routed++
		}
	}
	
	if len(ack) > 0 {
		client.XAck(ctx, ghevent.Stream, "ci", ack...)
	}
	
	return counts, lines, nil
}

func isInfraLog(log string) (string, bool) {
	lower := strings.ToLower(log)
	if strings.Contains(lower, "timeout budget spent") {
		return "timeout budget spent", true
	}
	if strings.Contains(lower, "toolchain/std mismatch") {
		return "toolchain/std mismatch", true
	}
	if strings.Contains(lower, "signal: terminated") {
		return "signal: terminated", true
	}
	if strings.Contains(lower, "runner lost") {
		return "runner lost", true
	}
	if strings.Contains(lower, "exit code 2") && !strings.Contains(lower, "--- fail") {
		return "exit code 2", true
	}
	return "", false
}
