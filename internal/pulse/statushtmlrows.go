package pulse

// The rows that come from the forge, the flags that shape them, and the host the verb runs
// on. Each one is a gap the adoption attempt found: the page the fleet reads has a merge
// queue, a tip with its CI run, a Studio row and a loops line, and the verb that replaces
// it must have them too.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ghLimit is what a list verb asks for. gh's own default is THIRTY, which is how the page
// said 30 merged where the script said 273; worse than a wrong number, a capped count
// flatlines the series for the rest of the day and reads as a steady fleet.
const ghLimit = "500"

// ghEnv is the environment a gh child runs in. gh answers as whoever GH_CONFIG_DIR says,
// so a verb that drops it runs as somebody else, or as nobody. The flag OVERRIDES and
// never invents: with no --gh-config the caller's own environment goes through untouched.
func ghEnv(base []string, ghConfig string) []string {
	if strings.TrimSpace(ghConfig) == "" {
		return base
	}
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		if strings.HasPrefix(kv, "GH_CONFIG_DIR=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GH_CONFIG_DIR="+ghConfig)
}

// readGhPRs reads the pull requests the page counts. PRs only: the page shows no issue
// count, so the second round trip the eight-line report makes for issues is bought for
// nothing here, and it was half this verb's gh time.
func readGhPRs(repo string, timeout time.Duration, env []string) []ghPR {
	var prs []ghPR
	out := runGhEnv(timeout, env, "pr", "list", "-R", repo, "--state", "all",
		"--limit", ghLimit, "--json", "number,title,createdAt,mergedAt")
	if out != "" {
		_ = json.Unmarshal([]byte(out), &prs)
	}
	return prs
}

// mergeQueueCounts asks the forge for the branch's merge queue. The entries' states are
// GitHub's: one entry has the checks running, the rest are queued behind it, and an
// unmergeable entry is the one holding everybody up.
func mergeQueueCounts(repo, branch string, timeout time.Duration, env []string) (mergeQueueCount, bool) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return mergeQueueCount{}, false
	}
	query := fmt.Sprintf(`{repository(owner:%q,name:%q){mergeQueue(branch:%q){entries(first:50){nodes{pullRequest{number} state}}}}}`,
		owner, name, branch)
	out := runGhEnv(timeout, env, "api", "graphql", "-f", "query="+query)
	if out == "" {
		return mergeQueueCount{}, false
	}
	var answer struct {
		Data struct {
			Repository struct {
				MergeQueue struct {
					Entries struct {
						Nodes []struct {
							State string `json:"state"`
						} `json:"nodes"`
					} `json:"entries"`
				} `json:"mergeQueue"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &answer); err != nil {
		return mergeQueueCount{}, false
	}
	var c mergeQueueCount
	for _, n := range answer.Data.Repository.MergeQueue.Entries.Nodes {
		c.total++
		switch {
		case strings.HasPrefix(n.State, "AWAITING"):
			c.running++
		case n.State == "QUEUED":
			c.waiting++
		case n.State == "UNMERGEABLE":
			c.unmergeable++
		}
	}
	return c, true
}

// branchTip is the commit the fleet is building on and the verdict of its CI run. A red tip
// is the one fact that makes every other number on the page beside the point.
func branchTip(repo, branch string, timeout time.Duration, env []string) (sha, run string, ok bool) {
	out := runGhEnv(timeout, env, "api", "repos/"+repo+"/commits/"+branch)
	if out == "" {
		return "", "", false
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal([]byte(out), &commit); err != nil || commit.SHA == "" {
		return "", "", false
	}
	sha = commit.SHA
	if len(sha) > 8 {
		sha = sha[:8]
	}
	run = "unknown"
	if raw := runGhEnv(timeout, env, "run", "list", "-R", repo, "--branch", branch,
		"--workflow", "ci.yml", "--limit", "1", "--json", "status,conclusion"); raw != "" {
		var runs []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		}
		if err := json.Unmarshal([]byte(raw), &runs); err == nil && len(runs) > 0 {
			run = strings.TrimSpace(runs[0].Status + " " + runs[0].Conclusion)
		}
	}
	return sha, run, true
}

// parseDayStart reads the HH:MMZ the day's merged counter resets at.
func parseDayStart(s string) (hour, min int, err error) {
	t := strings.TrimSpace(s)
	if !strings.HasSuffix(t, "Z") {
		return 0, 0, fmt.Errorf("it must end in Z, because the boundary is UTC and a local one would move twice a year")
	}
	t = strings.TrimSuffix(t, "Z")
	h, m, ok := strings.Cut(t, ":")
	if !ok {
		return 0, 0, fmt.Errorf("it wants HH:MMZ")
	}
	hour, err = strconv.Atoi(h)
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("the hour is not 00 to 23")
	}
	min, err = strconv.Atoi(m)
	if err != nil || min < 0 || min > 59 {
		return 0, 0, fmt.Errorf("the minute is not 00 to 59")
	}
	return hour, min, nil
}

// dayBoundary is the most recent reset at or before now. Before the reset hour the window
// is still yesterday's, which is the whole point: at 01:00Z the day that matters started
// twenty-three hours ago.
func dayBoundary(now time.Time, hour, min int) time.Time {
	u := now.UTC()
	b := time.Date(u.Year(), u.Month(), u.Day(), hour, min, 0, 0, time.UTC)
	if b.After(u) {
		b = b.AddDate(0, 0, -1)
	}
	return b
}

// parseLoops reads the --loop pairs. A pattern with no label is a count nobody can read.
func parseLoops(loops []string) ([]LoopCount, error) {
	var out []LoopCount
	seen := map[string]bool{}
	for _, raw := range loops {
		label, pattern, ok := strings.Cut(raw, "=")
		label, pattern = strings.TrimSpace(label), strings.TrimSpace(pattern)
		if !ok || label == "" || pattern == "" {
			return nil, fmt.Errorf("%s is not <label>=<pattern>", oneline.Field(raw))
		}
		if seen[label] {
			return nil, fmt.Errorf("the label %s is used twice", oneline.Field(label))
		}
		seen[label] = true
		out = append(out, LoopCount{Label: label})
	}
	return out, nil
}

// splitPublish reads the host:dir the page is shipped to.
func splitPublish(dest string) (host, dir string, err error) {
	host, dir, ok := strings.Cut(strings.TrimSpace(dest), ":")
	host, dir = strings.TrimSpace(host), strings.TrimSpace(dir)
	if !ok || host == "" || dir == "" {
		return "", "", fmt.Errorf("it is not <host>:<dir>")
	}
	if strings.Contains(dir, "..") {
		return "", "", fmt.Errorf("the directory holds \"..\"")
	}
	return host, dir, nil
}

// ParseTimeout reads a bound on a child, for the flag layer. A bare number is seconds,
// because that is what every caller of this tool has typed for a year; anything else is a
// Go duration, because the progress line prints one and a flag that will not accept what
// the tool prints is a trap. There is no default here and no zero: a bound of nothing is
// not a bound.
func ParseTimeout(s string) (time.Duration, error) { return parseTimeout(s) }

func parseTimeout(s string) (time.Duration, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, fmt.Errorf("it wants a bound, as in 120, 90s or 2m")
	}
	if n, err := strconv.Atoi(t); err == nil {
		if n < 1 {
			return 0, fmt.Errorf("a bound of %d is not a bound; it wants one second or more", n)
		}
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(t)
	if err != nil {
		return 0, fmt.Errorf("%s is neither a whole number of seconds nor a duration like 90s or 2m", oneline.Field(t))
	}
	if d < time.Second {
		return 0, fmt.Errorf("%s is under a second; it wants one second or more", oneline.Field(t))
	}
	return d, nil
}

// readSelf reads the host running the verb. It is the production SelfReader and is never
// called from a test: the readers are injected precisely so no test runs ps.
//
// The columns are the host's own. A coordination bench runs CI runners rather than cards,
// and what kills it is orphans -- the detached tars, shells and harnesses nobody waits on.
// The Studio drowned at load 147 on 2026-09-17 with nineteen of them, and the page that
// showed four Linux benches idling never said so.
func readSelf(name string, loops []string, now time.Time) SelfReading {
	out := SelfReading{Name: name, Cores: runtime.NumCPU()}
	wanted, err := parseLoops(loops)
	if err != nil {
		wanted = nil
	}
	ps, perr := hostProcesses()
	if perr != nil {
		// No process list is no reading: the row says UNKNOWN rather than claiming a quiet
		// zero runners and zero orphans on a host nobody could see.
		out.Unknown = true
		return out
	}
	for i := range wanted {
		pattern := loopPattern(loops, wanted[i].Label)
		for _, p := range ps {
			if strings.Contains(p.command, pattern) {
				wanted[i].N++
			}
		}
	}
	out.Loops = wanted
	for _, p := range ps {
		if strings.Contains(p.command, "Runner.Worker") {
			out.CIRunners++
		}
		if p.ppid == 1 && p.longLived && isOrphanKind(p.command) {
			out.Orphans++
		}
	}
	out.Load = hostLoad()
	out.FreeGB = hostFreeGB()
	return out
}

// loopPattern finds the pattern a label was given, so the count matches what the caller
// named rather than the label they named it.
func loopPattern(loops []string, label string) string {
	for _, raw := range loops {
		l, p, ok := strings.Cut(raw, "=")
		if ok && strings.TrimSpace(l) == label {
			return strings.TrimSpace(p)
		}
	}
	return label
}

// hostProcess is one line of the host's process table, reduced to what the row reads.
type hostProcess struct {
	ppid      int
	longLived bool
	command   string
}

// isOrphanKind names the reparented processes that pile up here: a detached archive, a
// shell nobody is typing into, a harness whose parent is gone.
func isOrphanKind(command string) bool {
	for _, kind := range []string{"tar", "zsh", "bash", "opencode", "claude"} {
		if strings.Contains(command, kind) {
			return true
		}
	}
	return false
}

// hostProcesses reads the process table. On a host with no ps -- Windows -- there is no
// reading and the row says so.
func hostProcesses() ([]hostProcess, error) {
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("no ps on this host")
	}
	raw, err := exec.Command("ps", "-axo", "ppid=,etime=,command=").Output()
	if err != nil {
		return nil, err
	}
	var out []hostProcess
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		ppid, cerr := strconv.Atoi(fields[0])
		if cerr != nil {
			continue
		}
		// etime is [[dd-]hh:]mm:ss, so a dash or two colons is an hour or more.
		et := fields[1]
		out = append(out, hostProcess{
			ppid:      ppid,
			longLived: strings.Contains(et, "-") || strings.Count(et, ":") >= 2,
			command:   strings.Join(fields[2:], " "),
		})
	}
	return out, nil
}

// hostLoad is the one-minute load average, whole.
func hostLoad() int {
	if raw, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(raw)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				return int(v)
			}
		}
	}
	if runtime.GOOS == "darwin" {
		if raw, err := exec.Command("sysctl", "-n", "vm.loadavg").Output(); err == nil {
			// "{ 1.23 4.56 7.89 }"
			if f := strings.Fields(string(raw)); len(f) > 1 {
				if v, err := strconv.ParseFloat(f[1], 64); err == nil {
					return int(v)
				}
			}
		}
	}
	return 0
}

// hostFreeGB is the free space on the host's home, in whole gigabytes. df -k is the one
// spelling both darwin and linux answer the same way.
func hostFreeGB() int {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return 0
	}
	raw, derr := exec.Command("df", "-k", home).Output()
	if derr != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		return 0
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 4 {
		return 0
	}
	kb, cerr := strconv.Atoi(f[3])
	if cerr != nil {
		return 0
	}
	return kb / (1024 * 1024)
}

// publishOverSSH ships the page and its series to where they are served. It is one ssh
// child writing both files through `bash -s`, the same door readFleetOverSSH uses, so
// nothing new has to be trusted and no shell script enters this repo.
func publishOverSSH(ssh, dest string, files []PublishFile, timeout time.Duration) error {
	host, dir, err := splitPublish(dest)
	if err != nil {
		return err
	}
	const delim = "NOVA_STATUS_PAGE_EOF"
	var b strings.Builder
	b.WriteString("set -e\nmkdir -p " + fleetQuote(dir) + "\n")
	for _, f := range files {
		if f.Name == "" || strings.ContainsAny(f.Name, "/\\") || f.Name == ".." {
			return fmt.Errorf("%s is not a plain file name", oneline.Field(f.Name))
		}
		if strings.Contains(string(f.Body), delim) {
			// The page is generated here, so this cannot happen without something else
			// having gone wrong first -- which is exactly when a heredoc must refuse.
			return fmt.Errorf("%s carries the delimiter this ships with", oneline.Field(f.Name))
		}
		b.WriteString("cat > " + fleetQuote(path.Join(dir, f.Name)) + " <<'" + delim + "'\n")
		b.Write([]byte(f.Body))
		if len(f.Body) > 0 && f.Body[len(f.Body)-1] != '\n' {
			b.WriteString("\n")
		}
		b.WriteString(delim + "\n")
	}
	ctx, cancel := contextWithTimeout(timeout)
	defer cancel()
	out, serr := fleetSSH(ctx, ssh, host, b.String())
	if serr != nil {
		return fmt.Errorf("%s: %s", oneline.Err(serr), oneline.Field(oneline.Cap(strings.TrimSpace(out), 200)))
	}
	return nil
}
