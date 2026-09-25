package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// #3277: the first batch against a closed port is Unreachable, so a verb keeps
// its unreachable exit code; a command's own refusal is not.
func TestUnreachable3277(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	st, err := store.Open(context.Background(), "127.0.0.1:1")
	if err != nil {
		t.Fatalf("Open sends nothing and must not fail on a closed port: %v", err)
	}
	defer st.Close()
	_, err = st.PipelineHMGet(context.Background(), []store.HashRead{{Key: "k", Fields: []string{"f"}}})
	if !store.Unreachable(err) {
		t.Fatalf("first batch on a closed port = %v; want Unreachable", err)
	}
	if err := st.Reach(context.Background()); !store.Unreachable(err) {
		t.Fatalf("Reach on a closed port = %v; want Unreachable", err)
	}
	for _, e := range []error{nil, errors.New("WRONGTYPE Operation against a key holding the wrong kind of value"), errors.New("ERR unknown command")} {
		if store.Unreachable(e) {
			t.Fatalf("Unreachable(%v) = true; want false", e)
		}
	}
}
