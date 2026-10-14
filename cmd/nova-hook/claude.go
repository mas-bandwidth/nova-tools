package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// claudeRoute is --harness claude's flags: the generation file's directory,
// the settings directory the Stop hook goes in, and whether this run is the
// Stop hook itself.
type claudeRoute struct {
	state, settings string
	boundary        bool
}

// claudeConfig checks the Claude Code route before any hook record is written.
func claudeConfig(c *tool.Call, native *codexRoute) (*claudeRoute, error) {
	if native != nil {
		return nil, errors.New("claude delivery and codex delivery do not run in one hook")
	}
	if c.Bool("once") {
		return nil, errors.New("claude delivery wants a running hook client; omit --once")
	}
	state, err := existingDir(c.Str("claude-state"), "--claude-state")
	if err != nil {
		return nil, err
	}
	settings, err := existingDir(c.Str("claude-settings"), "--claude-settings")
	if err != nil {
		return nil, err
	}
	return &claudeRoute{state: state, settings: settings, boundary: c.Bool("claude-boundary")}, nil
}

func existingDir(path, flag string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("claude delivery wants %s", flag)
	}
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("claude delivery wants %s to be an existing directory", flag)
	}
	return clean, nil
}

// installClaude installs the Stop hook and the notice directory, then
// answers the shim.
func (w world) installClaude(c *tool.Call, r *claudeRoute, role string) (*tool.Out, notice.Shim) {
	bin, err := os.Executable()
	if err == nil && w.claudeInstall != nil {
		err = w.claudeInstall(bin, c.Str("as"), role, r.state, r.settings)
	} else if err == nil {
		err = errors.New("no installer")
	}
	notices := ""
	if err == nil {
		notices, err = notice.MakeDir(filepath.Join(r.state, "notices"))
	}
	if err != nil {
		return tool.Refuse("claude setup could not be installed: " + err.Error()), nil
	}
	line(c, tool.Done().As("STATE").Fact("state", "claude-installed").Fact("settings", r.settings).Note("a Monitor on this process's stdout delivers each PUSH line; the Stop hook is the turn boundary"))
	return nil, &notice.Claude{State: r.state, Notices: notices}
}
