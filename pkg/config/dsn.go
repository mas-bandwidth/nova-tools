package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// The environment that names the config store: the DSN, the variable that
// holds its password, and the variable read when that one is not named.
// Every tool that reads nova-config (nova-config itself, nova-sprint fleet
// sync) resolves the store's address with ResolveDSN, so there is one set of
// address rules (docs/nova-config/README.md, "Connecting").
const (
	EnvPG          = "NOVA_PG_DSN"
	EnvPGPassEnv   = "NOVA_PG_PASSWORD_ENV"
	DefaultPassEnv = "NOVA_PG_PASSWORD"
)

// ResolveDSN is the Postgres DSN a tool dials: the flag, else NOVA_PG_DSN; a
// flag that carries a password is refused in every spelling the pgconn
// parser accepts (it would be on the command line, where a ps reads it);
// a DSN without one takes it from the variable
// NOVA_PG_PASSWORD_ENV names, NOVA_PG_PASSWORD when unset, and a named
// variable that is empty is refused with its name (the shape
// pkg/nsprint/redisauth keeps for Redis).
func ResolveDSN(flagValue string, getenv func(string) string) (string, error) {
	dsn := flagValue
	if dsn == "" {
		dsn = getenv(EnvPG)
	}
	if dsn == "" {
		return "", fmt.Errorf("--pg is required: postgres://user@host:5432/nova (or %s)", EnvPG)
	}
	// The boundary is the flag's own text, judged before any parse: the
	// flag's refusal quotes nothing, while a parse error quotes the DSN it
	// could not read, so a flag that carries a password must never reach
	// one. A flag whose text cannot be parsed at all is refused without echo
	// too, because the raw text may carry a credential in a spelling
	// flagCarriesPassword did not anticipate. A DSN from the environment is
	// not on a command line and is never refused for its password; when the
	// parser cannot read it the refusal names the variable and the wanted
	// shape and the pgconn error's kind, never its text: pgconn's own text
	// runs the raw connection string through a best-effort redactor that
	// malformed input defeats (a non-numeric port, spaces around a keyword's
	// '=', an unclosed quote) and leaves the password in the open. A password
	// pgconn takes from the process environment is never mistaken for one on
	// the line.
	if flagValue != "" && flagCarriesPassword(flagValue) {
		return "", refuseFlagPassword()
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		if flagValue != "" {
			return "", fmt.Errorf("--pg: cannot be read, so it is refused as a flag that carries a password; want postgres://user@host:5432/nova with no password, and export it as the variable %s names", EnvPGPassEnv)
		}
		return "", fmt.Errorf("%s could not be parsed; want postgres://user@host:5432/nova (%T)", EnvPG, err)
	}
	// The parsed config is the second look, after any parse: the lexical
	// check judges the flag's text, but net/url refuses a control byte the
	// pgconn URI reader accepts, so a password it reads from such a flag is
	// still on the command line and meets the same refusal, quoted by
	// nothing (docs/nova-config/README.md, "Connecting"). A password pgconn
	// takes from the process environment is refused by the same line: a
	// caller that puts one there hands the flag a ps can read, and the
	// environment path never reaches this check.
	if flagValue != "" && cfg.Password != "" {
		return "", refuseFlagPassword()
	}
	if cfg.Password != "" {
		return dsn, nil
	}
	name := getenv(EnvPGPassEnv)
	named := name != ""
	if !named {
		name = DefaultPassEnv
	}
	// The name is operator input too, and a password pasted where a variable
	// name belongs must not come back in a message: only a name of letters,
	// digits and underscores is quoted (docs/nova-config/README.md, "Connecting").
	if named && !isEnvName(name) {
		return "", fmt.Errorf("%s does not name a variable: want letters, digits and underscores only", EnvPGPassEnv)
	}
	pw := getenv(name)
	if pw == "" {
		if named {
			return "", fmt.Errorf("%s=%s but %s is empty; run under nova-secrets exec --only %s", EnvPGPassEnv, name, name, name)
		}
		return dsn, nil
	}
	return withPassword(dsn, cfg.User, pw)
}

// refuseFlagPassword is the one refusal for a flag that carries a password:
// it names the remedy and quotes nothing, because the DSN itself may hold
// the secret (docs/nova-config/README.md, "Connecting").
func refuseFlagPassword() error {
	return fmt.Errorf("--pg carries a password; leave it out and export it as the variable %s names (a ps reads the line)", EnvPGPassEnv)
}

// flagCarriesPassword reports whether the flag's DSN text names a password
// itself, in either spelling the pgconn parser accepts: a URI userinfo or
// query parameter, or the keyword form's password key. sslpassword, the
// passphrase for the SSL client key, is a secret the parser reads the same
// way and meets the same refusal. The same refusal holds the fleet kind's
// stored pg_dsn (checkFleet), whose query keys it matches case-insensitively;
// a password pgconn takes from the process environment is never seen here, so
// it is never mistaken for one on the line.
func flagCarriesPassword(dsn string) bool {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		// The pgconn URI reader takes the userinfo from the text before the
		// first "/", so a colon pair there is a password even where net/url
		// reads the text as a query.
		rest := dsn[strings.Index(dsn, "://")+len("://"):]
		if i := strings.IndexAny(rest, "@/"); i >= 0 && rest[i] == '@' {
			if _, _, has := strings.Cut(rest[:i], ":"); has {
				return true
			}
		}
		u, err := url.Parse(dsn)
		if err != nil {
			// net/url refuses a control byte the pgconn URI reader takes as
			// data, so the query is read here the way pgconn reads it: the
			// flag whose parse failed may still name an sslpassword.
			return uriQueryCarriesPassword(dsn)
		}
		if _, has := u.User.Password(); has {
			return true
		}
		// ignored: ParseQuery returns the pairs it read beside its error; a
		// pair it drops (a bad escape) still reaches the parse-error refusal
		// below, which quotes nothing.
		query, _ := url.ParseQuery(u.RawQuery)
		for key := range query {
			// The pgconn URI reader trims the keyword whitespace around a
			// query key before it reads it, so a padded key is the same
			// password field.
			if isPasswordKey(strings.Trim(key, kwSpaces)) {
				return true
			}
		}
		return false
	}
	return keywordCarriesPassword(dsn)
}

// uriQueryCarriesPassword reads the query of a URI the net/url parser refused,
// as the pgconn URI reader does: pairs split on "&", the key before the first
// "=", percent-decoded when it decodes and kept as written when it does not
// (pgconn then refuses the DSN, and ResolveDSN refuses it without quoting it).
// It reads keys only, so it holds no value and a message built from it has
// none to echo (docs/nova-config/README.md, "Connecting").
func uriQueryCarriesPassword(dsn string) bool {
	_, query, has := strings.Cut(dsn, "?")
	if !has {
		return false
	}
	for query != "" {
		var pair string
		pair, query, _ = strings.Cut(query, "&")
		key, _, _ := strings.Cut(pair, "=")
		if decoded, err := url.QueryUnescape(key); err == nil {
			key = decoded
		}
		if isPasswordKey(strings.Trim(key, kwSpaces)) {
			return true
		}
	}
	return false
}

// kwSpaces is the whitespace the keyword/value grammar knows, the set the
// pgconn parser trims and breaks values on.
const kwSpaces = " \t\n\r\v\f"

// isPasswordKey reports whether a connection key names a secret the pgconn
// parser reads from the flag: password, or sslpassword, the passphrase for
// the SSL client key. Both are on the command line where a ps reads them, so
// both meet the one refusal (docs/nova-config/README.md, "Connecting").
func isPasswordKey(key string) bool {
	return strings.EqualFold(key, "password") || strings.EqualFold(key, "sslpassword")
}

// keywordCarriesPassword walks the keyword/value DSN the way the pgconn
// parser walks it (the same whitespace set, quotes and backslash escapes),
// long enough to find a password key; it reads no values, so a value that
// quotes the word password is not one. A key is read before its value, so a
// password key is refused even when its value never closes; a key whose
// value fails to close is left to the pgconn parser, and ResolveDSN refuses
// a flag it cannot parse without quoting it.
func keywordCarriesPassword(dsn string) bool {
	s := strings.TrimLeft(dsn, kwSpaces)
	for len(s) > 0 {
		eqIdx := strings.IndexRune(s, '=')
		if eqIdx < 0 {
			return false // no more pairs: the pgconn parser refuses the DSN
		}
		key := strings.Trim(s[:eqIdx], kwSpaces)
		if strings.ContainsAny(key, kwSpaces) {
			return false // a keyword with whitespace in it: the pgconn parser refuses the DSN
		}
		if isPasswordKey(key) {
			return true
		}
		s = strings.TrimLeft(s[eqIdx+1:], kwSpaces)
		switch {
		case len(s) == 0:
		case s[0] == '\'':
			s = s[1:]
			end := 0
			for ; end < len(s); end++ {
				if s[end] == '\'' {
					break
				}
				if s[end] == '\\' {
					end++
					if end == len(s) {
						return false // an unterminated quoted value: the pgconn parser refuses the DSN
					}
				}
			}
			if end == len(s) {
				return false // an unterminated quoted value: the pgconn parser refuses the DSN
			}
			s = strings.TrimLeft(s[end+1:], kwSpaces)
		default:
			end := 0
			for ; end < len(s); end++ {
				if strings.ContainsRune(kwSpaces, rune(s[end])) {
					break
				}
				if s[end] == '\\' {
					end++ // a backslash escapes the byte after it
					if end == len(s) {
						break
					}
				}
			}
			s = strings.TrimLeft(s[end:], kwSpaces)
		}
	}
	return false
}

// withPassword puts the password into the DSN in memory, in whichever of
// the two spellings it is written.
func withPassword(dsn, user, pw string) (string, error) {
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			// url.Error quotes the raw DSN, which may hold a credential in a
			// spelling pgconn took and net/url did not: the refusal names the
			// defect class and quotes no byte of the input.
			return "", fmt.Errorf("--pg: the address is not a URL this tool can add the password to; want postgres://user@host:5432/nova (%T)", err)
		}
		if user == "" {
			user = u.User.Username()
		}
		u.User = url.UserPassword(user, pw)
		return u.String(), nil
	}
	escaped := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(pw)
	return dsn + " password='" + escaped + "'", nil
}

// isEnvName reports whether s is spelled as an environment variable name: the
// one class of the operator's text a refusal here may quote.
func isEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
