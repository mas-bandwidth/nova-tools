package deal

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/beat"
	"github.com/redis/go-redis/v9"
)

// StatusFunction is `deal status` list mode: a read-only Function of the
// nova_sprint library (deal.lua), called with FCALL_RO (#3605). The bench
// seat may FCALL but has no scripting grant, so list mode sends no ad-hoc script.
const StatusFunction = "ns_deal_status_list"

// StatusUsage is the verb's help line (#3605). The fleet Redis has its
// default user off: NOVA_SPRINT_REDIS_USER names the ACL seat (bench on a
// bench) and the password stays in the environment (store.Open), never a flag.
const StatusUsage = "deal status --redis <addr> --sprint <S> [--bench <b>]: bench dealability and sprint cards left; " +
	"on the fleet Redis set NOVA_SPRINT_REDIS_USER=bench (password from nova-secrets exec, never a flag) " +
	"(exit 2 on usage, 6 no Redis)"

func evalVerdict(isMember, beatExists, paused bool, free int) string {
	if !isMember {
		return "unregistered"
	}
	if !beatExists {
		return "down"
	}
	if paused {
		return "paused"
	}
	if free <= 0 {
		return "full"
	}
	return "ok"
}

func toInt(v any) int {
	switch val := v.(type) {
	case int64:
		return int(val)
	case int:
		return val
	case string:
		n, _ := strconv.Atoi(val)
		return n
	default:
		return 0
	}
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func formatBenchLine(name, verdict string, free, starting, living, queue int, sshState, sshAtStr string, nowMs int64) string {
	ssh := "-"
	sshAge := "-"
	if sshState != "" {
		ssh = sshState
		if atMs, err := strconv.ParseInt(sshAtStr, 10, 64); err == nil && atMs > 0 {
			diffSec := (nowMs - atMs) / 1000
			if diffSec < 0 {
				diffSec = 0
			}
			sshAge = strconv.FormatInt(diffSec, 10)
		}
	}
	return fmt.Sprintf("bench %s verdict=%s free=%d starting=%d living=%d queue=%d ssh=%s ssh_age=%s",
		name, verdict, free, starting, living, queue, ssh, sshAge)
}

func formatSprintLine(sprint string, pool, waiting, dealt int64, logMsgs []redis.XMessage, nowMs int64) string {
	lastDealAge := "-"
	for _, m := range logMsgs {
		if m.Values["kind"] == "card deal" {
			if atVal, ok := m.Values["at"]; ok {
				if atMs, err := strconv.ParseInt(fmt.Sprint(atVal), 10, 64); err == nil && atMs > 0 {
					diffSec := (nowMs - atMs) / 1000
					if diffSec < 0 {
						diffSec = 0
					}
					lastDealAge = strconv.FormatInt(diffSec, 10)
					break
				}
			}
		}
	}
	return fmt.Sprintf("sprint %s pool=%d waiting=%d dealt=%d last_deal_age=%s",
		sprint, pool, waiting, dealt, lastDealAge)
}

// Status reads the deal status for sprint in one pipelined round trip.
// In list mode (bench == ""), it reads every member of benches, sorted by name.
// In lookup mode (bench != ""), it reads exactly that bench.
func Status(ctx context.Context, c *redis.Client, sprint, bench string) ([]string, error) {
	if sprint == "" {
		return nil, errors.New("sprint is required")
	}

	pipe := c.Pipeline()
	poolCmd := pipe.ZCard(ctx, "s:"+sprint+":pool")
	waitingCmd := pipe.SCard(ctx, "s:"+sprint+":waiting")
	dealtCmd := pipe.SCard(ctx, "s:"+sprint+":idx:card:dealt")
	logCmd := pipe.XRevRangeN(ctx, "s:"+sprint+":log", "+", "-", 1000)
	timeCmd := pipe.Time(ctx)

	var (
		benchListCmd *redis.Cmd
		isMemberCmd  *redis.BoolCmd
		beatCmd      *beat.Cmd
		desiredCmd   *redis.SliceCmd
		startingCmd  *redis.IntCmd
		livingCmd    *redis.IntCmd
		queueCmd     *redis.IntCmd
		sshCmd       *redis.SliceCmd
	)

	if bench == "" {
		// List mode
		benchListCmd = pipe.FCallRo(ctx, StatusFunction, []string{}, sprint)
	} else {
		// Lookup mode
		isMemberCmd = pipe.SIsMember(ctx, "benches", bench)
		beatCmd = beat.Read(ctx, pipe, beat.BenchKey(bench))
		desiredCmd = pipe.HMGet(ctx, "bench:"+bench+":desired", "slots", "paused", "legs")
		startingCmd = pipe.ZCard(ctx, "bench:"+bench+":starting")
		livingCmd = pipe.ZCard(ctx, "bench:"+bench+":living")
		queueCmd = pipe.ZCard(ctx, "s:"+sprint+":bench:"+bench+":queue")
		sshCmd = pipe.HMGet(ctx, "bench:"+bench+":ssh", "state", "at")
	}

	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		if strings.Contains(err.Error(), "Function not found") {
			return nil, fmt.Errorf("%s is not loaded; converge the library as its owner (nova-sprint fn load): %w", StatusFunction, err)
		}
		return nil, err
	}

	serverTime, err := timeCmd.Result()
	if err != nil {
		return nil, err
	}
	nowMs := serverTime.UnixMilli()

	var lines []string

	if bench == "" {
		rawList, err := benchListCmd.Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		if rawSlice, ok := rawList.([]any); ok {
			for _, item := range rawSlice {
				row, ok := item.([]any)
				if !ok || len(row) < 9 {
					continue
				}
				bName := toStr(row[0])
				beatExists := toInt(row[1]) > 0
				slots := toInt(row[2])
				pausedStr := toStr(row[3])
				paused := pausedStr == "1" || pausedStr == "true"
				starting := toInt(row[4])
				living := toInt(row[5])
				queue := toInt(row[6])
				sshState := toStr(row[7])
				sshAt := toStr(row[8])

				free := slots - starting - living
				if free < 0 {
					free = 0
				}
				verdict := evalVerdict(true, beatExists, paused, free)
				line := formatBenchLine(bName, verdict, free, starting, living, queue, sshState, sshAt, nowMs)
				lines = append(lines, line)
			}
		}
	} else {
		isMember := isMemberCmd.Val()
		beatExists := beatCmd.Live(serverTime)
		desiredVals := desiredCmd.Val()
		slots := 0
		paused := false
		if len(desiredVals) >= 2 {
			slots = toInt(desiredVals[0])
			pStr := toStr(desiredVals[1])
			paused = pStr == "1" || pStr == "true"
		}
		starting := int(startingCmd.Val())
		living := int(livingCmd.Val())
		queue := int(queueCmd.Val())
		sshVals := sshCmd.Val()
		sshState := ""
		sshAt := ""
		if len(sshVals) >= 2 {
			sshState = toStr(sshVals[0])
			sshAt = toStr(sshVals[1])
		}

		free := slots - starting - living
		if free < 0 {
			free = 0
		}
		verdict := evalVerdict(isMember, beatExists, paused, free)
		line := formatBenchLine(bench, verdict, free, starting, living, queue, sshState, sshAt, nowMs)
		lines = append(lines, line)
	}

	sprintLine := formatSprintLine(sprint, poolCmd.Val(), waitingCmd.Val(), dealtCmd.Val(), logCmd.Val(), nowMs)
	lines = append(lines, sprintLine)

	return lines, nil
}
