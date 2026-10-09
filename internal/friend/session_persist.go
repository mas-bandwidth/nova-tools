package friend

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"regexp"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

var sessionArgs = regexp.MustCompile(`(?s)(<key>ProgramArguments</key>\s*<array>)(.*?)(</array>)`)
var sessionFlag = regexp.MustCompile(`<string>--session</string>\s*<string>[^<]*</string>`)

// RenewSessionPlist preserves every installed argument and setting, replacing only
// the proven session (SPEC-FRIEND, Session binding). No launchd command is run.
func RenewSessionPlist(path, id string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	var value bytes.Buffer
	if err := xml.EscapeText(&value, []byte(id)); err != nil {
		return err
	}
	flag := "<string>--session</string>\n<string>" + value.String() + "</string>"
	parts := sessionArgs.FindSubmatch(raw)
	if parts == nil {
		return fmt.Errorf("the plist has no ProgramArguments; run nova-friend install again")
	}
	args := parts[2]
	if sessionFlag.Match(args) {
		args = sessionFlag.ReplaceAllLiteral(args, []byte(flag))
	} else {
		args = append(args, []byte(flag)...)
	}
	changed := bytes.Join([][]byte{parts[1], args, parts[3]}, nil)
	return atomicfile.WriteFile(path, bytes.Replace(raw, parts[0], changed, 1), info.Mode().Perm())
}
