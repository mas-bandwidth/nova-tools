package friend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

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

type dshSessionLine struct {
	Data struct {
		Usage struct {
			InputTokens      int64 `json:"inputTokens"`
			CacheReadTokens  int64 `json:"cacheReadTokens"`
			CacheWriteTokens int64 `json:"cacheWriteTokens"`
			OutputTokens     int64 `json:"outputTokens"`
		} `json:"usage"`
		Message struct {
			Source struct {
				Model string `json:"model"`
			} `json:"source"`
		} `json:"message"`
		Model string `json:"model"`
	} `json:"data"`
}

// FindDSHSessionFile locates the session.v4.jsonl.zstd (or uncompressed .jsonl)
// whose header contains the given title or job identifier.
func FindDSHSessionFile(bucket, title, job string) (string, error) {
	entries, err := os.ReadDir(bucket)
	if err != nil {
		return "", err
	}
	var best string
	var bestTime time.Time
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "session-") {
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
			raw, _ := os.ReadFile(file)
			head := string(raw)
			if len(head) > 4096 {
				head = head[:4096]
			}
			if strings.Contains(head, "Lane: "+title+".") || (job != "" && strings.Contains(head, "inbox/"+job+"/BRIEF.md")) {
				return file, nil
			}
			if best == "" || info.ModTime().After(bestTime) {
				best = file
				bestTime = info.ModTime()
			}
		}
	}
	if best != "" {
		return best, nil
	}
	return "", errors.New("no matching dsh session file found")
}

// TokensFromDSHSessionFile decodes usage tokens from a session file.
func TokensFromDSHSessionFile(ctx context.Context, run Exec, path string) (LaneTokens, string, error) {
	var content string
	if strings.HasSuffix(path, ".zstd") && run != nil {
		out, exit, err := run(ctx, filepath.Dir(path), "zstd", []string{"-dcq", "--", path}, "")
		if err == nil && exit == 0 && out != "" {
			content = out
		}
	}
	if content == "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return LaneTokens{}, "", err
		}
		content = string(raw)
	}
	return ParseDSHSessionJSON(content)
}

// ParseDSHSessionJSON parses newline-delimited session records and returns summed tokens.
func ParseDSHSessionJSON(content string) (LaneTokens, string, error) {
	var in, cr, cw, out int64
	var model string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record dshSessionLine
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		u := record.Data.Usage
		in += u.InputTokens
		cr += u.CacheReadTokens
		cw += u.CacheWriteTokens
		out += u.OutputTokens
		if record.Data.Message.Source.Model != "" {
			model = record.Data.Message.Source.Model
		} else if record.Data.Model != "" {
			model = record.Data.Model
		}
	}
	lt := LaneTokens{
		Tokens: cardcost.Tokens{
			Input:      in,
			CacheRead:  cr,
			CacheWrite: cw,
			Output:     out,
			Reasoning:  0,
		},
		USD:      "-",
		Sessions: 1,
	}
	return lt, model, nil
}
