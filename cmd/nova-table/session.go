package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/tty"
	"github.com/redis/go-redis/v9"
)

// An invocation owns its input and, in a shell, its connection. Sessions never
// install a process-global client or close a borrowed connection after a verb.
type application struct {
	in       io.Reader
	shared   *connection
	addr     string
	defaults ntable.WriteOptions
	receipts bool
	// dryRun is the --dry-run default of every write verb: a shell entered
	// with --dry-run plans each write line and writes nothing.
	dryRun bool
	// getenv is the environment the login is read from (login); nil is the
	// process's. A test hands its own, so it runs in parallel.
	getenv func(string) string
	// lookPath finds a program on PATH (firstTry); nil is exec.LookPath.
	lookPath func(string) (string, error)
}

// env is the environment the login is read from.
func (app *application) env() func(string) string {
	if app.getenv != nil {
		return app.getenv
	}
	return os.Getenv
}

type connection struct {
	*redisconn.Conn
	shared  bool
	counter *redisconn.Trips
	reopen  func() (*redisconn.Conn, error)
	broken  atomic.Bool
}

// A shell's connection is opened by the first line that needs the store,
// never on entering the shell (tla/TableSession.tla Init: conn = "none"), and
// again by the first line after a transport failure: the failed client is
// retired whole, so no dial error it kept can answer a later line (the model's
// NoFalseAlarm). Never replay the failed command: the store may have committed
// a write before its reply was lost (AtMostOnce; redisconn sends a command at
// most once, MaxRetries -1). A nil conn is one not opened yet.
func sharedConnection(conn *redisconn.Conn, reopen func() (*redisconn.Conn, error)) *connection {
	c := &connection{Conn: conn, shared: true, reopen: reopen}
	if conn != nil {
		conn.Client().AddHook(c)
	}
	return c
}

func (c *connection) prepare() error {
	if c.reopen == nil || (c.Conn != nil && !c.broken.Load()) {
		return nil
	}
	next, err := c.reopen()
	if err != nil {
		return err
	}
	if err := c.Conn.Close(); err != nil {
		return errors.Join(err, next.Close())
	}
	c.Conn, c.counter = next, nil
	c.broken.Store(false)
	next.Client().AddHook(c)
	return nil
}

func (c *connection) DialHook(next redis.DialHook) redis.DialHook { return next }
func (c *connection) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if lost(err) {
			c.broken.Store(true)
		}
		return err
	}
}
func (c *connection) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		err := next(ctx, cmds)
		if lost(err) {
			c.broken.Store(true)
		}
		// A pipeline's first error may be a logical refusal while a later
		// command lost the connection. Inspect every command before reusing it.
		for _, cmd := range cmds {
			if lost(cmd.Err()) {
				c.broken.Store(true)
			}
		}
		return err
	}
}

func (c *connection) Close() error {
	if c.shared {
		return nil
	}
	return c.Conn.Close()
}

// Attach only one hook per connection. A long session must not accumulate a
// hook per verb; each command reports a window of the shared counter instead.
type tripWindow struct {
	counter *redisconn.Trips
	before  int64
}

func (c *connection) CountTrips() *tripWindow {
	if c.counter == nil {
		c.counter = redisconn.CountTrips(c.Client())
	}
	return &tripWindow{c.counter, c.counter.N()}
}
func (w *tripWindow) N() int64 { return w.counter.N() - w.before }

func (app *application) cmdShell(args []string, stdout, stderr io.Writer) int {
	const verb = "shell"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	defaults, receipts := app.writeFlags(fs)
	// Receipt IDs are useful when driving a resident process. They can be
	// disabled for the session or overridden by an individual write.
	// ignored: Set on a flag writeFlags just defined, with a value its parser accepts
	_ = fs.Set("receipt", "true")
	in := app.in
	if in == nil {
		in = os.Stdin
	}
	interactive := false
	if f, ok := in.(*os.File); ok {
		interactive = tty.IsTerminal(f)
	}
	keepGoing := fs.Bool("keep-going", interactive, "continue after errors; final exit still reports failure (default true on a terminal)")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 0 {
		return refuse(stderr, verb, "reads commands from stdin; wants no positional arguments")
	}
	if app.shared != nil {
		return refuse(stderr, verb, "already in a shell; enter a table command or quit")
	}
	if strings.TrimSpace(*addr) == "" {
		return refuse(stderr, verb, "--redis <addr> is required (or a configured seat)")
	}
	getenv := app.env()
	// The login is resolved on entering, as it always was (a seat that cannot
	// be read, a password variable that is empty, refuse the shell here); the
	// store is dialed by the first line that needs it.
	if o, env, err := login(*addr, seatcred.Process(), getenv); err != nil {
		return refuse(stderr, verb, err.Error())
	} else if _, err := redisconn.Resolve(o, env); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	child := &application{in: in, shared: sharedConnection(nil, func() (*redisconn.Conn, error) {
		return openShellStore(*addr, getenv)
	}), addr: *addr, defaults: *defaults, receipts: *receipts, dryRun: fs.Lookup("dry-run").Value.String() == "true", getenv: getenv}
	// prepare may replace the connection, so close the final owner, not the first.
	// ignored: a deferred close after the session's last answer is printed
	defer func() { _ = child.shared.Conn.Close() }()
	return child.readCommands(in, stdout, stderr, *keepGoing, interactive)
}

// openShellStore is the shell's one connection, opened the one way
// (redisconn.Open): one dial attempt, the handshake inside OpenTimeout, and
// no command retries (MaxRetries -1), so a lost reply is never replayed; the
// client stays reusable while the store answers.
func openShellStore(addr string, getenv func(string) string) (*redisconn.Conn, error) {
	return open(context.Background(), addr, getenv)
}

const maxShellLine = 1024 * 1024

const shellUsageDetails = `Enter one command per line, optionally prefixed with nova-table.
Quotes and backslashes preserve values; blank lines and # comments are skipped.
No variable, glob or command expansion. Maximum line: 1048576 bytes, excluding LF/CRLF.
File/pipe input stops at the first error. --keep-going discards an overlong line
and continues at the next line; terminal input defaults to continuing and prints
nova-table> on stderr. Command failures name their input line.
The ordinary exit is the highest command status: 0 success, 1 store refusal,
2 usage/input/connection failure. Earlier successful writes stay committed.
A failed connection is replaced for the next command; a failed write is not replayed.
Use help <verb>, quit, exit or EOF. Ctrl-C in watch returns to the shell.
On Unix, SIGTERM terminates the whole process (status 143); Ctrl-C at the prompt
terminates it (status 130). A write already sent may have committed.`

func (app *application) readCommands(in io.Reader, stdout, stderr io.Writer, keepGoing, prompt bool) int {
	reader := bufio.NewReader(in)
	result, line := 0, 0
	for {
		if prompt {
			fmt.Fprint(stderr, "nova-table> ")
		}
		text, readErr := readShellLine(reader)
		if errors.Is(readErr, io.EOF) {
			return result
		}
		line++
		if readErr != nil {
			result = refuse(stderr, "shell", fmt.Sprintf("reading line %d (maximum %d bytes): %v", line, maxShellLine, readErr))
			if keepGoing && errors.Is(readErr, errShellLineTooLong) {
				continue
			}
			return result
		}
		args, err := shellWords(text)
		if err == nil && len(args) > 0 && args[0] == "nova-table" {
			args = args[1:]
			if len(args) == 0 {
				err = fmt.Errorf("wants a verb after nova-table")
			}
		}
		code := 0
		if err != nil {
			code = refuse(stderr, "shell", fmt.Sprintf("line %d: %v", line, err))
		} else if len(args) == 0 {
			continue
		} else if (args[0] == "quit" || args[0] == "exit") && len(args) == 1 {
			return result
		} else {
			code = app.run(args, stdout, stderr)
			if code != 0 {
				fmt.Fprintf(stderr, "nova-table shell: line %d failed (exit %d)\n", line, code)
			}
		}
		result = max(result, code)
		if code != 0 && !keepGoing {
			return result
		}
	}
}

var errShellLineTooLong = errors.New("line too long")

// Bound retained input even for a huge line, but consume the whole rejected
// line so --keep-going resumes at the next command. Reserve one byte for CR
// before removing LF/CRLF. A read failure cannot execute an incomplete line.
func readShellLine(reader *bufio.Reader) (string, error) {
	var data []byte
	tooLong := false
	for {
		part, err := reader.ReadSlice('\n')
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) && !errors.Is(err, io.EOF) {
			return "", err
		}
		if len(part) > 0 && part[len(part)-1] == '\n' {
			part = part[:len(part)-1]
		}
		if !tooLong {
			if len(data)+len(part) > maxShellLine+1 {
				tooLong = true
				data = nil
			} else {
				data = append(data, part...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(data) == 0 && !tooLong {
			return "", io.EOF
		}
		if len(data) > 0 && data[len(data)-1] == '\r' {
			data = data[:len(data)-1]
		}
		if tooLong || len(data) > maxShellLine {
			return "", errShellLineTooLong
		}
		return string(data), nil
	}
}

// shellWords reads words, quotes and escapes, not an operating-system shell.
// It never expands variables/globs, substitutes commands, or executes a pipe.
func shellWords(line string) ([]string, error) {
	var words []string
	var word strings.Builder
	quote := byte(0)
	started := false
	flush := func() {
		if started {
			words = append(words, word.String())
			word.Reset()
			started = false
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			} else {
				word.WriteByte(c)
			}
			continue
		}
		if c == '\\' {
			if i+1 == len(line) {
				return nil, fmt.Errorf("unfinished escape; use one complete command per line")
			}
			next := line[i+1]
			if quote == '"' && !strings.ContainsRune("\"\\$`", rune(next)) {
				word.WriteByte(c)
				continue
			}
			i++
			word.WriteByte(next)
			started = true
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
			} else {
				word.WriteByte(c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			started = true
		case ' ', '\t', '\r':
			flush()
		case '#':
			if !started {
				flush()
				return words, nil
			}
			word.WriteByte(c)
		case ';', '|', '&', '<', '>':
			return nil, fmt.Errorf("one table command per line; quote %q when it is part of a value", c)
		default:
			word.WriteByte(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote; use one complete command per line")
	}
	flush()
	return words, nil
}
