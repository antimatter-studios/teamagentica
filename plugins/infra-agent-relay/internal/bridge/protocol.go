package bridge

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Wire protocol constants — must match agent-bridge.
const maxPayloadSize = 16 * 1024 * 1024

// Message types — client to server.
const (
	MsgPrompt  byte = 0x01
	MsgCommand byte = 0x02
)

// Message types — server to client.
const (
	MsgQueued       byte = 0x10
	MsgResponseLine byte = 0x11
	MsgResponseEnd  byte = 0x12
	MsgError        byte = 0x13
)

// Message is a framed protocol message.
type Message struct {
	Type    byte
	Payload []byte
}

// WriteMessage writes a framed message.
func WriteMessage(w io.Writer, msg Message) error {
	length := uint32(len(msg.Payload))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return err
	}
	if _, err := w.Write([]byte{msg.Type}); err != nil {
		return err
	}
	if length > 0 {
		if _, err := w.Write(msg.Payload); err != nil {
			return err
		}
	}
	return nil
}

// ReadMessage reads a framed message.
func ReadMessage(r io.Reader) (Message, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return Message{}, err
	}
	if length > maxPayloadSize {
		return Message{}, fmt.Errorf("payload too large: %d", length)
	}

	typeBuf := make([]byte, 1)
	if _, err := io.ReadFull(r, typeBuf); err != nil {
		return Message{}, err
	}

	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return Message{}, err
		}
	}

	return Message{Type: typeBuf[0], Payload: payload}, nil
}
