package peer

import (
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/danferreira/gtorrent/internal/bitfield"
	"github.com/danferreira/gtorrent/internal/handshake"
	"github.com/danferreira/gtorrent/internal/message"
	"github.com/danferreira/gtorrent/internal/piece"
	"github.com/danferreira/gtorrent/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	InfoHash   = [20]byte{0x01, 0x02, 0x03} // truncated for brevity
	MockPeerID = [20]byte{0x99, 0x88, 0x77} // truncated for brevity
)

func TestDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		require.NoError(t, err)
		defer conn.Close()

		// - Receive Handshake -
		ch, err := handshake.Read(conn)
		require.NoError(t, err)
		require.Equal(t, InfoHash, ch.InfoHash)
		require.Equal(t, MockPeerID, ch.PeerID)

		ph := handshake.Handshake{
			InfoHash: InfoHash,
			PeerID:   [20]byte{0x66, 0x55, 0x44},
		}

		// - Send Handshake -
		err = ph.Write(conn)
		require.NoError(t, err)

		// Receive bitfield
		msg, err := message.Read(conn)
		require.NoError(t, err)
		require.Equal(t, message.MessageBitfield, msg.ID)
	}()

	addr := ln.Addr().(*net.TCPAddr)
	peer := Peer{
		Addr: net.JoinHostPort(addr.IP.String(), strconv.Itoa(addr.Port)),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	c := newConnection(&peer, 16384, bitfield.Bitfield{}, &storage.Storage{})
	err = c.dial(ctx, InfoHash, MockPeerID)

	assert.NoError(t, err)
	assert.Equal(t, &peer, c.peer)
	assert.True(t, c.peerChoking.Load())
	assert.True(t, c.amChoking.Load())
}

func TestDialTimeout(t *testing.T) {
	// Use a non-routable IP to trigger timeout
	peer := Peer{Addr: "10.255.255.1:1234"}

	start := time.Now()
	c := newConnection(&peer, 16384, bitfield.Bitfield{}, &storage.Storage{})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := c.dial(ctx, InfoHash, MockPeerID)
	duration := time.Since(start)

	assert.Error(t, err)
	assert.True(t, duration >= 200*time.Millisecond)
}

func TestAccept(t *testing.T) {
	client, server := net.Pipe()

	defer client.Close()
	defer server.Close()

	peer := Peer{Addr: "dummy:1234"}
	done := make(chan struct{}, 1)

	go func() {
		h, err := handshake.Read(client)
		require.NoError(t, err)
		assert.Equal(t, InfoHash, h.InfoHash)
		assert.Equal(t, MockPeerID, h.PeerID)

		// Receive bitfield
		msg, err := message.Read(client)
		require.NoError(t, err)
		require.Equal(t, message.MessageBitfield, msg.ID)

		done <- struct{}{}
	}()

	c := newConnection(&peer, 16384, bitfield.Bitfield{0b00000000}, &storage.Storage{})
	err := c.accept(server, InfoHash, MockPeerID)
	require.NoError(t, err)

	<-done
}

func TestHandleChoke(t *testing.T) {
	pc := newConnection(&Peer{}, 16384, bitfield.Bitfield{}, &storage.Storage{})

	pc.handleChoke()

	assert.True(t, pc.peerChoking.Load())
	assert.Equal(t, int32(0), pc.inflightRequests.Load())
}

func TestHandleUnchoke(t *testing.T) {
	pc := newConnection(&Peer{}, 16384, bitfield.Bitfield{}, &storage.Storage{})
	pc.amInterested.Store(true)

	pc.handleUnchoke()

	assert.False(t, pc.peerChoking.Load())
	assert.Equal(t, int32(0), pc.inflightRequests.Load())

	// Should request work when unchoked and interested
	select {
	case <-pc.workRequested:
		// Work was requested
	case <-time.After(200 * time.Millisecond):
		t.Error("Expected work to be requested after unchoke")
	}
}

func TestHandleInterested(t *testing.T) {
	pc := newConnection(&Peer{}, 16384, bitfield.Bitfield{}, &storage.Storage{})

	pc.handleInterested()

	assert.True(t, pc.peerInterested.Load())

	select {
	case msg := <-pc.outgoing:
		assert.Equal(t, message.MessageUnchoke, msg.ID)
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected work to be requested after unchoke")
	}
}

func TestHandleHave(t *testing.T) {
	bf := bitfield.Bitfield{0b00000000}
	ownBf := bitfield.Bitfield{0b00000000}

	pc := newConnection(&Peer{}, 16384, ownBf, &storage.Storage{})
	pc.peerBitfield = bf

	err := pc.handleHave(0)
	assert.NoError(t, err)
	assert.True(t, bf.HasPiece(0))

	// Should send interested message if we don't have the piece
	select {
	case msg := <-pc.outgoing:
		assert.Equal(t, message.MessageInterested, msg.ID)
	default:
		t.Error("Expected interested message to be sent")
	}
}

func TestHandleBitfield(t *testing.T) {
	pc := newConnection(&Peer{}, 16384, bitfield.Bitfield{0b00000000}, &storage.Storage{})
	pc.peerChoking.Store(false)
	peerBf := bitfield.Bitfield{0b11111111}
	err := pc.handleBitfield(peerBf)

	assert.NoError(t, err)
	assert.True(t, pc.bitfieldReceived.Load())
	assert.Equal(t, peerBf, pc.peerBitfield)
	assert.True(t, pc.amInterested.Load())

	// Should send interested message
	select {
	case msg := <-pc.outgoing:
		assert.Equal(t, message.MessageInterested, msg.ID)
	default:
		t.Error("expected interested message")
	}
}
func TestHandleBitfieldDuplicated(t *testing.T) {
	pc := newConnection(&Peer{}, 16384, bitfield.Bitfield{}, &storage.Storage{})
	pc.bitfieldReceived.Store(true)

	bf := bitfield.Bitfield{0b11111111}
	err := pc.handleBitfield(bf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate bitfield")
}

func TestHandlePiece(t *testing.T) {
	pieceWork := &piece.PieceWork{
		Index:  0,
		Hash:   [20]byte{},
		Length: BlockSize * 2, // Two blocks
	}
	pc := newConnection(&Peer{}, 16384, bitfield.Bitfield{}, &storage.Storage{})
	pc.inflightRequests.Store(2)
	// Add pending piece
	pc.pendingPieces[0] = &pendingPiece{
		pw:        pieceWork,
		data:      make(map[int][]byte),
		timestamp: time.Now(),
	}

	done := make(chan *piece.PieceDownloaded, 1)

	// First block
	piece1 := &message.PiecePayload{
		Index: 0,
		Begin: 0,
		Data:  make([]byte, BlockSize),
	}

	err := pc.handlePiece(piece1, done)
	assert.NoError(t, err)
	assert.Equal(t, int32(1), pc.inflightRequests.Load())

	// Piece shouldn't be complete yet
	select {
	case <-done:
		t.Error("Piece should not be complete yet")
	default:
		// Expected
	}

	// Second block - should complete the piece
	piece2 := &message.PiecePayload{
		Index: 0,
		Begin: BlockSize,
		Data:  make([]byte, BlockSize),
	}

	err = pc.handlePiece(piece2, done)
	assert.NoError(t, err)
	assert.Equal(t, int32(0), pc.inflightRequests.Load())

	// Should have completed piece
	select {
	case downloaded := <-done:
		assert.Equal(t, 0, downloaded.Index)
		assert.Equal(t, BlockSize*2, len(downloaded.Data))
		assert.Equal(t, pieceWork, downloaded.PW)
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected completed piece")
	}

	// Pending piece should be removed
	assert.Nil(t, pc.pendingPieces[0])

	// Should request more work
	select {
	case <-pc.workRequested:
		// Work was requested
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected work to be requested after piece completion")
	}
}

func TestHandleUnrequestedPiece(t *testing.T) {
	pc := newConnection(&Peer{}, 16384, bitfield.Bitfield{}, &storage.Storage{})

	done := make(chan *piece.PieceDownloaded, 1)
	piece := &message.PiecePayload{
		Index: 999, // Not in pending pieces
		Begin: 0,
		Data:  make([]byte, BlockSize),
	}

	err := pc.handlePiece(piece, done)
	assert.NoError(t, err)

	// Should not send anything to done channel
	select {
	case <-done:
		t.Error("Should not complete unrequested piece")
	default:
		// Expected
	}
}

func TestValidateRequest(t *testing.T) {
	pc := newConnection(&Peer{}, 16384, bitfield.Bitfield{}, &storage.Storage{})

	tests := []struct {
		name    string
		req     *message.RequestPayload
		wantErr bool
	}{
		{
			name: "valid request",
			req: &message.RequestPayload{
				Index:  0,
				Begin:  0,
				Length: BlockSize,
			},
			wantErr: false,
		},
		{
			name: "zero length",
			req: &message.RequestPayload{
				Index:  0,
				Begin:  0,
				Length: 0,
			},
			wantErr: true,
		},
		{
			name: "length too large",
			req: &message.RequestPayload{
				Index:  0,
				Begin:  0,
				Length: BlockSize + 1,
			},
			wantErr: true,
		},
		{
			name: "exceeds piece boundary",
			req: &message.RequestPayload{
				Index:  0,
				Begin:  16000,
				Length: BlockSize,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := pc.validateRequest(tt.req)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCanRequest(t *testing.T) {
	tests := []struct {
		name             string
		peerChoking      bool
		amInterested     bool
		inflightRequests int
		expected         bool
	}{
		{"can request", false, true, 0, true},
		{"choked", true, true, 0, false},
		{"not interested", false, false, 0, false},
		{"too many requests", false, true, 30, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := newConnection(&Peer{}, 16384, bitfield.Bitfield{}, &storage.Storage{})

			pc.peerChoking.Store(tt.peerChoking)
			pc.amInterested.Store(tt.amInterested)
			pc.inflightRequests.Store(int32(tt.inflightRequests))

			result := pc.canRequest()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestProcessPieceRequest(t *testing.T) {
	t.Run("peer has piece", func(t *testing.T) {
		pc := newConnection(&Peer{}, 16384, bitfield.Bitfield{}, &storage.Storage{})
		pc.peerBitfield = bitfield.Bitfield{0b11111111}

		pw := &piece.PieceWork{
			Index:  0,
			Hash:   [20]byte{},
			Length: BlockSize,
		}

		failChan := make(chan *piece.PieceWork, 1)
		pc.processPieceRequest(pw, failChan)

		// Should create pending piece
		assert.NotNil(t, pc.pendingPieces[0])
		assert.Equal(t, int32(1), pc.inflightRequests.Load())

		// Should send request message
		select {
		case msg := <-pc.outgoing:
			assert.Equal(t, message.MessageRequest, msg.ID)
		default:
			t.Error("Expected request message")
		}
	})

	t.Run("peer doesn't have piece", func(t *testing.T) {
		bf := bitfield.Bitfield{0b00000000}
		pc := newConnection(&Peer{}, 16384, bf, &storage.Storage{})
		pc.peerBitfield = bf

		pw := &piece.PieceWork{
			Index:  0,
			Hash:   [20]byte{},
			Length: BlockSize,
		}

		failChan := make(chan *piece.PieceWork, 1)
		pc.processPieceRequest(pw, failChan)

		// Should send to fail channel
		select {
		case failed := <-failChan:
			assert.Equal(t, pw, failed)
		default:
			t.Error("Expected piece to be sent to fail channel")
		}
	})
}

func TestWriteKeepAlive(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	pc := newConnection(&Peer{Addr: "pipe"}, BlockSize,
		bitfield.Bitfield{}, bytes.NewReader(nil),
	)
	pc.conn = client

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	go pc.messageWriterWorker(ctx)

	// send keep-alive (nil message)
	go pc.sendMessage(nil)

	// expect exactly 4 zero bytes on the wire
	b := make([]byte, 4)
	_, err := io.ReadFull(server, b)
	require.NoError(t, err)
	require.Equal(t, []byte{0, 0, 0, 0}, b)
}

func TestRequestWorkerTimeout(t *testing.T) {
	workCh := make(chan *piece.PieceWork)
	failCh := make(chan *piece.PieceWork, 1)

	pc := newConnection(&Peer{}, BlockSize, nil, nil)
	pc.config.CheckTimeoutsInterval = 100 * time.Millisecond

	pw := &piece.PieceWork{Index: 0, Length: BlockSize}
	pc.pendingPieces[0] = &pendingPiece{pw: pw, timestamp: time.Now().Add(-1 * time.Minute)}
	pc.inflightRequests.Store(1)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go pc.requestPiecesWorker(ctx, workCh, failCh)

	select {
	case got := <-failCh:
		require.Equal(t, pw, got)
	case <-ctx.Done():
		t.Fatal("worker did not time out piece")
	}
}

func TestConnection_DownloadFlow(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// Build a single-piece bitfield and data payload
	const pieceLen = BlockSize
	payload := bytes.Repeat([]byte{0xaa}, pieceLen)

	peerBF := bitfield.Bitfield{0b11111111}

	go func() {
		// - Receive Handshake -
		ch, err := handshake.Read(server)
		require.NoError(t, err)
		require.Equal(t, InfoHash, ch.InfoHash)
		require.Equal(t, MockPeerID, ch.PeerID)

		// ─ Receive bitfield ─
		msg, err := message.Read(server)
		require.NoError(t, err)
		require.Equal(t, message.MessageBitfield, msg.ID)

		// ─ Send bitfield ─
		msg = &message.Message{
			ID:      message.MessageBitfield,
			Payload: peerBF,
		}
		_, err = server.Write(msg.Serialize())
		require.NoError(t, err)

		// ─ Client is interested ─
		msg, err = message.Read(server)
		require.NoError(t, err)
		require.Equal(t, message.MessageInterested, msg.ID)

		// ─ Send Unchoke ─
		msg = &message.Message{ID: message.MessageUnchoke}
		_, err = server.Write(msg.Serialize())
		require.NoError(t, err)

		// ─ Client request a piece ─
		msg, err = message.Read(server)
		require.NoError(t, err)
		require.Equal(t, message.MessageRequest, msg.ID)

		req, err := msg.ParseAsRequest()
		require.NoError(t, err)

		// // - Send piece back -
		pieceMsg := message.NewPiece(int(req.Index), int(req.Begin), payload)
		server.Write(pieceMsg.Serialize())
	}()

	stor := bytes.NewReader([]byte{})
	pc := newConnection(&Peer{Addr: "pipe"}, pieceLen, bitfield.Bitfield{0b00000000}, stor)
	err := pc.accept(client, InfoHash, MockPeerID)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	workCh := make(chan *piece.PieceWork, 1)
	failCh := make(chan *piece.PieceWork, 1)
	doneCh := make(chan *piece.PieceDownloaded, 1)

	// queue one work item
	workCh <- &piece.PieceWork{Index: 0, Length: pieceLen}

	pc.start(ctx, workCh, failCh, doneCh)

	select {
	case got := <-doneCh:
		require.Equal(t, 0, got.Index)
		require.Equal(t, payload, got.Data)
	case <-ctx.Done():
		t.Fatal("piece never completed")
	}
}

func TestConnection_UploadFlow(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	const pieceLen = BlockSize
	payload := bytes.Repeat([]byte{0xbb}, pieceLen)

	// peer goroutine asks for the block and verifies reply
	go func() {
		// - Receive Handshake -
		_, err := handshake.Read(server)
		require.NoError(t, err)

		// read bitfield
		_, err = message.Read(server)
		require.NoError(t, err)
		// send Interested
		server.Write((&message.Message{ID: message.MessageInterested}).Serialize())

		// expect Unchoke
		msg, err := message.Read(server)
		require.NoError(t, err)
		require.Equal(t, message.MessageUnchoke, msg.ID)

		// request piece
		req := message.NewRequest(0, 0, pieceLen)
		server.Write(req.Serialize())

		// read piece
		msg, err = message.Read(server)
		require.NoError(t, err)
		require.Equal(t, message.MessagePiece, msg.ID)

		pp := msg.AsPiece()
		require.Equal(t, payload, pp.Data)
	}()

	// storage returns payload

	stor := bytes.NewReader(payload)

	ownBF := bitfield.Bitfield{0b10000000}

	pc := newConnection(&Peer{Addr: "pipe_up"}, pieceLen, ownBF, stor)
	err := pc.accept(client, InfoHash, MockPeerID)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	pc.start(ctx, make(chan *piece.PieceWork), make(chan *piece.PieceWork), make(chan *piece.PieceDownloaded))

	// wait peer side to finish (pipe closes)
	<-ctx.Done()
}
