package message

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
)

type MessageID uint8

const (
	MessageChoke         MessageID = 0
	MessageUnchoke       MessageID = 1
	MessageInterested    MessageID = 2
	MessageNotInterested MessageID = 3
	MessageHave          MessageID = 4
	MessageBitfield      MessageID = 5
	MessageRequest       MessageID = 6
	MessagePiece         MessageID = 7
	MessageCancel        MessageID = 8
)

type Message struct {
	ID      MessageID
	Payload []byte
}

type RequestPayload struct {
	Index  uint32
	Begin  uint32
	Length uint32
}

type PiecePayload struct {
	Index uint32
	Begin uint32
	Data  []byte
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
	if _, err := io.ReadFull(reader, msgLen); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, err
	}

	length := binary.BigEndian.Uint32(msgLen)
	if length == 0 {
		slog.Info("received Keep Alive message")

		return nil, nil
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.ErrUnexpectedEOF
		}

		return nil, fmt.Errorf("read payload: %w", err)
	}

	return &Message{
		ID:      MessageID(payload[0]),
		Payload: payload[1:],
	}, nil
}

func NewRequest(index, begin, length int) Message {
	buff := make([]byte, 12)
	binary.BigEndian.PutUint32(buff, uint32(index))
	binary.BigEndian.PutUint32(buff[4:], uint32(begin))
	binary.BigEndian.PutUint32(buff[8:], uint32(length))

	return Message{
		ID:      MessageRequest,
		Payload: buff,
	}
}

func NewPiece(index, begin int, data []byte) *Message {
	buff := make([]byte, 8+len(data))
	binary.BigEndian.PutUint32(buff[0:4], uint32(index))
	binary.BigEndian.PutUint32(buff[4:], uint32(begin))
	copy(buff[8:], data)

	return &Message{
		ID:      MessagePiece,
		Payload: buff,
	}
}

func NewHave(index int) Message {
	buff := make([]byte, 4)
	binary.BigEndian.PutUint32(buff, uint32(index))

	return Message{
		ID:      MessageHave,
		Payload: buff,
	}
}

func (m *Message) AsPiece() *PiecePayload {
	index := binary.BigEndian.Uint32(m.Payload[0:4])
	begin := binary.BigEndian.Uint32(m.Payload[4:8])
	data := m.Payload[8:]

	return &PiecePayload{
		Index: index,
		Begin: begin,
		Data:  data,
	}
}

func (m *Message) ParseAsHave() int {
	index := binary.BigEndian.Uint32(m.Payload[0:4])
	return int(index)
}

func (m *Message) ParseAsRequest() (*RequestPayload, error) {
	if len(m.Payload) < 12 {
		return nil, fmt.Errorf("invalid length for a request message")
	}

	return &RequestPayload{
		Index:  binary.BigEndian.Uint32(m.Payload[0:4]),
		Begin:  binary.BigEndian.Uint32(m.Payload[4:8]),
		Length: binary.BigEndian.Uint32(m.Payload[8:12]),
	}, nil
}
