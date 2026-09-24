package land

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"

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

// RunnerID returns the runner identity string (§2.2, §5.5).
func RunnerID() string {
	return fmt.Sprintf("nova-tools-v0.12.0/go-%s", runtime.Version())
}

// LoadPolicy parses a YAML policy file without requiring external yaml dependencies.
func LoadPolicy(path string) (*RepoPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePolicy(string(data))
}

// ParsePolicy parses yaml string for repo policy.
func ParsePolicy(content string) (*RepoPolicy, error) {
	rp := &RepoPolicy{
		Bases: make(map[string]*BasePolicy),
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	var currentBase *BasePolicy
	var currentList *[]string

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Check for repo:
		if strings.HasPrefix(trimmed, "repo:") {
			rp.Repo = strings.TrimSpace(strings.TrimPrefix(trimmed, "repo:"))
			currentList = nil
			continue
		}

		if strings.HasPrefix(trimmed, "bases:") {
			currentList = nil
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " "))

		// Base level (2 spaces, e.g. "  dev:")
		if indent == 2 && strings.HasSuffix(trimmed, ":") {
			baseName := strings.TrimSuffix(trimmed, ":")
			currentBase = &BasePolicy{
				Base:       baseName,
				LandBar:    10,
				Readers:    0,
				MergeStyle: "merge",
			}
			rp.Bases[baseName] = currentBase
			currentList = nil
			continue
		}

		if currentBase == nil {
			continue
		}

		// Field level under base (4 spaces)
		if indent == 4 {
			currentList = nil
			parts := strings.SplitN(trimmed, ":", 2)
			key := strings.TrimSpace(parts[0])
			val := ""
			if len(parts) > 1 {
				val = strings.TrimSpace(parts[1])
			}

			switch key {
			case "land_bar":
				if n, err := strconv.Atoi(val); err == nil {
					currentBase.LandBar = n
				}
			case "readers":
				if n, err := strconv.Atoi(val); err == nil {
					currentBase.Readers = n
				}
			case "merge_style":
				currentBase.MergeStyle = val
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
			continue
		}

		// Deadlines or list items under base (6 spaces)
		if indent == 6 {
			if strings.HasPrefix(trimmed, "- ") && currentList != nil {
				item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				*currentList = append(*currentList, item)
				continue
			}
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				k := strings.TrimSpace(parts[0])
				v := strings.TrimSpace(parts[1])
				switch k {
				case "step":
					currentBase.StepDeadline = v
				case "gate":
					currentBase.GateDeadline = v
				}
			}
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rp, nil
}

// SyncPolicyToRedis stores base policy to Redis via ns_policy_set.
func SyncPolicyToRedis(ctx context.Context, c *redis.Client, repo string, bp *BasePolicy) error {
	return CallPolicySet(ctx, c, repo, bp.Base, bp.PolicyID(), bp.RequiredSetID(), RunnerID())
}
