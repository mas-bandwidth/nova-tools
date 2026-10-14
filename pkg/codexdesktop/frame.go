package codexdesktop

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
)

const (
	maxOutboundFrame = 64 << 10
	maxInboundFrame  = 1 << 20
)

type framedConn struct {
	connection net.Conn
}

// send enforces the outbound bound in SPEC-CODEX-DESKTOP-WAKE before writing.
func (wire *framedConn) send(ctx context.Context, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}
	if len(payload) == 0 || len(payload) > maxOutboundFrame {
		return fmt.Errorf("outbound frame size %d is outside 1..%d", len(payload), maxOutboundFrame)
	}
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(len(payload)))
	if err := writeFull(wire.connection, header); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if err := writeFull(wire.connection, payload); err != nil {
		return fmt.Errorf("write frame body: %w", err)
	}
	return nil
}

func (wire *framedConn) receive(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return readFrame(wire.connection)
}

func (wire *framedConn) close() error {
	return wire.connection.Close()
}

// readFrame enforces the inbound bound in SPEC-CODEX-DESKTOP-WAKE.
func readFrame(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, fmt.Errorf("read frame header: %w", err)
	}
	size := binary.LittleEndian.Uint32(header[:])
	if size == 0 || size > maxInboundFrame {
		return nil, fmt.Errorf("inbound frame size %d is outside 1..%d", size, maxInboundFrame)
	}
	payload := make([]byte, int(size))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, fmt.Errorf("read frame body: %w", err)
	}
	return payload, nil
}

func writeFull(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrUnexpectedEOF
		}
		payload = payload[written:]
	}
	return nil
}

// connectNative applies the spec's local private-IPC checks before dialing.
func connectNative(ctx context.Context, socketPath string) (transport, error) {
	if err := validateSocketPath(socketPath); err != nil {
		return nil, err
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		closeErr := connection.Close()
		if closeErr != nil {
			return nil, errors.Join(errors.New("bounded context has no deadline"), closeErr)
		}
		return nil, errors.New("bounded context has no deadline")
	}
	if err := connection.SetDeadline(deadline); err != nil {
		closeErr := connection.Close()
		if closeErr != nil {
			return nil, errors.Join(fmt.Errorf("set IPC deadline: %w", err), closeErr)
		}
		return nil, fmt.Errorf("set IPC deadline: %w", err)
	}
	return &framedConn{connection: connection}, nil
}
