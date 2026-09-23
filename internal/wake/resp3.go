package wake

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Resp3ConnPool is one RESP3 connection shared by a friend window's heartbeat
// and its wake. The window never polls: Wait parks on XREAD BLOCK against the
// wake stream, so an XADD reaches the window as soon as Redis serves it, and
// Run renews the presence key on the same connection between blocks.
type Resp3ConnPool struct {
	mu       sync.Mutex
	client   *redis.Client
	conn     *redis.Conn
	presence string
	stream   string
	ttl      time.Duration
	lastID   string
	closed   bool
}

// NewResp3ConnPool dials addr with protocol 3 and pins a single connection.
// presence is the heartbeat key (renewed with ttl); stream is the wake stream.
func NewResp3ConnPool(ctx context.Context, addr, presence, stream string, ttl time.Duration) (*Resp3ConnPool, error) {
	if presence == "" || stream == "" {
		return nil, errors.New("resp3: presence key and wake stream are required")
	}
	if ttl <= 0 {
		return nil, errors.New("resp3: heartbeat ttl must be positive")
	}
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		Protocol:     3,
		PoolSize:     1,
		MinIdleConns: 0,
		ReadTimeout:  -1, // XREAD BLOCK sets its own deadline
	})
	conn := client.Conn()
	hello, err := conn.Hello(ctx, 3, "", "", "").Result()
	if err != nil {
		_ = conn.Close()
		_ = client.Close()
		return nil, fmt.Errorf("resp3: HELLO 3: %w", err)
	}
	if proto, ok := hello["proto"].(int64); !ok || proto != 3 {
		_ = conn.Close()
		_ = client.Close()
		return nil, fmt.Errorf("resp3: server answered proto %v, want 3", hello["proto"])
	}
	return &Resp3ConnPool{client: client, conn: conn, presence: presence, stream: stream, ttl: ttl, lastID: "$"}, nil
}

// Heartbeat renews the presence key on the shared connection.
func (p *Resp3ConnPool) Heartbeat(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("resp3: closed")
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	return p.conn.Set(ctx, p.presence, stamp, p.ttl).Err()
}

// Subscribe anchors the wake cursor at the stream's current tail, so every
// XADD after Subscribe returns is delivered by Wait and none before it is.
func (p *Resp3ConnPool) Subscribe(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("resp3: closed")
	}
	tail, err := p.conn.XRevRangeN(ctx, p.stream, "+", "-", 1).Result()
	if err != nil {
		return fmt.Errorf("resp3: XREVRANGE %s: %w", p.stream, err)
	}
	p.lastID = "0-0"
	if len(tail) == 1 {
		p.lastID = tail[0].ID
	}
	return nil
}

// Wait parks on XREAD BLOCK for up to block and returns the wakes that
// arrived. A timeout with no wake returns (nil, nil).
func (p *Resp3ConnPool) Wait(ctx context.Context, block time.Duration) ([]redis.XMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("resp3: closed")
	}
	if block <= 0 {
		block = time.Millisecond
	}
	res, err := p.conn.XRead(ctx, &redis.XReadArgs{
		Streams: []string{p.stream, p.lastID},
		Count:   64,
		Block:   block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resp3: XREAD %s: %w", p.stream, err)
	}
	var out []redis.XMessage
	for _, s := range res {
		out = append(out, s.Messages...)
	}
	if n := len(out); n > 0 {
		p.lastID = out[n-1].ID
	}
	return out, nil
}

// Run beats, then blocks for wakes, beat interval at a time, until ctx ends.
// Heartbeat and wake share the pool's one connection; nothing polls.
func (p *Resp3ConnPool) Run(ctx context.Context, every time.Duration, onWake func(redis.XMessage)) error {
	if every <= 0 || every >= p.ttl {
		return fmt.Errorf("resp3: beat interval %s must be positive and under ttl %s", every, p.ttl)
	}
	for {
		if err := p.Heartbeat(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		msgs, err := p.Wait(ctx, every)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		for _, m := range msgs {
			onWake(m)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

// Close releases the shared connection.
func (p *Resp3ConnPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return errors.Join(p.conn.Close(), p.client.Close())
}
