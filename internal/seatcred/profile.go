package seatcred

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// A seat profile (nova-tools#4330) is one row of a tool's seats.tsv: the seat
// is a fact of the machine, not of the shell, so the fleet play writes where
// its Redis is, the ACL user, which key of the seat's file holds that user's
// password, and the store and age key that open the file. A tool given
// --seat <name> reads the row and needs no wrapper script to reach Redis.

// ProfileFile is the profile's name under $XDG_CONFIG_HOME/<tool>/.
const ProfileFile = "seats.tsv"

// ProfileColumns names the tab-separated columns, in order; a refusal about a
// row prints it so the fix needs no doc. The seventh, the GitHub token env, is
// optional: a six-column row is the #4330 row, and its GitHub verbs read
// GH_TOKEN from the session as before.
const ProfileColumns = "name, redis addr, redis user, secret env, store, key[, github token env]"

// Profile is one seats.tsv row.
type Profile struct {
	Name      string // the seat --seat names
	Addr      string // host:port of the seat's Redis
	User      string // the Redis ACL user
	SecretEnv string // the key of the seat's file that holds User's password
	Store     string // the nova-secrets store directory
	Key       string // the age key that opens the seat's file; <as>.key names the file <as>.yaml
	GitHubEnv string // optional seventh column: the key of the seat's file that holds its GitHub token, "" when none
}

// ProfilePath is $XDG_CONFIG_HOME/<tool>/seats.tsv, else
// $HOME/.config/<tool>/seats.tsv (the XDG default).
func ProfilePath(tool string, getenv func(string) string) (string, error) {
	if x := getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, tool, ProfileFile), nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", fmt.Errorf("seat profile: HOME and XDG_CONFIG_HOME are unset, so %s/%s has no place", tool, ProfileFile)
	}
	return filepath.Join(home, ".config", tool, ProfileFile), nil
}

var secretEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// ErrNoProfileRow is wrapped by LoadProfile's refusal when the file has no
// row for the seat (or there is no file).
var ErrNoProfileRow = errors.New("no row")

// LoadProfile reads path and returns the row named seat. A missing file or
// row is an error wrapping ErrNoProfileRow that names the file and its
// columns; a malformed row is an error naming file:line, even when it is not
// the seat's row, since the fleet play wrote the whole file. Blank lines and
// lines starting with # are skipped. A leading ~/ in store or key is home.
func LoadProfile(path, seat, home string) (Profile, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Profile{}, fmt.Errorf("seat %s: %w: %s does not exist; the fleet play writes it, one tab-separated row per seat (%s)", seat, ErrNoProfileRow, path, ProfileColumns)
	}
	if err != nil {
		return Profile{}, fmt.Errorf("seat %s: cannot read %s: %v", seat, path, err)
	}
	defer f.Close()
	var found *Profile
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		p, err := parseProfileRow(line, home)
		if err != nil {
			return Profile{}, fmt.Errorf("seat profile %s:%d: %v; a row is six or seven tab-separated columns (%s)", path, n, err, ProfileColumns)
		}
		if p.Name == seat {
			if found != nil {
				return Profile{}, fmt.Errorf("seat profile %s:%d: a second row for seat %s; keep one", path, n, seat)
			}
			found = &p
		}
	}
	if err := sc.Err(); err != nil {
		return Profile{}, fmt.Errorf("seat %s: cannot read %s: %v", seat, path, err)
	}
	if found == nil {
		return Profile{}, fmt.Errorf("seat %s: %w for it in %s; add one tab-separated row (%s)", seat, ErrNoProfileRow, path, ProfileColumns)
	}
	return *found, nil
}

func parseProfileRow(line, home string) (Profile, error) {
	cols := strings.Split(line, "\t")
	if len(cols) != 6 && len(cols) != 7 {
		return Profile{}, fmt.Errorf("%d columns, want 6 or 7", len(cols))
	}
	for i := range cols {
		cols[i] = strings.TrimSpace(cols[i])
	}
	p := Profile{Name: cols[0], Addr: cols[1], User: cols[2], SecretEnv: cols[3], Store: tilde(cols[4], home), Key: tilde(cols[5], home)}
	if len(cols) == 7 {
		p.GitHubEnv = cols[6]
		if !secretEnvName.MatchString(p.GitHubEnv) {
			return Profile{}, fmt.Errorf("github token env %q must match [A-Z_][A-Z0-9_]*", p.GitHubEnv)
		}
	}
	if !secrets.IsValidAsName(p.Name) {
		return Profile{}, fmt.Errorf("name %q must match [A-Za-z0-9_-]+", p.Name)
	}
	if h, port, err := net.SplitHostPort(p.Addr); err != nil || h == "" || port == "" {
		return Profile{}, fmt.Errorf("redis addr %q is not host:port", p.Addr)
	}
	if p.User == "" || strings.ContainsAny(p.User, " ") {
		return Profile{}, fmt.Errorf("redis user %q is empty or has a space", p.User)
	}
	if !secretEnvName.MatchString(p.SecretEnv) {
		return Profile{}, fmt.Errorf("secret env %q must match [A-Z_][A-Z0-9_]*", p.SecretEnv)
	}
	if !filepath.IsAbs(p.Store) {
		return Profile{}, fmt.Errorf("store %q is not an absolute path (or ~/...)", p.Store)
	}
	if !filepath.IsAbs(p.Key) || !strings.HasSuffix(p.Key, ".key") || !secrets.IsValidAsName(p.AsName()) {
		return Profile{}, fmt.Errorf("key %q is not an absolute <seat>.key path (or ~/...)", p.Key)
	}
	return p, nil
}

func tilde(p, home string) string {
	if home != "" && (p == "~" || strings.HasPrefix(p, "~/")) {
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

// AsName is the seat's name in the store: the key's file name without .key,
// the fleet layout (<store>/<as>.yaml opened by <keydir>/<as>.key).
func (p Profile) AsName() string {
	return strings.TrimSuffix(filepath.Base(p.Key), ".key")
}

// ResolveProfile opens the row's seat file through secrets.OpenSeatFile --
// the store, key and checks nova-secrets exec uses -- and returns the row's
// login: User with the password held under SecretEnv, and, when the row names
// a GitHub token env, the token held under it (a file without it is GitHubErr,
// refused only when a GitHub verb asks). sops is SopsEnv's, else the one on
// PATH. A refusal names the seat, the row's file and the remedy, never a
// value.
func ResolveProfile(p Profile, getenv func(string) string) (Cred, error) {
	sops := getenv(SopsEnv)
	if sops == "" {
		s, err := exec.LookPath("sops")
		if err != nil {
			return Cred{}, fmt.Errorf("seat %s: no sops on PATH; install it or set %s", p.Name, SopsEnv)
		}
		sops = s
	}
	sf, err := secrets.OpenSeatFile(p.Store, p.AsName(), p.Key, sops)
	if err != nil {
		return Cred{}, fmt.Errorf("seat %s: %w", p.Name, err)
	}
	if pw, ok := sf.Secrets[p.SecretEnv]; ok && pw.Loaded() && !pw.Empty() {
		c := Cred{Seat: p.Name, User: p.User, Key: p.SecretEnv, Password: pw, GitHubKey: p.GitHubEnv}
		if p.GitHubEnv != "" {
			if tok, ok := sf.Secrets[p.GitHubEnv]; ok && tok.Loaded() && !tok.Empty() {
				c.GitHub = tok
			} else {
				c.GitHubErr = fmt.Errorf("seat %s: %s holds no %s, the GitHub token its seats.tsv row names; seal it with nova-secrets seal --as %s --name %s", p.Name, sf.Path, p.GitHubEnv, p.AsName(), p.GitHubEnv)
			}
		}
		return c, nil
	}
	return Cred{}, fmt.Errorf("seat %s: %s holds no %s; seal it with nova-secrets seal --as %s --name %s", p.Name, sf.Path, p.SecretEnv, p.AsName(), p.SecretEnv)
}
