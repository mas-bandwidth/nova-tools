package keepalive

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

type redisHook struct {
	one   func(redis.Cmder) error
	batch func([]redis.Cmder) error
}

func (h redisHook) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) { panic("keepalive unit tests must not dial") }
}
func (h redisHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, c redis.Cmder) error { return h.one(c) }
}
func (h redisHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, cs []redis.Cmder) error { return h.batch(cs) }
}

func hookedRedis(t *testing.T, h redisHook) Redis {
	t.Helper()
	c := redis.NewClient(&redis.Options{})
	c.AddHook(h)
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	return Redis{C: c}
}

func sampleFrame() Frame {
	return Frame{Version: Version, From: "leader", To: "worker", Role: "coordinator", Seat: Seat{Holder: "leader", Epoch: 1, Generation: 1}, Instance: "boot-1", Seq: 1}
}

func TestAppendUsesDedicatedLaneAndPreservesUncertainResults(t *testing.T) {
	t.Parallel()
	for _, uncertain := range []bool{false, true} {
		name := "success"
		if uncertain {
			name = "second-reply-lost"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			lost := errors.New("reply lost")
			trips := 0
			r := hookedRedis(t, redisHook{batch: func(cs []redis.Cmder) error {
				trips++
				require.Len(t, cs, 2)
				for i, c := range cs {
					a := c.Args()
					require.Len(t, a, 5)
					assert.Equal(t, "eval", a[0])
					assert.Equal(t, appendScript, a[1])
					assert.Equal(t, 1, a[2])
					assert.True(t, strings.HasPrefix(a[3].(string), "bus2:keepalive:"))
					var f Frame
					require.NoError(t, json.Unmarshal([]byte(a[4].(string)), &f))
					assert.Equal(t, "leader", f.From)
					assert.Equal(t, "coordinator", f.Role)
					if i == 1 && uncertain {
						c.SetErr(lost)
					} else {
						c.(*redis.Cmd).SetVal("100000-" + string(rune('0'+i)))
					}
				}
				if uncertain {
					return lost
				}
				return nil
			}})
			first, second := sampleFrame(), sampleFrame()
			second.To = "worker-two"
			got, err := r.AppendBatch(context.Background(), []Frame{first, second})
			require.Len(t, got, 2)
			assert.Equal(t, AppendResult{ID: "100000-0"}, got[0])
			assert.Equal(t, 1, trips, "there is no implicit retry")
			if uncertain {
				assert.ErrorIs(t, err, lost)
				assert.Equal(t, AppendResult{Unknown: true}, got[1])
			} else {
				assert.NoError(t, err)
				assert.Equal(t, AppendResult{ID: "100000-1"}, got[1])
			}
		})
	}
}

func TestInvalidBatchWritesNothing(t *testing.T) {
	t.Parallel()
	bad := sampleFrame()
	bad.To = "../message-stream"
	r := hookedRedis(t, redisHook{batch: func([]redis.Cmder) error { t.Fatal("invalid batch reached Redis"); return nil }})
	got, err := r.AppendBatch(context.Background(), []Frame{sampleFrame(), bad})
	assert.Error(t, err)
	assert.Nil(t, got)
}

func TestReadAdvancesPastMalformedFrame(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(sampleFrame())
	require.NoError(t, err)
	r := hookedRedis(t, redisHook{one: func(c redis.Cmder) error {
		assert.Equal(t, []interface{}{"xrange", "bus2:keepalive:worker", "(99999-0", "+", "count", int64(3)}, c.Args())
		c.(*redis.XMessageSliceCmd).SetVal([]redis.XMessage{
			{ID: "100000-0", Values: map[string]interface{}{"frame": "not json"}},
			{ID: "100000-1", Values: map[string]interface{}{"frame": string(body)}},
			{ID: "100000-2", Values: map[string]interface{}{"frame": strings.Repeat("x", FrameLimit+1)}},
		})
		return nil
	}})
	got, err := r.ReadBatch(context.Background(), "worker", "99999-0", 3)
	require.NoError(t, err)
	require.Len(t, got.Entries, 3)
	assert.NotEmpty(t, got.Entries[0].DecodeError)
	assert.Empty(t, got.Entries[1].DecodeError)
	assert.Equal(t, sampleFrame(), got.Entries[1].Frame)
	assert.NotEmpty(t, got.Entries[2].DecodeError)
	assert.Equal(t, "100000-2", got.Next)
}

func TestHeadAndReadHaveExplicitEmptyCursor(t *testing.T) {
	t.Parallel()
	for _, head := range []bool{true, false} {
		name := "read"
		if head {
			name = "head"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := hookedRedis(t, redisHook{one: func(c redis.Cmder) error {
				assert.NotContains(t, c.Args(), "$")
				c.(*redis.XMessageSliceCmd).SetVal(nil)
				return nil
			}})
			if head {
				cursor, err := r.Head(context.Background(), "worker")
				assert.NoError(t, err)
				assert.Equal(t, "0-0", cursor)
			} else {
				read, err := r.ReadBatch(context.Background(), "worker", "", 1)
				assert.NoError(t, err)
				assert.Equal(t, "0-0", read.Next)
				assert.Empty(t, read.Entries)
			}
		})
	}
}

func TestRetentionScriptUsesStreamTimeAndExpiresIdleLane(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, id, cutoff string }{
		{"early", "59999-0", ""},
		{"boundary", "60000-7", "0-0"},
		{"normal", "1791124000000-3", "1791123940000-0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := lua.NewState()
			defer l.Close()
			keys, argv, server := l.NewTable(), l.NewTable(), l.NewTable()
			keys.RawSetInt(1, lua.LString("bus2:keepalive:worker"))
			argv.RawSetInt(1, lua.LString("frame-json"))
			l.SetGlobal("KEYS", keys)
			l.SetGlobal("ARGV", argv)
			var calls [][]string
			server.RawSetString("call", l.NewFunction(func(l *lua.LState) int {
				var args []string
				for i := 1; i <= l.GetTop(); i++ {
					args = append(args, l.Get(i).String())
				}
				calls = append(calls, args)
				if args[0] == "XADD" {
					l.Push(lua.LString(tc.id))
				} else {
					l.Push(lua.LNumber(1))
				}
				return 1
			}))
			l.SetGlobal("redis", server)
			require.NoError(t, l.DoString(appendScript))
			want := [][]string{{"XADD", "bus2:keepalive:worker", "*", "frame", "frame-json"}}
			if tc.cutoff != "" {
				want = append(want, []string{"XTRIM", "bus2:keepalive:worker", "MINID", tc.cutoff})
			}
			want = append(want, []string{"PEXPIRE", "bus2:keepalive:worker", "60000"})
			assert.Equal(t, want, calls)
		})
	}
}

func TestFrameDecodeRejectsAmbiguousAndInvalidInputs(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(sampleFrame())
	require.NoError(t, err)
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", string(body), true},
		{"trailing", string(body) + " {}", false},
		{"unknown-field", strings.TrimSuffix(string(body), "}") + `,"other":1}`, false},
		{"unsupported-version", strings.Replace(string(body), `"version":1`, `"version":2`, 1), false},
		{"wrong-role", strings.Replace(string(body), `"coordinator"`, `"friend"`, 1), false},
		{"missing-generation", strings.Replace(string(body), `"generation":1`, `"generation":0`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, problem := decodeFrame(tc.body)
			assert.Equal(t, tc.valid, problem == "", problem)
		})
	}
}
