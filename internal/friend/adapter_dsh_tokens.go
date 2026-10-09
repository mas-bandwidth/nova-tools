package friend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// DSHRouteModel maps internal dsh model names to sprint route row model names.
var DSHRouteModel = map[string]string{
	"deepseek-flash":  "deepseek-v4.1-flash",
	"deepseek-v4-pro": "deepseek-v4",
}

func ModelForDSH(dshModel string) string {
	if m, ok := DSHRouteModel[dshModel]; ok {
		return "deepseek/" + m
	}
	if strings.HasPrefix(dshModel, "deepseek/") {
		return dshModel
	}
	if dshModel == "" {
		return "deepseek/deepseek-v4.1-flash"
	}
	return "deepseek/" + dshModel
}

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
		Usage   *dshUsage `json:"usage"`
		Message struct {
			Source struct {
				Model string `json:"model"`
			} `json:"source"`
		} `json:"message"`
		Model string `json:"model"`
	} `json:"data"`
}

var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

func decodeDSHSessionFile(ctx context.Context, run Exec, path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err == nil {
		path = absPath
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if (len(raw) >= 4 && bytes.Equal(raw[:4], zstdMagic)) || strings.HasSuffix(path, ".zstd") {
		if run != nil {
			out, exit, err := run(ctx, filepath.Dir(path), "zstd", []string{"-dcq", "--", path}, "")
			if err != nil {
				return "", fmt.Errorf("zstd %s: %w", path, err)
			}
			if exit != 0 {
				return "", fmt.Errorf("zstd %s exited %d: %s", path, exit, out)
			}
			return out, nil
		}
		cmd := exec.CommandContext(ctx, "zstd", "-dcq", "--", path)
		cmd.Dir = filepath.Dir(path)
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("zstd %s: %w", path, err)
		}
		return string(out), nil
	}
	return string(raw), nil
}

func isHex(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func isDSHSessionDir(name string) bool {
	return strings.HasPrefix(name, "session-") || isHex(name)
}

// FindDSHSessionFile locates the session.v4.jsonl.zstd (or uncompressed .jsonl)
// whose decoded transcript contains the given title or job identifier.
func FindDSHSessionFile(ctx context.Context, run Exec, bucket, title, job string) (string, error) {
	entries, err := os.ReadDir(bucket)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() || !isDSHSessionDir(e.Name()) {
			continue
		}
		dir := filepath.Join(bucket, e.Name())
		candidates := []string{
			filepath.Join(dir, "session.v4.jsonl.zstd"),
			filepath.Join(dir, "session.v4.jsonl"),
		}
		for _, file := range candidates {
			info, err := os.Stat(file)
			if err != nil || info.Size() == 0 {
				continue
			}
			decoded, err := decodeDSHSessionFile(ctx, run, file)
			if err != nil {
				continue
			}
			if strings.Contains(decoded, "Lane: "+title+".") ||
				(job != "" && (strings.Contains(decoded, "inbox/"+job+"/BRIEF.md") || strings.Contains(decoded, "inbox/"+job+"~"))) {
				return file, nil
			}
		}
	}
	return "", fmt.Errorf("no matching dsh session file found for lane %q job %q", title, job)
}

// TokensFromDSHSessionFile decodes usage tokens and model from a session file.
func TokensFromDSHSessionFile(ctx context.Context, run Exec, path string) (LaneTokens, string, error) {
	content, err := decodeDSHSessionFile(ctx, run, path)
	if err != nil {
		return LaneTokens{}, "", err
	}
	return parseDSHSessionJSON(content)
}

// parseDSHSessionJSON parses newline-delimited session records and returns summed tokens.
func parseDSHSessionJSON(content string) (LaneTokens, string, error) {
	var in, cr, cw, out, reas int64
	var model string
	sc := bufio.NewScanner(strings.NewReader(content))
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
		if (m.Type != "" && m.Type != "assistant/message") || m.Data.Usage == nil {
			continue
		}
		u := m.Data.Usage
		input := u.InputTokens
		if input == 0 && u.InputTokensSnake != 0 {
			input = u.InputTokensSnake
		}
		output := u.OutputTokens
		if output == 0 && u.OutputTokensSnake != 0 {
			output = u.OutputTokensSnake
		}
		cRead := u.CacheReadTokens
		if cRead == 0 && u.CacheReadTokensSnake != 0 {
			cRead = u.CacheReadTokensSnake
		}
		cWrite := u.CacheWriteTokens
		if cWrite == 0 && u.CacheWriteTokensSnake != 0 {
			cWrite = u.CacheWriteTokensSnake
		}
		reasoning := u.ReasoningTokens
		if reasoning == 0 && u.ReasoningTokensSnake != 0 {
			reasoning = u.ReasoningTokensSnake
		}
		in += input
		cr += cRead
		cw += cWrite
		out += output
		reas += reasoning
		if m.Data.Message.Source.Model != "" {
			model = m.Data.Message.Source.Model
		} else if m.Data.Model != "" {
			model = m.Data.Model
		}
	}
	if err := sc.Err(); err != nil {
		return LaneTokens{}, "", fmt.Errorf("read dsh transcript: %w", err)
	}
	lt := LaneTokens{
		Tokens: cardcost.Tokens{
			Input:      in,
			CacheRead:  cr,
			CacheWrite: cw,
			Output:     out,
			Reasoning:  reas,
			Requests:   cardcost.Unreported,
			MaxPrompt:  cardcost.Unreported,
		},
		USD:      "-",
		Sessions: 1,
		Harness:  "dsh",
	}
	return lt, model, nil
}

// TokensOfDSH parses the JSONL transcript of a DSH session and sums the usage
// across all assistant messages.
func TokensOfDSH(transcript string) (LaneTokens, error) {
	lt, _, err := parseDSHSessionJSON(transcript)
	return lt, err
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
