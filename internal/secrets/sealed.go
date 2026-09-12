package secrets

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"filippo.io/age"
)

// THE SEALED STORE.
//
// The store holds CIPHERTEXT ONLY: one `<name>.age` file per secret, sealed to one or
// more X25519 public keys. Nothing in the store is a secret, so the store's own
// permissions are not what keeps a value: the PRIVATE KEY FILE is. Certainty here is
// exactly as strong as who can read that file, and no stronger — docs/SPEC-SECRETS.md
// says it in those words, because a store that implied more would be worse than one that
// implied nothing.
//
// What follows from that, and is the reason for this shape rather than a keychain:
//
//   - SEALING NEEDS ONLY PUBLIC KEYS. A person can seal a secret FOR a line without
//     holding anything that line holds, and without seeing any other secret that line
//     can read. `put` therefore never takes a key path.
//   - OPENING NEEDS THE PRIVATE KEY, and there is no default path to it, ever. It comes
//     from `--key` or NOVA_SECRETS_KEY and from nowhere else, so no process opens a
//     secret by accident of where it happened to be run.
//   - A RECOVERY RECIPIENT IS JUST ANOTHER `--to`. A secret sealed to a line's key and a
//     recovery key survives the loss of the line.

// Directory and file modes. 0700 and 0600 are not defaults, they are the statement: even
// though the store holds only ciphertext, nothing here widens a bit for anybody.
const (
	StoreMode os.FileMode = 0o700
	EntryMode os.FileMode = 0o600
	KeyMode   os.FileMode = 0o600
)

// StoreDirName is the directory the store gets inside the config directory.
const StoreDirName = "nova-secrets"

// KeyEnv is the one environment variable that may name the private key. There is no
// default path and no search: an absent --key and an absent KeyEnv is a refusal.
const KeyEnv = "NOVA_SECRETS_KEY"

// Ext is the suffix every store entry carries. It is in the name on disk so that a
// person listing the directory sees ciphertext and knows it.
const Ext = ".age"

// MaxNameLen bounds a name so a ref cannot push a path past what a filesystem takes and
// get truncated into a DIFFERENT entry.
const MaxNameLen = 128

// MaxValueBytes bounds what `put` will read from stdin. A credential is small; a pipe
// that is not a credential is a mistake, and a mistake that is allowed to be a gigabyte
// is a mistake that fills a disk with something nobody can read back.
const MaxValueBytes = 64 << 10

// ---------------------------------------------------------------- names

// BadName returns why a name is unacceptable, or "" if it is fine.
//
// An ALLOW-LIST, not a deny-list of "/" and "..": a deny-list is the version that gets
// defeated by the character nobody thought of, and this string becomes a path. A name is
// one or more segments joined by "/", so `github/rowan-claude` is one name and two path
// segments; each segment is letters, digits and - _ . @ + and may not begin with a dot.
func BadName(name string) string {
	if name == "" {
		return "the name is empty; a stored secret has no unnamed form"
	}
	if len(name) > MaxNameLen {
		return "the name is longer than " + strconv.Itoa(MaxNameLen) +
			" characters; a truncated name is a different entry"
	}
	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return "the name begins or ends with a slash; a name is segments, not a path"
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" {
			return "the name has an empty segment; a name is segments joined by one slash"
		}
		if strings.HasPrefix(seg, ".") {
			return "a segment of the name starts with a dot; that is a relative path or a " +
				"hidden file, not a name"
		}
		for i := 0; i < len(seg); i++ {
			c := seg[i]
			ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
				c == '-' || c == '_' || c == '.' || c == '@' || c == '+'
			if !ok {
				return "the name contains a character that is not allowed (letters, digits, " +
					"- _ . @ + and / between segments only)"
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------- the store

// Store is one directory of sealed secrets.
type Store struct{ Dir string }

// ResolveStore answers where the store is, and REFUSES rather than inventing a path.
//
// The order is: --store, then $XDG_CONFIG_HOME/nova-secrets, then $HOME/.config/
// nova-secrets. The fallback that is deliberately NOT here is the working directory: a
// store that moves with the shell is a different store on every invocation, and a `put`
// into it would report success while placing the entry somewhere nobody will look again.
func ResolveStore(flagDir string) (Store, string) {
	if flagDir != "" {
		if !filepath.IsAbs(flagDir) {
			abs, err := filepath.Abs(flagDir)
			if err != nil {
				return Store{}, "the store path could not be made absolute: " + errKind(err)
			}
			flagDir = abs
		}
		return Store{Dir: flagDir}, ""
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		if !filepath.IsAbs(d) {
			return Store{}, "XDG_CONFIG_HOME is set to a relative path; it must be absolute, " +
				"because a store that moves with the working directory is a different store each time"
		}
		return Store{Dir: filepath.Join(d, StoreDirName)}, ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return Store{}, "this process has no home directory and neither --store nor " +
			"XDG_CONFIG_HOME is set, so there is nowhere this bench's store could be — " +
			"refusing to guess a path"
	}
	return Store{Dir: filepath.Join(home, ".config", StoreDirName)}, ""
}

// PathOf is the entry's file path, or the reason the name is not one.
func (s Store) PathOf(name string) (string, string) {
	if why := BadName(name); why != "" {
		return "", why
	}
	if s.Dir == "" {
		return "", "the store directory is empty; refusing to guess a path"
	}
	return filepath.Join(s.Dir, filepath.FromSlash(name)+Ext), ""
}

// ---------------------------------------------------------------- keygen

// Keygen mints an X25519 identity, writes the private half to path with O_EXCL and 0600,
// and returns the PUBLIC key.
//
// O_EXCL is the refusal, and it is the kernel's rather than a Stat-then-write this
// function could lose a race inside: overwriting a key file destroys every secret sealed
// to it, and that is not an outcome a flag should be able to ask for by accident.
func Keygen(path string) (pub string, why string) {
	if path == "" {
		return "", "no key path was given; refusing to guess where a private key should live"
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", "could not generate a key: " + errKind(err)
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, StoreMode); err != nil {
			return "", "could not make the key's directory: " + errKind(err)
		}
	}
	fh, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, KeyMode)
	if err != nil {
		if os.IsExist(err) {
			return "", "a file is already at that path; refusing to overwrite a private key, " +
				"because every secret sealed to it would become unreadable"
		}
		return "", "could not create the key file: " + errKind(err)
	}
	// Chmod after create: open(2) only ever NARROWS the mode by the umask, so 0600 here
	// is at most 0600 — but a bench running under a narrowing umask would be left with a
	// key file whose mode is not the mode this package states, and a mode a later audit
	// has to argue with is a mode worth setting exactly.
	if err := fh.Chmod(KeyMode); err != nil {
		_ = fh.Close()
		_ = os.Remove(path)
		return "", "could not set 0600 on the key file: " + errKind(err)
	}
	// The private key is written to its own file and to nothing else. It is never
	// returned from this function, so no caller can print it.
	_, werr := io.WriteString(fh, id.String()+"\n")
	cerr := fh.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return "", "the key file could not be written: " + errKind(werr)
	}
	return id.Recipient().String(), ""
}

// LoadIdentity reads a private key file. The value it returns is an age identity, never
// a string, and the file's contents are not put in any message this function produces.
func LoadIdentity(path string) (*age.X25519Identity, string) {
	if path == "" {
		return nil, "no private key was named: pass --key <path> or set " + KeyEnv +
			" (there is no default path, on purpose)"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "there is no key file at that path; mint one with: nova-secrets keygen --key <path>"
		}
		return nil, "the key file could not be read: " + errKind(err)
	}
	// Only the identity line is parsed, and a parse failure never quotes the file.
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, err := age.ParseX25519Identity(line)
		if err != nil {
			return nil, "that file does not hold an age X25519 private key"
		}
		return id, ""
	}
	return nil, "the key file is empty"
}

// ParseRecipients turns --to values into recipients, refusing the whole set if any one is
// not a public key. Partial acceptance would seal a secret to fewer readers than the
// caller asked for and report success.
func ParseRecipients(tos []string) ([]age.Recipient, string) {
	if len(tos) == 0 {
		return nil, "no recipient was given: a secret sealed to nobody can be read by nobody — " +
			"pass --to <age1...> at least once"
	}
	var out []age.Recipient
	seen := map[string]bool{}
	for _, t := range tos {
		t = strings.TrimSpace(t)
		r, err := age.ParseX25519Recipient(t)
		if err != nil {
			return nil, "one of the --to values is not an age X25519 public key (they begin `age1`)"
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, r)
	}
	return out, ""
}

// ---------------------------------------------------------------- seal

// Placement says what a Seal did. Three words rather than a bool, because "it is there
// now because of me" and "it was already there" lead to opposite acts.
type Placement string

const (
	PlacementDone     Placement = "PLACED"
	PlacementReplaced Placement = "REPLACED"
	PlacementOccupied Placement = "OCCUPIED"
)

// Seal encrypts s to recipients and writes the store entry. Without replace, an existing
// entry is OCCUPIED and nothing is written and nothing is overwritten.
func (st Store) Seal(ctx context.Context, name string, s Secret, recipients []age.Recipient, replace bool) (Placement, string) {
	if err := ctx.Err(); err != nil {
		return PlacementOccupied, "the store was not written: " + errKind(err)
	}
	if !s.Loaded() {
		return PlacementOccupied, "nothing was read, so there is no value to seal — " +
			"an unloaded Secret is not an empty one"
	}
	if len(recipients) == 0 {
		return PlacementOccupied, "no recipient: refusing to write a file nobody can open"
	}
	p, why := st.PathOf(name)
	if why != "" {
		return PlacementOccupied, why
	}
	if err := os.MkdirAll(filepath.Dir(p), StoreMode); err != nil {
		return PlacementOccupied, "could not make the store directory: " + errKind(err)
	}
	// The ciphertext is built IN MEMORY first. The alternative — stream into the entry
	// file — leaves a half-written entry on any error, and a half-written ciphertext
	// reads as a corrupt secret rather than an absent one.
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipients...)
	if err != nil {
		return PlacementOccupied, "could not start the encryption: " + errKind(err)
	}
	if err := s.Use(func(v string) error { _, e := io.WriteString(w, v); return e }); err != nil {
		return PlacementOccupied, "could not encrypt the value: " + errKind(err)
	}
	if err := w.Close(); err != nil {
		return PlacementOccupied, "could not finish the encryption: " + errKind(err)
	}

	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	placement := PlacementDone
	if replace {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		if _, err := os.Stat(p); err == nil {
			placement = PlacementReplaced
		}
	}
	fh, err := os.OpenFile(p, flags, EntryMode)
	if err != nil {
		if os.IsExist(err) {
			return PlacementOccupied, "an entry named " + name + " is already in this store; " +
				"pass --replace to seal over it deliberately"
		}
		return PlacementOccupied, "could not create the store entry: " + errKind(err)
	}
	if err := fh.Chmod(EntryMode); err != nil {
		_ = fh.Close()
		return PlacementOccupied, "could not set 0600 on the store entry: " + errKind(err)
	}
	_, werr := fh.Write(buf.Bytes())
	cerr := fh.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		if placement == PlacementDone {
			_ = os.Remove(p)
		}
		return PlacementOccupied, "the store entry could not be written: " + errKind(werr)
	}
	return placement, ""
}

// ---------------------------------------------------------------- open

// Presence is what a look in the store found. UNASKABLE and ABSENT are different facts
// and only the second is a reason to go seal one.
type Presence string

const (
	PresenceFound     Presence = "FOUND"
	PresenceAbsent    Presence = "ABSENT"
	PresenceUnaskable Presence = "UNASKABLE"
)

// Open decrypts one entry IN MEMORY with the given identity.
//
// A wrong key fails CLOSED: the reason says the key does not open the entry, and it never
// carries a byte of the ciphertext or of any partial plaintext.
func (st Store) Open(ctx context.Context, name string, id *age.X25519Identity) (Secret, Presence, string) {
	if err := ctx.Err(); err != nil {
		return Secret{}, PresenceUnaskable, "the store was not read: " + errKind(err)
	}
	if id == nil {
		return Secret{}, PresenceUnaskable, "no private key was loaded, so nothing can be opened"
	}
	p, why := st.PathOf(name)
	if why != "" {
		return Secret{}, PresenceUnaskable, why
	}
	ct, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Secret{}, PresenceAbsent, "no secret named " + name + " is in this store"
		}
		return Secret{}, PresenceUnaskable, "the store entry could not be read: " + errKind(err)
	}
	r, err := age.Decrypt(bytes.NewReader(ct), id)
	if err != nil {
		return Secret{}, PresenceUnaskable, "this key does not open " + name +
			"; it was sealed to other recipients"
	}
	var out bytes.Buffer
	if _, err := io.Copy(&out, io.LimitReader(r, MaxValueBytes+1)); err != nil {
		return Secret{}, PresenceUnaskable, "the sealed value could not be read: " + errKind(err)
	}
	if out.Len() > MaxValueBytes {
		return Secret{}, PresenceUnaskable, "the sealed value is larger than " +
			strconv.Itoa(MaxValueBytes) + " bytes; this tool stores credentials, not files"
	}
	return NewSecret(out.String()), PresenceFound, ""
}

// Check answers presence WITHOUT a key. A person can ask whether a secret is in place
// without being able to read it, which is the question a setup routine actually has.
func (st Store) Check(name string) (Presence, string) {
	p, why := st.PathOf(name)
	if why != "" {
		return PresenceUnaskable, why
	}
	info, err := os.Stat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return PresenceAbsent, ""
		}
		return PresenceUnaskable, "the store entry could not be checked: " + errKind(err)
	}
	if info.IsDir() {
		return PresenceUnaskable, "a directory is where that entry's file should be"
	}
	return PresenceFound, ""
}

// Delete removes one entry. It returns whether an entry was there to remove.
func (st Store) Delete(name string) (bool, string) {
	p, why := st.PathOf(name)
	if why != "" {
		return false, why
	}
	err := os.Remove(p)
	if err == nil {
		return true, ""
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, ""
	}
	return false, "the store entry could not be removed: " + errKind(err)
}

// Names lists what the store holds. NEVER a value, and it opens nothing: names come from
// the directory, so a line with no key can still say what is in place.
func (st Store) Names() ([]string, string) {
	if st.Dir == "" {
		return nil, "the store directory is empty; refusing to guess a path"
	}
	var names []string
	err := filepath.WalkDir(st.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == st.Dir {
				return io.EOF // an absent store holds no names; that is not a failure
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, Ext) {
			return nil
		}
		rel, rerr := filepath.Rel(st.Dir, p)
		if rerr != nil {
			return rerr
		}
		names = append(names, strings.TrimSuffix(filepath.ToSlash(rel), Ext))
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, "the store could not be listed: " + errKind(err)
	}
	sort.Strings(names)
	return names, ""
}

// RecipientCount counts the `->` stanzas in an entry's age header: how many keys can open
// it, and NOTHING about which ones. The header is public by construction, so this needs
// no private key — and it deliberately does not print the recipients themselves, because
// a recipient list is a map of who holds what.
func (st Store) RecipientCount(name string) (int, string) {
	p, why := st.PathOf(name)
	if why != "" {
		return 0, why
	}
	fh, err := os.Open(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, "no secret named " + name + " is in this store"
		}
		return 0, "the store entry could not be read: " + errKind(err)
	}
	defer func() { _ = fh.Close() }()
	n := 0
	sc := bufio.NewScanner(io.LimitReader(fh, 64<<10))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "---") {
			break // the header's end; the body is ciphertext and is not read here
		}
		if strings.HasPrefix(line, "-> ") {
			n++
		}
	}
	if err := sc.Err(); err != nil {
		return 0, "the store entry's header could not be read: " + errKind(err)
	}
	if n == 0 {
		return 0, "that file has no age recipient stanzas; it is not a sealed secret"
	}
	return n, ""
}

// ---------------------------------------------------------------- reading a value in

// ReadValue reads one value from r: at most one line, with exactly one trailing newline
// stripped if it is there.
//
// ONE newline, not TrimSpace: `printf %s` and a heredoc differ by exactly one byte, and a
// token whose last character is a space is a token this tool must not quietly change. A
// second line is a REFUSAL rather than a truncation, because silently keeping the first
// line of a two-line paste stores half a credential that fails at the far end.
func ReadValue(r io.Reader) (Secret, string) {
	b, err := io.ReadAll(io.LimitReader(r, MaxValueBytes+1))
	if err != nil {
		return Secret{}, "the value could not be read from stdin: " + errKind(err)
	}
	if len(b) > MaxValueBytes {
		return Secret{}, "the value on stdin is larger than " + strconv.Itoa(MaxValueBytes) +
			" bytes; this tool stores credentials, not files"
	}
	v := string(b)
	v = strings.TrimSuffix(v, "\n")
	if strings.Contains(v, "\n") {
		return Secret{}, "the value on stdin is more than one line; a credential is one line, " +
			"and storing the first line of a paste would store half a credential"
	}
	if v == "" {
		return Secret{}, "the value on stdin is empty; an empty entry is not a credential"
	}
	return NewSecret(v), ""
}

// errKind returns an error's CLASS, never its subject. os errors carry the path they
// failed on, which is fine, but a wrapped error from a library this package does not own
// can carry anything, so only the kind crosses this line.
func errKind(err error) string {
	switch {
	case err == nil:
		return "no error"
	case errors.Is(err, context.Canceled):
		return "the call was cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "the call ran out of time"
	case errors.Is(err, fs.ErrNotExist):
		return "it does not exist"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	case errors.Is(err, fs.ErrExist):
		return "it already exists"
	default:
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return pe.Op + " failed on " + pe.Path
		}
		return "the operation failed"
	}
}
