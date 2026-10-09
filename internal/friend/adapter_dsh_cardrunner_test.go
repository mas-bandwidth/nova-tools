package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestADSHLaneRunsACardAsAProcessAndReadsItsOutbox verifies that DSH as a CardRunner
// executes each card as a headless process with the brief on stdin and reads the result
// from the card's outbox. Missing REPORT/RESULT files return NoReport error.
func TestADSHLaneRunsACardAsAProcessAndReadsItsOutbox(t *testing.T) {
	t.Parallel()

	t.Run("outbox written", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		briefDir := filepath.Join(dir, "inbox", "c1~1")
		outboxDir := filepath.Join(dir, "outbox", "c1~1")
		require.NoError(t, os.MkdirAll(briefDir, 0o755))
		require.NoError(t, os.MkdirAll(outboxDir, 0o755))

		briefPath := filepath.Join(briefDir, "BRIEF.md")
		require.NoError(t, os.WriteFile(briefPath, []byte("STATUS: nova-sprint card c1\nDo work"), 0o644))

		card := Card{
			ID:     "c1",
			Brief:  briefPath,
			Outbox: outboxDir,
		}

		var runCalls [][]string
		var runStdin string
		fakeExec := func(ctx context.Context, runDir, prog string, args []string, stdin string) (string, int, error) {
			runCalls = append(runCalls, append([]string{runDir, prog}, args...))
			runStdin = stdin
			// Simulate DSH headless run writing REPORT.md and RESULT.md
			require.NoError(t, os.WriteFile(filepath.Join(outboxDir, "REPORT.md"), []byte("Verdict: LAND\n"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(outboxDir, "RESULT.md"), []byte("RESULT: c1\n"), 0o644))
			return "turn completed\n", 0, nil
		}

		var out strings.Builder
		d := &DSH{
			Dir:     dir,
			Friend:  "zhi",
			Run:     fakeExec,
			Program: "dsh",
			Out:     &out,
		}

		lt, err := d.RunCard(context.Background(), card)
		require.NoError(t, err)
		assert.Equal(t, 0, lt.Exit)
		require.Len(t, runCalls, 1)
		assert.Equal(t, []string{dir, "dsh", "--profile", "headless", "-"}, runCalls[0])
		assert.Contains(t, runStdin, "STATUS: nova-sprint card c1")
		assert.Contains(t, runStdin, "Lane: zhi one-shot c1")
		assert.Contains(t, out.String(), "turn completed")
	})

	t.Run("outbox missing report and result", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		briefDir := filepath.Join(dir, "inbox", "c2~1")
		outboxDir := filepath.Join(dir, "outbox", "c2~1")
		require.NoError(t, os.MkdirAll(briefDir, 0o755))
		require.NoError(t, os.MkdirAll(outboxDir, 0o755))

		briefPath := filepath.Join(briefDir, "BRIEF.md")
		require.NoError(t, os.WriteFile(briefPath, []byte("BRIEF c2"), 0o644))

		card := Card{
			ID:     "c2",
			Brief:  briefPath,
			Outbox: outboxDir,
		}

		fakeExec := func(ctx context.Context, runDir, prog string, args []string, stdin string) (string, int, error) {
			return "exit 0 but no outbox written\n", 0, nil
		}

		d := &DSH{
			Dir:     dir,
			Run:     fakeExec,
			Program: "dsh",
		}

		lt, err := d.RunCard(context.Background(), card)
		assert.Equal(t, 0, lt.Exit)
		var noRep NoReport
		require.ErrorAs(t, err, &noRep)
		assert.Contains(t, err.Error(), "REPORT.md")
		assert.Contains(t, err.Error(), "RESULT.md")
	})

	t.Run("refusal on missing dir", func(t *testing.T) {
		t.Parallel()
		d := &DSH{}
		assert.Equal(t, "dsh has no working directory", d.Refusal())
		_, err := d.RunCard(context.Background(), Card{ID: "c3"})
		require.Error(t, err)
		assert.Equal(t, "dsh has no working directory", err.Error())
	})

	t.Run("refusal on agent preset in output", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		briefDir := filepath.Join(dir, "inbox", "c4~1")
		outboxDir := filepath.Join(dir, "outbox", "c4~1")
		require.NoError(t, os.MkdirAll(briefDir, 0o755))
		require.NoError(t, os.MkdirAll(outboxDir, 0o755))
		briefPath := filepath.Join(briefDir, "BRIEF.md")
		require.NoError(t, os.WriteFile(briefPath, []byte("BRIEF c4"), 0o644))

		fakeExec := func(ctx context.Context, runDir, prog string, args []string, stdin string) (string, int, error) {
			return `dsh: session "session-preset" runs under agent preset "minimal", which the one-shot runner does not compose` + "\n", 0, nil
		}

		d := &DSH{
			Dir:     dir,
			Run:     fakeExec,
			Program: "dsh",
		}

		_, err := d.RunCard(context.Background(), Card{ID: "c4", Brief: briefPath, Outbox: outboxDir})
		var refused SessionRefused
		require.ErrorAs(t, err, &refused)
		assert.Contains(t, refused.Reason, "agent preset minimal")
	})
}

// TestADSHMessageTurnPongsWithTheNonce verifies that DeliverTo handles message turns,
// requiring a lane context, executing headless turns with text on stdin, and properly
// handling session IDs and pongs.
func TestADSHMessageTurnPongsWithTheNonce(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	jobDir := filepath.Join(dir, "jobs", "lane-1")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))

	t.Run("fails outside lane", func(t *testing.T) {
		t.Parallel()
		d := &DSH{Dir: dir, Program: "dsh"}
		_, err := d.DeliverTo(context.Background(), "-", "pong nonce-test")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "needs a managed lane")
	})

	t.Run("message turn with empty session executes headless turn with text on stdin", func(t *testing.T) {
		t.Parallel()
		var runCalls [][]string
		var runStdin string
		fakeExec := func(ctx context.Context, rDir, prog string, args []string, stdin string) (string, int, error) {
			runCalls = append(runCalls, append([]string{rDir, prog}, args...))
			runStdin = stdin
			return "pong delivered\n", 0, nil
		}

		var out strings.Builder
		d := &DSH{
			Dir:     dir,
			Run:     fakeExec,
			Program: "dsh",
			Out:     &out,
		}

		ctx := WithLaneDir(LaneContext(context.Background()), jobDir)
		text := "CHECK nonce=nonce-98765\nSend pong"
		turn, err := d.DeliverTo(ctx, "", text)
		require.NoError(t, err)
		assert.Equal(t, 0, turn.Exit)
		require.Len(t, runCalls, 1)
		assert.Equal(t, []string{jobDir, "dsh", "--profile", "headless", "-"}, runCalls[0])
		assert.Equal(t, text, runStdin)
		assert.Contains(t, out.String(), "pong delivered")
	})

	t.Run("message turn into named session executes headless with session id", func(t *testing.T) {
		t.Parallel()
		var runCalls [][]string
		var runStdin string
		fakeExec := func(ctx context.Context, rDir, prog string, args []string, stdin string) (string, int, error) {
			runCalls = append(runCalls, append([]string{rDir, prog}, args...))
			runStdin = stdin
			return "session turn ok\n", 0, nil
		}

		d := &DSH{
			Dir:     dir,
			Run:     fakeExec,
			Program: "dsh",
		}

		ctx := WithLaneDir(LaneContext(context.Background()), jobDir)
		text := "pong nonce-4444"
		turn, err := d.DeliverTo(ctx, "session-zhi-real", text)
		require.NoError(t, err)
		assert.Equal(t, 0, turn.Exit)
		require.Len(t, runCalls, 1)
		assert.Equal(t, []string{jobDir, "dsh", "headless", "--session-id", "session-zhi-real", "-"}, runCalls[0])
		assert.Equal(t, text, runStdin)
	})
}

// TestDSHLanesRunToWidthAndPublishCost verifies that multiple concurrent lane runs
// discover their session token records, compute cost from route pricing, update outbox
// REPORT.md and RESULT.md files, and report cumulative spend.
func TestDSHLanesRunToWidthAndPublishCost(t *testing.T) {
	t.Parallel()

	sessionsRoot := t.TempDir()
	dir := t.TempDir()
	realDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	bucket := filepath.Join(sessionsRoot, DSHSessionKey(realDir))
	require.NoError(t, os.MkdirAll(bucket, 0o755))

	rp := RoutePrice{
		Name:  "deepseek-chat",
		Found: true,
		Prices: cardcost.Prices{
			Input:     "1.0",
			CacheRead: "0.1",
			Output:    "2.0",
		},
	}

	d := &DSH{
		Dir:      dir,
		Sessions: sessionsRoot,
		Friend:   "zhi",
		Model:      "deepseek/deepseek-chat",
		RoutePrice: func() RoutePrice { return rp },
		Now:        func() time.Time { return time.Unix(1234567890, 0) },
		Program:  "dsh",
	}

	const width = 4
	var wg sync.WaitGroup
	errs := make([]error, width)

	for i := 0; i < width; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			cardID := fmt.Sprintf("card-w%d", idx)
			sessionName := fmt.Sprintf("session-%s", cardID)
			sDir := filepath.Join(bucket, sessionName)
			if err := os.MkdirAll(sDir, 0o755); err != nil {
				errs[idx] = err
				return
			}

			// Header containing Lane marker so FindDSHSessionFile matches
			header := fmt.Sprintf("Lane: zhi one-shot %s 1234567890.\n", cardID)
			jsonLine := `{"data":{"usage":{"inputTokens":100000,"cacheReadTokens":50000,"cacheWriteTokens":0,"outputTokens":10000},"message":{"source":{"model":"deepseek-chat"}}}}` + "\n"
			sessionFile := filepath.Join(sDir, "session.v4.jsonl")
			if err := os.WriteFile(sessionFile, []byte(header+jsonLine), 0o644); err != nil {
				errs[idx] = err
				return
			}

			briefDir := filepath.Join(dir, "inbox", cardID+"~1")
			outboxDir := filepath.Join(dir, "outbox", cardID+"~1")
			if err := os.MkdirAll(briefDir, 0o755); err != nil {
				errs[idx] = err
				return
			}
			if err := os.MkdirAll(outboxDir, 0o755); err != nil {
				errs[idx] = err
				return
			}

			briefPath := filepath.Join(briefDir, "BRIEF.md")
			if err := os.WriteFile(briefPath, []byte("STATUS: brief "+cardID), 0o644); err != nil {
				errs[idx] = err
				return
			}
			if err := os.WriteFile(filepath.Join(outboxDir, "REPORT.md"), []byte("Verdict: LAND\n"), 0o644); err != nil {
				errs[idx] = err
				return
			}
			if err := os.WriteFile(filepath.Join(outboxDir, "RESULT.md"), []byte("RESULT: "+cardID+"\n"), 0o644); err != nil {
				errs[idx] = err
				return
			}

			card := Card{
				ID:     cardID,
				Brief:  briefPath,
				Outbox: outboxDir,
			}

			// Local exec for each runner that succeeds
			cardDSH := &DSH{
				Dir:      dir,
				Sessions: sessionsRoot,
				Friend:   "zhi",
				Model:      "deepseek/deepseek-chat",
				RoutePrice: func() RoutePrice { return rp },
				Now:        func() time.Time { return time.Unix(1234567890, 0) },
				Program:  "dsh",
				Run: func(ctx context.Context, rDir, prog string, args []string, stdin string) (string, int, error) {
					return "ok", 0, nil
				},
			}

			lt, err := cardDSH.RunCard(context.Background(), card)
			if err != nil {
				errs[idx] = err
				return
			}
			if lt.Exit != 0 {
				errs[idx] = fmt.Errorf("unexpected exit %d", lt.Exit)
				return
			}

			// Check RESULT.md and REPORT.md updated with cost
			resBytes, err := os.ReadFile(filepath.Join(outboxDir, "RESULT.md"))
			if err != nil {
				errs[idx] = err
				return
			}
			if !strings.Contains(string(resBytes), "cost: ") {
				errs[idx] = fmt.Errorf("RESULT.md lacks cost line: %s", string(resBytes))
				return
			}

			repBytes, err := os.ReadFile(filepath.Join(outboxDir, "REPORT.md"))
			if err != nil {
				errs[idx] = err
				return
			}
			if !strings.Contains(string(repBytes), "Cost: ") {
				errs[idx] = fmt.Errorf("REPORT.md lacks Cost line: %s", string(repBytes))
				return
			}

			// Also verify the shared DSH accumulator
			d.publishCardCost(context.Background(), card, fmt.Sprintf("zhi one-shot %s 1234567890", cardID))
		}(i)
	}

	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "lane %d failed", i)
	}

	cost, tokens := d.Spent()
	assert.Equal(t, int64(width*160000), tokens, "all tokens aggregated across width")
	assert.Greater(t, cost, 0.0, "cost was calculated")

	spendLine := d.SpendLine()
	assert.Contains(t, spendLine, fmt.Sprintf("runs=%d", width))
	assert.Contains(t, spendLine, "cost_usd=")
	assert.Contains(t, spendLine, "harness=dsh")
}
