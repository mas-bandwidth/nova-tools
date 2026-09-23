package pulse

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A gateway death is an attempt that ended with no model turn (issue #2634): the
// provider's gateway answered 5xx, or the attempt failed with no tokens and no error
// recorded. Either way the route died before the card ran. It is not a card failure.
//
// The class is decided when the job is indexed, from the usage row and the capture
// present then. A warm tick reads the cached class and does not open the log again.

// failureColumns counts indexed attempts. cardFail is the card's own verdict
// (abstain, blocked) and every other failed attempt; gateway is the route deaths.
// A gateway death is in one column, never both.
func failureColumns(files []usageFile) (cardFail, gateway int) {
	for _, f := range files {
		switch f.class {
		case "gateway":
			gateway++
		case "failed", "abstain", "blocked":
			cardFail++
		}
	}
	return cardFail, gateway
}

// resultVerdict is RESULT.md line 2 when it is the card's own word: abstain,
// blocked or done. Anything else — including no file — is empty, and the attempt
// is classified from its usage and its capture.
func resultVerdict(jobDir string) string {
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if len(lines) <= 1 {
		return ""
	}
	switch line2 := strings.TrimSpace(lines[1]); {
	case strings.HasPrefix(line2, "ABSTAIN"):
		return "abstain"
	case strings.HasPrefix(line2, "BLOCKED"):
		return "blocked"
	case strings.HasPrefix(line2, "DONE"):
		return "done"
	}
	return ""
}

// classifyNoVerdict is the class of an attempt that published no card verdict.
func classifyNoVerdict(jobDir string, usageRaw []byte, rows []indexRow) string {
	if gatewayDeath(jobDir, usageRaw, rows) {
		return "gateway"
	}
	f := factsFromUsage(usageRaw, rows)
	if f.rcKnown {
		if f.rcNonZero {
			return "failed"
		}
		return "done"
	}
	class, _ := entrySummary(rows)
	return class
}

type attemptFacts struct {
	turn      bool
	rcKnown   bool
	rcNonZero bool
	end       string
}

// factsFromUsage reads tokens, rc and end by header name, so a sixteen-column
// usage row and the thirteen-column shape status already stored agree. Rows with
// no token columns fall back to the positional cells the index kept.
func factsFromUsage(raw []byte, rows []indexRow) attemptFacts {
	var f attemptFacts
	lines := strings.Split(string(raw), "\n")
	headerTokens := false
	if len(lines) > 0 && strings.Contains(lines[0], "\t") {
		head := strings.Split(lines[0], "\t")
		idx := make(map[string]int, len(head))
		for i, h := range head {
			idx[strings.TrimSpace(h)] = i
		}
		_, hasIn := idx["tokens_in"]
		_, hasOut := idx["tokens_out"]
		_, hasRC := idx["rc"]
		_, hasEnd := idx["end"]
		headerTokens = hasIn || hasOut
		if headerTokens || hasRC || hasEnd {
			get := func(cols []string, name string) string {
				i, ok := idx[name]
				if !ok || i >= len(cols) {
					return ""
				}
				return strings.TrimSpace(cols[i])
			}
			for _, line := range lines[1:] {
				if strings.TrimSpace(line) == "" {
					continue
				}
				cols := strings.Split(line, "\t")
				if headerTokens {
					for _, name := range []string{"tokens_in", "tokens_out"} {
						v := get(cols, name)
						if v == "" || v == "-" {
							continue
						}
						if n, err := strconv.Atoi(v); err == nil && n > 0 {
							f.turn = true
						}
					}
				}
				if hasRC {
					v := get(cols, "rc")
					if v != "" && v != "-" {
						if n, err := strconv.Atoi(v); err == nil {
							f.rcKnown = true
							if n != 0 {
								f.rcNonZero = true
							}
						}
					}
				}
				if hasEnd {
					if v := get(cols, "end"); v != "" && v != "-" {
						f.end = v
					}
				}
			}
		}
	}
	if headerTokens {
		return f
	}
	for _, r := range rows {
		if r.tokens == "" || r.tokens == "-" {
			continue
		}
		if n, err := strconv.Atoi(r.tokens); err == nil && n > 0 {
			f.turn = true
			break
		}
	}
	if !f.rcKnown && len(rows) > 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(rows[0].rc)); err == nil {
			f.rcKnown = true
			f.rcNonZero = n != 0
		}
	}
	return f
}

// gatewayDeath reports an attempt the route ended before the model had a turn.
func gatewayDeath(jobDir string, usageRaw []byte, rows []indexRow) bool {
	f := factsFromUsage(usageRaw, rows)
	if f.turn {
		return false
	}
	end, capture := jobEvidence(jobDir)
	if f.end != "" {
		end = f.end
	}
	if nonProviderEnd(end) {
		return false
	}
	if end == swarm.EndProvider || end == swarm.EndLaunchFailed {
		return true
	}
	if _, ok := swarm.ProviderLaunchFailure([]byte(capture)); ok {
		return true
	}
	if strings.TrimSpace(capture) != "" {
		return false
	}
	// No tokens and no error, and the attempt still failed: the quiet provider
	// death, not a card that ran and abstained.
	return f.rcNonZero || end == swarm.EndFailed
}

func nonProviderEnd(end string) bool {
	switch end {
	case swarm.EndWall, swarm.EndKilled, swarm.EndBudget, swarm.EndUnverifiable,
		swarm.EndViolation, swarm.EndInputLimit, swarm.EndDone:
		return true
	default:
		return false
	}
}

// jobEvidence is the supervisor's end word and the capture a gateway error would
// be in: the head of each harness log, its tail when the file is longer than that
// head, and exit.json's reason. Missing files are empty, not an error.
func jobEvidence(jobDir string) (end, capture string) {
	end, reason := readExit(jobDir)
	var b strings.Builder
	for _, name := range []string{"harness-output.log", "harness.log"} {
		path := filepath.Join(jobDir, name)
		if s := readHead(path, captureHeadBytes); s != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(s)
		}
		if s := readTail(path, captureHeadBytes); s != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(s)
		}
	}
	if reason != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(reason)
	}
	return end, b.String()
}

func readExit(jobDir string) (end, reason string) {
	f, err := os.Open(filepath.Join(jobDir, "exit.json"))
	if err != nil {
		return "", ""
	}
	defer f.Close()
	var rec struct {
		End    string `json:"end"`
		Reason string `json:"reason"`
	}
	if json.NewDecoder(io.LimitReader(f, int64(captureHeadBytes))).Decode(&rec) != nil {
		return "", ""
	}
	return strings.TrimSpace(rec.End), strings.TrimSpace(rec.Reason)
}

func readHead(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, n)
	k, _ := f.Read(buf)
	if k <= 0 {
		return ""
	}
	return string(buf[:k])
}

// readTail is the last n bytes, empty when the head read already covered the file.
func readTail(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() <= int64(n) {
		return ""
	}
	if _, err := f.Seek(fi.Size()-int64(n), io.SeekStart); err != nil {
		return ""
	}
	buf := make([]byte, n)
	k, _ := f.Read(buf)
	if k <= 0 {
		return ""
	}
	s := string(buf[:k])
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}
