package codexdesktop

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadFrameEnforcesInboundBound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		size uint32
	}{
		{name: "empty", size: 0},
		{name: "over cap", size: maxInboundFrame + 1},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var frame bytes.Buffer
			require.NoError(t, binary.Write(&frame, binary.LittleEndian, test.size))
			payload, err := readFrame(&frame)
			assert.Nil(t, payload)
			assert.Error(t, err)
		})
	}
}

func TestReadFrameReadsExactlyOnePayload(t *testing.T) {
	t.Parallel()

	var frame bytes.Buffer
	require.NoError(t, binary.Write(&frame, binary.LittleEndian, uint32(3)))
	_, err := frame.Write([]byte("oneextra"))
	require.NoError(t, err)
	payload, err := readFrame(&frame)
	require.NoError(t, err)
	assert.Equal(t, []byte("one"), payload)
	assert.Equal(t, "extra", frame.String())
}
