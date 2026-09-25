package line

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
)

type Redis struct {
	conn net.Conn
	r    *bufio.Reader
}

func DialRedis(addr string) (*Redis, error) {
	if addr == "" {
		addr = "localhost:6379"
	}
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Redis{conn: c, r: bufio.NewReader(c)}, nil
}

func (r *Redis) Close() error {
	return r.conn.Close()
}

func (r *Redis) Do(args ...string) (interface{}, error) {
	fmt.Fprintf(r.conn, "*%d\r\n", len(args))
	for _, arg := range args {
		fmt.Fprintf(r.conn, "$%d\r\n%s\r\n", len(arg), arg)
	}
	return r.readReply()
}

func (r *Redis) readReply() (interface{}, error) {
	line, err := r.r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 3 {
		return nil, errors.New("short reply")
	}
	line = line[:len(line)-2]
	switch line[0] {
	case '+':
		return line[1:], nil
	case '-':
		return nil, errors.New(line[1:])
	case ':':
		return strconv.ParseInt(line[1:], 10, 64)
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if n == -1 {
			return nil, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r.r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if n == -1 {
			return nil, nil
		}
		var arr []interface{}
		for i := 0; i < n; i++ {
			val, err := r.readReply()
			if err != nil {
				return nil, err
			}
			arr = append(arr, val)
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("unknown reply type: %c", line[0])
	}
}
