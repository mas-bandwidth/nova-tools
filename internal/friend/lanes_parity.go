package friend

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// FriendRowConfig is configuration for a friend read off her friend row or beat line.
type FriendRowConfig struct {
	Mode     string
	Width    int
	Profile  string
	Filter   string
	Tiers    []string
	LoadMax  float64
	TokenCap int64
	Model    string
	Provider string
}

// ParseFriendRow parses the friend row fields from her beat answer line.
func ParseFriendRow(answer string) FriendRowConfig {
	var cfg FriendRowConfig
	for _, w := range strings.Fields(answer) {
		if v, found := strings.CutPrefix(w, "row_mode="); found {
			cfg.Mode = v
		}
		if v, found := strings.CutPrefix(w, "row_width="); found {
			if n, err := strconv.Atoi(v); err == nil {
				cfg.Width = n
			}
		}
		if v, found := strings.CutPrefix(w, "row_profile="); found {
			cfg.Profile = v
		}
		if v, found := strings.CutPrefix(w, "row_filter="); found {
			cfg.Filter = v
		}
		if v, found := strings.CutPrefix(w, "row_tiers="); found {
			cfg.Tiers = splitCommas(v)
		}
		if v, found := strings.CutPrefix(w, "row_load_max="); found {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				cfg.LoadMax = f
			}
		}
		if v, found := strings.CutPrefix(w, "row_token_cap="); found {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				cfg.TokenCap = n
			}
		}
		if v, found := strings.CutPrefix(w, "row_model="); found {
			cfg.Model = v
		}
		if v, found := strings.CutPrefix(w, "row_provider="); found {
			cfg.Provider = v
		}
	}
	return cfg
}

func splitCommas(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

var (
	briefTierRE   = regexp.MustCompile(`(?m)\btier:\s*([a-z]+)`)
	briefStreamRE = regexp.MustCompile(`(?m)\((?:stream\s+)?([a-zA-Z0-9_\-\.]+)\)|\bstream:\s*([a-zA-Z0-9_\-\.]+)`)
)

// ReadCardTier reads the tier (e.g. "flash", "pro") from BRIEF.md content.
func ReadCardTier(briefContent string) string {
	m := briefTierRE.FindStringSubmatch(briefContent)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// ReadCardStream reads the stream from BRIEF.md content or falls back to card ID prefix.
func ReadCardStream(briefContent, cardID string) string {
	m := briefStreamRE.FindStringSubmatch(briefContent)
	if len(m) > 1 && m[1] != "" {
		return m[1]
	}
	if len(m) > 2 && m[2] != "" {
		return m[2]
	}
	if idx := strings.Index(cardID, "-"); idx > 0 {
		return cardID[:idx]
	}
	return ""
}

// CardFilter evaluates whether a card should be run, skipped, or taken back.
// filter: "flash" or "security" (or empty).
// allowedTiers: list of tiers the friend works (e.g. ["flash"]).
// friend: friend's name (e.g. "Freddy" or "Alex").
func CardFilter(id, stream, tier, filter string, allowedTiers []string, friend string) (action, reason string) {
	if friend == "" {
		friend = "the friend"
	}
	switch filter {
	case "security":
		if strings.HasPrefix(stream, "security") ||
			strings.HasPrefix(id, "fp-sec") ||
			strings.HasPrefix(id, "sec-") ||
			strings.HasPrefix(id, "security-") {
			return "run", ""
		}
		return "skip", fmt.Sprintf("not a security card (stream %s)", stream)
	case "flash":
		if tier == "flash" {
			return "run", ""
		}
		return "take", fmt.Sprintf("tier %s (%s runs flash cards only; the rest go back to the dealer)", tier, friend)
	default:
		if len(allowedTiers) > 0 {
			if slices.Contains(allowedTiers, tier) {
				return "run", ""
			}
			return "take", fmt.Sprintf("tier %s (%s works %s cards only; the rest go back to the dealer)", tier, friend, strings.Join(allowedTiers, ","))
		}
		return "run", ""
	}
}

// ShouldTakeBack reports whether a card with the given filter action should be taken back,
// given whether its job directory already exists.
func ShouldTakeBack(action string, jobDirExists bool) bool {
	return strings.HasPrefix(action, "take") && !jobDirExists
}

// FormatJob formats a job name for a card, sprint epoch, and generation.
// Generations past the first (gen > 1) carry .g<gen>.
func FormatJob(card string, epoch int, gen int) string {
	job := card
	if epoch > 0 {
		job = fmt.Sprintf("%s~%d", card, epoch)
	}
	if gen > 1 {
		job = fmt.Sprintf("%s.g%d", job, gen)
	}
	return job
}

// ParseJob parses a job name into its card ID, epoch, and generation.
func ParseJob(name string) (card string, epoch int, gen int, ok bool) {
	if name == "" {
		return "", 0, 0, false
	}
	gen = 1
	rest := name
	if before, after, found := strings.Cut(rest, ".g"); found {
		g, err := strconv.Atoi(after)
		if err != nil || g < 1 {
			return "", 0, 0, false
		}
		gen = g
		rest = before
	}
	if before, after, found := strings.Cut(rest, "~"); found {
		ep, err := strconv.Atoi(after)
		if err != nil || ep < 0 {
			return "", 0, 0, false
		}
		epoch = ep
		rest = before
	}
	if rest == "" {
		return "", 0, 0, false
	}
	return rest, epoch, gen, true
}

// DefaultHeldWidth is the maximum width while machine load is above the threshold.
const DefaultHeldWidth = 3

// EffectiveWidth calculates the effective lane width given desired width, current
// 1-minute load, and loadMax threshold. When loadMax > 0 and load > loadMax, width
// is held to DefaultHeldWidth (if width > DefaultHeldWidth).
func EffectiveWidth(width int, load float64, loadMax float64) int {
	return EffectiveWidthHeld(width, load, loadMax, DefaultHeldWidth)
}

// EffectiveWidthHeld calculates effective width holding to heldWidth when load exceeds loadMax.
func EffectiveWidthHeld(width int, load float64, loadMax float64, heldWidth int) int {
	if loadMax > 0 && load > loadMax && width > heldWidth {
		return heldWidth
	}
	return width
}

// CheckTokenCap reports whether tokens have reached or exceeded cap (when cap > 0).
func CheckTokenCap(tokens int64, cap int64) bool {
	return cap > 0 && tokens >= cap
}

// HoldReportTokenCap writes the HOLD report when a card exceeds its token cap.
func HoldReportTokenCap(tokens int64, turns int, lastStep, friend string, cap int64) string {
	if lastStep == "" {
		lastStep = "unknown"
	}
	return fmt.Sprintf("Verdict: HOLD\nHead: none\n\ntoken cap: %d tokens, %d turns, last step: %s. %s's one-shot lane was stopped at the runner's per-card cap of %d tokens (Mercury re-sends its whole context, so a long card snowballs); the card goes to a bud, and whatever was pushed on its branch is a draft only.\n",
		tokens, turns, lastStep, friend, cap)
}

// ProviderFailureRE matches unrecoverable provider failures that pause the friend.
var ProviderFailureRE = regexp.MustCompile(`(?i)(^|[^0-9])(402|429)([^0-9]|$)|insufficient[ _-]?(funds|credit|balance|quota)|payment required|out of (funds|credits)|credit balance|exceeded your current quota|rate[ _-]?limit|too many requests`)

// IsProviderFailure reports whether turn output contains an unrecoverable provider failure.
func IsProviderFailure(out string) (message string, isFailure bool) {
	for _, line := range strings.Split(out, "\n") {
		clean := stripANSI(line)
		if ProviderFailureRE.MatchString(clean) {
			return oneLine(clean, 300), true
		}
	}
	return "", false
}

// OpencodeTokens represents token usage queried from opencode's SQLite database.
type OpencodeTokens struct {
	Input      int64
	CacheRead  int64
	CacheWrite int64
	Output     int64
	Reasoning  int64
	Cost       float64
	Sessions   int
}

// Total returns total tokens (input + cache_read + cache_write + output + reasoning).
func (t OpencodeTokens) Total() int64 {
	return t.Input + t.CacheRead + t.CacheWrite + t.Output + t.Reasoning
}

// RoutePrice holds pricing information from a nova-sprint route row.
type RoutePrice struct {
	RouteName         string
	Input             string // USD per 1M tokens
	CacheRead         string
	CacheWrite        string
	Output            string
	ReasoningAsOutput bool
	HasExtra          bool // long_context, request, gateway_percent
}

// CostOf computes the dollar cost of token counts using a route price sheet,
// rounded up to the cent, or returns "unpriced (<reason>)".
func CostOf(tk OpencodeTokens, route *RoutePrice, model string) string {
	if route == nil || route.RouteName == "" {
		return fmt.Sprintf("unpriced (no route row for %s in nova-sprint routes)", model)
	}
	if route.HasExtra {
		return fmt.Sprintf("unpriced (route %s has long-context, request or gateway prices this runner does not apply)", route.RouteName)
	}
	if tk.Input > 0 && route.Input == "" {
		return fmt.Sprintf("unpriced (route %s has no input price)", route.RouteName)
	}
	if tk.CacheRead > 0 && route.CacheRead == "" {
		return fmt.Sprintf("unpriced (route %s has no cache_read price)", route.RouteName)
	}
	if tk.CacheWrite > 0 && route.CacheWrite == "" {
		return fmt.Sprintf("unpriced (route %s has no cache_write price)", route.RouteName)
	}
	out := tk.Output
	if route.ReasoningAsOutput {
		out += tk.Reasoning
	}
	if out > 0 && route.Output == "" {
		return fmt.Sprintf("unpriced (route %s has no output price)", route.RouteName)
	}

	pIn, _ := strconv.ParseFloat(route.Input, 64)
	pCr, _ := strconv.ParseFloat(route.CacheRead, 64)
	pCw, _ := strconv.ParseFloat(route.CacheWrite, 64)
	pOut, _ := strconv.ParseFloat(route.Output, 64)

	totalUSD := (float64(tk.Input)*pIn + float64(tk.CacheRead)*pCr + float64(tk.CacheWrite)*pCw + float64(out)*pOut) / 1000000.0
	return Dollars(totalUSD)
}

// Dollars formats an amount in USD rounded up to the next cent.
func Dollars(amount float64) string {
	cents := int64(math.Ceil(amount * 100.0))
	if cents < 0 {
		cents = 0
	}
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}

// FormatCostLine formats the Cost: line for REPORT.md.
func FormatCostLine(cost, oc string, tk OpencodeTokens, model, route string) string {
	if route == "" {
		route = "-"
	}
	return fmt.Sprintf("Cost: %s (opencode: %s) tokens input=%d cache_read=%d cache_write=%d output=%d reasoning=%d model=%s harness=opencode price_route=%s",
		cost, oc, tk.Input, tk.CacheRead, tk.CacheWrite, tk.Output, tk.Reasoning, model, route)
}

// PublishReportWithCost inserts or updates the Cost: line in REPORT.md under Head:.
func PublishReportWithCost(reportContent, costLine string) string {
	lines := strings.Split(reportContent, "\n")
	var out []string
	inserted := false
	headRe := regexp.MustCompile(`(?i)^[#*_ -]*head:`)
	for _, l := range lines {
		out = append(out, l)
		if !inserted && headRe.MatchString(l) {
			out = append(out, costLine)
			inserted = true
		}
	}
	if !inserted {
		out = append(out, costLine)
	}
	return strings.Join(out, "\n")
}

// AppendResultCost appends tokens: and cost: lines to RESULT.md content.
func AppendResultCost(resultContent, cost, oc string, tk OpencodeTokens, model string) string {
	var b strings.Builder
	trimmed := strings.TrimRight(resultContent, "\n")
	if trimmed != "" {
		b.WriteString(trimmed)
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "tokens: input=%d cache_read=%d cache_write=%d output=%d reasoning=%d model=%s harness=opencode\n",
		tk.Input, tk.CacheRead, tk.CacheWrite, tk.Output, tk.Reasoning, model)
	fmt.Fprintf(&b, "cost: %s (opencode: %s)\n", cost, oc)
	return b.String()
}

// ParseTokensOutput parses the whitespace-delimited columns from sqlite3.
func ParseTokensOutput(out string) (OpencodeTokens, error) {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) < 7 {
		return OpencodeTokens{}, fmt.Errorf("unexpected tokens output: %q", out)
	}
	in, _ := strconv.ParseInt(fields[0], 10, 64)
	cr, _ := strconv.ParseInt(fields[1], 10, 64)
	cw, _ := strconv.ParseInt(fields[2], 10, 64)
	outTok, _ := strconv.ParseInt(fields[3], 10, 64)
	rs, _ := strconv.ParseInt(fields[4], 10, 64)
	cost, _ := strconv.ParseFloat(fields[5], 64)
	sess, _ := strconv.Atoi(fields[6])
	return OpencodeTokens{
		Input:      in,
		CacheRead:  cr,
		CacheWrite: cw,
		Output:     outTok,
		Reasoning:  rs,
		Cost:       cost,
		Sessions:   sess,
	}, nil
}

// SessionTokensSQL is the exact SQLite query runner.zsh executes to read token counts
// for a session and its children.
const SessionTokensSQL = `with s as (select id from session where title = '%s' or id = '%s') ` +
	`select coalesce(sum(tokens_input),0), coalesce(sum(tokens_cache_read),0), ` +
	`coalesce(sum(tokens_cache_write),0), coalesce(sum(tokens_output),0), ` +
	`coalesce(sum(tokens_reasoning),0), coalesce(sum(cost),0), count(*) ` +
	`from session where id in (select id from s) or parent_id in (select id from s);`

// QuerySessionTokens reads token counts from opencode's SQLite database.
func QuerySessionTokens(ctx context.Context, dbPath, titleOrID string, run func(ctx context.Context, name string, args ...string) (string, error)) (OpencodeTokens, error) {
	if run == nil {
		run = func(ctx context.Context, name string, args ...string) (string, error) {
			cmd := subproc.Context(ctx, name, args...)
			out, err := cmd.Output()
			return string(out), err
		}
	}
	query := fmt.Sprintf(SessionTokensSQL, titleOrID, titleOrID)
	out, err := run(ctx, "sqlite3", "-readonly", "-tabs", dbPath, query)
	if err != nil {
		return OpencodeTokens{}, err
	}
	return ParseTokensOutput(out)
}

// WriteRefusalShims writes refusal shims for go and gofmt into <dir>/bin.
func WriteRefusalShims(dir, friend string) (string, error) {
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	for _, prog := range []string{"go", "gofmt"} {
		script := fmt.Sprintf(`#!/bin/sh
# Refusal shim: go tools never run on this host.
echo "REFUSED: %s never runs on this host. Sync the clone to the build runner and run it there: rsync -a --delete jobs/<job>/repo/ <host>:%s-bench/<job>/repo/ && ssh <host> 'cd %s-bench/<job>/repo && export GOCACHE=\$HOME/%s-bench/.cache/go-build GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 && %s ...'" >&2
exit 1
`, prog, friend, friend, friend, prog)
		path := filepath.Join(binDir, prog)
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			return "", err
		}
	}
	return binDir, nil
}

// FinishBusNote formats the subject and body of the bus notification sent to
// the coordinator when a card finishes in a one-shot lane.
func FinishBusNote(friend, job, rep, cost, oc string, wall time.Duration) (subject, body string) {
	subject = fmt.Sprintf("%s card %s: %s", friend, job, rep)
	body = fmt.Sprintf("%s one-shot lane finished %s: %s; cost %s (opencode %s); wall %ds",
		friend, job, rep, cost, oc, int(wall.Seconds()))
	return subject, body
}

// FindRoutePrice searches routes JSON (from `nova-sprint routes --json`) for the
// route matching the given model (either "provider/model" or "model").
func FindRoutePrice(routesJSON, targetModel string) (*RoutePrice, error) {
	var raw any
	if err := json.Unmarshal([]byte(routesJSON), &raw); err != nil {
		return nil, err
	}
	targetProvider := ""
	if p, m, ok := strings.Cut(targetModel, "/"); ok {
		targetProvider, targetModel = p, m
	}

	var foundMap map[string]any
	var walk func(v any)
	walk = func(v any) {
		if foundMap != nil {
			return
		}
		switch val := v.(type) {
		case map[string]any:
			mod, _ := val["model"].(string)
			prov, _ := val["provider"].(string)
			_, hasPrices := val["prices"]
			if hasPrices && mod == targetModel && (targetProvider == "" || prov == targetProvider) {
				foundMap = val
				return
			}
			for _, child := range val {
				walk(child)
			}
		case []any:
			for _, child := range val {
				walk(child)
			}
		}
	}
	walk(raw)

	if foundMap == nil {
		return nil, nil
	}

	name, _ := foundMap["name"].(string)
	pMap, _ := foundMap["prices"].(map[string]any)
	if pMap == nil {
		return &RoutePrice{RouteName: name}, nil
	}

	getString := func(k string) string {
		if s, ok := pMap[k].(string); ok {
			return s
		}
		return ""
	}
	ro := true
	if b, ok := pMap["reasoning_as_output"].(bool); ok {
		ro = b
	}
	longCtx := int64(0)
	if n, ok := pMap["long_context"].(float64); ok {
		longCtx = int64(n)
	}
	hasExtra := longCtx > 0 || getString("request") != "" || getString("gateway_percent") != ""

	return &RoutePrice{
		RouteName:         name,
		Input:             getString("input"),
		CacheRead:         getString("cache_read"),
		CacheWrite:        getString("cache_write"),
		Output:            getString("output"),
		ReasoningAsOutput: ro,
		HasExtra:          hasExtra,
	}, nil
}
