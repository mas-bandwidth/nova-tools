package land

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// BasePolicy represents policy declarations for one base branch (§7.4).
type BasePolicy struct {
	Base          string
	RequiredSteps []string
	LandBar       int
	Readers       int
	SecurityPaths []string
	AlonePaths    []string
	SelGOOS       []string
	BuildTags     []string
	MergeStyle    string
	StepDeadline  string
	GateDeadline  string
}

// RepoPolicy represents policy declarations for one repo (§7.4).
type RepoPolicy struct {
	Repo  string
	Bases map[string]*BasePolicy
}

// PolicyID returns the sha256 hash of the canonical base configuration (§2.2).
func (bp *BasePolicy) PolicyID() string {
	steps := append([]string(nil), bp.RequiredSteps...)
	sort.Strings(steps)
	sec := append([]string(nil), bp.SecurityPaths...)
	sort.Strings(sec)
	alone := append([]string(nil), bp.AlonePaths...)
	sort.Strings(alone)
	goos := append([]string(nil), bp.SelGOOS...)
	sort.Strings(goos)
	tags := append([]string(nil), bp.BuildTags...)
	sort.Strings(tags)

	canonical := fmt.Sprintf(
		"base=%s|bar=%d|readers=%d|style=%s|steps=%s|sec=%s|alone=%s|goos=%s|tags=%s|deadlines=%s,%s",
		bp.Base, bp.LandBar, bp.Readers, bp.MergeStyle,
		strings.Join(steps, ","),
		strings.Join(sec, ","),
		strings.Join(alone, ","),
		strings.Join(goos, ","),
		strings.Join(tags, ","),
		bp.StepDeadline, bp.GateDeadline,
	)
	h := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(h[:])
}

// RequiredSetID returns the sha256 hash of the sorted required step names (§2.2).
func (bp *BasePolicy) RequiredSetID() string {
	steps := append([]string(nil), bp.RequiredSteps...)
	sort.Strings(steps)
	canonical := strings.Join(steps, "\n")
	h := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(h[:])
}

// ToolsVersion is the nova-tools build the runner identity names. The
// release build stamps it (-ldflags "-X .../land.ToolsVersion=<tag>");
// unstamped, RunnerID reads the module version and VCS revision from the
// build info, so runner_id tracks the build (§2.2 runner_id).
var ToolsVersion = ""

// RunnerID returns the runner identity string (§2.2, §5.5): nova-tools
// version and go version.
func RunnerID() string {
	return fmt.Sprintf("nova-tools-%s/go-%s", toolsVersion(debug.ReadBuildInfo), runtime.Version())
}

// toolsVersion is ToolsVersion, else the main module version when it is a
// release, else the VCS revision (12 hex, "+dirty" when modified), else "devel".
func toolsVersion(read func() (*debug.BuildInfo, bool)) string {
	if ToolsVersion != "" {
		return ToolsVersion
	}
	info, ok := read()
	if !ok || info == nil {
		return "devel"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "devel"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "+dirty"
	}
	return rev
}

// LoadPolicy parses a YAML policy file without requiring external yaml dependencies.
func LoadPolicy(path string) (*RepoPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rp, err := ParsePolicy(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rp, nil
}

// The policy file's grammar (fleet/land/<repo>.yml): `repo:` and `bases:` at
// the margin, a base name at two spaces, its fields at four, and a field's
// list items or the deadlines' step/gate at six. Every field a base may
// carry is named here, and a line the grammar does not know is an error that
// names the line, the key and the shape wanted: a review policy is never
// silently reinterpreted (Stella's audit stella-e6353bf80360, finding 2).
var (
	policyIntFields  = map[string]bool{"land_bar": true, "readers": true}
	policyListFields = map[string]bool{"required_steps": true, "security_paths": true, "alone_paths": true, "sel_goos": true, "build_tags": true}
	policyBaseFields = "land_bar, readers, merge_style, required_steps, security_paths, alone_paths, sel_goos, build_tags, deadlines"
)

// policyLineError is one line the grammar refuses, with what it wanted.
func policyLineError(n int, line, want string) error {
	return fmt.Errorf("policy line %d: %s: %s", n, strings.TrimSpace(line), want)
}

// ParsePolicy parses yaml string for repo policy. An empty policy, a field
// under no base, an unknown key, a malformed number or duration, a list
// item under no list, a duplicate base or an indent the grammar has no
// level for is an error naming the line; a policy with no repo or no base
// is refused whole.
func ParsePolicy(content string) (*RepoPolicy, error) {
	rp := &RepoPolicy{
		Bases: make(map[string]*BasePolicy),
	}
	scanner := bufio.NewScanner(strings.NewReader(content))
	var currentBase *BasePolicy
	var currentList *[]string
	currentKey, lineNo, declared, seenBases := "", 0, 0, false

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		declared++
		if strings.HasPrefix(line, "\t") {
			return nil, policyLineError(lineNo, line, "indented with a tab; the policy indents with spaces (2, 4, 6)")
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		key, val, hasColon := trimmed, "", false
		if i := strings.Index(trimmed, ":"); i >= 0 {
			key, val, hasColon = strings.TrimSpace(trimmed[:i]), strings.TrimSpace(trimmed[i+1:]), true
		}

		switch indent {
		case 0:
			currentBase, currentList, currentKey = nil, nil, ""
			switch {
			case !hasColon:
				return nil, policyLineError(lineNo, line, "not a key (want repo: <name> or bases:)")
			case key == "repo":
				if val == "" {
					return nil, policyLineError(lineNo, line, "repo: wants the repository name")
				}
				rp.Repo = val
			case key == "bases":
				if val != "" {
					return nil, policyLineError(lineNo, line, "bases: takes no value; the bases follow at two spaces")
				}
				seenBases = true
			default:
				return nil, policyLineError(lineNo, line, fmt.Sprintf("unknown top-level key %q (want repo: and bases:)", key))
			}
		case 2:
			// Base level (2 spaces, e.g. "  dev:")
			if !hasColon || val != "" || key == "" {
				return nil, policyLineError(lineNo, line, "a base is `  <name>:` with nothing after the colon")
			}
			if !seenBases {
				return nil, policyLineError(lineNo, line, "a base before bases:; write bases: first")
			}
			if _, dup := rp.Bases[key]; dup {
				return nil, policyLineError(lineNo, line, fmt.Sprintf("base %s is declared twice", key))
			}
			currentBase = &BasePolicy{
				Base:       key,
				LandBar:    10,
				Readers:    0,
				MergeStyle: "merge",
			}
			rp.Bases[key] = currentBase
			currentList, currentKey = nil, ""
		case 4:
			// Field level under base (4 spaces)
			if currentBase == nil {
				return nil, policyLineError(lineNo, line, "a base field under no base; declare `  <base>:` above it")
			}
			if !hasColon || key == "" {
				return nil, policyLineError(lineNo, line, "a base field is `    <key>: <value>` (keys: "+policyBaseFields+")")
			}
			currentList, currentKey = nil, key
			switch {
			case policyIntFields[key]:
				n, err := strconv.Atoi(val)
				if err != nil || n < 0 {
					return nil, policyLineError(lineNo, line, fmt.Sprintf("field %s: value %q is not a whole number (want %s: <n>, n >= 0)", key, val, key))
				}
				if key == "land_bar" {
					currentBase.LandBar = n
				} else {
					currentBase.Readers = n
				}
			case key == "merge_style":
				if val == "" {
					return nil, policyLineError(lineNo, line, "field merge_style: wants a value (merge, squash or rebase)")
				}
				currentBase.MergeStyle = val
			case policyListFields[key]:
				if val != "" && val != "[]" {
					return nil, policyLineError(lineNo, line, fmt.Sprintf("field %s: is a list; write [] for none, or its items as `      - <item>` lines under it", key))
				}
				switch key {
				case "required_steps":
					currentList = &currentBase.RequiredSteps
				case "security_paths":
					currentList = &currentBase.SecurityPaths
				case "alone_paths":
					currentList = &currentBase.AlonePaths
				case "sel_goos":
					currentList = &currentBase.SelGOOS
				case "build_tags":
					currentList = &currentBase.BuildTags
				}
				if val == "[]" {
					currentList = nil
				}
			case key == "deadlines":
				if val != "" {
					return nil, policyLineError(lineNo, line, "field deadlines: takes no value; step: and gate: follow at six spaces")
				}
			default:
				return nil, policyLineError(lineNo, line, fmt.Sprintf("unknown base field %q (keys: %s)", key, policyBaseFields))
			}
		case 6:
			// List items, or the deadlines' step and gate (6 spaces)
			if currentBase == nil {
				return nil, policyLineError(lineNo, line, "under no base")
			}
			if strings.HasPrefix(trimmed, "- ") {
				if currentList == nil {
					if policyListFields[currentKey] {
						return nil, policyLineError(lineNo, line, fmt.Sprintf("a list item under %s: [], which declared the list empty", currentKey))
					}
					return nil, policyLineError(lineNo, line, fmt.Sprintf("a list item under %q, which is not a list field", currentKey))
				}
				item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				if item == "" {
					return nil, policyLineError(lineNo, line, "an empty list item")
				}
				*currentList = append(*currentList, item)
				continue
			}
			if currentKey != "deadlines" || !hasColon {
				return nil, policyLineError(lineNo, line, fmt.Sprintf("under %q: want `      - <item>` for a list, or step:/gate: under deadlines:", currentKey))
			}
			if _, err := time.ParseDuration(val); err != nil {
				return nil, policyLineError(lineNo, line, fmt.Sprintf("deadline %s: value %q is not a duration (want e.g. %s: 10m)", key, val, key))
			}
			switch key {
			case "step":
				currentBase.StepDeadline = val
			case "gate":
				currentBase.GateDeadline = val
			default:
				return nil, policyLineError(lineNo, line, fmt.Sprintf("unknown deadline %q (want step: and gate:)", key))
			}
		default:
			return nil, policyLineError(lineNo, line, fmt.Sprintf("indent of %d spaces; the policy indents by 2 (base), 4 (field) and 6 (item or deadline)", indent))
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if declared == 0 {
		return nil, fmt.Errorf("policy is empty: wants repo: <name> and bases: with at least one base")
	}
	if rp.Repo == "" {
		return nil, fmt.Errorf("policy names no repo: add `repo: <name>` at the top")
	}
	if len(rp.Bases) == 0 {
		return nil, fmt.Errorf("policy declares no base: add `bases:` and `  <base>:` under it")
	}
	return rp, nil
}

// SyncPolicyToRedis stores base policy to Redis via ns_policy_set.
func SyncPolicyToRedis(ctx context.Context, c *redis.Client, repo string, bp *BasePolicy) error {
	return CallPolicySet(ctx, c, repo, bp.Base, bp.PolicyID(), bp.RequiredSetID(), RunnerID())
}
