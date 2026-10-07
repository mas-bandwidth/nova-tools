package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorUnitsCheckFindsAHandPlistAndAMissingLoop(t *testing.T) {
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("no HOME set, skipping test")
	}
	root := filepath.Join(home, "nova")
	configDir := filepath.Join(root, "nova-config", "records", "loops")
	launchAgentsPath := filepath.Join(home, "Library", "LaunchAgents")

	// Clean and recreate directories
	os.RemoveAll(configDir)
	os.RemoveAll(filepath.Join(launchAgentsPath, "redis-local.plist"))
	os.RemoveAll(filepath.Join(launchAgentsPath, "com.nova.loop.friend-beat-test.plist"))
	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(launchAgentsPath, 0o755)
	os.MkdirAll(filepath.Join(root, "nova-config"), 0o755)

	// Write the seat.env
	seatPath := filepath.Join(root, "seat.env")
	os.WriteFile(seatPath, []byte("NOVA_SECRETS_SEAT=coordinator\n"), 0o644)

	// Create loop record for redis-local (with matching unit)
	os.WriteFile(filepath.Join(configDir, "redis-local"),
		[]byte("Name=redis-local\nCommand=/usr/local/bin/nova-redis serve\n"),
		0o644)

	// Create loop record for beat-local (missing unit)
	os.WriteFile(filepath.Join(configDir, "beat-local"),
		[]byte("Name=beat-local\nCommand=/usr/local/bin/nova-redis beat\n"),
		0o644)

	// Create installed unit for redis-local
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
<key>Label</key>
<string>redis-local</string>
<key>ProgramArguments</key>
<array>
<string>/usr/local/bin/nova-redis serve</string>
</array>
</dict>
</plist>`
	os.WriteFile(filepath.Join(launchAgentsPath, "redis-local.plist"), []byte(plist), 0o644)

	// Create hand plist (not from any record)
	handPlist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
<key>Label</key>
<string>com.nova.loop.friend-beat-test</string>
<key>ProgramArguments</key>
<array>
<string>/usr/local/bin/custom-beat</string>
</array>
</dict>
</plist>`
	os.WriteFile(filepath.Join(launchAgentsPath, "com.nova.loop.friend-beat-test.plist"),
		[]byte(handPlist), 0o644)

	// Run the check
	result := checkUnits(nil, OSEnv{})

	// Should fail because:
	// 1. beat-local record has no unit
	// 2. hand plist com.nova.loop.friend-beat-test exists
	if result.Status == OK {
		t.Errorf("expected check to fail, got ok")
	}

	// Should mention the missing unit
	if !strings.Contains(result.Evidence, "beat-local") {
		t.Errorf("expected evidence to mention beat-local missing unit, got: %s", result.Evidence)
	}

	// Should mention the hand plist
	if !strings.Contains(result.Evidence, "hand plist") {
		t.Errorf("expected evidence to mention hand plist, got: %s", result.Evidence)
	}

	// Should have a fix line
	if result.Fix == "" {
		t.Errorf("expected fix line, got none")
	}
}

func TestDoctorUnitsCheckOK(t *testing.T) {
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("no HOME set, skipping test")
	}
	root := filepath.Join(home, "nova")
	configDir := filepath.Join(root, "nova-config", "records", "loops")
	launchAgentsPath := filepath.Join(home, "Library", "LaunchAgents")

	// Clean and recreate directories
	os.RemoveAll(configDir)
	os.RemoveAll(filepath.Join(launchAgentsPath, "redis-local.plist"))
	os.RemoveAll(filepath.Join(launchAgentsPath, "com.nova.loop.friend-beat-test.plist"))
	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(launchAgentsPath, 0o755)
	os.MkdirAll(filepath.Join(root, "nova-config"), 0o755)

	// Write the seat.env
	os.WriteFile(filepath.Join(root, "seat.env"), []byte("NOVA_SECRETS_SEAT=coordinator\n"), 0o644)

	// Create matching loop record and unit
	os.WriteFile(filepath.Join(configDir, "redis-local"),
		[]byte("Name=redis-local\nCommand=/usr/local/bin/nova-redis serve\n"),
		0o644)

	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
<key>Label</key>
<string>redis-local</string>
<key>ProgramArguments</key>
<array>
<string>/usr/local/bin/nova-redis serve</string>
</array>
</dict>
</plist>`
	os.WriteFile(filepath.Join(launchAgentsPath, "redis-local.plist"), []byte(plist), 0o644)

	// Run the check
	result := checkUnits(nil, OSEnv{})

	if result.Status != OK {
		t.Errorf("expected check to pass, got: %s - %s", result.Status, result.Evidence)
	}
}

func TestDoctorUnitsCheckFindsCommandMismatch(t *testing.T) {
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("no HOME set, skipping test")
	}
	root := filepath.Join(home, "nova")
	configDir := filepath.Join(root, "nova-config", "records", "loops")
	launchAgentsPath := filepath.Join(home, "Library", "LaunchAgents")

	// Clean and recreate directories
	os.RemoveAll(configDir)
	os.RemoveAll(filepath.Join(launchAgentsPath, "redis-local.plist"))
	os.RemoveAll(filepath.Join(launchAgentsPath, "com.nova.loop.friend-beat-test.plist"))
	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(launchAgentsPath, 0o755)
	os.MkdirAll(filepath.Join(root, "nova-config"), 0o755)

	// Write the seat.env
	os.WriteFile(filepath.Join(root, "seat.env"), []byte("NOVA_SECRETS_SEAT=coordinator\n"), 0o644)

	// Create loop record with one command
	os.WriteFile(filepath.Join(configDir, "redis-local"),
		[]byte("Name=redis-local\nCommand=/usr/local/bin/nova-redis serve\n"),
		0o644)

	// Create unit with DIFFERENT command
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
<key>Label</key>
<string>redis-local</string>
<key>ProgramArguments</key>
<array>
<string>/usr/local/bin/custom-redis</string>
</array>
</dict>
</plist>`
	os.WriteFile(filepath.Join(launchAgentsPath, "redis-local.plist"), []byte(plist), 0o644)

	// Run the check
	result := checkUnits(nil, OSEnv{})

	if result.Status == OK {
		t.Errorf("expected check to fail due to command mismatch, got ok")
	}

	if !strings.Contains(result.Evidence, "command") {
		t.Errorf("expected evidence to mention command mismatch, got: %s", result.Evidence)
	}
}
