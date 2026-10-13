package up

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

var seatEnvironmentEndpoints = []struct {
	flag string
	env  string
}{
	{flag: "sprint-redis", env: "NOVA_SPRINT_REDIS"},
	{flag: "server", env: "NOVA_SPRINT_SERVER"},
	{flag: "bus-redis", env: "NOVA_BUS_REDIS"},
}

func writeSeatEnvironment(c *tool.Call, getenv func(string) string) *tool.Out {
	environment := make(map[string]string, len(seatEnvironmentEndpoints))
	var missing, invalid []string
	for _, endpoint := range seatEnvironmentEndpoints {
		value := getenv(endpoint.env)
		if c.Given(endpoint.flag) {
			value = c.Str(endpoint.flag)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			missing = append(missing, endpoint.env)
			continue
		}
		if !validSeatEndpoint(value) {
			invalid = append(invalid, endpoint.env)
			continue
		}
		environment[endpoint.env] = value
	}
	if len(missing) > 0 || len(invalid) > 0 {
		var problems []string
		if len(missing) > 0 {
			problems = append(problems, "missing fleet endpoints "+strings.Join(missing, ","))
		}
		if len(invalid) > 0 {
			problems = append(problems, "invalid password-free host:port endpoints "+strings.Join(invalid, ","))
		}
		return tool.Refuse(strings.Join(problems, "; ") + "; supply --sprint-redis, --server and --bus-redis or the matching environment values; --local's mem: twin is not a fleet endpoint")
	}

	profile, exists, err := seatcred.ReadSeatEnvironment(getenv)
	if err != nil {
		return tool.Refuse("the existing seat environment could not be validated; it was left unchanged")
	}
	if !exists {
		profile.Version = 1
	}
	profile.Environment = environment
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return tool.Refuse("the seat environment could not be encoded; no file was changed")
	}
	data = append(data, '\n')
	path, err := seatcred.SeatEnvironmentPath(getenv)
	if err != nil {
		return tool.Refuse("HOME must name an absolute directory when XDG_CONFIG_HOME is not absolute")
	}
	if c.DryRun() {
		return tool.Done().Note("dry run: seat environment validated; nothing written").Fact("fields", 3)
	}
	if err := ensureSeatEnvironmentDir(filepath.Dir(path)); err != nil {
		return tool.Refuse("the per-user seat environment directory is not safe to write")
	}
	if err := atomicfile.WriteFile(path, data, 0o600, atomicfile.ExactMode()); err != nil {
		return tool.Refuse("the seat environment could not be written atomically; the previous file was left intact")
	}
	return tool.Done().Note("seat environment written").Fact("fields", 3)
}

func validSeatEndpoint(value string) bool {
	if strings.HasPrefix(value, "mem:") || strings.Contains(value, "://") || strings.ContainsAny(value, "@/?#") {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || r == '\'' || r == '"' || r == ' ' {
			return false
		}
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	return err == nil && n > 0
}

func ensureSeatEnvironmentDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("not a real directory")
	}
	current, err := user.Current()
	if err != nil || !ownedBy(info, current.Uid) {
		return fmt.Errorf("directory is not owned by the current user")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("directory is writable by another user")
	}
	return nil
}

func ownedBy(info os.FileInfo, uid string) bool {
	sys := reflect.ValueOf(info.Sys())
	if !sys.IsValid() || sys.Kind() != reflect.Ptr || sys.IsNil() {
		return false
	}
	field := sys.Elem().FieldByName("Uid")
	if !field.IsValid() {
		return false
	}
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprint(field.Uint()) == uid
	case reflect.String:
		return field.String() == uid
	default:
		return false
	}
}
