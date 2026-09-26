package store

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Unreachable reports an error that says the store could not be reached or
// would not let this client in: a dial, a timeout, a dropped connection or a
// refused login. Open sends nothing (#3277), so these arrive on the caller's
// first batch instead of from Open; a verb that answered an unreachable store
// with its own exit code (6 for most nova-sprint verbs) checks its first
// batch's error with Unreachable and keeps that code.
func Unreachable(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, redis.ErrClosed) || errors.Is(err, redis.ErrPoolTimeout) {
		return true
	}
	return UnreachableText(err.Error())
}

// UnreachableText is Unreachable for an error already turned into text (a
// refusal line's cause): the marks a dial, a timeout, a dropped connection
// or a refused login leave in go-redis's and net's error strings.
func UnreachableText(s string) bool { return NoAnswerText(s) || hasMark(s, authMarks) }

// NoAnswerText is the network half of UnreachableText: the store did not
// answer at all (a dial, a timeout, a dropped connection), never a login it
// refused. The CLI's shared refusal answers this class with --redis and
// doctor; a refused login keeps its own #3520 hint.
func NoAnswerText(s string) bool {
	return hasMark(s, netMarks) || strings.HasSuffix(s, ": EOF") || s == "EOF"
}

var (
	authMarks = []string{"NOAUTH", "WRONGPASS", "failed to authenticate"}
	netMarks  = []string{"connection refused", "no such host", "i/o timeout", "dial tcp", "connection reset",
		"broken pipe", "unexpected EOF", "redis: client is closed", "redis: connection pool timeout"}
)

func hasMark(s string, marks []string) bool {
	for _, mark := range marks {
		if strings.Contains(s, mark) {
			return true
		}
	}
	return false
}

// Reach sends one PING. It is for a long-lived verb (a daemon or a loop) that
// must refuse at start rather than loop on a store it cannot reach; a
// one-shot verb never calls it, because its first batch is the probe and the
// PING would be the round trip #3277 removed.
func (s *Store) Reach(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}
