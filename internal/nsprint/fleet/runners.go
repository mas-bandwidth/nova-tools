package fleet

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// RunnersKeyRegistered is the Redis key caching GitHub registered runner counts per bench.
const RunnersKeyRegistered = "fleet:runners:registered"

// RunnersCacheTTL is how long the GitHub runners cache is held in Redis.
const RunnersCacheTTL = 5 * time.Minute

// RunnerRow is one row of runners.tsv.
type RunnerRow struct {
	Host        string
	Count       int
	Labels      string
	User        string
	Description string
}

// RunnersTable represents the parsed runners.tsv file preserving formatting and comments.
type RunnersTable struct {
	Rows     []RunnerRow
	RawLines []string
	RowIndex map[string]int // host -> index in Rows
}

// ReadRunners parses runners.tsv from an io.Reader.
func ReadRunners(r io.Reader) (*RunnersTable, error) {
	scanner := bufio.NewScanner(r)
	tbl := &RunnersTable{
		RowIndex: make(map[string]int),
	}

	for scanner.Scan() {
		line := scanner.Text()
		tbl.RawLines = append(tbl.RawLines, line)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		host := strings.TrimSpace(parts[0])
		count, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			continue
		}
		labels := ""
		if len(parts) > 2 {
			labels = strings.TrimSpace(parts[2])
		}
		user := ""
		if len(parts) > 3 {
			user = strings.TrimSpace(parts[3])
		}
		desc := ""
		if len(parts) > 4 {
			desc = strings.TrimSpace(parts[4])
		}

		idx := len(tbl.Rows)
		tbl.Rows = append(tbl.Rows, RunnerRow{
			Host:        host,
			Count:       count,
			Labels:      labels,
			User:        user,
			Description: desc,
		})
		tbl.RowIndex[host] = idx
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read runners: %w", err)
	}
	return tbl, nil
}

// ReadRunnersFile reads and parses a runners.tsv file from disk.
func ReadRunnersFile(path string) (*RunnersTable, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open runners file: %w", err)
	}
	defer f.Close()
	return ReadRunners(f)
}

// Count returns the declared runner count for a given host, or 0 if not declared.
func (t *RunnersTable) Count(host string) int {
	if idx, ok := t.RowIndex[host]; ok {
		return t.Rows[idx].Count
	}
	return 0
}

// SetCount updates the runner count for a host, updating both Rows and RawLines.
// If the host is not found, a new row is appended. Returns old count.
func (t *RunnersTable) SetCount(host string, count int) int {
	oldCount := 0
	if idx, ok := t.RowIndex[host]; ok {
		oldCount = t.Rows[idx].Count
		t.Rows[idx].Count = count

		// Update corresponding line in RawLines
		targetHost := host
		for i, line := range t.RawLines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) >= 2 && strings.TrimSpace(parts[0]) == targetHost {
				parts[1] = strconv.Itoa(count)
				t.RawLines[i] = strings.Join(parts, "\t")
				break
			}
		}
		return oldCount
	}

	// Host not present; append new row
	newRow := RunnerRow{
		Host:        host,
		Count:       count,
		Labels:      host,
		User:        "-",
		Description: fmt.Sprintf("added by fleet runners set on %s", time.Now().Format("2006-01-02")),
	}
	idx := len(t.Rows)
	t.Rows = append(t.Rows, newRow)
	t.RowIndex[host] = idx
	newLine := fmt.Sprintf("%s\t%d\t%s\t%s\t%s", newRow.Host, newRow.Count, newRow.Labels, newRow.User, newRow.Description)
	t.RawLines = append(t.RawLines, newLine)
	return oldCount
}

// Save writes the updated table back to path atomically.
func (t *RunnersTable) Save(path string) error {
	var buf bytes.Buffer
	for _, l := range t.RawLines {
		buf.WriteString(l)
		buf.WriteByte('\n')
	}
	tmpPath := fmt.Sprintf("%s.tmp-%d", path, os.Getpid())
	if err := os.WriteFile(tmpPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("write runners file temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename runners file: %w", err)
	}
	return nil
}

// FindRunnersPath locates runners.tsv using flag, env, play-dir, or current directory.
func FindRunnersPath(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err == nil {
			return explicit, nil
		}
		return explicit, nil
	}
	if env := os.Getenv("NOVA_FLEET_RUNNERS"); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
	}
	home, err := os.UserHomeDir()
	if err == nil {
		candidate := filepath.Join(home, "fleet", "runners.tsv")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	// Try relative candidates
	for _, c := range []string{filepath.Join("fleet", "runners.tsv"), filepath.Join("..", "fleet", "runners.tsv"), "runners.tsv"} {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return filepath.Join("fleet", "runners.tsv"), nil
}

// RunnersBenchStatus is declared vs registered vs online status for one bench.
type RunnersBenchStatus struct {
	Bench      string
	Declared   int
	Registered int
	Online     int
}

// Line returns the formatted line for one bench:
// RUNNERS bench=<b> declared=<d> registered=<r> online=<o>
func (s RunnersBenchStatus) Line() string {
	return fmt.Sprintf("RUNNERS bench=%s declared=%d registered=%d online=%d",
		s.Bench, s.Declared, s.Registered, s.Online)
}

// HasDrift returns true if declared, registered, or online counts disagree.
func (s RunnersBenchStatus) HasDrift() bool {
	return s.Declared != s.Registered || s.Declared != s.Online
}

// RunnersStatusResult is the fleet-wide runners status.
type RunnersStatusResult struct {
	Benches         []RunnersBenchStatus
	TotalDeclared   int
	TotalRegistered int
	TotalOnline     int
	Drifting        []string
}

// SummaryLine returns the final status line.
func (r RunnersStatusResult) SummaryLine() string {
	if len(r.Drifting) == 0 {
		return fmt.Sprintf("RUNNERS OK total_declared=%d total_registered=%d total_online=%d",
			r.TotalDeclared, r.TotalRegistered, r.TotalOnline)
	}
	return fmt.Sprintf("RUNNERS DRIFT total_declared=%d total_registered=%d total_online=%d drifting=%s",
		r.TotalDeclared, r.TotalRegistered, r.TotalOnline, strings.Join(r.Drifting, ","))
}

// IsRunnerUnit returns true if a systemd/launchd unit name corresponds to a runner.
func IsRunnerUnit(name string) bool {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "runner") &&
		(strings.HasPrefix(lower, "nova-runner-") ||
			strings.HasPrefix(lower, "com.nova.runner-") ||
			strings.Contains(lower, "-runner-") ||
			strings.HasSuffix(lower, ".service") ||
			strings.HasSuffix(lower, ".plist")) {
		return true
	}
	if strings.Contains(lower, "actions.runner") {
		return true
	}
	if strings.Contains(lower, "-nova-") &&
		(strings.HasSuffix(lower, ".service") || strings.HasSuffix(lower, ".plist")) {
		return true
	}
	return false
}

// CountOnlineRunners extracts online runner count from beat's ps sample and ci field.
func CountOnlineRunners(beat map[string]string) int {
	if psField, ok := beat["ps"]; ok && psField != "" {
		sample, err := DecodePS(psField)
		if err == nil && len(sample.Units) > 0 {
			count := 0
			for _, u := range sample.Units {
				if IsRunnerUnit(u.Name) {
					count++
				}
			}
			if count > 0 {
				return count
			}
		}
	}
	if ciField, ok := beat["ci"]; ok && ciField != "" {
		if n, err := strconv.Atoi(ciField); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// GitHubRunnersFetcher fetches registered runners per bench from GitHub Actions API.
type GitHubRunnersFetcher func(ctx context.Context) (map[string]int, error)

type ghRunnerEntry struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	OS     string `json:"os"`
	Status string `json:"status"`
	Busy   bool   `json:"busy"`
}

type ghRunnersResponse struct {
	TotalCount int             `json:"total_count"`
	Runners    []ghRunnerEntry `json:"runners"`
}

// parseRepoFromRemote extracts owner/repo from a git remote URL.
func parseRepoFromRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimSuffix(remote, ".git")
	if idx := strings.Index(remote, "github.com/"); idx >= 0 {
		return strings.Trim(remote[idx+len("github.com/"):], "/")
	}
	if idx := strings.Index(remote, "github.com:"); idx >= 0 {
		return strings.Trim(remote[idx+len("github.com:"):], "/")
	}
	return ""
}

// DefaultGitHubRunnersFetcher queries the GitHub API for repo runners.
func DefaultGitHubRunnersFetcher(repo string) GitHubRunnersFetcher {
	return func(ctx context.Context) (map[string]int, error) {
		token, err := gh.Token()
		if err != nil {
			return nil, fmt.Errorf("get github token: %w", err)
		}
		if repo == "" {
			if r := os.Getenv("GITHUB_REPOSITORY"); r != "" {
				repo = r
			} else if r := os.Getenv("NOVA_REPO"); r != "" {
				repo = r
			} else if out, err := exec.CommandContext(ctx, "git", "config", "--get", "remote.origin.url").Output(); err == nil {
				repo = parseRepoFromRemote(string(out))
			}
		}
		if repo == "" {
			return nil, errors.New("github repository not determined (set GITHUB_REPOSITORY or remote.origin.url)")
		}
		url := fmt.Sprintf("https://api.github.com/repos/%s/actions/runners?per_page=100", repo)
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "nova-sprint-fleet-runners")

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github api request: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("github api returned %s", resp.Status)
		}

		var payload ghRunnersResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil, fmt.Errorf("decode github runners json: %w", err)
		}

		counts := make(map[string]int)
		for _, r := range payload.Runners {
			// Runners are named <bench>-nova-<n>
			name := r.Name
			if idx := strings.Index(name, "-nova-"); idx > 0 {
				bench := name[:idx]
				counts[bench]++
			} else {
				parts := strings.Split(name, "-")
				if len(parts) > 0 {
					counts[parts[0]]++
				}
			}
		}
		return counts, nil
	}
}

// GetRegisteredRunners fetches registered runner counts, using Redis cache if available.
func GetRegisteredRunners(ctx context.Context, c redis.Cmdable, fetcher GitHubRunnersFetcher) (map[string]int, error) {
	// Try Redis cache first
	if c != nil {
		cached, err := c.HGetAll(ctx, RunnersKeyRegistered).Result()
		if err == nil && len(cached) > 0 {
			res := make(map[string]int)
			for k, v := range cached {
				if n, err := strconv.Atoi(v); err == nil {
					res[k] = n
				}
			}
			return res, nil
		}
	}

	if fetcher == nil {
		return map[string]int{}, nil
	}

	// Fetch fresh from GitHub
	fresh, err := fetcher(ctx)
	if err != nil {
		return map[string]int{}, err
	}

	// Cache into Redis if client available
	if c != nil && len(fresh) > 0 {
		fields := make([]any, 0, len(fresh)*2)
		for k, v := range fresh {
			fields = append(fields, k, strconv.Itoa(v))
		}
		_ = c.HSet(ctx, RunnersKeyRegistered, fields...).Err()
		_ = c.Expire(ctx, RunnersKeyRegistered, RunnersCacheTTL).Err()
	}
	return fresh, nil
}

// ReadRunnersStatus reads declared, registered, and online runner status across benches.
func ReadRunnersStatus(ctx context.Context, c redis.Cmdable, runnersPath string, benchFilter string, fetcher GitHubRunnersFetcher) (*RunnersStatusResult, error) {
	// 1. Read declared table
	var table *RunnersTable
	if runnersPath != "" {
		t, err := ReadRunnersFile(runnersPath)
		if err == nil {
			table = t
		}
	}
	if table == nil {
		table = &RunnersTable{RowIndex: make(map[string]int)}
	}

	// 2. Read registered runners from cache or GitHub API
	regMap, _ := GetRegisteredRunners(ctx, c, fetcher)

	// 3. Read benches list and beats from Redis
	var benches []string
	if benchFilter != "" {
		benches = []string{benchFilter}
	} else if c != nil {
		members, err := c.SMembers(ctx, "benches").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("read benches: %w", err)
		}
		if len(members) > 0 {
			benches = members
		}
	}
	// Fall back to table hosts if Redis benches set is empty
	if len(benches) == 0 {
		for _, r := range table.Rows {
			benches = append(benches, r.Host)
		}
	}
	// Ensure table rows are represented
	seen := make(map[string]bool)
	for _, b := range benches {
		seen[b] = true
	}
	if benchFilter == "" {
		for _, r := range table.Rows {
			if !seen[r.Host] {
				seen[r.Host] = true
				benches = append(benches, r.Host)
			}
		}
	}
	sort.Strings(benches)

	// Read beats in pipeline if Redis is available
	beats := make(map[string]map[string]string)
	if c != nil && len(benches) > 0 {
		pipe := c.Pipeline()
		cmds := make([]*redis.MapStringStringCmd, len(benches))
		for i, b := range benches {
			cmds[i] = pipe.HGetAll(ctx, "bench:"+b+":beat")
		}
		_, err := pipe.Exec(ctx)
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("read beats: %w", err)
		}
		for i, b := range benches {
			beats[b] = cmds[i].Val()
		}
	}

	result := &RunnersStatusResult{}
	for _, b := range benches {
		decl := table.Count(b)
		reg := regMap[b]
		online := CountOnlineRunners(beats[b])

		st := RunnersBenchStatus{
			Bench:      b,
			Declared:   decl,
			Registered: reg,
			Online:     online,
		}
		result.Benches = append(result.Benches, st)
		result.TotalDeclared += decl
		result.TotalRegistered += reg
		result.TotalOnline += online
		if st.HasDrift() {
			result.Drifting = append(result.Drifting, b)
		}
	}
	return result, nil
}

// PRCreatorSeam is an injectable seam for git commit/push and PR creation.
type PRCreatorSeam func(ctx context.Context, dir string, branch string, title string, body string) (string, error)

// DefaultPRCreatorSeam performs git checkout, commit, push, and gh pr create.
func DefaultPRCreatorSeam(ctx context.Context, dir string, branch string, title string, body string) (string, error) {
	testguard.RefuseHosts("git", "push", "origin", branch)
	runCmd := func(name string, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		if dir != "" {
			cmd.Dir = dir
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	if err := runCmd("git", "checkout", "-b", branch); err != nil {
		return "", err
	}
	if err := runCmd("git", "add", filepath.Join("fleet", "runners.tsv")); err != nil {
		_ = runCmd("git", "add", "runners.tsv")
	}
	msg := title
	if author := os.Getenv("GIT_AUTHOR_LINE"); author != "" {
		msg += "\n\n" + author
	}
	if err := runCmd("git", "commit", "-m", msg); err != nil {
		return "", err
	}
	if err := runCmd("git", "push", "-u", "origin", branch); err != nil {
		return "", err
	}

	testguard.RefuseHosts("gh", "pr", "create")
	ghCmd := exec.CommandContext(ctx, "gh", "pr", "create", "--base", "dev", "--head", branch, "--title", title, "--body", body)
	if dir != "" {
		ghCmd.Dir = dir
	}
	out, err := ghCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr create: %w: %s", err, strings.TrimSpace(string(out)))
	}
	prURL := strings.TrimSpace(string(out))
	return prURL, nil
}

// SetRunnersRequest configures SetRunners.
type SetRunnersRequest struct {
	Bench       string
	Count       int
	RunnersPath string
	PRCreator   PRCreatorSeam
}

// SetRunnersResult is the result of SetRunners.
type SetRunnersResult struct {
	Bench    string
	Count    int
	OldCount int
	PRURL    string
	PlayCmd  string
}

// SetRunners edits runners.tsv, creates git branch and PR, and returns the play command.
func SetRunners(ctx context.Context, req SetRunnersRequest) (*SetRunnersResult, error) {
	if req.Bench == "" {
		return nil, errors.New("bench name required")
	}
	if req.Count < 0 {
		return nil, fmt.Errorf("runner count must be >= 0, got %d", req.Count)
	}

	path := req.RunnersPath
	if path == "" {
		p, err := FindRunnersPath("")
		if err != nil {
			return nil, err
		}
		path = p
	}

	table, err := ReadRunnersFile(path)
	if err != nil {
		return nil, err
	}

	oldCount := table.SetCount(req.Bench, req.Count)
	if err := table.Save(path); err != nil {
		return nil, err
	}

	playCmd := fmt.Sprintf("nova-sprint fleet play runners --limit %s", req.Bench)

	branch := fmt.Sprintf("fleet/runners-%s-%d-%d", req.Bench, req.Count, time.Now().Unix())
	title := fmt.Sprintf("fleet: set %s runners to %d", req.Bench, req.Count)
	body := fmt.Sprintf("Automated runner count change for %s from %d to %d via nova-sprint fleet runners set.\n\nPlay command: %s",
		req.Bench, oldCount, req.Count, playCmd)

	prCreator := req.PRCreator
	if prCreator == nil {
		prCreator = DefaultPRCreatorSeam
	}

	repoDir := filepath.Dir(filepath.Dir(path))
	prURL, err := prCreator(ctx, repoDir, branch, title, body)
	if err != nil {
		// Even if PR creation had an error, we return the playCmd with the error
		return &SetRunnersResult{
			Bench:    req.Bench,
			Count:    req.Count,
			OldCount: oldCount,
			PlayCmd:  playCmd,
		}, fmt.Errorf("create PR: %w", err)
	}

	return &SetRunnersResult{
		Bench:    req.Bench,
		Count:    req.Count,
		OldCount: oldCount,
		PRURL:    prURL,
		PlayCmd:  playCmd,
	}, nil
}
