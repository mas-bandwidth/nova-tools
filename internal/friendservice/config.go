// Package friendservice owns the local receive-loop process and launchd configuration.
// Libraries considered: encoding/json, encoding/xml, os/exec and context.
package friendservice

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Config contains local executable routing only. Shared fleet/wire fields await
// server agreement; no local connection-window override stands in for fleet truth.
type Config struct {
	Friend     string `json:"friend"`
	Server     string `json:"server"`
	Redis      string `json:"redis"`
	Consumer   string `json:"consumer"`
	Self       string `json:"self"`
	Bus        string `json:"bus"`
	Sprint     string `json:"sprint"`
	Deliver    string `json:"deliver"`
	ConfigPath string `json:"config_path"`
	StdoutPath string `json:"stdout_path"`
	StderrPath string `json:"stderr_path"`
}

var name = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Parse admits one typed configuration, never trailing or unknown fields.
func Parse(raw []byte) (Config, error) {
	var c Config
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if err := d.Decode(&extra); err == nil {
		return c, errors.New("config wants one JSON object")
	} else if !errors.Is(err, io.EOF) {
		return c, err
	}
	return c, c.Validate()
}

// Validate refuses a nonexistent or nonexecutable native delivery path.
// Rowan client contract: explicit deliver executable, no unsupported adapter fallback.
func (c Config) Validate() error {
	var problems []string
	if !name.MatchString(c.Friend) {
		problems = append(problems, "friend wants lowercase letters, digits or hyphens, at most 64 bytes")
	}
	for key, value := range map[string]string{"server": c.Server, "redis": c.Redis, "consumer": c.Consumer} {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, key+" wants an explicit nonempty value")
		}
	}
	for key, path := range map[string]string{"self": c.Self, "bus": c.Bus, "sprint": c.Sprint, "deliver": c.Deliver} {
		info, err := os.Stat(path)
		if !filepath.IsAbs(path) || err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			problems = append(problems, key+" wants an absolute regular executable path")
		}
	}
	for key, path := range map[string]string{"config_path": c.ConfigPath, "stdout_path": c.StdoutPath, "stderr_path": c.StderrPath} {
		if !filepath.IsAbs(path) {
			problems = append(problems, key+" wants an absolute path")
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// ReceiveArgs uses existing bus2 flags only. The shell sees a quoted executable,
// never message data; each message reaches that executable on stdin.
func (c Config) ReceiveArgs() []string {
	return []string{"recv", "--as", c.Friend, "--consumer", c.Consumer, "--redis", c.Redis, "--forever", "--exec", "exec '" + strings.ReplaceAll(c.Deliver, "'", "'\\''") + "'"}
}

// Plist renders one agent; writing/bootstrap is separate from rendering.
// Rowan client contract: login launch and restart owned by launchd.
func (c Config) Plist() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	esc := func(s string) string { var b strings.Builder; _ = xml.EscapeText(&b, []byte(s)); return b.String() } // ignored: strings.Builder writes cannot fail.
	var args strings.Builder
	for _, v := range []string{c.Self, "serve", "--config", c.ConfigPath} {
		args.WriteString("<string>" + esc(v) + "</string>")
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>org.nova.friend.` + esc(c.Friend) + `</string>
<key>ProgramArguments</key><array>` + args.String() + `</array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>ThrottleInterval</key><integer>10</integer>
<key>StandardOutPath</key><string>` + esc(c.StdoutPath) + `</string>
<key>StandardErrorPath</key><string>` + esc(c.StderrPath) + `</string>
</dict></plist>
`), nil
}
