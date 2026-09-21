package pulse

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// KeeperUnitName constants for the four Essential 10 launchd engines.
const (
	KeeperUnitDealer       = "dealer"
	KeeperUnitBackpressure = "backpressure"
	KeeperUnitHarvest      = "harvest"
	KeeperUnitSprint       = "sprint"
)

// AllKeeperUnits returns the canonical list of the four keeper engines.
var AllKeeperUnits = []string{
	KeeperUnitDealer,
	KeeperUnitBackpressure,
	KeeperUnitHarvest,
	KeeperUnitSprint,
}

// KeeperLaunchdConfig holds configuration for generating and managing Darwin launchd units.
type KeeperLaunchdConfig struct {
	HomeDir     string // User HOME directory
	WorkDir     string // Working directory (default: $HOME/rowan-working)
	BinDir      string // Directory where engine scripts/binaries reside (default: $WorkDir/bin)
	LogDir      string // Directory where engine stdout/stderr logs are placed (default: $WorkDir/tmp/keeper-logs)
	LabelPrefix string // Prefix for launchd labels (default: "com.mas-bandwidth.nova-")
	PlistDir    string // Directory where plist files are installed (default: $HOME/Library/LaunchAgents)
	PathEnv     string // PATH environment variable for launchd units
	GHConfigDir string // GH_CONFIG_DIR if set (default: $HOME/.config/gh-rowan)
}

// Resolve fills default paths and values into cfg if unspecified.
func (cfg *KeeperLaunchdConfig) Resolve() {
	if cfg.HomeDir == "" {
		if h, err := os.UserHomeDir(); err == nil && h != "" {
			cfg.HomeDir = h
		} else {
			cfg.HomeDir = os.Getenv("HOME")
		}
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = filepath.Join(cfg.HomeDir, "rowan-working")
	}
	if cfg.BinDir == "" {
		cfg.BinDir = filepath.Join(cfg.WorkDir, "bin")
	}
	if cfg.LogDir == "" {
		cfg.LogDir = filepath.Join(cfg.WorkDir, "tmp", "keeper-logs")
	}
	if cfg.LabelPrefix == "" {
		cfg.LabelPrefix = "com.mas-bandwidth.nova-"
	}
	if cfg.PlistDir == "" {
		cfg.PlistDir = filepath.Join(cfg.HomeDir, "Library", "LaunchAgents")
	}
	if cfg.PathEnv == "" {
		cfg.PathEnv = "/opt/homebrew/bin:/Users/glenn/.local/bin:/usr/local/bin:/usr/bin:/bin"
	}
	if cfg.GHConfigDir == "" {
		cfg.GHConfigDir = filepath.Join(cfg.HomeDir, ".config", "gh-rowan")
	}
}

// KeeperUnit represents one launchd plist unit specification.
type KeeperUnit struct {
	Name                 string            // "dealer", "backpressure", "harvest", "sprint"
	Label                string            // e.g. "com.mas-bandwidth.nova-dealer"
	Description          string            // Engine description
	ProgramArguments     []string          // Command line arguments (e.g. ["/bin/zsh", "-lc", "exec ..."])
	WorkingDirectory     string            // Working directory
	StandardOutPath      string            // stdout log file path
	StandardErrorPath    string            // stderr log file path
	KeepAlive            bool              // must be true
	RunAtLoad            bool              // true
	EnvironmentVariables map[string]string // Environment variables
}

func (u KeeperUnit) PlistFileName() string {
	return u.Label + ".plist"
}

func (u KeeperUnit) PlistPath(dir string) string {
	return filepath.Join(dir, u.PlistFileName())
}

func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// PlistXML formats this unit as an Apple-compliant launchd plist XML document.
func (u KeeperUnit) PlistXML() string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	b.WriteString(fmt.Sprintf("\t<key>Label</key>\n\t<string>%s</string>\n", xmlEscape(u.Label)))
	if len(u.ProgramArguments) > 0 {
		b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
		for _, arg := range u.ProgramArguments {
			b.WriteString(fmt.Sprintf("\t\t<string>%s</string>\n", xmlEscape(arg)))
		}
		b.WriteString("\t</array>\n")
	}
	if u.WorkingDirectory != "" {
		b.WriteString(fmt.Sprintf("\t<key>WorkingDirectory</key>\n\t<string>%s</string>\n", xmlEscape(u.WorkingDirectory)))
	}
	if u.StandardOutPath != "" {
		b.WriteString(fmt.Sprintf("\t<key>StandardOutPath</key>\n\t<string>%s</string>\n", xmlEscape(u.StandardOutPath)))
	}
	if u.StandardErrorPath != "" {
		b.WriteString(fmt.Sprintf("\t<key>StandardErrorPath</key>\n\t<string>%s</string>\n", xmlEscape(u.StandardErrorPath)))
	}
	if u.RunAtLoad {
		b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	}
	if u.KeepAlive {
		b.WriteString("\t<key>KeepAlive</key>\n\t<true/>\n")
	}
	if len(u.EnvironmentVariables) > 0 {
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		keys := make([]string, 0, len(u.EnvironmentVariables))
		for k := range u.EnvironmentVariables {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := u.EnvironmentVariables[k]
			if val != "" {
				b.WriteString(fmt.Sprintf("\t\t<key>%s</key>\n\t\t<string>%s</string>\n", xmlEscape(k), xmlEscape(val)))
			}
		}
		b.WriteString("\t</dict>\n")
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// DefaultKeeperUnits returns the launchd unit configurations for dealer, backpressure, harvest, and sprint.
func DefaultKeeperUnits(cfg KeeperLaunchdConfig) []KeeperUnit {
	cfg.Resolve()
	env := map[string]string{
		"PATH": cfg.PathEnv,
		"HOME": cfg.HomeDir,
	}
	if cfg.GHConfigDir != "" {
		env["GH_CONFIG_DIR"] = cfg.GHConfigDir
	}

	return []KeeperUnit{
		{
			Name:        KeeperUnitDealer,
			Label:       cfg.LabelPrefix + KeeperUnitDealer,
			Description: "nova keeper dealer engine: moves cards from ready queues into per-bench queues",
			ProgramArguments: []string{
				"/bin/zsh",
				"-lc",
				fmt.Sprintf("exec %s/card-dealer", cfg.BinDir),
			},
			WorkingDirectory:     cfg.WorkDir,
			StandardOutPath:      filepath.Join(cfg.LogDir, "dealer.log"),
			StandardErrorPath:    filepath.Join(cfg.LogDir, "dealer.log"),
			KeepAlive:            true,
			RunAtLoad:            true,
			EnvironmentVariables: env,
		},
		{
			Name:        KeeperUnitBackpressure,
			Label:       cfg.LabelPrefix + KeeperUnitBackpressure,
			Description: "nova keeper backpressure engine: throttles bulk card launches to match reading debt",
			ProgramArguments: []string{
				"/bin/zsh",
				"-lc",
				fmt.Sprintf("exec %s/backpressure --cap 40", cfg.BinDir),
			},
			WorkingDirectory:     cfg.WorkDir,
			StandardOutPath:      filepath.Join(cfg.LogDir, "backpressure.log"),
			StandardErrorPath:    filepath.Join(cfg.LogDir, "backpressure.log"),
			KeepAlive:            true,
			RunAtLoad:            true,
			EnvironmentVariables: env,
		},
		{
			Name:        KeeperUnitHarvest,
			Label:       cfg.LabelPrefix + KeeperUnitHarvest,
			Description: "nova keeper harvest engine: folds finished cards into pushed branches and PRs",
			ProgramArguments: []string{
				"/bin/zsh",
				"-lc",
				fmt.Sprintf("exec %s/harvest-priority --loop 240", cfg.BinDir),
			},
			WorkingDirectory:     cfg.WorkDir,
			StandardOutPath:      filepath.Join(cfg.LogDir, "harvest.log"),
			StandardErrorPath:    filepath.Join(cfg.LogDir, "harvest.log"),
			KeepAlive:            true,
			RunAtLoad:            true,
			EnvironmentVariables: env,
		},
		{
			Name:        KeeperUnitSprint,
			Label:       cfg.LabelPrefix + KeeperUnitSprint,
			Description: "nova keeper sprint engine: monitors fleet capacity, throughput and sprint tables",
			ProgramArguments: []string{
				"/bin/zsh",
				"-lc",
				fmt.Sprintf("exec %s/sprint-table 10", cfg.BinDir),
			},
			WorkingDirectory:     cfg.WorkDir,
			StandardOutPath:      filepath.Join(cfg.LogDir, "sprint.log"),
			StandardErrorPath:    filepath.Join(cfg.LogDir, "sprint.log"),
			KeepAlive:            true,
			RunAtLoad:            true,
			EnvironmentVariables: env,
		},
	}
}

// KeeperUnitByName returns the unit with the matching name.
func KeeperUnitByName(cfg KeeperLaunchdConfig, name string) (KeeperUnit, error) {
	for _, u := range DefaultKeeperUnits(cfg) {
		if u.Name == name {
			return u, nil
		}
	}
	return KeeperUnit{}, fmt.Errorf("unknown unit %q (valid units: %s)", name, strings.Join(AllKeeperUnits, ", "))
}

// LaunchctlRunner executes launchctl operations.
type LaunchctlRunner interface {
	Load(ctx context.Context, plistPath string) (string, error)
	Unload(ctx context.Context, plistPath string) (string, error)
	List(ctx context.Context, label string) (string, error)
}

// ExecLaunchctlRunner is the production runner invoking Darwin's /bin/launchctl.
type ExecLaunchctlRunner struct {
	Program string
}

func (r ExecLaunchctlRunner) prog() string {
	if r.Program != "" {
		return r.Program
	}
	return "launchctl"
}

func (r ExecLaunchctlRunner) Load(ctx context.Context, plistPath string) (string, error) {
	cmd := exec.CommandContext(ctx, r.prog(), "load", "-w", plistPath)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (r ExecLaunchctlRunner) Unload(ctx context.Context, plistPath string) (string, error) {
	cmd := exec.CommandContext(ctx, r.prog(), "unload", plistPath)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (r ExecLaunchctlRunner) List(ctx context.Context, label string) (string, error) {
	cmd := exec.CommandContext(ctx, r.prog(), "list", label)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// FleetKeeperInput is everything `fleet keeper` needs apart from flag parsing.
type FleetKeeperInput struct {
	Action string // "inspect", "install", "status", "unload", "generate"
	Unit   string // "dealer", "backpressure", "harvest", "sprint", "all"
	Config KeeperLaunchdConfig
	Runner LaunchctlRunner
	DryRun bool
	Stdout io.Writer
	Stderr io.Writer
}

var (
	pidRe      = regexp.MustCompile(`"PID"\s*=\s*([0-9]+)`)
	lastExitRe = regexp.MustCompile(`"LastExitStatus"\s*=\s*([0-9]+)`)
)

// FleetKeeper executes the keeper action across target units.
func FleetKeeper(input FleetKeeperInput) int {
	input.Config.Resolve()

	var targets []KeeperUnit
	unitChoice := strings.TrimSpace(input.Unit)
	if unitChoice == "" || unitChoice == "all" {
		targets = DefaultKeeperUnits(input.Config)
	} else {
		u, err := KeeperUnitByName(input.Config, unitChoice)
		if err != nil {
			fmt.Fprintf(input.Stderr, "nova-pulse fleet keeper: unknown unit %q (the valid units are dealer, backpressure, harvest, sprint, all)\n", unitChoice)
			return 2
		}
		targets = []KeeperUnit{u}
	}

	action := strings.ToLower(strings.TrimSpace(input.Action))
	switch action {
	case "inspect", "generate", "install", "status", "unload":
	default:
		fmt.Fprintf(input.Stderr, "nova-pulse fleet keeper: unknown action %q (valid actions are inspect, install, status, unload, generate)\n", input.Action)
		return 2
	}

	runner := input.Runner
	if runner == nil {
		runner = ExecLaunchctlRunner{}
	}
	ctx := context.Background()

	switch action {
	case "inspect":
		for i, u := range targets {
			if i > 0 {
				fmt.Fprintln(input.Stdout)
			}
			fmt.Fprint(input.Stdout, u.PlistXML())
		}
		return 0

	case "generate":
		if err := os.MkdirAll(input.Config.PlistDir, 0o755); err != nil {
			fmt.Fprintf(input.Stderr, "nova-pulse fleet keeper: cannot create directory %s: %s\n", input.Config.PlistDir, oneline.Err(err))
			return 1
		}
		for _, u := range targets {
			plistPath := u.PlistPath(input.Config.PlistDir)
			if !input.DryRun {
				if err := os.WriteFile(plistPath, []byte(u.PlistXML()), 0o644); err != nil {
					fmt.Fprintf(input.Stderr, "nova-pulse fleet keeper: cannot write %s: %s\n", plistPath, oneline.Err(err))
					return 1
				}
			}
			fmt.Fprintf(input.Stdout, "KEEPER GENERATE unit=%s label=%s file=%s\n", u.Name, u.Label, plistPath)
		}
		return 0

	case "install":
		if err := os.MkdirAll(input.Config.PlistDir, 0o755); err != nil {
			fmt.Fprintf(input.Stderr, "nova-pulse fleet keeper: cannot create plist directory %s: %s\n", input.Config.PlistDir, oneline.Err(err))
			return 1
		}
		if err := os.MkdirAll(input.Config.LogDir, 0o755); err != nil {
			fmt.Fprintf(input.Stderr, "nova-pulse fleet keeper: cannot create log directory %s: %s\n", input.Config.LogDir, oneline.Err(err))
			return 1
		}
		for _, u := range targets {
			plistPath := u.PlistPath(input.Config.PlistDir)
			if !input.DryRun {
				if err := os.WriteFile(plistPath, []byte(u.PlistXML()), 0o644); err != nil {
					fmt.Fprintf(input.Stderr, "nova-pulse fleet keeper: cannot write %s: %s\n", plistPath, oneline.Err(err))
					return 1
				}
				if _, err := runner.Load(ctx, plistPath); err != nil {
					fmt.Fprintf(input.Stderr, "nova-pulse fleet keeper: launchctl load failed for %s (%s): %s\n", u.Name, plistPath, oneline.Err(err))
					return 1
				}
			}
			fmt.Fprintf(input.Stdout, "KEEPER INSTALL unit=%s label=%s file=%s status=LOADED\n", u.Name, u.Label, plistPath)
		}
		return 0

	case "status":
		for _, u := range targets {
			plistPath := u.PlistPath(input.Config.PlistDir)
			fileState := "MISSING"
			if _, err := os.Stat(plistPath); err == nil {
				fileState = "EXISTS"
			}

			state := "UNLOADED"
			extra := ""
			out, err := runner.List(ctx, u.Label)
			if err == nil {
				state = "ACTIVE"
				if m := pidRe.FindStringSubmatch(out); len(m) > 1 {
					extra += fmt.Sprintf(" pid=%s", m[1])
				}
				if m := lastExitRe.FindStringSubmatch(out); len(m) > 1 {
					extra += fmt.Sprintf(" exit=%s", m[1])
				}
			}
			fmt.Fprintf(input.Stdout, "KEEPER STATUS unit=%s label=%s state=%s file=%s%s\n", u.Name, u.Label, state, fileState, extra)
		}
		return 0

	case "unload":
		for _, u := range targets {
			plistPath := u.PlistPath(input.Config.PlistDir)
			if !input.DryRun {
				if _, err := runner.Unload(ctx, plistPath); err != nil {
					fmt.Fprintf(input.Stderr, "nova-pulse fleet keeper: launchctl unload failed for %s (%s): %s\n", u.Name, plistPath, oneline.Err(err))
					return 1
				}
			}
			fmt.Fprintf(input.Stdout, "KEEPER UNLOAD unit=%s label=%s file=%s status=UNLOADED\n", u.Name, u.Label, plistPath)
		}
		return 0
	}

	return 0
}
