package testredis

import (
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

// THE SENTRY. A test's cleanup kills its server, and a cleanup runs only when
// the test binary lives to run it. A binary that times out, calls os.Exit,
// panics off the test's goroutine or is killed runs none, and its servers
// would stand until the machine restarts (the repository once found day-old
// test binaries on a CI runner).
//
// So the first Start of a test binary runs a second copy of the binary as its
// sentry. The sentry leads a process group of its own and reads its standard
// input, a pipe this binary holds the other end of and never writes to. Every
// server joins the sentry's group as it is forked, before it runs an
// instruction. When this binary is gone, however it went, the kernel closes
// the pipe, the read ends, and the sentry kills its group: every server, and
// itself.
//
// Nothing is registered and nothing is remembered: a server is in the group
// or it does not exist, and a server that cannot join (the sentry is gone) is
// not started. Without process groups (Windows) there is no sentry.

// sentryEnv marks a copy of the test binary as a sentry. It is set in the
// sentry's environment and nowhere else.
const sentryEnv = "NOVA_TESTREDIS_SENTRY"

// sentryStands is the one line a sentry prints, once it is about to read.
const sentryStands = "the sentry stands\n"

// sentrySpec is how a sentry is started, so a test can start one that fails.
type sentrySpec struct {
	exe  func() (string, error) // os.Executable
	args []string
	env  []string
	wait time.Duration // how long the sentry may take to say it stands
}

// realSentry is this binary again, asked to run no test (should the mark be
// lost, it runs nothing and exits), with the mark and nothing else in its
// environment.
var realSentry = sentrySpec{
	exe:  os.Executable,
	args: []string{"-test.run=^$"},
	env:  []string{sentryEnv + "=1"},
	wait: 30 * time.Second,
}

// post is a sentry standing.
type post struct {
	group int             // its process group, which the servers join
	gone  <-chan struct{} // closed when the sentry's process has ended
	hold  io.Closer       // the other end of its standard input, open for as long as this process lives
}

// sentry is the test binary's one sentry, enlisted by the first Start.
type sentry struct {
	mu     sync.Mutex
	enlist func() (*post, error)
	at     *post
	err    error
}

// group is the process group a server joins. It is an error when the sentry
// could not be enlisted, and when it was and has ended since: a server
// started then would have nobody to outlive this binary for it.
func (s *sentry) group() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.at == nil && s.err == nil {
		s.at, s.err = s.enlist()
	}
	if s.err != nil {
		return 0, s.err
	}
	select {
	case <-s.at.gone:
		return 0, errors.New("the sentry has ended while this test binary runs; something killed it")
	default:
	}
	return s.at.group, nil
}

// stand is a sentry's whole life: say it stands, read until the test binary
// is gone, kill. It returns only when the kill left it alive, which is a test
// or a failure.
func stand(in io.Reader, out io.Writer, kill func() error) int {
	if _, err := io.WriteString(out, sentryStands); err != nil {
		// Nobody reads: the binary that enlisted this sentry is gone already,
		// before it could start a server.
		return 1
	}
	// The read ends when the pipe closes. An error ends it as well, and the
	// answer to a pipe that broke is the same as to one that closed.
	_, _ = io.Copy(io.Discard, in)
	if err := kill(); err != nil {
		return 1
	}
	return 0
}
