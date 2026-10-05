package friend

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// planClaude makes CLAUDE_CONFIG_DIR a real directory and, when the agent's
// plist is already on disk, names a drift if that plist does not carry the
// same path (docs/SPEC-FRIEND.md, harness settings). A missing plist is not
// drift: install writes the plist after these settings. Empty is <home>/.claude.
func planClaude(p *Prepared) error {
	dir := p.settings.ConfigDir
	if dir == "" {
		dir = filepath.Join(p.settings.Home, ".claude")
	}
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("claude config dir: %s is not an absolute real directory; name the real directory and run nova-friend install again", dir)
	}
	exists, err := classifyDir(dir)
	if err != nil {
		return fmt.Errorf("claude config dir: %w", err)
	}
	if !exists {
		p.drifts = append(p.drifts, fmt.Sprintf("claude config_dir: %s is not a real directory", dir))
		p.Dirs = append(p.Dirs, dir)
	}
	p.ConfigDir = dir
	plist := Agent{Friend: p.settings.Friend, Home: p.settings.Home}.PlistPath()
	raw, err := os.ReadFile(plist)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	installed, ok := plistString(string(raw), "CLAUDE_CONFIG_DIR")
	if !ok || installed != dir {
		shown := "missing"
		if ok {
			shown = strconv.Quote(installed)
		}
		p.drifts = append(p.drifts, fmt.Sprintf("claude config_dir: installed %s, want %s", shown, strconv.Quote(dir)))
	}
	return nil
}

// plistString is the string value of key in a plist this package wrote:
// <key>KEY</key><string>VALUE</string>, XML escapes undone.
func plistString(plist, key string) (string, bool) {
	mark := "<key>" + key + "</key><string>"
	i := strings.Index(plist, mark)
	if i < 0 {
		return "", false
	}
	rest := plist[i+len(mark):]
	j := strings.Index(rest, "</string>")
	if j < 0 {
		return "", false
	}
	return xmlUnescape(rest[:j]), true
}

func xmlUnescape(s string) string {
	return strings.NewReplacer(
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", "\"",
		"&apos;", "'",
		"&amp;", "&",
	).Replace(s)
}
