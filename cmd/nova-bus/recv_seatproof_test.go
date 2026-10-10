package main

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"testing"
	"time"
)

func TestTheCoordinatorReceiverBeatsAtMostOnceAMinuteWithTheObservedBinding(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	var commands []string
	w := world{now: func() time.Time { return now }, getenv: func(k string) string {
		if k == "NOVA_SPRINT_SERVER" {
			return "scratch"
		}
		return ""
	}, run: func(_ context.Context, command, stdin string, out, errs io.Writer) (int, error) {
		commands = append(commands, command)
		switch len(commands) {
		case 1:
			_, err := io.WriteString(out, `{"holder":"seat-a"}`)
			return 0, err
		case 2:
			_, err := io.WriteString(out, `{"seat":{"holder":"seat-a","epoch":7,"generation":9},"set":{"target":"/scratch/job","session":"session-a"}}`)
			return 0, err
		}
		return 0, nil
	}}
	last, err := w.receiverSeatBeat(context.Background(), "seat-a", time.Time{})
	require.NoError(t, err)
	assert.Equal(t, now, last)
	assert.Equal(t, "nova-sprint seat push --actor seat-a --beat bus --epoch 7 --seat-generation 9 --observed-target /scratch/job --observed-session session-a", commands[2])
	now = now.Add(time.Second)
	_, err = w.receiverSeatBeat(context.Background(), "seat-a", last)
	require.NoError(t, err)
	assert.Len(t, commands, 3, "no extra command before the next minute")
}

func TestAnUnconfiguredForeignOrFailedReceiverRenewsNoSeatProof(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"unconfigured", "foreign", "failed"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			calls := 0
			w := world{now: func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }, getenv: func(string) string {
				if mode == "unconfigured" {
					return ""
				}
				return "scratch"
			}, run: func(_ context.Context, _ string, _ string, out, errs io.Writer) (int, error) {
				calls++
				if mode == "failed" {
					return 1, nil
				}
				_, err := io.WriteString(out, `{"holder":"another-seat"}`)
				return 0, err
			}}
			last, err := w.receiverSeatBeat(context.Background(), "seat-a", time.Time{})
			assert.Zero(t, last)
			if mode == "failed" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.LessOrEqual(t, calls, 1)
		})
	}
}
