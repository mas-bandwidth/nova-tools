package nsprint

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type RedisClient struct {
	conn net.Conn
	r    *bufio.Reader
}

func ConnectRedis(addr string) (*RedisClient, error) {
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return nil, err
	}
	return &RedisClient{
		conn: conn,
		r:    bufio.NewReader(conn),
	}, nil
}

func (c *RedisClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *RedisClient) Do(args ...string) (any, error) {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("*%d\r\n", len(args)))
	for _, arg := range args {
		buf.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(arg), arg))
	}
	if _, err := c.conn.Write(buf.Bytes()); err != nil {
		return nil, err
	}
	return c.readResponse()
}

func (c *RedisClient) readResponse() (any, error) {
	line, err := c.r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(line, "\r\n")
	if len(line) == 0 {
		return nil, errors.New("empty response")
	}

	prefix := line[0]
	val := line[1:]

	switch prefix {
	case '+':
		return val, nil
	case '-':
		return nil, errors.New(val)
	case ':':
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, err
		}
		return n, nil
	case '$':
		n, err := strconv.Atoi(val)
		if err != nil {
			return nil, err
		}
		if n == -1 {
			return nil, nil
		}
		data := make([]byte, n)
		_, err = io.ReadFull(c.r, data)
		if err != nil {
			return nil, err
		}
		// Read trailing \r\n
		trailing := make([]byte, 2)
		_, _ = io.ReadFull(c.r, trailing)
		return string(data), nil
	case '*':
		n, err := strconv.Atoi(val)
		if err != nil {
			return nil, err
		}
		if n == -1 {
			return nil, nil
		}
		arr := make([]any, n)
		for i := 0; i < n; i++ {
			item, err := c.readResponse()
			if err != nil {
				return nil, err
			}
			arr[i] = item
		}
		return arr, nil
	default:
		return nil, fmt.Errorf("unknown resp prefix: %c", prefix)
	}
}

func (c *RedisClient) LoadFunctions(cardLua, routeLua string) error {
	_, _ = c.Do("FUNCTION", "LOAD", "REPLACE", cardLua)
	_, _ = c.Do("FUNCTION", "LOAD", "REPLACE", routeLua)
	return nil
}

func ResolveRedisAddr(flagRedis string) string {
	if flagRedis != "" {
		return flagRedis
	}
	if v := os.Getenv("NOVA_SPRINT_REDIS"); v != "" {
		return v
	}
	if v := os.Getenv("NOVA_REDIS_ADDR"); v != "" {
		return v
	}
	return "127.0.0.1:6379"
}
