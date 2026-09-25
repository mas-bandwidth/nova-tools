package line

import (
	"fmt"
	"time"
)

type Record struct {
	Card  string
	Repo  string
	N     string
	Head  string
	Who   string
	Kind  string
	Score string
	Gates string
	Body  string
}

const NsCardMoveLua = `
local id = ARGV[1]
local where = ARGV[2]
local outcome = ARGV[3]
local bench = ARGV[4]

local card_key = "card:" .. id
local old_where = redis.call("HGET", card_key, "where")
local stream = redis.call("HGET", card_key, "stream")

if old_where and old_where ~= "" and stream and stream ~= "" then
	redis.call("ZREM", "ws:" .. stream .. ":" .. old_where, id)
end

redis.call("HSET", card_key, "where", where)
if outcome and outcome ~= "" then
	redis.call("HSET", card_key, "outcome", outcome)
end
if bench and bench ~= "" then
	redis.call("HSET", card_key, "bench", bench)
end

if stream and stream ~= "" then
	local score = redis.call("HGET", card_key, "created_at")
	if not score or score == "" then score = "0" end
	redis.call("ZADD", "ws:" .. stream .. ":" .. where, score, id)
end
`

func Post(addr string, r Record) error {
	if r.Repo == "" || r.N == "" || r.Head == "" || r.Who == "" || r.Kind == "" {
		return fmt.Errorf("malformed record: missing required fields")
	}

	redis, err := DialRedis(addr)
	if err != nil {
		return err
	}
	defer redis.Close()

	key := fmt.Sprintf("line:%s:%s:%s:%s:%s", r.Repo, r.N, r.Head, r.Who, r.Kind)
	_, err = redis.Do("HSET", key,
		"card", r.Card,
		"repo", r.Repo,
		"n", r.N,
		"head", r.Head,
		"who", r.Who,
		"kind", r.Kind,
		"score", r.Score,
		"gates", r.Gates,
		"body", r.Body,
		"created_at", fmt.Sprintf("%d", time.Now().UnixMilli()))
	if err != nil {
		return err
	}

	streamKey := fmt.Sprintf("pr:%s:%s:lines", r.Repo, r.N)
	_, err = redis.Do("XADD", streamKey, "*", "line", key)
	if err != nil {
		return err
	}

	if r.Kind == "CLOSE" && r.Card != "" {
		_, err = redis.Do("EVAL", NsCardMoveLua, "0", r.Card, "done", "", "")
		if err != nil {
			return err
		}
	}

	return nil
}

func ListByHead(addr, repo, n, head string) ([]Record, error) {
	redis, err := DialRedis(addr)
	if err != nil {
		return nil, err
	}
	defer redis.Close()

	streamKey := fmt.Sprintf("pr:%s:%s:lines", repo, n)
	res, err := redis.Do("XRANGE", streamKey, "-", "+")
	if err != nil {
		return nil, err
	}

	var records []Record
	entries, ok := res.([]interface{})
	if !ok {
		return nil, nil // Stream might not exist
	}

	seen := make(map[string]bool)

	for _, entry := range entries {
		entryArr := entry.([]interface{})
		fields := entryArr[1].([]interface{})
		var lineKey string
		for i := 0; i < len(fields); i += 2 {
			k := fields[i].(string)
			v := fields[i+1].(string)
			if k == "line" {
				lineKey = v
				break
			}
		}
		if lineKey == "" {
			continue
		}
		
		seen[lineKey] = true
	}

	for lineKey := range seen {
		hres, err := redis.Do("HGETALL", lineKey)
		if err != nil {
			return nil, err
		}
		hfields := hres.([]interface{})
		if len(hfields) == 0 {
			continue
		}

		var rec Record
		for i := 0; i < len(hfields); i += 2 {
			k := hfields[i].(string)
			v := hfields[i+1].(string)
			switch k {
			case "card":
				rec.Card = v
			case "repo":
				rec.Repo = v
			case "n":
				rec.N = v
			case "head":
				rec.Head = v
			case "who":
				rec.Who = v
			case "kind":
				rec.Kind = v
			case "score":
				rec.Score = v
			case "gates":
				rec.Gates = v
			case "body":
				rec.Body = v
			}
		}
		
		if rec.Head == head {
			records = append(records, rec)
		}
	}

	return records, nil
}
