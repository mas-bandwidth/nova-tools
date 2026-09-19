// Package workclient is the S1 wire of the resident work session's thin client:
// one request line in, one reply line out over the Unix-domain socket
// --session names, newline-terminated both ways.
//
// It lives here rather than in cmd/nova-work so that the binary keeps a single
// classified print path: the raw .Write of a socket is the one thing the
// package's source-level audit cannot classify, and moving the dial and the
// read out of the binary lets every fmt call in package main remain escaped.
package workclient

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

// replyCap bounds the one reply line, refused whole rather than truncated, the
// family's own law in miniature. One MiB is the S1 wire's standing bound; the
// framed protocol the spec pins carries its own.
const replyCap = 1 << 20

// Exchange dials the session's socket, writes the request line and returns the
// one reply line without its newline. A dial, write or read that cannot
// complete is returned as an error; the caller owns the refusal line.
func Exchange(socket, request string) (string, error) {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(request + "\n")); err != nil {
		return "", err
	}
	return readReply(conn)
}

func readReply(conn net.Conn) (string, error) {
	// Bound the whole read, not just the value checked afterwards: the cap plus
	// the one delimiter byte is enough to see the newline after a full-length
	// reply or to learn the line ran past the cap. A bare ReadString grows with
	// a stream that never sends one, so the length check must not be the only
	// wall between the wire and memory.
	limited := &io.LimitedReader{R: conn, N: replyCap + 1}
	line, err := bufio.NewReader(limited).ReadString('\n')
	if err != nil {
		if limited.N <= 0 {
			return "", fmt.Errorf("reply line past %d bytes", replyCap)
		}
		return "", errors.New("the session closed before one line")
	}
	line = strings.TrimSuffix(line, "\n")
	if len(line) > replyCap {
		return "", fmt.Errorf("reply line past %d bytes", replyCap)
	}
	return line, nil
}
