package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSwarmInstallCoverSwarmKindNames(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "disk-guard|mirror-refresh", swarmKindNames())
}

func TestSwarmInstallCoverOrDash(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"", "-"},
		{"linux", "linux"},
		{"darwin", "darwin"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, orDash(tt.input))
		})
	}
}

func TestSwarmInstallCoverUnitDir(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		goos     string
		home     string
		getenv   func(string) string
		expected string
	}{
		{
			name: "linux with XDG_CONFIG_HOME",
			goos: "linux",
			home: "/home/user",
			getenv: func(s string) string {
				if s == "XDG_CONFIG_HOME" {
					return "/home/user/.config"
				}
				return ""
			},
			expected: "/home/user/.config/systemd/user",
		},
		{
			name:     "linux without XDG_CONFIG_HOME",
			goos:     "linux",
			home:     "/home/user",
			getenv:   func(s string) string { return "" },
			expected: "/home/user/.config/systemd/user",
		},
		{
			name:     "darwin",
			goos:     "darwin",
			home:     "/Users/user",
			getenv:   func(s string) string { return "" },
			expected: "/Users/user/Library/LaunchAgents",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, unitDir(tt.goos, tt.home, tt.getenv))
		})
	}
}

func TestSwarmInstallCoverForeignInstall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
		found bool
	}{
		{"store", "nova-redis install store", true},
		{"bus", "nova-redis install bus", true},
		{"server", "nova-sprint install server", true},
		{"member", "nova-sprint install member", true},
		{"seat-push", "nova-sprint install seat-push", true},
		{"friend-sync", "nova-sprint install friend-sync", true},
		{"table", "nova-sprint install table", true},
		{"unknown", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got, found := foreignInstall(tt.input)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.found, found)
		})
	}
}

func TestSwarmInstallCoverXmlEscape(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{`"`, "&#34;"},
		{"'", "&#39;"},
		{"&", "&amp;"},
		{"<", "&lt;"},
		{">", "&gt;"},
		{"\t", "&#x9;"},
		{"\n", "&#xA;"},
		{"\r", "&#xD;"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, xmlEscape(tt.input))
		})
	}
}

func TestSwarmInstallCoverThrottleSeconds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input time.Duration
		want  int
	}{
		{0, 10},
		{time.Nanosecond, 1},
		{1500 * time.Millisecond, 2},
	}
	for _, tt := range tests {
		t.Run(tt.input.String(), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, throttleSeconds(tt.input))
		})
	}
}

func TestSwarmInstallCoverDiskGuardText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		goos            string
		args            []string
		every           time.Duration
		wantErr         string
		shouldCheckText bool
	}{
		{
			name:    "refuses goos \"\"",
			goos:    "",
			args:    []string{"/opt/nova/bin/nova-swarm", "disk-guard"},
			every:   15 * time.Minute,
			wantErr: "on -",
		},
		{
			name:    "refuses no args",
			goos:    "linux",
			args:    []string{},
			every:   15 * time.Minute,
			wantErr: "not -",
		},
		{
			name:    "refuses relative path",
			goos:    "linux",
			args:    []string{"nova-swarm", "disk-guard"},
			every:   15 * time.Minute,
			wantErr: "not nova-swarm",
		},
		{
			name:    "refuses wrapper base name",
			goos:    "linux",
			args:    []string{"/opt/nova/bin/wrapper", "disk-guard"},
			every:   15 * time.Minute,
			wantErr: "is not nova-swarm",
		},
		{
			name:    "refuses second word other than disk-guard",
			goos:    "linux",
			args:    []string{"/opt/nova/bin/nova-swarm", "mirror"},
			every:   15 * time.Minute,
			wantErr: "runs nova-swarm disk-guard",
		},
		{
			name:    "refuses negative every",
			goos:    "linux",
			args:    []string{"/opt/nova/bin/nova-swarm", "disk-guard"},
			every:   -1 * time.Minute,
			wantErr: "--every",
		},
		{
			name:            "returns systemd text for linux",
			goos:            "linux",
			args:            []string{"/opt/nova/bin/nova-swarm", "disk-guard"},
			every:           15 * time.Minute,
			shouldCheckText: true,
		},
		{
			name:            "returns launchd text for darwin",
			goos:            "darwin",
			args:            []string{"/opt/nova/bin/nova-swarm", "disk-guard"},
			every:           15 * time.Minute,
			shouldCheckText: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			k, _ := swarmKindOf("disk-guard")
			got, err := diskGuardText(k, tt.goos, tt.args, "", tt.every)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
				if tt.shouldCheckText {
					assert.NotEmpty(t, got)
				}
			}
		})
	}
}

func TestSwarmInstallCoverWriteUnit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		setup   func(tempDir string) (path string, loadFunc func(string) error)
		text    string
		changed bool
	}{
		{
			name: "writing a new file (changed true, loader called)",
			setup: func(tempDir string) (string, func(string) error) {
				path := filepath.Join(tempDir, "test-unit")
				loadFunc := func(p string) error { return nil }
				return path, loadFunc
			},
			text:    "new content",
			changed: true,
		},
		{
			name: "writing the same text again (changed false)",
			setup: func(tempDir string) (string, func(string) error) {
				path := filepath.Join(tempDir, "test-unit2")
				err := os.WriteFile(path, []byte("existing"), 0644)
				require.NoError(t, err)
				loadFunc := func(p string) error { return nil }
				return path, loadFunc
			},
			text:    "existing",
			changed: false,
		},
		{
			name: "loader error",
			setup: func(tempDir string) (string, func(string) error) {
				path := filepath.Join(tempDir, "test-unit3")
				loadFunc := func(p string) error { return os.ErrNotExist }
				return path, loadFunc
			},
			text: "content with loader error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tempDir := t.TempDir()
			path, loadFunc := tt.setup(tempDir)
			changed, err := writeUnit(path, tt.text, loadFunc)
			if tt.changed {
				assert.True(t, changed)
				assert.NoError(t, err)
			} else if tt.text == "existing" {
				assert.False(t, changed)
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "the unit is written")
			}
		})
	}
}

func TestSwarmInstallCoverRemoveUnit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		setup       func(tempDir string) (path string, unloadFunc func(string) error)
		wantRemoved bool
		wantErr     bool
	}{
		{
			name: "missing path",
			setup: func(tempDir string) (string, func(string) error) {
				return filepath.Join(tempDir, "nonexistent"), func(p string) error { return nil }
			},
			wantRemoved: false,
		},
		{
			name: "unload error",
			setup: func(tempDir string) (string, func(string) error) {
				path := filepath.Join(tempDir, "test-unit")
				err := os.WriteFile(path, []byte("content"), 0644)
				require.NoError(t, err)
				return path, func(p string) error { return os.ErrNotExist }
			},
			wantRemoved: false,
			wantErr:     true,
		},
		{
			name: "successful removal",
			setup: func(tempDir string) (string, func(string) error) {
				path := filepath.Join(tempDir, "test-unit2")
				err := os.WriteFile(path, []byte("content"), 0644)
				require.NoError(t, err)
				return path, func(p string) error { return nil }
			},
			wantRemoved: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tempDir := t.TempDir()
			path, unloadFunc := tt.setup(tempDir)
			removed, err := removeUnit(path, unloadFunc)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantRemoved, removed)
			if tt.wantRemoved {
				_, err := os.Stat(path)
				assert.True(t, os.IsNotExist(err))
			}
		})
	}
}

func TestSwarmInstallCoverInstallKind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		kind          string
		exitCode      int
		checkContains string
	}{
		{
			name:          "foreign kind",
			kind:          "store",
			exitCode:      2,
			checkContains: "nova-redis install store",
		},
		{
			name:          "unknown kind",
			kind:          "unknown",
			exitCode:      2,
			checkContains: "no unit kind",
		},
		{
			name:          "mirror-refresh owed refusal",
			kind:          "mirror-refresh",
			exitCode:      2,
			checkContains: "no mirror verb",
		},
		{
			name:          "disk-guard --every 0",
			kind:          "disk-guard",
			exitCode:      2,
			checkContains: "the unit runs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stdout := &testWriter{}
			stderr := &testWriter{}
			exitCode := installKind(tt.kind, nil, stdout, stderr)
			assert.Equal(t, tt.exitCode, exitCode)
			output := stderr.String()
			if tt.checkContains != "" {
				assert.Contains(t, output, tt.checkContains)
			}
		})
	}
}

func TestSwarmInstallCoverCmdInstall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		args          []string
		exitCode      int
		checkContains string
	}{
		{
			name:          "cmdInstall with no kind",
			args:          []string{},
			exitCode:      2,
			checkContains: "disk-guard|mirror-refresh",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stdout := &testWriter{}
			stderr := &testWriter{}
			exitCode := cmdInstall(tt.args, stdout, stderr)
			assert.Equal(t, tt.exitCode, exitCode)
			output := stderr.String()
			if tt.checkContains != "" {
				assert.Contains(t, output, tt.checkContains)
			}
		})
	}
}

func TestSwarmInstallCoverCmdUninstall(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		args          []string
		exitCode      int
		checkContains string
	}{
		{
			name:          "cmdUninstall with no kind",
			args:          []string{},
			exitCode:      2,
			checkContains: "disk-guard|mirror-refresh",
		},
		{
			name:          "cmdUninstall foreign kind",
			args:          []string{"store"},
			exitCode:      2,
			checkContains: "nova-redis uninstall store",
		},
		{
			name:          "cmdUninstall unknown kind",
			args:          []string{"unknown"},
			exitCode:      2,
			checkContains: "no unit kind",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stdout := &testWriter{}
			stderr := &testWriter{}
			exitCode := cmdUninstall(tt.args, stdout, stderr)
			assert.Equal(t, tt.exitCode, exitCode)
			output := stderr.String()
			if tt.checkContains != "" {
				assert.Contains(t, output, tt.checkContains)
			}
		})
	}
}

func TestSwarmInstallCoverLoadSwarmUnit(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_TEST_NO_HOST") == "" {
		t.Skip("NOVA_TEST_NO_HOST not set")
	}
	err := loadSwarmUnit("linux", "load", "/test/unit")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NOVA_TEST_NO_HOST")
}

type testWriter struct {
	buf string
}

func (w *testWriter) Write(p []byte) (int, error) {
	w.buf += string(p)
	return len(p), nil
}

func (w *testWriter) String() string {
	return w.buf
}
