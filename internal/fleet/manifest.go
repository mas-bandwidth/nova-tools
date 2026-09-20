package fleet

import (
	"fmt"
	"regexp"
	"strings"
)

// StandardGoVersion is the pinned Go toolchain across all fleet nodes for this sprint (Issue #2053).
const StandardGoVersion = "go1.26.6"

// ToolRequirement defines the expected version pattern and necessity of a tool on a node.
type ToolRequirement struct {
	Name     string `json:"name"`
	Pattern  string `json:"pattern"`  // regex pattern or "-" for any version
	Required bool   `json:"required"` // strictly required on this node
}

// PathRequirement defines a required directory or file on a node.
type PathRequirement struct {
	Path     string `json:"path"`     // relative to HOME or absolute
	Mode     string `json:"mode"`     // expected mode e.g. "1777" or "0777", or "" for any
	IsDir    bool   `json:"is_dir"`   // whether path must be a directory
	Required bool   `json:"required"` // whether strictly required
}

// NodeManifest defines the expected tools, paths, seat, and settings for a node.
type NodeManifest struct {
	NodeName  string                     `json:"node_name"`
	OS        string                     `json:"os"`
	Arch      string                     `json:"arch"`
	Roles     []string                   `json:"roles"`
	Seat      string                     `json:"seat"`
	User      string                     `json:"user"`
	GoVersion string                     `json:"go_version"`
	MinFreeGB int                        `json:"min_free_gb"`
	Tools     map[string]ToolRequirement `json:"tools"`
	Paths     []PathRequirement          `json:"paths"`
}

// StandardBenchTools is the complete 16-tool manifest required on all bench nodes (Issue #2053).
var StandardBenchTools = map[string]ToolRequirement{
	"go":      {Name: "go", Pattern: StandardGoVersion, Required: true},
	"sbcl":    {Name: "sbcl", Pattern: "-", Required: true},
	"cargo":   {Name: "cargo", Pattern: `1\.98\.1`, Required: true},
	"rustc":   {Name: "rustc", Pattern: `1\.98\.1`, Required: true},
	"dotnet":  {Name: "dotnet", Pattern: `^10\.0\.`, Required: true},
	"elixir":  {Name: "elixir", Pattern: `1\.20\.4`, Required: true},
	"erl":     {Name: "erl", Pattern: `^29$`, Required: true},
	"node":    {Name: "node", Pattern: `v26\.`, Required: true},
	"javac":   {Name: "javac", Pattern: `^javac 21`, Required: true},
	"dart":    {Name: "dart", Pattern: `3\.13\.2`, Required: true},
	"cc":      {Name: "cc", Pattern: "-", Required: true},
	"c++":     {Name: "c++", Pattern: "-", Required: true},
	"make":    {Name: "make", Pattern: "-", Required: true},
	"cmake":   {Name: "cmake", Pattern: "-", Required: true},
	"git":     {Name: "git", Pattern: "-", Required: true},
	"sqlite3": {Name: "sqlite3", Pattern: "-", Required: true},
}

// StandardRunnerTools is the tool manifest expected on CI runner nodes.
var StandardRunnerTools = map[string]ToolRequirement{
	"go":   {Name: "go", Pattern: StandardGoVersion, Required: true},
	"git":  {Name: "git", Pattern: "-", Required: true},
	"make": {Name: "make", Pattern: "-", Required: true},
	"cc":   {Name: "cc", Pattern: "-", Required: true},
	"c++":  {Name: "c++", Pattern: "-", Required: true},
}

// DefaultManifestForNode constructs the canonical expected manifest for a machine from its registry record.
func DefaultManifestForNode(m Machine) NodeManifest {
	manifest := NodeManifest{
		NodeName:  m.Name,
		OS:        m.OS,
		Arch:      m.Arch,
		Roles:     append([]string(nil), m.Roles...),
		Seat:      m.Seat,
		GoVersion: StandardGoVersion,
		MinFreeGB: 25,
		Tools:     make(map[string]ToolRequirement),
		Paths:     nil,
	}

	// Determine tool requirements based on roles
	if m.HasRole(RoleBench) {
		for k, v := range StandardBenchTools {
			manifest.Tools[k] = v
		}
	} else if m.HasRole(RoleRunner) || m.HasRole(RoleCoordination) {
		for k, v := range StandardRunnerTools {
			manifest.Tools[k] = v
		}
		if m.HasRole(RoleCoordination) {
			manifest.Tools["sbcl"] = ToolRequirement{Name: "sbcl", Pattern: "-", Required: true}
		}
	}

	// Determine path requirements based on OS and roles
	if m.OS == "linux" {
		manifest.Paths = append(manifest.Paths,
			PathRequirement{Path: "sdk", IsDir: true, Required: true},
			PathRequirement{Path: "go/pkg/mod", IsDir: true, Required: true},
			PathRequirement{Path: "sdk/env.sh", IsDir: false, Required: true},
			PathRequirement{Path: "/tmp/.dotnet", Mode: "1777", IsDir: true, Required: m.HasRole(RoleBench)},
		)
		if m.Seat != "" {
			manifest.Paths = append(manifest.Paths,
				PathRequirement{Path: ".config/nova-secrets/" + m.Seat + ".key", IsDir: false, Required: true},
			)
		}
	} else if m.OS == "darwin" {
		manifest.Paths = append(manifest.Paths,
			PathRequirement{Path: "sdk", IsDir: true, Required: false},
			PathRequirement{Path: "go/pkg/mod", IsDir: true, Required: false},
			PathRequirement{Path: "/opt/homebrew/Cellar/go", IsDir: true, Required: false},
			PathRequirement{Path: "/opt/homebrew/Cellar/sbcl", IsDir: true, Required: false},
			PathRequirement{Path: "/opt/homebrew/opt/openjdk", IsDir: true, Required: false},
			PathRequirement{Path: "/Library/Java/JavaVirtualMachines", IsDir: true, Required: false},
			PathRequirement{Path: "/usr/local/share/dotnet", IsDir: true, Required: false},
		)
		if m.Seat != "" {
			manifest.Paths = append(manifest.Paths,
				PathRequirement{Path: ".config/nova-secrets/" + m.Seat + ".key", IsDir: false, Required: true},
			)
		}
	}

	return manifest
}

// FixManifestDiscrepancies compares a node's manifest against the standard configuration for its machine record,
// repairs any missing tools or paths, corrects Go versions and seats, and returns a list of fixed discrepancy descriptions.
func FixManifestDiscrepancies(m Machine, manifest *NodeManifest) []string {
	var repairs []string
	canonical := DefaultManifestForNode(m)

	if manifest.GoVersion != StandardGoVersion {
		repairs = append(repairs, fmt.Sprintf("updated go version from %q to %q", manifest.GoVersion, StandardGoVersion))
		manifest.GoVersion = StandardGoVersion
	}

	if manifest.Seat != m.Seat && m.Seat != "" {
		repairs = append(repairs, fmt.Sprintf("aligned seat from %q to registry seat %q", manifest.Seat, m.Seat))
		manifest.Seat = m.Seat
	}

	if manifest.MinFreeGB < 25 {
		repairs = append(repairs, fmt.Sprintf("adjusted min free disk from %d GB to required 25 GB", manifest.MinFreeGB))
		manifest.MinFreeGB = 25
	}

	if manifest.Tools == nil {
		manifest.Tools = make(map[string]ToolRequirement)
	}

	// Check missing tools
	for toolName, req := range canonical.Tools {
		existing, ok := manifest.Tools[toolName]
		if !ok {
			manifest.Tools[toolName] = req
			repairs = append(repairs, fmt.Sprintf("added missing tool configuration: %s (pattern=%s)", toolName, req.Pattern))
		} else if req.Required && !existing.Required {
			existing.Required = true
			manifest.Tools[toolName] = existing
			repairs = append(repairs, fmt.Sprintf("marked tool %s as required", toolName))
		} else if toolName == "go" && existing.Pattern != StandardGoVersion {
			existing.Pattern = StandardGoVersion
			manifest.Tools[toolName] = existing
			repairs = append(repairs, fmt.Sprintf("fixed tool go pattern from %q to %q", existing.Pattern, StandardGoVersion))
		}
	}

	// Check missing paths
	existingPaths := make(map[string]PathRequirement)
	for _, p := range manifest.Paths {
		existingPaths[p.Path] = p
	}

	for _, reqPath := range canonical.Paths {
		_, ok := existingPaths[reqPath.Path]
		if !ok {
			manifest.Paths = append(manifest.Paths, reqPath)
			existingPaths[reqPath.Path] = reqPath
			repairs = append(repairs, fmt.Sprintf("added missing path configuration: %s", reqPath.Path))
		}
	}

	return repairs
}

// NodeState is the observed state of a node during inspection.
type NodeState struct {
	Name          string            `json:"name"`
	OS            string            `json:"os"`
	Arch          string            `json:"arch"`
	Status        string            `json:"status"` // "OK", "DRIFT", or "DOWN"
	ToolVersions  map[string]string `json:"tool_versions"`
	ToolPaths     map[string]string `json:"tool_paths"`
	PathsPresent  map[string]bool   `json:"paths_present"`
	PathModes     map[string]string `json:"path_modes"`
	FreeGB        int               `json:"free_gb"`
	SeatKeys      []string          `json:"seat_keys"`
	ShadowedTools map[string]string `json:"shadowed_tools"` // tool -> path shadowing sdk tool
	Error         string            `json:"error,omitempty"`
}

// NodeResult is the validated verdict for a node.
type NodeResult struct {
	Name          string   `json:"name"`
	OS            string   `json:"os"`
	Arch          string   `json:"arch"`
	Roles         string   `json:"roles"`
	Status        string   `json:"status"` // "OK", "DRIFT", "DOWN"
	Discrepancies []string `json:"discrepancies"`
	Details       string   `json:"details"`
}

// ValidateNodeManifest validates an observed NodeState against a NodeManifest.
func ValidateNodeManifest(manifest NodeManifest, state NodeState) NodeResult {
	res := NodeResult{
		Name:          manifest.NodeName,
		OS:            manifest.OS,
		Arch:          manifest.Arch,
		Roles:         strings.Join(manifest.Roles, ","),
		Status:        "OK",
		Discrepancies: nil,
	}

	if state.Status == "DOWN" || state.Error != "" {
		res.Status = "DOWN"
		reason := state.Error
		if reason == "" {
			reason = "unreachable"
		}
		res.Details = reason
		res.Discrepancies = append(res.Discrepancies, "node is DOWN: "+reason)
		return res
	}

	// Validate tools
	for toolName, req := range manifest.Tools {
		version, hasVersion := state.ToolVersions[toolName]
		if !hasVersion || strings.TrimSpace(version) == "" {
			if req.Required {
				res.Discrepancies = append(res.Discrepancies, fmt.Sprintf("missing required tool %s", toolName))
			}
			continue
		}

		if req.Pattern != "-" && req.Pattern != "" {
			re, err := regexp.Compile(req.Pattern)
			if err != nil {
				if !strings.Contains(version, req.Pattern) {
					res.Discrepancies = append(res.Discrepancies, fmt.Sprintf("tool %s version %q does not match %q", toolName, version, req.Pattern))
				}
			} else if !re.MatchString(version) {
				res.Discrepancies = append(res.Discrepancies, fmt.Sprintf("tool %s version %q does not match pattern %s", toolName, version, req.Pattern))
			}
		}

		// Report shadowing if tool earlier on PATH shadows the intended tool
		if shadow, ok := state.ShadowedTools[toolName]; ok && shadow != "" {
			res.Discrepancies = append(res.Discrepancies, fmt.Sprintf("tool %s shadowed by %s", toolName, shadow))
		}
	}

	// Validate paths
	for _, reqPath := range manifest.Paths {
		present, checked := state.PathsPresent[reqPath.Path]
		if !checked || !present {
			if reqPath.Required {
				res.Discrepancies = append(res.Discrepancies, fmt.Sprintf("missing required path %s", reqPath.Path))
			}
			continue
		}

		if reqPath.Mode != "" {
			actualMode, hasMode := state.PathModes[reqPath.Path]
			if hasMode && actualMode != "" {
				// /tmp/.dotnet accepts sticky 1777 (drwxrwxrwt) or 0777 (drwxrwxrwx)
				if reqPath.Path == "/tmp/.dotnet" {
					if actualMode != "1777" && actualMode != "0777" && actualMode != "drwxrwxrwt" && actualMode != "drwxrwxrwx" {
						res.Discrepancies = append(res.Discrepancies, fmt.Sprintf("path %s mode is %s, want 1777 or 0777", reqPath.Path, actualMode))
					}
				} else if actualMode != reqPath.Mode {
					res.Discrepancies = append(res.Discrepancies, fmt.Sprintf("path %s mode is %s, want %s", reqPath.Path, actualMode, reqPath.Mode))
				}
			}
		}
	}

	// Validate disk space
	if manifest.MinFreeGB > 0 && state.FreeGB > 0 && state.FreeGB < manifest.MinFreeGB {
		res.Discrepancies = append(res.Discrepancies, fmt.Sprintf("disk free %d GB below required %d GB", state.FreeGB, manifest.MinFreeGB))
	}

	// Validate seat keys
	if manifest.Seat != "" {
		seatMatched := false
		for _, k := range state.SeatKeys {
			base := k
			if idx := strings.LastIndex(k, "/"); idx >= 0 {
				base = k[idx+1:]
			}
			if strings.TrimSuffix(base, ".key") == manifest.Seat {
				seatMatched = true
				break
			}
		}
		if !seatMatched && len(state.SeatKeys) > 0 {
			res.Discrepancies = append(res.Discrepancies, fmt.Sprintf("expected seat key %s.key not found in seat keys %v", manifest.Seat, state.SeatKeys))
		}
	}

	if len(res.Discrepancies) > 0 {
		res.Status = "DRIFT"
		res.Details = strings.Join(res.Discrepancies, "; ")
	} else {
		res.Status = "OK"
		res.Details = fmt.Sprintf("all %d tools and %d paths verified", len(manifest.Tools), len(manifest.Paths))
	}

	return res
}

// FleetValidationReport holds multi-node validation results and formatting.
type FleetValidationReport struct {
	Nodes []NodeResult `json:"nodes"`
	Total int          `json:"total"`
	OK    int          `json:"ok"`
	Drift int          `json:"drift"`
	Down  int          `json:"down"`
}

// ValidateFleet runs multi-node manifest validation for all machines in a registry against observed states.
func ValidateFleet(reg *Registry, states map[string]NodeState) FleetValidationReport {
	return ValidateFleetMachines(reg.Machines(), states)
}

// ValidateFleetMachines runs multi-node manifest validation for a specific list of machines against observed states.
func ValidateFleetMachines(machines []Machine, states map[string]NodeState) FleetValidationReport {
	report := FleetValidationReport{
		Total: len(machines),
	}

	for _, m := range machines {
		manifest := DefaultManifestForNode(m)
		state, ok := states[m.Name]
		if !ok {
			// Node state not found -> marked DOWN
			state = NodeState{
				Name:   m.Name,
				OS:     m.OS,
				Arch:   m.Arch,
				Status: "DOWN",
				Error:  "node unreachable or missing from state probe",
			}
		}
		res := ValidateNodeManifest(manifest, state)
		report.Nodes = append(report.Nodes, res)
		switch res.Status {
		case "OK":
			report.OK++
		case "DRIFT":
			report.Drift++
		case "DOWN":
			report.Down++
		}
	}

	return report
}

// Table returns a columnar representation of the fleet validation with named cells (including DOWN).
func (r FleetValidationReport) Table() string {
	var lines []string
	header := fmt.Sprintf("%-18s %-14s %-20s %-8s %s", "NODE", "OS/ARCH", "ROLES", "STATUS", "DETAILS")
	lines = append(lines, header)
	lines = append(lines, strings.Repeat("-", 80))

	// Sort nodes in file/registry order or stable order
	for _, n := range r.Nodes {
		osArch := fmt.Sprintf("%s/%s", n.OS, n.Arch)
		line := fmt.Sprintf("%-18s %-14s %-20s %-8s %s", n.Name, osArch, n.Roles, n.Status, n.Details)
		lines = append(lines, line)
	}

	lines = append(lines, strings.Repeat("-", 80))
	lines = append(lines, r.Summary())
	return strings.Join(lines, "\n")
}

// Summary returns a single-line summary of the fleet validation verdict.
func (r FleetValidationReport) Summary() string {
	if r.Drift == 0 && r.Down == 0 {
		return fmt.Sprintf("FLEET STANDARD OK total=%d ok=%d", r.Total, r.OK)
	}
	return fmt.Sprintf("FLEET STANDARD DRIFT total=%d ok=%d drift=%d down=%d", r.Total, r.OK, r.Drift, r.Down)
}

// ExitCode returns the appropriate exit code: 0 on all OK, 3 if any DOWN, 2 if any DRIFT.
func (r FleetValidationReport) ExitCode() int {
	if r.Down > 0 {
		return 3
	}
	if r.Drift > 0 {
		return 2
	}
	return 0
}
