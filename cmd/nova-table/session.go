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

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
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
}

type connection struct {
	*store.Store
	shared  bool
	counter *store.Trips
	reopen  func() (*store.Store, error)
	broken  atomic.Bool
}

// A transport failure can saturate go-redis's one-connection dial circuit.
// Retire that client before the next command uses it. Never replay the failed
// command: the store may have committed a write before its reply was lost.
func sharedConnection(st *store.Store, reopen func() (*store.Store, error)) *connection {
	c := &connection{Store: st, shared: true, reopen: reopen}
	st.Client().AddHook(c)
	return c
}

func (c *connection) prepare() error {
	if c.reopen == nil || !c.broken.Load() {
		return nil
	}
	st, err := c.reopen()
	if err != nil {
		return err
	}
	if err := c.Store.Close(); err != nil {
		return errors.Join(err, st.Close())
	}
	c.Store, c.counter = st, nil
	c.broken.Store(false)
	st.Client().AddHook(c)
	return nil
}

func (c *connection) DialHook(next redis.DialHook) redis.DialHook { return next }
func (c *connection) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if store.Unreachable(err) {
			c.broken.Store(true)
		}
		return err
	}
}
func (c *connection) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		err := next(ctx, cmds)
		if store.Unreachable(err) {
			c.broken.Store(true)
		}
		// A pipeline's first error may be a logical refusal while a later
		// command lost the connection. Inspect every command before reusing it.
		for _, cmd := range cmds {
			if store.Unreachable(cmd.Err()) {
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
	return c.Store.Close()
}

// Attach only one hook per connection. A long session must not accumulate a
// hook per verb; each command reports a window of the shared counter instead.
type tripWindow struct {
	counter *store.Trips
	before  int64
}

func (c *connection) CountTrips() *tripWindow {
	if c.counter == nil {
		c.counter = c.Store.CountTrips()
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
	_ = fs.Set("receipt", "true")
	in := app.in
	if in == nil {
		in = os.Stdin
	}
	interactive := false
	if f, ok := in.(*os.File); ok {
		interactive = shellTerminal(f)
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
	quietRedisOnce.Do(func() { redis.SetLogger(quietRedis{}) })
	st, err := openShellStore(*addr)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	child := &application{in: in, shared: sharedConnection(st, func() (*store.Store, error) {
		return openShellStore(*addr)
	}), addr: *addr, defaults: *defaults, receipts: *receipts}
	// prepare may replace the store, so close the final owner, not the first.
	defer func() { _ = child.shared.Store.Close() }()
	return child.readCommands(in, stdout, stderr, *keepGoing, interactive)
}

// Keep one connection, but do not automatically retry commands after a lost
// reply: the write may already have committed. OpenProbe supplies one bounded
// dial attempt and no command retries; the client remains reusable on success.
func openShellStore(addr string) (*store.Store, error) {
	return store.OpenProbe(context.Background(), addr, seatcred.Process())
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
		if code > result {
			result = code
		}
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
