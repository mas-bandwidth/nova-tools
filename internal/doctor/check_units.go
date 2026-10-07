package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// unitsTimeout bounds one launchctl or systemctl call.
const unitsTimeout = 10 * time.Second

func init() {
	Default.Register(Check{Name: "units", Dependency: "the service units (launchd and systemd) for every loop", Run: checkUnits})
}

// checkUnits compares the loop records (nova-config loop entries) with the
// installed units. It names a record without a unit, a unit with no record (a
// hand plist), and a unit whose command differs from its record. The fix line
// names the nova-up verb that applies the records.
func checkUnits(ctx context.Context, env Env) Result {
	records, err := listLoopRecords()
	if err != nil {
		return Result{Status: Fail, Evidence: "could not read loop records: " + err.Error(),
			Fix: "report this to the nova-tools maintainers"}
	}
	units, err := listInstalledUnits()
	if err != nil {
		return Result{Status: Fail, Evidence: "could not list installed units: " + err.Error(),
			Fix: "nova-up --local; if you are not on a coordinator machine, ask the coordinator to run nova-up"}
	}

	var findings []string
	seenUnits := map[string]bool{}

	for _, r := range records {
		cmd, ok := units[r.Name]
		if !ok {
			findings = append(findings, fmt.Sprintf("record %s has no installed unit", r.Name))
		} else {
			seenUnits[r.Name] = true
			if r.Command != cmd {
				findings = append(findings, fmt.Sprintf("unit %s has command %q, record says %q", r.Name, cmd, r.Command))
			}
		}
	}

	// Find hand plists (units not from records)
	for name, cmd := range units {
		if !seenUnits[name] {
			if strings.HasPrefix(name, "com.nova.loop.") {
				findings = append(findings, fmt.Sprintf("hand plist %s with command %q not from a record", name, cmd))
			}
		}
	}

	if len(findings) == 0 {
		return Result{Status: OK, Evidence: "all loop records have matching installed units"}
	}

	return Result{Status: Fail,
		Evidence: strings.Join(findings, "; "),
		Fix:      "nova-up --local (or nova-up fleet to apply to all machines)"}
}

// loopRecord is one nova-config loop entry.
type loopRecord struct {
	Name    string
	Command string
}

// listLoopRecords reads the loop records from the seat's nova-config directory.
func listLoopRecords() ([]loopRecord, error) {
	home := os.Getenv("HOME")
	if home == "" {
		return nil, fmt.Errorf("no HOME set")
	}
	// Check if seat exists in home/nova
	seatPath := filepath.Join(home, "nova", "seat.env")
	if _, err := os.ReadFile(seatPath); err != nil {
		return nil, fmt.Errorf("no seat.env found at %s: %v", seatPath, err)
	}
	root := filepath.Join(home, "nova")
	configPath := filepath.Join(root, "nova-config", "records", "loops")

	entries, err := os.ReadDir(configPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %v", configPath, err)
	}

	var records []loopRecord
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(configPath, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// Parse the record: expect Name=<name> and Command=<command>
		rec := parseLoopRecord(string(data))
		if rec.Name != "" {
			records = append(records, rec)
		}
	}
	return records, nil
}

func parseLoopRecord(content string) loopRecord {
	var rec loopRecord
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "Name=") {
			rec.Name = strings.TrimPrefix(line, "Name=")
		} else if strings.HasPrefix(line, "Command=") {
			rec.Command = strings.TrimPrefix(line, "Command=")
		}
	}
	return rec
}

// listInstalledUnits returns the names of installed launchd/systemd units and their commands.
func listInstalledUnits() (map[string]string, error) {
	units := map[string]string{}

	home := os.Getenv("HOME")
	if home == "" {
		return units, nil
	}

	// Try launchd first (darwin)
	launchAgentsPath := filepath.Join(home, "Library", "LaunchAgents")
	entries, err := os.ReadDir(launchAgentsPath)
	if err != nil {
		// Try systemd instead (linux)
		systemdPath := filepath.Join(home, ".config", "systemd", "user")
		entries, err = os.ReadDir(systemdPath)
		if err != nil {
			return units, nil
		}
		// Parse systemd units
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".service") {
				continue
			}
			path := filepath.Join(systemdPath, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".service")
			units[name] = extractCommandFromSystemd(string(data))
		}
		return units, nil
	}

	// Parse launchd plists
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".plist") {
			continue
		}
		path := filepath.Join(launchAgentsPath, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".plist")
		units[name] = extractCommandFromPlist(string(data))
	}

	return units, nil
}

func extractCommandFromPlist(content string) string {
	// Look for ProgramArguments or Program in the plist
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.Contains(line, "<key>ProgramArguments</key>") {
			// Next few lines contain the arguments
			for j := i + 1; j < len(lines) && j < i+10; j++ {
				if strings.Contains(lines[j], "<string>") {
					// Extract the first argument (the command)
					s := strings.TrimPrefix(lines[j], "<string>")
					s = strings.TrimSuffix(s, "</string>")
					return strings.TrimSpace(s)
				}
			}
		}
		if strings.Contains(line, "<key>Program</key>") {
			s := strings.TrimPrefix(lines[i], "<key>Program</key>")
			s = strings.TrimSuffix(s, "<string>")
			s = strings.TrimSuffix(s, "</string>")
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func extractCommandFromSystemd(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "ExecStart=") {
			cmd := strings.TrimPrefix(line, "ExecStart=")
			return strings.TrimSpace(cmd)
		}
	}
	return ""
}
