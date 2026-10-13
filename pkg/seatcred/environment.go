package seatcred

// The optional seat environment is data, never shell input. Its v1 contract is
// docs/SPEC-UP.md, "The seat environment".
// Libraries considered: encoding/json supplies decoding, but its decoder accepts
// case-folded struct field names and duplicate object keys; a short token walk
// therefore enforces this exact schema before decoding. pkg/readregular follows
// symlinks and includes the path in errors, while this reader refuses them and
// keeps its diagnostics value-free, so it uses os.Lstat/open/SameFile directly.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
func LoadSeatEnvironment(path string) (SeatEnvironment, error) {
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
	if err := validateEnvironmentKeys(b); err != nil {
		return zero, errors.New("seat environment is invalid")
	}
	if err := validateEnvironmentNulls(b); err != nil {
		return zero, errors.New("seat environment is invalid")
	}
	var record SeatEnvironment
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&record); err != nil || record.Version != 1 {
		return zero, errors.New("seat environment is not a supported v1 record")
	}
	if err := record.validate(); err != nil {
		return zero, err
	}
	return record, nil
}

// validateEnvironmentNulls keeps explicit null distinct from an omitted
// optional object or string. encoding/json otherwise treats null as an empty
// value for several non-pointer fields.
func validateEnvironmentNulls(b []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	for _, key := range []string{"forge_identity", "environment", "login"} {
		if raw, ok := fields[key]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return errors.New("null is not a seat environment value")
		}
	}
	return nil
}

// ReadSeatEnvironment returns no record when the per-user file is absent
// (docs/SPEC-UP.md, "The seat environment").
func ReadSeatEnvironment(getenv func(string) string) (SeatEnvironment, bool, error) {
	path, err := SeatEnvironmentPath(getenv)
	if err != nil {
		return SeatEnvironment{}, false, err
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

// seatEndpoint accepts an address and rejects credential-bearing URL syntax.
func seatEndpoint(value string) bool {
	return value != "" && !strings.ContainsAny(value, "@/?#\x00\r\n\t ") && !strings.Contains(value, "://")
}

// validateEnvironmentKeys rejects duplicate and unknown keys in the v1 schema.
func validateEnvironmentKeys(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	var value func(string) error
	value = func(parent string) error {
		tok, err := d.Token()
		if err != nil {
			return err
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
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] || !environmentKeyAllowed(parent, name) {
					return errors.New("duplicate or unknown key")
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
		_, err = d.Token()
		return err
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
