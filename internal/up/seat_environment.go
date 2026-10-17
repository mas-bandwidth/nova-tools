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
		return tool.Refuse(err.Error() + "; no file was changed")
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

// seatEnvTableModel is the seatrec SeatEnvironment table as its schema JSON
// text names it: the three endpoints are top-level fields, absent as null,
// where the legacy record nested them under "environment" (S1, S2).
type seatEnvTableModel struct {
	Version          *int64              `json:"version,omitempty"`
	ForgeIdentity    *string             `json:"forge_identity,omitempty"`
	NovaSprintRedis  *string             `json:"nova_sprint_redis,omitempty"`
	NovaSprintServer *string             `json:"nova_sprint_server,omitempty"`
	NovaBusRedis     *string             `json:"nova_bus_redis,omitempty"`
	Login            *seatcred.SeatLogin `json:"login,omitempty"`
}

// seatEnvironmentTableJSON is the profile as the schema JSON text form of the
// seatrec SeatEnvironment table (S1, S2): built through the generated reader
// and written with the generated writer, so a reader of any form reads back
// exactly the record written. A value the table cannot hold is refused by
// name before a byte is written, never written in the legacy form as a
// fallback (the legacy write is gone). The file ends in the newline the
// legacy writer left.
func seatEnvironmentTableJSON(profile seatcred.SeatEnvironment) ([]byte, error) {
	model := seatEnvTableModel{Version: ip(int64(profile.Version)), Login: profile.Login}
	if profile.ForgeIdentity != "" {
		model.ForgeIdentity = sp(profile.ForgeIdentity)
	}
	for _, e := range []struct {
		key  string
		slot **string
	}{{"NOVA_SPRINT_REDIS", &model.NovaSprintRedis}, {"NOVA_SPRINT_SERVER", &model.NovaSprintServer}, {"NOVA_BUS_REDIS", &model.NovaBusRedis}} {
		if v, ok := profile.Environment[e.key]; ok {
			*e.slot = sp(v)
		}
	}
	in, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("the seat environment could not be encoded (%v)", err)
	}
	var b wire.SeatEnvironmentBuilder
	var r wire.TableReport
	if !b.Init() || !wire.SeatEnvironmentFromJson(&b, in, &r) || !b.Lock() {
		if reason := r.Reason; reason != "" {
			return nil, fmt.Errorf("SEAT_ENVIRONMENT REFUSED: %s; run: nova-up -h", reason)
		}
		return nil, fmt.Errorf("the seat environment could not be encoded")
	}
	v := b.AsConst()
	if reason := seatEnvironmentWriteBound(v); reason != "" {
		return nil, fmt.Errorf("SEAT_ENVIRONMENT REFUSED: %s; run: nova-up -h", reason)
	}
	n := wire.SeatEnvironmentToJsonMeasure(v)
	text := make([]byte, n)
	if wire.SeatEnvironmentToJson(v, text) != n {
		return nil, fmt.Errorf("the seat environment could not be encoded")
	}
	return append(text, '\n'), nil
}

// seatEnvironmentWriteBound is the table's write-side bounds
// (pkg/schemaread): version is present and is 1, and a present login names
// every reference. It returns the named refusal reason, or "" when the table
// holds the value.
func seatEnvironmentWriteBound(v *wire.SeatEnvironment) string {
	if !v.VersionPresent {
		return "absent_required (version)"
	}
	if v.Version != 1 {
		return "clamped (version)"
	}
	if l := wire.SeatLoginAt(&v.Login); l != nil {
		for _, f := range []struct {
			name string
			slot *int64
		}{{"redis", &l.Redis}, {"user", &l.User}, {"store", &l.Store}, {"as", &l.AsName}, {"key", &l.Key}, {"sops", &l.Sops}, {"secret", &l.Secret}} {
			if *f.slot == 0 {
				return "absent_required (login." + f.name + ")"
			}
		}
	}
	return ""
}

func sp(s string) *string { return &s }
func ip(v int64) *int64 { return &v }

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
