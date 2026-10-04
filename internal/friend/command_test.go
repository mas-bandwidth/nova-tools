package friend

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friendbus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The native bridge is a direct executable with JSON on stdin. It must bind
// acceptance to the delivery key and return exactly one bounded response.
func TestCommandBridgeRequiresABoundReceiptWithoutEvaluatingMessageText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		mode string
		want string
	}{
		{"receipt bound to the message", "valid", ""},
		{"another message's receipt", "wrong-key", "delivery key"},
		{"two receipts", "duplicate", "exactly one receipt"},
		{"unbounded output", "oversized", "exceeds 64 KiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			adapter, err := NewCommandAdapter(CommandConfig{
				Argv: []string{os.Args[0], "-test.run=^TestNativeBridgeProcess$", "--", tc.mode},
				Env:  []string{"NOVA_FRIEND_TEST_BRIDGE=1"},
			}, time.Second*10)
			require.NoError(t, err)
			d := friendbus.Delivery{Key: "message-1/friend", Recipient: "friend", Body: []byte(`$(touch /never) ; "quote"`)}
			got, err := adapter.Wake(t.Context(), d, nativeSession())
			if tc.want == "" {
				require.NoError(t, err)
				assert.Equal(t, nativeReceipt(), got)
			} else {
				require.ErrorContains(t, err, tc.want)
			}
		})
	}
}

func TestNativeBridgeProcess(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_FRIEND_TEST_BRIDGE") != "1" {
		return
	}
	var request CommandRequest
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		fmt.Fprintln(os.Stderr, "no JSON request")
		os.Exit(1)
	}
	if request.Operation != "wake" || request.Delivery == nil || string(request.Delivery.Body) != `$(touch /never) ; "quote"` {
		fmt.Fprintln(os.Stderr, "message did not arrive as literal JSON")
		os.Exit(1)
	}
	mode := os.Args[len(os.Args)-1]
	if mode == "oversized" {
		fmt.Fprint(os.Stdout, strings.Repeat("x", commandOutputLimit+1))
		os.Exit(0)
	}
	response := CommandResponse{Acceptance: nativeReceipt(), DeliveryKey: request.Delivery.Key}
	if mode == "wrong-key" {
		response.DeliveryKey = "another-message/friend"
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		os.Exit(1)
	}
	if mode == "duplicate" {
		if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
			os.Exit(1)
		}
	}
	os.Exit(0)
}
