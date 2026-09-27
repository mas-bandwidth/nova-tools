package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
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
		if info, err := f.Stat(); err == nil {
			interactive = info.Mode()&os.ModeCharDevice != 0
		}
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
	st, err := store.OpenSingle(context.Background(), *addr)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	defer st.Close()
	child := &application{in: in, shared: &connection{Store: st, shared: true}, addr: *addr, defaults: *defaults, receipts: *receipts}
	return child.readCommands(in, stdout, stderr, *keepGoing, interactive)
}

const maxShellLine = 1024 * 1024

func (app *application) readCommands(in io.Reader, stdout, stderr io.Writer, keepGoing, prompt bool) int {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), maxShellLine)
	result, line := 0, 0
	for {
		if prompt {
			fmt.Fprint(stderr, "nova-table> ")
		}
		if !scanner.Scan() {
			break
		}
		line++
		args, err := shellWords(scanner.Text())
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
		}
		if code > result {
			result = code
		}
		if code != 0 && !keepGoing {
			return result
		}
	}
	if err := scanner.Err(); err != nil {
		return refuse(stderr, "shell", fmt.Sprintf("reading line %d (maximum %d bytes): %v", line+1, maxShellLine, err))
	}
	return result
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
