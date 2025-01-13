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
	MessageID MessageID
	Payload   []byte
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
		MessageID: MessageID(payload[0]),
		Payload:   payload[1:],
	}, nil
}
