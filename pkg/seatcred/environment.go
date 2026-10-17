package seatcred

// The optional seat environment is data, never shell input. Its v1 contract is
// docs/SPEC-UP.md, "The seat environment".
// Libraries considered: encoding/json supplies decoding, but its decoder accepts
// case-folded struct field names and duplicate object keys; a short token walk
// therefore enforces this exact schema before decoding. pkg/readregular follows
// symlinks and includes the path in errors, while this reader refuses them and
// keeps its diagnostics value-free, so it uses os.Lstat/open/SameFile directly.
// pkg/schemaread is the one strict read of the table forms (the seatrec
// SeatEnvironment table of pkg/seatrecwire); the legacy JSON object keeps its
// own walk, held as one of those forms (S2, C13).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	wire "github.com/mas-bandwidth/nova-tools/pkg/seatrecwire"
)

const (
	// EnvironmentFile is the versioned per-user seat environment record.
	EnvironmentFile = "seat-environment.json"
	EnvironmentDir  = "nova-sprint"
	ForgeConfigEnv  = "GH_CONFIG_DIR"
)

// SeatEnvironment is the non-secret v1 seat record. Login contains references
// only; its password remains in nova-secrets.
type SeatEnvironment struct {
	Version       int               `json:"version"`
	ForgeIdentity string            `json:"forge_identity,omitempty"`
	Environment   map[string]string `json:"environment,omitempty"`
	Login         *SeatLogin        `json:"login,omitempty"`
}

// SeatLogin names a complete store login without holding its password.
type SeatLogin struct {
	Redis  string `json:"redis"`
	User   string `json:"user"`
	Store  string `json:"store"`
	As     string `json:"as"`
	Key    string `json:"key"`
	Sops   string `json:"sops"`
	Secret string `json:"secret"`
}

// SeatEnvironmentPath follows the seat environment's XDG, then HOME location
// (docs/SPEC-UP.md, "The seat environment").
func SeatEnvironmentPath(getenv func(string) string) (string, error) {
	if x := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, EnvironmentDir, EnvironmentFile), nil
	}
	if home := getenv("HOME"); filepath.IsAbs(home) {
		return filepath.Join(home, ".config", EnvironmentDir, EnvironmentFile), nil
	}
	return "", errors.New("seat environment has no absolute XDG_CONFIG_HOME or HOME")
}

// LoadSeatEnvironment reads and validates one private v1 seat record. Errors
// name no input values or file contents (docs/SPEC-UP.md, "The seat environment").
// Every form reads through the package's one SeatEnvironmentReader, counted
// per form (S2, C13).
func LoadSeatEnvironment(path string) (SeatEnvironment, error) {
	return seatEnvironmentReader.Load(path)
}

// seatEnvironmentReader is the one seat environment reader of the package; its
// Counts are the per-form read counts (S2, C13).
var seatEnvironmentReader = NewSeatEnvironmentReader()

// SeatEnvironmentReader reads the seat environment in every form (the owner's
// S2, the reader-first order C13): the binary image of the seatrec
// SeatEnvironment table, the padded standard base64 of that image, the schema
// JSON text of the table, and the legacy JSON object every writer writes
// today. Each form reads to one equal SeatEnvironment, is held to every rule
// the legacy record is held to, and a damaged value is the named refusal
// (pkg/schemaread), never an absent, empty or zero record. One read at a
// time: the legacy cause is the read's own state.
type SeatEnvironmentReader struct {
	// Counts are the per-form read counts and the refusal tallies.
	Counts *schemaread.Counts

	r     *schemaread.Reader[seatDoc]
	mu    sync.Mutex
	cause error
}

// The refusal prefix and the command a refusal points at.
const (
	seatEnvironmentRecord = "SEAT_ENVIRONMENT"
	seatEnvironmentHelp   = "nova-up -h"
	// seatUnknownField refuses an unknown field in every form: the v1 record
	// is closed (environmentKeyAllowed), so a field no reader names is damage,
	// never a newer writer's addition read past.
	seatUnknownField = "unknown_field"
)

// seatDoc is one read: the table value and the region that holds it for the
// table forms, and the record every form reads to.
type seatDoc struct {
	v      *wire.SeatEnvironment
	region []byte
	rec    SeatEnvironment
}

// NewSeatEnvironmentReader is a seat environment reader with empty counters.
func NewSeatEnvironmentReader() *SeatEnvironmentReader {
	s := &SeatEnvironmentReader{}
	s.r = schemaread.NewReader(&schemaread.Codec[seatDoc]{
		Name:  seatEnvironmentRecord,
		Help:  seatEnvironmentHelp,
		Reset: func(d *seatDoc) { *d = seatDoc{} },
		Measure: func(w []byte) error {
			_, err := wire.SeatEnvironmentLoadMeasureReason(w)
			return err
		},
		LoadWire:    s.loadWire,
		LoadJSON:    s.loadJSON,
		Legacy:      seatEnvironmentLegacy,
		LoadLegacy:  s.loadLegacy,
		Constraints: seatEnvironmentConstraints,
	})
	s.Counts = s.r.Counts()
	return s
}

// Load reads one private seat record from path in any form, after the
// ownership and mode checks of docs/SPEC-UP.md, "The seat environment".
func (s *SeatEnvironmentReader) Load(path string) (SeatEnvironment, error) {
	var zero SeatEnvironment
	info, err := os.Lstat(path)
	if err != nil {
		return zero, errors.New("seat environment is unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !seatEnvironmentOwner(info) {
		return zero, errors.New("seat environment is not a private file owned by this user")
	}
	dir, err := os.Lstat(filepath.Dir(path))
	if err != nil || !dir.IsDir() || dir.Mode().Perm()&0022 != 0 || !seatEnvironmentOwner(dir) {
		return zero, errors.New("seat environment directory is writable by others or is not owned by this user")
	}
	f, err := os.Open(path)
	if err != nil {
		return zero, errors.New("seat environment is unavailable")
	}
	defer func() { _ = f.Close() }() // ignored: closing a file opened only for reading
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return zero, errors.New("seat environment changed while opening")
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return zero, errors.New("seat environment is unreadable")
	}
	return s.Read(b)
}

// Read reads one seat record's bytes in any form (S2, C13). A legacy record
// that fails a rule keeps the error it has always had, which also unwraps to
// the named refusal; a table form's refusal reads
// `seat environment is invalid: SEAT_ENVIRONMENT REFUSED: <reason> (<cause>); run: nova-up -h`.
func (s *SeatEnvironmentReader) Read(b []byte) (SeatEnvironment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cause = nil
	d, res, err := s.r.Read(b)
	if err == nil {
		return d.rec, nil
	}
	var refusal *schemaread.Refusal
	if !errors.As(err, &refusal) {
		return SeatEnvironment{}, fmt.Errorf("seat environment is invalid: %w", err)
	}
	if res.Form == schemaread.FormLegacy && s.cause != nil {
		return SeatEnvironment{}, &legacyRefusal{cause: s.cause, refusal: refusal}
	}
	if s.cause != nil && refusal.Field == "" {
		named := *refusal
		named.Field = s.cause.Error()
		refusal = &named
	}
	return SeatEnvironment{}, fmt.Errorf("seat environment is invalid: %w", refusal)
}

// legacyRefusal is a refused legacy record: its text is the cause the legacy
// reader has always given (value-free), and it unwraps to that cause and to
// the named refusal the read was counted under.
type legacyRefusal struct {
	cause   error
	refusal *schemaread.Refusal
}

func (e *legacyRefusal) Error() string   { return e.cause.Error() }
func (e *legacyRefusal) Unwrap() []error { return []error{e.cause, e.refusal} }

// seatEnvironmentLegacy is true when the input is the legacy JSON object. The
// legacy writer (nova-up --seat) and the schema JSON text are both JSON, so
// the form is decided by the object's shape, never by its whitespace: the
// schema JSON names the three endpoints as top-level fields (the generated
// writer writes each, null when absent), where the legacy record nests them
// under "environment". An object naming none of them is the legacy form; it
// reads to the same record either way. An object that is not JSON is the
// legacy form too, so its damage is named by the legacy walk.
func seatEnvironmentLegacy(in []byte) bool {
	t := bytes.TrimLeft(in, " \t\r\n")
	return len(t) > 0 && t[0] == '{' && !seatEnvironmentTableJSON(t)
}

// seatEnvironmentTableJSON reports whether the object's top level names a
// field only the schema JSON text has.
func seatEnvironmentTableJSON(t []byte) bool {
	d := json.NewDecoder(bytes.NewReader(t))
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return false
	}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return false
		}
		switch key {
		case "nova_sprint_redis", "nova_sprint_server", "nova_bus_redis":
			return true
		}
		var skip json.RawMessage
		if err := d.Decode(&skip); err != nil {
			return false
		}
	}
	return false
}

// loadLegacy reads the legacy record exactly as it has always been read: the
// key and null walks, a strict decode, version 1, then validate.
func (s *SeatEnvironmentReader) loadLegacy(d *seatDoc, b []byte) schemaread.Report {
	if err := validateEnvironmentKeys(b); err != nil {
		return s.refuse(fmt.Errorf("seat environment is invalid: %w", err))
	}
	if err := validateEnvironmentNulls(b); err != nil {
		return s.refuse(fmt.Errorf("seat environment is invalid: %w", err))
	}
	var record SeatEnvironment
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&record); err != nil || record.Version != 1 {
		return s.refuse(errors.New("seat environment is not a supported v1 record"))
	}
	if err := record.validate(); err != nil {
		return s.refuse(err)
	}
	d.rec = record
	return schemaread.Report{}
}

// refuse keeps the read's cause and names the refusal malformed.
func (s *SeatEnvironmentReader) refuse(cause error) schemaread.Report {
	s.cause = cause
	return schemaread.Report{Reason: schemaread.ReasonMalformed}
}

// loadWire loads the table's binary image; base64 reaches it decoded.
func (s *SeatEnvironmentReader) loadWire(d *seatDoc, w []byte) schemaread.Report {
	n, err := wire.SeatEnvironmentLoadMeasureReason(w)
	if err != nil || n < 0 {
		return schemaread.Report{Malformed: true}
	}
	var r wire.TableReport
	d.region = seatRegion(n)
	if d.v = wire.SeatEnvironmentLoad(d.region, w, &r); d.v == nil {
		return schemaread.Report{Malformed: true, Reason: r.Reason}
	}
	return s.table(d, &r)
}

// loadJSON loads the table's schema JSON text.
func (s *SeatEnvironmentReader) loadJSON(d *seatDoc, text []byte) schemaread.Report {
	var b wire.SeatEnvironmentBuilder
	var r wire.TableReport
	if !b.Init() || !wire.SeatEnvironmentFromJson(&b, text, &r) || !b.Lock() {
		return schemaread.Report{Malformed: true, Reason: r.Reason}
	}
	d.v, d.region = b.AsConst(), b.Region()
	return s.table(d, &r)
}

// table holds a loaded table value to the legacy record's rules: an unknown
// field is refused by name (the v1 record is closed), a duplicate JSON key is
// malformed, and the record it reads to passes validate.
func (s *SeatEnvironmentReader) table(d *seatDoc, r *wire.TableReport) schemaread.Report {
	rep := schemaread.Report{
		Unknown:      int(r.Unknown),
		KindMismatch: int(r.KindMismatch),
		Clamped:      int(r.Clamped),
		Malformed:    r.Malformed || r.Duplicate > 0,
		Reason:       r.Reason,
	}
	if rep.Unknown > 0 && rep.Reason == "" {
		s.cause = errors.New("unknown key")
		rep.Reason = seatUnknownField
	}
	if rep.Reason != "" || rep.Malformed || rep.KindMismatch > 0 || rep.Clamped > 0 {
		return rep
	}
	d.rec = seatEnvironmentOf(d.v)
	if err := d.rec.validate(); err != nil {
		return s.refuse(err)
	}
	return rep
}

// seatEnvironmentConstraints are the table's bounds the compiler cannot
// declare (the seatrec schema's SeatEnvironment.version): version is present
// and is 1, refused as absent or clamped; and every text is UTF-8 with no NUL.
// The legacy form is held by its own walk.
func seatEnvironmentConstraints(d *seatDoc, c *schemaread.Checker) {
	if d.v == nil {
		return
	}
	c.Required("version", d.v.VersionPresent)
	c.Bound("version", int64(d.v.Version), 1, 1)
	c.Text("forge_identity", d.rec.ForgeIdentity, math.MaxInt)
	for _, key := range []string{"NOVA_SPRINT_REDIS", "NOVA_SPRINT_SERVER", "NOVA_BUS_REDIS"} {
		c.Text("environment."+key, d.rec.Environment[key], math.MaxInt)
	}
	if l := d.rec.Login; l != nil {
		for _, f := range []struct{ name, value string }{
			{"redis", l.Redis}, {"user", l.User}, {"store", l.Store}, {"as", l.As},
			{"key", l.Key}, {"sops", l.Sops}, {"secret", l.Secret},
		} {
			c.Text("login."+f.name, f.value, math.MaxInt)
		}
	}
}

// seatEnvironmentOf is the table value as the record: a present endpoint is a
// key of Environment (even when empty, so validate refuses it as the legacy
// reader does), and a present login is a SeatLogin.
func seatEnvironmentOf(v *wire.SeatEnvironment) SeatEnvironment {
	rec := SeatEnvironment{Version: int(v.Version), ForgeIdentity: seatText(&v.ForgeIdentity)}
	for _, e := range []struct {
		key  string
		slot *int64
	}{{"NOVA_SPRINT_REDIS", &v.NovaSprintRedis}, {"NOVA_SPRINT_SERVER", &v.NovaSprintServer}, {"NOVA_BUS_REDIS", &v.NovaBusRedis}} {
		if *e.slot != 0 {
			if rec.Environment == nil {
				rec.Environment = map[string]string{}
			}
			rec.Environment[e.key] = seatText(e.slot)
		}
	}
	if l := wire.SeatLoginAt(&v.Login); l != nil {
		rec.Login = &SeatLogin{Redis: seatText(&l.Redis), User: seatText(&l.User), Store: seatText(&l.Store),
			As: seatText(&l.AsName), Key: seatText(&l.Key), Sops: seatText(&l.Sops), Secret: seatText(&l.Secret)}
	}
	return rec
}

// seatText is the text of one table string slot; a null slot is "".
func seatText(slot *int64) string {
	if *slot == 0 {
		return ""
	}
	v := wire.TableStringAt(slot)
	return string(v.Data[:v.Length])
}

// seatRegion is a 64-byte aligned region of n bytes for a table load.
func seatRegion(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

// validateEnvironmentNulls rejects explicit null values without exposing the
// input value in the returned error.
func validateEnvironmentNulls(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	var value func() error
	value = func() error {
		tok, err := d.Token()
		if err != nil {
			return errors.New("invalid JSON")
		}
		if tok == nil {
			return errors.New("null value")
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			for d.More() {
				if _, err := d.Token(); err != nil { // object key
					return errors.New("invalid JSON")
				}
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid JSON")
		}
		if _, err := d.Token(); err != nil { // closing delimiter
			return errors.New("invalid JSON")
		}
		return nil
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

// ReadSeatEnvironment returns no record when the per-user file is absent
// (docs/SPEC-UP.md, "The seat environment").
func ReadSeatEnvironment(getenv func(string) string) (SeatEnvironment, bool, error) {
	path, err := SeatEnvironmentPath(getenv)
	if err != nil {
		// A seat record is optional. When neither XDG_CONFIG_HOME nor HOME
		// identifies an absolute location, no record can be present.
		return SeatEnvironment{}, false, nil
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return SeatEnvironment{}, false, nil
	} else if err != nil {
		return SeatEnvironment{}, false, errors.New("seat environment is unavailable")
	}
	record, err := LoadSeatEnvironment(path)
	return record, err == nil, err
}

// ForgeConfigDir selects explicit GH_CONFIG_DIR before the seat's optional
// forge_identity reference. It never opens or lists that identity directory
// (docs/SPEC-UP.md, "The seat environment").
func ForgeConfigDir(getenv func(string) string) (string, error) {
	if explicit := getenv(ForgeConfigEnv); explicit != "" {
		return explicit, nil
	}
	record, ok, err := ReadSeatEnvironment(getenv)
	if err != nil || !ok {
		return "", err
	}
	return record.ForgeIdentity, nil
}

// ForgeCommandEnv adds the selected config directory to one child environment
// without changing the process environment. An explicit process value wins;
// otherwise an explicit child value wins
// (docs/SPEC-UP.md, "The seat environment").
func ForgeCommandEnv(env []string, getenv func(string) string) ([]string, error) {
	base := env
	if base == nil {
		base = os.Environ()
	}
	// The calling process's explicit identity is authoritative. If it is
	// unset, preserve the last value explicitly composed for this child,
	// including an empty value that asks gh to use its normal default.
	processValue := getenv(ForgeConfigEnv)
	selected, hasSelected := processValue, processValue != ""
	if !hasSelected {
		for _, entry := range base {
			if key, value, ok := strings.Cut(entry, "="); ok && key == ForgeConfigEnv {
				selected, hasSelected = value, true
			}
		}
	}
	dir := selected
	if !hasSelected {
		var err error
		dir, err = ForgeConfigDir(getenv)
		if err != nil {
			return env, err
		}
	}
	if dir == "" {
		return env, nil
	}
	out := make([]string, 0, len(base)+1)
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key != ForgeConfigEnv {
			out = append(out, entry)
		}
	}
	return append(out, ForgeConfigEnv+"="+dir), nil
}

var environmentSecretName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// validate enforces the v1 value restrictions from docs/SPEC-UP.md.
func (s SeatEnvironment) validate() error {
	for key, value := range s.Environment {
		switch key {
		case "NOVA_SPRINT_REDIS", "NOVA_SPRINT_SERVER", "NOVA_BUS_REDIS":
		default:
			return errors.New("seat environment names an unsupported default")
		}
		if !seatEndpoint(value) {
			return errors.New("seat environment has an invalid endpoint")
		}
	}
	if s.Login != nil {
		l := s.Login
		if !seatEndpoint(l.Redis) || l.User == "" || (StoreLogin{Store: l.Store, As: l.As, Key: l.Key, Sops: l.Sops, Secret: l.Secret}).missing() {
			return errors.New("seat environment has an incomplete store login")
		}
		for _, value := range []string{l.User, l.Store, l.As, l.Key, l.Sops, l.Secret} {
			if strings.ContainsAny(value, "\x00\r\n") {
				return errors.New("seat environment has an invalid store login reference")
			}
		}
	}
	if s.ForgeIdentity != "" {
		if !filepath.IsAbs(s.ForgeIdentity) {
			return errors.New("seat environment forge_identity must be an absolute directory")
		}
		info, err := os.Stat(s.ForgeIdentity)
		if err != nil || !info.IsDir() {
			return errors.New("seat environment forge_identity directory is unavailable")
		}
	}
	return nil
}

// StoreLogin reports whether one required reference is absent.
type StoreLogin struct{ Store, As, Key, Sops, Secret string }

// missing rejects an incomplete login reference from docs/SPEC-UP.md.
func (l StoreLogin) missing() bool {
	return strings.TrimSpace(l.Store) == "" || strings.TrimSpace(l.As) == "" || strings.TrimSpace(l.Key) == "" || strings.TrimSpace(l.Sops) == "" || !environmentSecretName.MatchString(l.Secret)
}

// ValidSeatEndpoint reports whether value is an accepted host:port endpoint.
// It rejects URL syntax and credential-bearing forms. Writers of the shared
// seat-environment.json contract should use this rather than maintaining a
// second endpoint parser.
func ValidSeatEndpoint(value string) bool {
	return seatEndpoint(value)
}

// seatEndpoint accepts an address and rejects credential-bearing URL syntax.
func seatEndpoint(value string) bool {
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

// validateEnvironmentKeys rejects duplicate and unknown keys in the v1 schema.
func validateEnvironmentKeys(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	var value func(string) error
	value = func(parent string) error {
		tok, err := d.Token()
		if err != nil {
			return errors.New("invalid JSON")
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return errors.New("invalid JSON")
				}
				name, ok := key.(string)
				if !ok {
					return errors.New("invalid JSON object key")
				}
				if seen[name] {
					return errors.New("duplicate key")
				}
				if !environmentKeyAllowed(parent, name) {
					return errors.New("unknown key")
				}
				seen[name] = true
				if err := value(name); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(parent); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid JSON")
		}
		if _, err := d.Token(); err != nil {
			return errors.New("invalid JSON")
		}
		return nil
	}
	if err := value(""); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

// environmentKeyAllowed holds the exact v1 object-key allowlists.
func environmentKeyAllowed(parent, key string) bool {
	switch parent {
	case "":
		return key == "version" || key == "forge_identity" || key == "environment" || key == "login"
	case "environment":
		return key == "NOVA_SPRINT_REDIS" || key == "NOVA_SPRINT_SERVER" || key == "NOVA_BUS_REDIS"
	case "login":
		switch key {
		case "redis", "user", "store", "as", "key", "sops", "secret":
			return true
		}
	}
	return false
}
