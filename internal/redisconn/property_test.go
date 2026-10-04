package redisconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// No secret is ever shown: the property, over arbitrary passwords and not
// over examples. A fixed-seed generator mixes what a password can hold that
// a careless message would trip on (formatting verbs, quotes, line breaks,
// the marks of the wire protocol, bytes that are not UTF-8, the words of
// this package's own messages) and everything the package can be made to
// show for that password is gathered: the options, the connection, its
// client, its counter, the error of every way Open fails, each under every
// formatting verb, and every error its chain unwraps to.
//
// The oracle is a second gathering, made with a password that is nothing
// like the first:
//
//	nothing the package shows holds the password, as it is or as a verb or
//	an escape would spell it; and
//	where the store's answers do not depend on the password, what the
//	package shows does not depend on it either, to the byte.
//
// A password can be a word the package shows under any password: "redis",
// which every message opens with, or "e". Such a word stands where it stood,
// and says nothing. So a spelling that stands in what the reference
// gathering shows of a value is not looked for in that value, and the
// examples of TestAPasswordThatIsAWordOfTheMessages hold what the property
// cannot: the text that held it is withheld whole.
//
// Fixed seeds, so a failure names a password that fails again.

const referencePassword = "Zq8-reference-password-Kx2"

var passwordPieces = []string{
	// formatting verbs, quotes and the backslash
	"%s", "%v", "%d", "%!", "%%", "%+v", `"`, "'", "`", `\`, `\n`, `\x00`,
	// spaces, line breaks and controls
	" ", "\t", "\n", "\r", "\r\n", "\x00", "\x1b[31m", "\x7f",
	// the marks of the wire protocol, and a reply of its own
	"$", "*", "+", "-", ":", "_", "%", "\r\n+OK\r\n", "*1\r\n$4\r\nPING\r\n",
	// the words of this package's messages and of these tests
	"redis", "bench", "PW", "NOVA_TEST_PW", "NOVA_REDIS_PASSWORD_ENV", "hidden", "[hidden]", "***", "[", "]",
	"next", "e", "a", "store.test:6379", "NOAUTH", "WRONGPASS", "password", "default", "auth", "unreachable",
	// text
	"s3cret", "Tr0ub4dor&3", "correct horse battery staple", "0", "=", "/", "@", "://",
	// beyond ASCII, by code point; bytes that are not UTF-8
	string(rune(0xe9)), string(rune(0x65e5)) + string(rune(0x672c)), string(rune(0x1f642)), string(rune(0x2028)), string(rune(0x202e)),
	"\xff", "\xc0", "\xe2\x80",
	// long
	strings.Repeat("x", 300),
}

func arbitraryPassword(r *rand.Rand) string {
	var b strings.Builder
	for n := 1 + r.IntN(6); n > 0; n-- {
		if r.IntN(6) == 0 {
			b.WriteByte(byte(r.IntN(256)))
			continue
		}
		b.WriteString(passwordPieces[r.IntN(len(passwordPieces))])
	}
	return b.String()
}

// showing is one thing the package showed. varies says the text may differ
// from one gathering to the next for a reason that is not the password: the
// store's answers held the password when it was made, or (numbers) it
// prints a pointer as a bare number.
type showing struct {
	what    string
	of      string // the value it is a showing of
	text    string
	varies  bool
	numbers bool
}

// address is a pointer as a formatting verb prints it, which differs from
// one run to the next and says nothing of the password.
var address = regexp.MustCompile(`0x[0-9a-f]+`)

// plain is a value that holds no pointer under every formatting verb that
// can print it. %x, %X and %d are there because they spell a string in
// hexadecimal.
func plain(what string, v any) []showing {
	var out []showing
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
		out = append(out, showing{what: what + " under " + verb, of: what, text: address.ReplaceAllString(fmt.Sprintf(verb, v), "0x")})
	}
	return out
}

// verbs is plain for a value that may hold pointers, which %x, %X and %d
// print as bare numbers.
func verbs(what string, v any) []showing {
	out := plain(what, v)
	for i := range out[5:] {
		out[5+i].varies, out[5+i].numbers = true, true
	}
	return out
}

// failed is an error: its class, its text, itself under every verb, and
// the texts of every error it unwraps to.
func failed(what string, err error) []showing {
	class, text, chain := "", "", []string(nil)
	if err == nil {
		err = errors.New("no error")
	} else {
		class, text = Classify(err).String(), err.Error()
		for e := errors.Unwrap(err); e != nil; e = errors.Unwrap(e) {
			chain = append(chain, e.Error(), fmt.Sprintf("%#v", e), fmt.Sprintf("%q", e))
		}
	}
	return append(verbs(what, err),
		showing{what: what + ": its class", of: what, text: class},
		showing{what: what + ": Error()", of: what, text: text},
		showing{what: what + ": what it unwraps to", of: what, text: address.ReplaceAllString(strings.Join(chain, " <- "), "0x")})
}

// echoing is a store that writes what it was sent into its refusals, as a
// store older than the command does, and refuses every login.
func echoing(_ int, cmd []string) string {
	line := "-ERR unknown command `" + cmd[0] + "`, with args beginning with: `" + strings.Join(cmd[1:], "`, `") + "`"
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(line) + "\r\n"
}

// everythingShown is all the package shows when the environment holds the
// password.
func everythingShown(t *testing.T, password string) []showing {
	t.Helper()
	env := environment(map[string]string{
		GeneralEnv.Addr: storeAddr, GeneralEnv.User: "bench", GeneralEnv.PasswordEnv: "NOVA_TEST_PW", "NOVA_TEST_PW": password,
	})
	ctx := context.Background()
	var out []showing

	options, err := Resolve(Options{Env: GeneralEnv}, env)
	out = append(out, failed("Resolve", err)...)
	out = append(out, showing{what: "the options: String()", of: "the options", text: options.String()})
	out = append(out, plain("the options", options)...)
	out = append(out, plain("a pointer to the options", &options)...)

	store := newFakeStore(t, accepting)
	conn, err := open(ctx, Options{Env: GeneralEnv}, env, store.dial)
	out = append(out, failed("Open", err)...)
	if err != nil {
		require.NoError(t, err, "Open with the password %q: %v", password, err)
	}
	trips := CountTrips(conn.Client())
	refusal := conn.Client().LPush(WithTripLabel(ctx, "push"), "k", "v").Err()
	out = append(out, showing{what: "the connection: String()", of: "the connection", text: conn.String()})
	out = append(out, verbs("the connection", conn)...)
	out = append(out, verbs("the connection, copied", *conn)...)
	out = append(out, verbs("the client", conn.Client())...)
	out = append(out, verbs("the client's options", conn.Client().Options())...)
	out = append(out, verbs("the client's options, copied", *conn.Client().Options())...)
	out = append(out, verbs("the counter", trips)...)
	out = append(out, plain("the counts by label", trips.ByLabel())...)
	out = append(out, plain("the label of a context", TripLabel(WithTripLabel(ctx, "push")))...)
	out = append(out, failed("Explain of the store's refusal", conn.Explain(refusal))...)
	out = append(out, failed("Explain of a dropped connection", conn.Explain(fmt.Errorf("row add: %w", io.EOF)))...)
	for _, s := range failed("Explain of an error that holds the password", conn.Explain(errors.New("the store said: auth bench "+password+" was refused"))) {
		s.varies = true
		out = append(out, s)
	}
	out = append(out, failed("Close", conn.Close())...)

	store = newFakeStore(t, refusing("-WRONGPASS invalid username-password pair or user is disabled.\r\n"))
	_, err = open(ctx, Options{Env: GeneralEnv}, env, store.dial)
	out = append(out, failed("Open, the login refused", err)...)

	dials := 0
	_, err = open(ctx, Options{Env: GeneralEnv}, env, refusedDial(&dials))
	out = append(out, failed("Open, the store not there", err)...)

	store = newFakeStore(t, func(int, []string) string { return hangUp })
	_, err = open(ctx, Options{Env: GeneralEnv}, env, store.dial)
	out = append(out, failed("Open, the store hanging up", err)...)

	store = newFakeStore(t, echoing)
	_, err = open(ctx, Options{Env: GeneralEnv}, env, store.dial)
	if err == nil {
		require.Error(t, err, "Open to a store that refuses every login, with the password %q: no error", password)
	}
	for _, s := range failed("Open, the store writing the login into its refusal", err) {
		s.varies = true
		out = append(out, s)
	}

	store = newFakeStore(t, func(int, []string) string {
		return "+unexpected" + password + "\r\n"
	})
	_, err = open(ctx, Options{Env: GeneralEnv}, env, store.dial)
	if err == nil {
		require.Error(t, err, "Open to a store that echoes as malformed handshake, with the password %q: no error", password)
	}
	for _, s := range failed("Open, the store writing the password into a malformed handshake reply", err) {
		s.varies = true
		out = append(out, s)
	}

	// The password put where the name of its variable belongs.
	if !envName(password) {
		misplaced := environment(map[string]string{GeneralEnv.Addr: storeAddr, GeneralEnv.User: "bench", GeneralEnv.PasswordEnv: password})
		_, err = Resolve(Options{Env: GeneralEnv}, misplaced)
		out = append(out, failed("Resolve, the password where its variable's name belongs", err)...)
		_, err = open(ctx, Options{PasswordEnv: password, Env: GeneralEnv}, misplaced, refusedDial(&dials))
		out = append(out, failed("Open, the password given as its variable's name", err)...)
	}
	return out
}

// spellings is the password as it is and as a formatting verb or an escape
// would write it.
func spellings(password string) []string {
	quoted := strconv.Quote(password)
	out := []string{password}
	ascii := strconv.QuoteToASCII(password)
	for _, s := range []string{quoted[1 : len(quoted)-1], ascii[1 : len(ascii)-1], oneline.Escape(password), oneline.Field(password), fmt.Sprintf("%x", password), fmt.Sprintf("%X", password)} {
		if s != password {
			out = append(out, s)
		}
	}
	return out
}

func TestPropertyNoSecretIsEverShown(t *testing.T) {
	t.Parallel()
	reference := everythingShown(t, referencePassword)
	if len(reference) < 150 {
		require.GreaterOrEqual(t, len(reference), 150, "only %d things gathered; the gathering is broken", len(reference))
	}
	for _, s := range reference {
		if strings.Contains(s.text, referencePassword) {
			require.NotContains(t, s.text, referencePassword, "%s shows the reference password: %q", s.what, s.text)
		}
	}

	// under is every text the reference gathering shows of one value.
	under := map[string]string{}
	for _, s := range reference {
		under[s.of] += s.text + "\n"
	}

	looked, held := 0, 0
	for seed := uint64(1); seed <= 3; seed++ {
		r := rand.New(rand.NewPCG(seed, 0x6e6f7661))
		for i := 0; i < 120; i++ {
			password := arbitraryPassword(r)
			shown := everythingShown(t, password)
			if !envName(password) && len(shown) != len(reference) {
				require.FailNowf(t, "", "seed %d case %d password %q: %d things shown, %d under the reference password", seed, i, password, len(shown), len(reference))
			}
			for n, s := range shown {
				ref := reference[n]
				if s.what != ref.what {
					require.EqualValues(t, ref.what, s.what, "seed %d case %d password %q: thing %d is %q, and %q under the reference password", seed, i, password, n, s.what, ref.what)
				}
				looked++
				if strings.Contains(under[s.of], password) {
					continue // a word of what is shown of this value under any password
				}
				held++
				for _, spelling := range spellings(password) {
					// A pointer printed as a bare number is up to sixteen
					// digits of chance; a spelling that short can stand in
					// it and mean nothing.
					if strings.Contains(under[s.of], spelling) || (s.numbers && len(spelling) <= 16) {
						continue
					}
					if strings.Contains(s.text, spelling) {
						require.NotContains(t, s.text, spelling, "seed %d case %d password %q: %s shows it (as %q):\n%q", seed, i, password, s.what, spelling, s.text)
					}
				}
				if !s.varies && s.text != ref.text {
					require.FailNowf(t, "", "seed %d case %d password %q: %s depends on the password:\n%q\nunder the reference password:\n%q", seed, i, password, s.what, s.text, ref.text)
				}
			}
		}
	}
	if held*10 < looked*9 {
		assert.GreaterOrEqual(t, held*10, looked*9, "only %d of %d things shown were held to the property; it is checking too little", held, looked)
	}
	t.Logf("%d things shown, %d held to the property", looked, held)
}

// TestAPasswordThatIsAWordOfTheMessages: a password that is a word of what
// came back is not taken out of the text, which would show it by the gap it
// left. The text is withheld whole, in the error and in what it unwraps to,
// and the class and the next thing to do are what they were.
func TestAPasswordThatIsAWordOfTheMessages(t *testing.T) {
	t.Parallel()
	const tried = "redis at store.test:6379 as user bench (password from PW): "
	for _, c := range []struct {
		password string
		reply    func(int, []string) string
		class    Class
		want     string
		raw      string
	}{
		{"password", refusing("-WRONGPASS invalid username-password pair or user is disabled.\r\n"), AuthRefused,
			tried + "login refused: ***; next: check that PW holds the password of bench and that the store has that user switched on",
			"WRONGPASS"},
		{"WRONGPASS", refusing("-WRONGPASS invalid username-password pair or user is disabled.\r\n"), AuthRefused,
			tried + "login refused: withheld: it held the password; next: check that PW holds the password of bench and that the store has that user switched on",
			"invalid username"},
		{"e", refusing("-WRONGPASS invalid username-password pair or user is disabled.\r\n"), AuthRefused,
			tried + "login refused: ***; next: check that PW holds the password of bench and that the store has that user switched on",
			"invalid username"},
		{"EOF", func(int, []string) string { return hangUp }, Unreachable,
			tried + "unreachable: withheld: it held the password; next: start the store or correct the address, which was given to this tool",
			"EOF;"},
		{"redis", func(int, []string) string { return "+not a map\r\n" }, Other,
			tried + "failed: withheld: it held the password; next: check that the address is a Redis store, version 6 or later",
			"parse"},
	} {
		store := newFakeStore(t, c.reply)
		_, err := open(context.Background(), Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}, environment(map[string]string{"PW": c.password}), store.dial)
		if err == nil || err.Error() != c.want {
			assert.Failf(t, "", "password %q:\n got %v\nwant %s", c.password, err, c.want)
			continue
		}
		if got := Classify(err); got != c.class {
			assert.EqualValues(t, c.class, got, "password %q: class %v; want %v", c.password, got, c.class)
		}
		cause := errors.Unwrap(err)
		if cause == nil || (cause.Error() != withheld && cause.Error() != "***") || errors.Unwrap(cause) != nil {
			assert.Failf(t, "", "password %q: the error unwraps to %#v; want the withheld text and nothing under it", c.password, cause)
		}
		for _, shown := range errorsText(err) {
			if strings.Contains(shown, c.raw) {
				assert.NotContains(t, shown, c.raw, "password %q: %q shows what came back, %q", c.password, shown, c.raw)
			}
		}
	}
}

// TestPropertyThePasswordReachesTheStoreAndNothingElse: the one place the
// password goes is the handshake, whole, whatever it holds.
func TestPropertyThePasswordReachesTheStoreAndNothingElse(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(7, 0x6e6f7661))
	for i := 0; i < 200; i++ {
		password := arbitraryPassword(r)
		store := newFakeStore(t, accepting)
		conn, err := open(context.Background(), Options{Addr: storeAddr, User: "bench", PasswordEnv: "PW"}, environment(map[string]string{"PW": password}), store.dial)
		if err != nil {
			require.NoError(t, err, "case %d password %q: %v", i, password, err)
		}
		if err := conn.Client().Set(context.Background(), "k", "v", 0).Err(); err != nil {
			require.NoError(t, err, "case %d password %q: %v", i, password, err)
		}
		got := store.commands()
		if len(got) != 2 || got[0] != "1: hello 3 auth bench "+password || got[1] != "1: set k v" {
			require.FailNowf(t, "", "case %d password %q: the store received %q", i, password, got)
		}
		if err := conn.Close(); err != nil {
			require.NoError(t, err, err)
		}
	}
}
