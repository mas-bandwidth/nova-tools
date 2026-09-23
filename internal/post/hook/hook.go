package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const Stream = "ev:cards"

type Emitter interface {
	Emit(ctx context.Context, stream string, values map[string]interface{}) (string, error)
}

type redisEmitter struct {
	rdb *redis.Client
}

func (e *redisEmitter) Emit(ctx context.Context, stream string, values map[string]interface{}) (string, error) {
	return e.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		MaxLen: 1_000_000,
		Approx: true,
		Values: values,
	}).Result()
}

func NewRedisEmitter(rdb *redis.Client) Emitter {
	return &redisEmitter{rdb: rdb}
}

type Receiver struct {
	Emitter Emitter
	Now     func() time.Time
}

func NewReceiver(emitter Emitter) *Receiver {
	return &Receiver{
		Emitter: emitter,
		Now:     func() time.Time { return time.Now().UTC() },
	}
}

func (rec *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventType := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	if eventType == "" {
		http.Error(w, "missing X-GitHub-Event header", http.StatusBadRequest)
		return
	}

	switch eventType {
	case "pull_request", "pull_request_review", "check_suite", "issue_comment":
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "failed to read body", http.StatusInternalServerError)
			return
		}

		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		action := ""
		if a, ok := payload["action"]; ok {
			action, _ = a.(string)
		}

		values := map[string]interface{}{
			"event":    eventType,
			"action":   action,
			"delivery": deliveryID,
			"labels":   labels(eventType, payload),
			"at":       rec.Now().UTC().Format(time.RFC3339),
		}

		if _, err := rec.Emitter.Emit(r.Context(), Stream, values); err != nil {
			http.Error(w, "failed to emit event", http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, "HOOK OK event=%s delivery=%s\n", eventType, deliveryID)
	default:
		http.Error(w, "unhandled event type", http.StatusNotAcceptable)
	}
}

func labels(eventType string, payload map[string]interface{}) string {
	switch eventType {
	case "pull_request":
		if pr, ok := payload["pull_request"].(map[string]interface{}); ok {
			if n, ok := pr["number"]; ok {
				return fmt.Sprintf("pr:%v", n)
			}
		}
	case "pull_request_review":
		if pr, ok := payload["pull_request"].(map[string]interface{}); ok {
			if n, ok := pr["number"]; ok {
				return fmt.Sprintf("pr:%v", n)
			}
		}
	case "check_suite":
		if cs, ok := payload["check_suite"].(map[string]interface{}); ok {
			var nums []string
			if prs, ok := cs["pull_requests"].([]interface{}); ok {
				for _, p := range prs {
					if pm, ok := p.(map[string]interface{}); ok {
						if n, ok := pm["number"]; ok {
							nums = append(nums, fmt.Sprintf("%v", n))
						}
					}
				}
			}
			return strings.Join(nums, ",")
		}
	case "issue_comment":
		if iss, ok := payload["issue"].(map[string]interface{}); ok {
			if n, ok := iss["number"]; ok {
				return fmt.Sprintf("iss:%v", n)
			}
		}
	}
	return ""
}
