package friend

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

type dshUsage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
	ReasoningTokens  int64 `json:"reasoningTokens"`
	TotalTokens      int64 `json:"totalTokens"`

	InputTokensSnake      int64 `json:"input_tokens"`
	OutputTokensSnake     int64 `json:"output_tokens"`
	CacheReadTokensSnake  int64 `json:"cache_read_tokens"`
	CacheWriteTokensSnake int64 `json:"cache_write_tokens"`
	ReasoningTokensSnake  int64 `json:"reasoning_tokens"`
}

type dshMessage struct {
	Type string `json:"type"`
	Data struct {
		Usage *dshUsage `json:"usage"`
	} `json:"data"`
}

// TokensOfDSH parses the JSONL transcript of a DSH session and sums the usage
// across all assistant messages.
func TokensOfDSH(transcript string) (LaneTokens, error) {
	var input, cacheRead, cacheWrite, output, reasoning int64
	sc := bufio.NewScanner(strings.NewReader(transcript))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m dshMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		if m.Type != "assistant/message" || m.Data.Usage == nil {
			continue
		}
		u := m.Data.Usage
		in := u.InputTokens
		if in == 0 && u.InputTokensSnake != 0 {
			in = u.InputTokensSnake
		}
		out := u.OutputTokens
		if out == 0 && u.OutputTokensSnake != 0 {
			out = u.OutputTokensSnake
		}
		cr := u.CacheReadTokens
		if cr == 0 && u.CacheReadTokensSnake != 0 {
			cr = u.CacheReadTokensSnake
		}
		cw := u.CacheWriteTokens
		if cw == 0 && u.CacheWriteTokensSnake != 0 {
			cw = u.CacheWriteTokensSnake
		}
		reas := u.ReasoningTokens
		if reas == 0 && u.ReasoningTokensSnake != 0 {
			reas = u.ReasoningTokensSnake
		}
		input += in
		output += out
		cacheRead += cr
		cacheWrite += cw
		reasoning += reas
	}
	if err := sc.Err(); err != nil {
		return LaneTokens{}, fmt.Errorf("read dsh transcript: %w", err)
	}
	return LaneTokens{
		Tokens: cardcost.Tokens{
			Input:      input,
			CacheRead:  cacheRead,
			CacheWrite: cacheWrite,
			Output:     output,
			Reasoning:  reasoning,
			Requests:   cardcost.Unreported,
			MaxPrompt:  cardcost.Unreported,
		},
		Harness:  "dsh",
		Sessions: 1,
	}, nil
}

// TokensFromDSH reads a session's tokens from its DSH session transcript
// (session.v4.jsonl.zstd, decompressing via run if present, or session.v4.jsonl).
func TokensFromDSH(ctx context.Context, run Exec, sessionsRoot, workDir, sess string) (LaneTokens, error) {
	if !sessionID.MatchString(sess) {
		return LaneTokens{}, fmt.Errorf("session %q is invalid", sess)
	}
	if sessionsRoot == "" {
		sessionsRoot = dshHome()
	}
	bucket := filepath.Join(sessionsRoot, DSHSessionKey(workDir))
	if fi, err := os.Stat(bucket); err != nil || !fi.IsDir() {
		if realDir, err := filepath.EvalSymlinks(workDir); err == nil && realDir != workDir {
			alt := filepath.Join(sessionsRoot, DSHSessionKey(realDir))
			if fi2, err := os.Stat(alt); err == nil && fi2.IsDir() {
				bucket = alt
			}
		}
	}
	sessionDir := filepath.Join(bucket, sess)

	zstdFile := filepath.Join(sessionDir, "session.v4.jsonl.zstd")
	jsonlFile := filepath.Join(sessionDir, "session.v4.jsonl")

	if fi, err := os.Lstat(zstdFile); err == nil && fi.Mode().IsRegular() {
		if run == nil {
			return LaneTokens{}, errors.New("no exec runner to decompress zstd transcript")
		}
		out, exit, err := run(ctx, sessionDir, "zstd", []string{"-dc", zstdFile}, "")
		if err != nil {
			return LaneTokens{}, fmt.Errorf("zstd %s: %w", zstdFile, err)
		}
		if exit != 0 {
			return LaneTokens{}, fmt.Errorf("zstd %s exited %d", zstdFile, exit)
		}
		return TokensOfDSH(out)
	}

	if fi, err := os.Lstat(jsonlFile); err == nil && fi.Mode().IsRegular() {
		raw, err := os.ReadFile(jsonlFile)
		if err != nil {
			return LaneTokens{}, fmt.Errorf("read %s: %w", jsonlFile, err)
		}
		return TokensOfDSH(string(raw))
	}

	return LaneTokens{}, fmt.Errorf("dsh session %s transcript not found in %s", sess, sessionDir)
}
