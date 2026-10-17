package up

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	wire "github.com/mas-bandwidth/nova-tools/pkg/seatrecwire"
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
		return tool.Refuse(fmt.Sprintf("the existing seat environment could not be validated (%v); it was left unchanged", err))
	}
	if !exists {
		profile.Version = 1
	}
	profile.Environment = environment
	data, err := seatEnvironmentTableJSON(profile)
	if err != nil {
		return tool.Refuse(fmt.Sprintf("the seat environment could not be encoded (%v); no file was changed", err))
	}
	path, err := seatcred.SeatEnvironmentPath(getenv)
	if err != nil {
		return tool.Refuse(fmt.Sprintf("HOME must name an absolute directory when XDG_CONFIG_HOME is not absolute (%v)", err))
	}
	if c.DryRun() {
		return tool.Done().Note("dry run: seat environment validated; nothing written").Fact("fields", 3)
	}
	if err := ensureSeatEnvironmentDir(filepath.Dir(path)); err != nil {
		return tool.Refuse(fmt.Sprintf("the per-user seat environment directory is not safe to write (%v)", err))
	}
	if err := atomicfile.WriteFile(path, data, 0o600, atomicfile.ExactMode()); err != nil {
		return tool.Refuse(fmt.Sprintf("the seat environment could not be written atomically (%v); the previous file was left intact", err))
	}
	return tool.Done().Note("seat environment written").Fact("fields", 3)
}

// seatEnvironmentTableJSON renders the seat environment record as the seatrec
// SeatEnvironment table's schema JSON text (S1, S2, C13): the table form a
// file read by people takes (the coordinator ruling of 2026-10-17). The
// generated table is built from the record and rendered by the generated
// writer, so a value the table cannot hold is the refusal, before any byte is
// written, and the legacy JSON object is never written as a fallback.
func seatEnvironmentTableJSON(r seatcred.SeatEnvironment) ([]byte, error) {
	var arg struct {
		Version          int               `json:"version"`
		ForgeIdentity    string            `json:"forge_identity,omitempty"`
		NovaSprintRedis  string            `json:"nova_sprint_redis,omitempty"`
		NovaSprintServer string            `json:"nova_sprint_server,omitempty"`
		NovaBusRedis     string            `json:"nova_bus_redis,omitempty"`
		Login            map[string]string `json:"login,omitempty"`
	}
	arg.Version = r.Version
	arg.ForgeIdentity = r.ForgeIdentity
	for key, value := range r.Environment {
		switch key {
		case "NOVA_SPRINT_REDIS":
			arg.NovaSprintRedis = value
		case "NOVA_SPRINT_SERVER":
			arg.NovaSprintServer = value
		case "NOVA_BUS_REDIS":
			arg.NovaBusRedis = value
		}
	}
	if l := r.Login; l != nil {
		arg.Login = map[string]string{
			"redis": l.Redis, "user": l.User, "store": l.Store, "as": l.As,
			"key": l.Key, "sops": l.Sops, "secret": l.Secret,
		}
	}
	in, err := json.Marshal(arg)
	if err != nil {
		return nil, err
	}
	var b wire.SeatEnvironmentBuilder
	if !b.Init() {
		return nil, fmt.Errorf("the table could not be allocated")
	}
	var rep wire.TableReport
	if !wire.SeatEnvironmentFromJson(&b, in, &rep) || rep.Reason != "" || rep.Malformed || rep.Duplicate > 0 || rep.Clamped > 0 || rep.KindMismatch > 0 {
		b.Shutdown()
		return nil, fmt.Errorf("a value the seat environment table cannot hold (%s)", rep.Reason)
	}
	if !b.Lock() {
		b.Shutdown()
		return nil, fmt.Errorf("the table could not be locked")
	}
	v := b.AsConst()
	n := wire.SeatEnvironmentToJsonMeasure(v)
	if n < 0 {
		return nil, fmt.Errorf("the seat environment table could not be measured")
	}
	out := make([]byte, n)
	if wire.SeatEnvironmentToJson(v, out) != n {
		return nil, fmt.Errorf("the seat environment table could not be rendered")
	}
	if _, err := seatcred.NewSeatEnvironmentReader().Read(out); err != nil {
		return nil, fmt.Errorf("a value the seat environment table cannot hold (%v)", err)
	}
	return out, nil
}

func validSeatEndpoint(value string) bool { return seatcred.ValidSeatEndpoint(value) }

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
	if err != nil {
		return fmt.Errorf("could not identify the current user: %w", err)
	}
	if !ownedBy(info, current.Uid) {
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
