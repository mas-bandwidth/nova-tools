package store

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/redis/go-redis/v9"
)

// CensusRequest names what one census reads. Exactly one of Set or KeysFrom.
type CensusRequest struct {
	Set      string
	KeysFrom io.Reader
	Fields   []string
}

// CensusSummary is the count line the table and the fold read.
type CensusSummary struct {
	Keys    int
	Present int
	Missing int
	Err     int
}

func (s CensusSummary) Line() string {
	return fmt.Sprintf("CENSUS keys=%d present=%d missing=%d err=%d", s.Keys, s.Present, s.Missing, s.Err)
}

func CensusSet(set string) (index, prefix string, err error) {
	switch set {
	case "benches":
		return "benches", "bench:", nil
	case "friends":
		return "friends", "friend:", nil
	}
	if rest, ok := strings.CutPrefix(set, "sprint:"); ok {
		name, state, ok := strings.Cut(rest, ":")
		if ok && name != "" && state != "" && !strings.ContainsAny(name+state, ": \t\n") {
			return "s:" + name + ":idx:task:" + state, "s:" + name + ":task:", nil
		}
		return "", "", fmt.Errorf("set %q: want sprint:<name>:<state>", set)
	}
	return "", "", fmt.Errorf("unknown set %q", set)
}

func CensusKeys(ctx context.Context, s *Store, req CensusRequest) ([]string, error) {
	if err := req.Check(); err != nil {
		return nil, err
	}
	if req.KeysFrom != nil {
		return readKeys(req.KeysFrom)
	}
	index, prefix, err := CensusSet(req.Set)
	if err != nil {
		return nil, err
	}
	members, err := s.client.SMembers(ctx, index).Result()
	if err != nil {
		return nil, fmt.Errorf("census set %s: %w", index, err)
	}
	sort.Strings(members)
	keys := make([]string, 0, len(members))
	for _, m := range members {
		if m != "" {
			keys = append(keys, prefix+m)
		}
	}
	return keys, nil
}

func (req CensusRequest) Check() error {
	if (req.Set == "") == (req.KeysFrom == nil) {
		return errors.New("census needs exactly one of --set or --keys-from")
	}
	if len(req.Fields) == 0 {
		return errors.New("census needs --fields")
	}
	for _, f := range req.Fields {
		if strings.TrimSpace(f) == "" {
			return errors.New("census --fields has a blank field")
		}
	}
	if req.Set != "" {
		if _, _, err := CensusSet(req.Set); err != nil {
			return err
		}
	}
	return nil
}

func readKeys(r io.Reader) ([]string, error) {
	var keys []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		keys = append(keys, line)
	}
	return keys, sc.Err()
}

func RunCensus(ctx context.Context, s *Store, req CensusRequest, out io.Writer) (CensusSummary, error) {
	if err := req.Check(); err != nil {
		return CensusSummary{}, err
	}
	keys, err := CensusKeys(ctx, s, req)
	if err != nil {
		return CensusSummary{}, err
	}
	rows, sum, err := s.census(ctx, keys, req.Fields)
	if err != nil {
		return CensusSummary{}, err
	}
	w := bufio.NewWriter(out)
	for _, row := range rows {
		w.WriteString(row)
		w.WriteByte('\n')
	}
	w.WriteString(sum.Line())
	w.WriteByte('\n')
	return sum, w.Flush()
}

func (s *Store) census(ctx context.Context, keys, fields []string) ([]string, CensusSummary, error) {
	sum := CensusSummary{Keys: len(keys)}
	if len(keys) == 0 {
		return nil, sum, nil
	}
	pipe := s.client.Pipeline()
	exists := make([]*redis.IntCmd, len(keys))
	reads := make([]*redis.SliceCmd, len(keys))
	for i, key := range keys {
		exists[i] = pipe.Exists(ctx, key)
		reads[i] = pipe.HMGet(ctx, key, fields...)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		var rerr redis.Error
		if !errors.As(err, &rerr) {
			return nil, CensusSummary{}, fmt.Errorf("census pipeline: %w", err)
		}
	}
	rows := make([]string, len(keys))
	for i, key := range keys {
		n, err := exists[i].Result()
		if err != nil {
			return nil, CensusSummary{}, fmt.Errorf("census EXISTS %s: %w", key, err)
		}
		if n == 0 {
			rows[i] = "MISSING " + key
			sum.Missing++
			continue
		}
		values, err := reads[i].Result()
		if err != nil {
			rows[i] = "ERR " + key + " " + strings.Join(strings.Fields(err.Error()), " ")
			sum.Err++
			continue
		}
		var b strings.Builder
		b.WriteString(key)
		for j, f := range fields {
			b.WriteByte(' ')
			b.WriteString(f)
			b.WriteByte('=')
			if j >= len(values) || values[j] == nil {
				b.WriteString("MISSING")
				continue
			}
			b.WriteString(censusValue(fmt.Sprint(values[j])))
		}
		rows[i] = b.String()
		sum.Present++
	}
	return rows, sum, nil
}

func censusValue(v string) string {
	if v == "" || v == "MISSING" {
		return strconv.Quote(v)
	}
	for _, r := range v {
		if unicode.IsSpace(r) || r == '"' || r == '=' || !unicode.IsPrint(r) {
			return strconv.Quote(v)
		}
	}
	return v
}
