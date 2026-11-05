package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

// redisBeats reads every machine's beat from Redis in one round trip: the
// bench hash bench:<m>:beat and the member record sprint:beat:<m>, the keys
// the machines write. It speaks just enough RESP2 to read them.
func redisBeats(ctx context.Context, addr, user, password string, machines []string) (map[string]*Beat, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("redis %s: %w", addr, err)
	}
	defer func() { _ = c.Close() }() // ignored: every reply has been read
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl) // ignored: without it the context's own deadline still ends the run
	}
	var cmds [][]string
	switch {
	case password != "" && user != "":
		cmds = append(cmds, []string{"AUTH", user, password})
	case password != "":
		cmds = append(cmds, []string{"AUTH", password})
	}
	for _, m := range machines {
		cmds = append(cmds, []string{"HGETALL", "bench:" + m + ":beat"}, []string{"GET", "sprint:beat:" + m})
	}
	replies, err := pipeline(c, cmds)
	if err != nil {
		return nil, fmt.Errorf("redis %s: %w", addr, err)
	}
	replies = replies[len(cmds)-2*len(machines):]
	out := map[string]*Beat{}
	for i, m := range machines {
		hash := map[string]string{}
		pairs, _ := replies[2*i].([]any)
		for j := 0; j+1 < len(pairs); j += 2 {
			k, _ := pairs[j].(string)
			v, _ := pairs[j+1].(string)
			hash[k] = v
		}
		member, _ := replies[2*i+1].(string)
		if b := mergeBeat(hash, member); b != nil {
			out[m] = b
		}
	}
	return out, nil
}

// pipeline writes every command, then reads one reply for each: a string, nil
// or a []any; an error reply is an error.
func pipeline(rw io.ReadWriter, cmds [][]string) ([]any, error) {
	var b strings.Builder
	for _, c := range cmds {
		fmt.Fprintf(&b, "*%d\r\n", len(c))
		for _, a := range c {
			fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
		}
	}
	if _, err := io.WriteString(rw, b.String()); err != nil {
		return nil, err
	}
	r := bufio.NewReader(rw)
	out := make([]any, len(cmds))
	for i := range cmds {
		v, err := reply(r)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func reply(r *bufio.Reader) (any, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(line, "\r\n")
	if line == "" {
		return nil, errors.New("an empty reply")
	}
	n, _ := strconv.Atoi(line[1:])
	switch line[0] {
	case '+', ':':
		return line[1:], nil
	case '-':
		return nil, errors.New(line[1:])
	case '$':
		if n < 0 {
			return nil, nil
		}
		if n > 1<<24 {
			return nil, fmt.Errorf("a reply of %d bytes", n)
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		if n < 0 {
			return nil, nil
		}
		if n > 1<<16 {
			return nil, fmt.Errorf("a reply of %d elements", n)
		}
		arr := make([]any, n)
		for i := range arr {
			if arr[i], err = reply(r); err != nil {
				return nil, err
			}
		}
		return arr, nil
	}
	return nil, fmt.Errorf("an unknown reply %q", line[:1])
}
