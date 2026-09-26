package task

// friend down|up (#3206 PR A): the one writer of friend:<f>:down, one Redis
// Function call (ns_friend_down in internal/nsprint/fn/lua/task_queue.lua).
// A down friend gets no push, take or copy.

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// FunctionFriendDown is the function task_queue.lua registers.
const FunctionFriendDown = "ns_friend_down"

// Reply is one function reply: the status word and the words after it.
type Reply struct {
	Status string
	Args   []string
}

// FriendDown sets (on) or clears friend:<f>:down, a HASH {reason, actor, at}.
// A down friend\x27s push and take are refused.
func FriendDown(ctx context.Context, st *store.Store, friend string, on bool, reason, actor, idem string) (Reply, error) {
	if friend == "" || actor == "" {
		return Reply{}, fmt.Errorf("task friend down: friend and actor are required")
	}
	flag := "0"
	if on {
		flag = "1"
	}
	if st == nil {
		return Reply{}, fmt.Errorf("%s: nil store", FunctionFriendDown)
	}
	raw, err := st.Client().FCall(ctx, FunctionFriendDown, nil, friend, flag, reason, actor, idem).Result()
	if err != nil {
		return Reply{}, fmt.Errorf("%s: %w", FunctionFriendDown, err)
	}
	values, ok := raw.([]any)
	if !ok || len(values) == 0 {
		return Reply{}, fmt.Errorf("%s: unexpected reply %T", FunctionFriendDown, raw)
	}
	out := Reply{Status: fmt.Sprint(values[0])}
	for _, v := range values[1:] {
		out.Args = append(out.Args, fmt.Sprint(v))
	}
	return out, nil
}
