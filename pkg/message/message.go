package message

import (
	"encoding/binary"
	"fmt"
	"io"
)

type MessageID uint8

const (
	MessageChoke       MessageID = 0
	MessageUnchoke     MessageID = 1
	MessageInterested  MessageID = 2
	MessageNotInterest MessageID = 3
	MessageHave        MessageID = 4
	MessageBitfield    MessageID = 5
	MessageRequest     MessageID = 6
	MessagePiece       MessageID = 7
	MessageCancel      MessageID = 8
)

type Message struct {
	ID      MessageID
	Payload []byte
}

func (m *Message) Serialize() []byte {
	if m == nil {
		return make([]byte, 4)
	}

	length := uint32(len(m.Payload) + 1) // +1 for id
	buf := make([]byte, 4+length)
	binary.BigEndian.PutUint32(buf[0:4], length)
	buf[4] = byte(m.ID)
	copy(buf[5:], m.Payload)
	return buf
}

func Read(reader io.Reader) (*Message, error) {
	msgLen := make([]byte, 4)
	_, err := io.ReadFull(reader, msgLen)
	if err != nil {
		return nil, fmt.Errorf("buffer is too short: %w", err)
	}
	length := binary.BigEndian.Uint32(msgLen)

	if length == 0 {
		fmt.Println("Received Keep Alive message")

		return nil, nil
	}

	payload := make([]byte, length)

	_, err = io.ReadFull(reader, payload)
	if err != nil {
		return nil, fmt.Errorf("payload is too short: %w", err)
	}

	return &Message{
		ID:      MessageID(payload[0]),
		Payload: payload[1:],
	}, nil
}

func NewRequest(index int, begin int, length int) Message {
	buff := make([]byte, 12)
	binary.BigEndian.PutUint32(buff, uint32(index))
	binary.BigEndian.PutUint32(buff, uint32(begin))
	binary.BigEndian.PutUint32(buff, uint32(length))

	return Message{
		ID:      MessageRequest,
		Payload: buff,
	}
}
