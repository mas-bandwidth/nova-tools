package config

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveDSNRefusesEveryFlagPassword pins the boundary the store's
// connecting contract promises (docs/nova-config/README.md, "Connecting"):
// a --pg flag that carries a password is refused in every spelling the
// pgconn parser accepts, because the flag line is where a ps reads it,
// while a DSN from the environment and a password injected from the
// password variable keep working.
func TestResolveDSNRefusesEveryFlagPassword(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		flag   string
		env    map[string]string
		refuse bool
		want   string // the resolved DSN when the flag carries no password
	}{
		{name: "url userinfo password", flag: "postgres://store:synthetic-secret@db.invalid:5432/nova", refuse: true},
		{name: "url query password", flag: "postgres://store@db.invalid:5432/nova?password=synthetic-secret", refuse: true},
		{name: "url query percent-encoded password key", flag: "postgres://store@db.invalid:5432/nova?pass%77ord=synthetic-secret", refuse: true},
		{name: "url query mixed-case encoded password key", flag: "postgres://store@db.invalid:5432/nova?%70aSsWoRd=synthetic-secret", refuse: true},
		{name: "url query password key with trailing blank", flag: "postgres://store@db.invalid:5432/nova?password =synthetic-secret", refuse: true},
		{name: "url query password key with leading blank", flag: "postgres://store@db.invalid:5432/nova? password=synthetic-secret", refuse: true},
		{name: "keyword password", flag: "host=db.invalid user=store port=5432 password=synthetic-secret", refuse: true},
		{name: "keyword quoted password", flag: "host=db.invalid port=5432 password='synthetic-secret'", refuse: true},
		{name: "keyword quoted escaped password", flag: `host=db.invalid password='synth\'etic-secret'`, refuse: true},
		{name: "url explicit empty password", flag: "postgres://store:@db.invalid:5432/nova", refuse: true},
		{name: "url query explicit empty password", flag: "postgres://store@db.invalid:5432/nova?password=", refuse: true},
		{name: "keyword quoted explicit empty password", flag: "host=db.invalid port=5432 password=''", refuse: true},
		{name: "keyword bare explicit empty password", flag: "host=db.invalid port=5432 password=", refuse: true},
		{name: "malformed keyword carrying a password", flag: "host=db.invalid password='synthetic-secret", refuse: true},
		{name: "malformed encoded password key never echoes the dsn", flag: "postgres://store@db.invalid:5432/nova?%70assword=synthetic-secret%zz", refuse: true},
		{name: "malformed keyword never echoes the dsn", flag: `pass\ word=synthetic-secret`, refuse: true},
		{
			name: "environment dsn with a password is kept",
			flag: "",
			env:  map[string]string{EnvPG: "postgres://store:synthetic-secret@db.invalid:5432/nova"},
			want: "postgres://store:synthetic-secret@db.invalid:5432/nova",
		},
		{
			name: "url flag without a password takes the injected one",
			flag: "postgres://store@db.invalid:5432/nova",
			env:  map[string]string{DefaultPassEnv: "synthetic-secret"},
			want: "postgres://store:synthetic-secret@db.invalid:5432/nova",
		},
		{
			name: "keyword flag without a password takes the injected one",
			flag: "host=db.invalid user=store port=5432",
			env:  map[string]string{DefaultPassEnv: "synthetic-secret"},
			want: "host=db.invalid user=store port=5432 password='synthetic-secret'",
		},
		{
			name: "keyword value quoting the word password is not a password field",
			flag: `options='password=x' host=db.invalid user=store`,
			env:  map[string]string{DefaultPassEnv: "synthetic-secret"},
			want: `options='password=x' host=db.invalid user=store password='synthetic-secret'`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveDSN(tc.flag, envOf(tc.env))
			if tc.refuse {
				require.Error(t, err, "a flag DSN carrying a password is refused")
				assert.ErrorContains(t, err, "carries a password")
				assert.ErrorContains(t, err, EnvPGPassEnv, "the refusal names the password variable as the remedy")
				assert.NotContains(t, err.Error(), "synthetic-secret", "the refusal never echoes the secret")
				assert.NotContains(t, err.Error(), tc.flag, "the refusal never echoes the DSN")
				return
			}
			require.NoError(t, err, "a DSN with no flag password resolves")
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestResolveDSNRefusesAFlagPasswordBesideAControlByte pins the parsed-side
// boundary the lexical check cannot reach: net/url refuses a flag carrying a
// control byte, so flagCarriesPassword's URL branch never sees its query,
// while the pgconn URI reader trims only a literal space around a query key
// and treats other bytes as data, parses the flag, and sets a password from
// it. A flag whose parsed config carries a non-empty Password is refused with
// refuseFlagPassword's text whatever the lexical check said, and neither the
// flag nor its secret is ever returned or echoed
// (docs/nova-config/README.md, "Connecting").
func TestResolveDSNRefusesAFlagPasswordBesideAControlByte(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		flag string
	}{
		{name: "control byte beside a password query key", flag: "postgres://store@db.invalid:5432/nova? password=synthetic-secret&application_name=a\tb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveDSN(tc.flag, envOf(nil))
			require.Error(t, err, "a flag whose parsed config carries a password is refused")
			assert.Equal(t, refuseFlagPassword().Error(), err.Error(), "the refusal is refuseFlagPassword's own text")
			assert.NotContains(t, err.Error(), "synthetic-secret", "the refusal never echoes the secret")
			assert.NotContains(t, err.Error(), tc.flag, "the refusal never echoes the DSN")
			assert.NotEqual(t, tc.flag, got, "the flag text is never returned")
			assert.Empty(t, got, "nothing resolves when the flag is refused")
		})
	}
}

// TestResolveDSNEnvParseErrorNeverEchoesTheDSN pins the environment path's
// parse refusal (docs/nova-config/README.md, "Connecting"): with an empty
// flag and a NOVA_PG_DSN the pgconn parser cannot read, the error names the
// variable and the wanted shape and quotes neither the DSN nor its password.
// pgconn's own error text runs the raw connection string through a
// best-effort redactor that malformed input defeats, so a DSN that is not on
// a command line still must not have its text echoed back.
func TestResolveDSNEnvParseErrorNeverEchoesTheDSN(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		dsn  string
	}{
		{name: "uri with a non-numeric port", dsn: "postgres://store:synthetic-secret@db.invalid:notaport/nova"},
		{name: "keyword with spaces around the equals and an unclosed quote", dsn: "password = 'synthetic-secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ResolveDSN("", envOf(map[string]string{EnvPG: tc.dsn}))
			require.Error(t, err, "an unparsable environment DSN is refused")
			assert.Contains(t, err.Error(), EnvPG, "the refusal names the variable it could not read")
			assert.Contains(t, err.Error(), "postgres://user@host:5432/nova", "the refusal carries the wanted shape")
			assert.NotContains(t, err.Error(), "synthetic-secret", "the refusal never echoes the password")
			assert.NotContains(t, err.Error(), tc.dsn, "the refusal never echoes the DSN text")
		})
	}
}

// TestResolveDSNRefusesAFlagSSLPassword pins the boundary the store's
// connecting contract promises (docs/nova-config/README.md, "Connecting"):
// sslpassword is the passphrase for the SSL client key, a secret the pgconn
// parser reads from the flag, so a --pg flag that carries one in either
// spelling the parser accepts is refused with refuseFlagPassword's own text,
// which quotes neither the flag nor its secret.
func TestResolveDSNRefusesAFlagSSLPassword(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		flag string
	}{
		{name: "url query sslpassword", flag: "postgres://store@db.invalid:5432/nova?sslpassword=synthetic-secret"},
		{name: "keyword sslpassword", flag: "sslpassword=synthetic-secret host=db.invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveDSN(tc.flag, envOf(nil))
			require.Error(t, err, "a flag DSN carrying an sslpassword is refused")
			assert.Equal(t, refuseFlagPassword().Error(), err.Error(), "the refusal is refuseFlagPassword's own text")
			assert.NotContains(t, err.Error(), "synthetic-secret", "the refusal never echoes the secret")
			assert.NotContains(t, err.Error(), tc.flag, "the refusal never echoes the DSN")
			assert.Empty(t, got, "nothing resolves when the flag is refused")
		})
	}
}

// envOf is the getenv ResolveDSN reads: one lookup into the case's
// variables, so a test never touches the process environment.
func envOf(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

// TestTheDSNRefusalNamesNoUnclassifiedByte pins the rule every refusal in
// dsn.go keeps (docs/nova-config/README.md, "Connecting"): a message may name
// the key, the position and the class of defect, never a substring of the
// input outside an allowlisted class. A DSN may carry a password that neither
// net/url nor the keyword parser could split, so the boundary is judged on the
// error text alone: for every password spelling (control bytes, spaces, '@',
// '/', quotes, percent escapes, invalid UTF-8) in every DSN shape, through the
// flag entry, the environment entry and the injected-password entry, and
// through Redact, no 3-byte substring of the password appears in the text,
// raw or in its quoted spelling. The values are made up for this test.
func TestTheDSNRefusalNamesNoUnclassifiedByte(t *testing.T) {
	t.Parallel()

	secrets := []string{
		"zq7Kp9 wv\x01@/'\"%41%zzmn\xff\xfeend",
		"Hj4x\x7f\tRt2/Lm8@Bn6'Vc3",
		"Qa5 \"Ws8\\Ed1%2fRf4%Tg7",
		"Yh6\n@Uj9 /Ik2'Ol5\x00",
		"P%zz%Q@/ X'y \"Zk3",
	}
	shapes := []struct {
		name, tmpl string
		// secret is true where the DSN names a password or sslpassword, so a
		// flag that resolves without a refusal has put the secret on the line.
		secret bool
	}{
		{"uri userinfo", "postgres://store:%s@db.invalid:5432/nova", true},
		{"uri userinfo, non-numeric port", "postgres://store:%s@db.invalid:notaport/nova", true},
		{"uri userinfo, no host", "postgres://store:%s@", true},
		{"uri query password", "postgres://store@db.invalid:5432/nova?password=%s", true},
		{"uri query sslpassword", "postgres://store@db.invalid:5432/nova?sslpassword=%s", true},
		{"uri query sslpassword beside a control byte", "postgres://store@db.invalid:5432/nova?sslpassword=%s&application_name=a\tb", true},
		{"uri query sslpassword and a bad escape", "postgres://store@db.invalid:5432/nova?sslpassword=%s&application_name=%zz", true},
		{"keyword password", "host=db.invalid user=store password=%s dbname=nova", true},
		{"keyword quoted password", "host=db.invalid password='%s' dbname=nova", true},
		{"keyword unterminated quote", "host=db.invalid password='%s", true},
		{"keyword spaced equals", "host=db.invalid password = %s", true},
		{"keyword sslpassword", "host=db.invalid sslpassword=%s port=5432", true},
		{"keyword sslpassword, bad port", "host=db.invalid sslpassword='%s' port=notaport", true},
		{"keyword unknown key", "host=db.invalid %s=x", false},
	}

	// leaks names the first 3-byte substring of secret found in text, in
	// its raw spelling or in the quoted spelling an error built with %q uses,
	// and is empty when none is.
	leaks := func(text, secret string) string {
		quoted := strconv.Quote(secret)
		quoted = quoted[1 : len(quoted)-1]
		for _, form := range []string{secret, quoted} {
			for i := 0; i+3 <= len(form); i++ {
				if strings.Contains(text, form[i:i+3]) {
					return strconv.Quote(form[i : i+3])
				}
			}
		}
		return ""
	}

	const injected = "Vb3Nm8!Xq9"
	entries := []struct {
		name string
		run  func(dsn string) (string, error)
		// refuses is true where a DSN that names a secret must be refused.
		refuses bool
	}{
		{"flag", func(dsn string) (string, error) { return ResolveDSN(dsn, envOf(nil)) }, true},
		{"environment", func(dsn string) (string, error) {
			return ResolveDSN("", envOf(map[string]string{EnvPG: dsn}))
		}, false},
		{"environment with an injected password", func(dsn string) (string, error) {
			return ResolveDSN("", envOf(map[string]string{EnvPG: dsn, DefaultPassEnv: injected}))
		}, false},
		{"redact", func(dsn string) (string, error) { return Redact(dsn), nil }, false},
	}

	for _, shape := range shapes {
		for si, secret := range secrets {
			dsn := strings.Replace(shape.tmpl, "%s", secret, 1)
			for _, entry := range entries {
				t.Run(fmt.Sprintf("%s/secret %d/%s", shape.name, si, entry.name), func(t *testing.T) {
					t.Parallel()
					got, err := entry.run(dsn)
					text := got
					switch {
					case err != nil:
						text = err.Error()
					case entry.name != "redact":
						// A resolved DSN is the caller's own text, not a message;
						// a flag that names a secret must not resolve at all.
						text = ""
						assert.False(t, entry.refuses && shape.secret, "the %s entry resolved a DSN that names a password or sslpassword", entry.name)
					}
					assert.Empty(t, leaks(text, secret), "the %s entry echoed a piece of the password in %q", entry.name, text)
					assert.Empty(t, leaks(text, injected), "the %s entry echoed a piece of the injected password in %q", entry.name, text)
				})
			}
		}
	}

	// The variable name NOVA_PG_PASSWORD_ENV carries is operator text as well: a
	// password pasted there is refused without being quoted.
	for si, secret := range secrets {
		t.Run(fmt.Sprintf("password variable name/secret %d", si), func(t *testing.T) {
			t.Parallel()
			_, err := ResolveDSN("postgres://store@db.invalid:5432/nova", envOf(map[string]string{EnvPGPassEnv: secret}))
			require.Error(t, err)
			assert.Empty(t, leaks(err.Error(), secret), "the refusal echoed a piece of the variable name in %q", err.Error())
		})
	}
}
