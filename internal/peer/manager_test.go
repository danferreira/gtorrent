package peer

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/danferreira/gtorrent/internal/bitfield"
	"github.com/danferreira/gtorrent/internal/handshake"
	"github.com/danferreira/gtorrent/internal/message"
	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/piece"
	"github.com/danferreira/gtorrent/internal/state"
	"github.com/danferreira/gtorrent/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	m := &metadata.Metadata{
		Info: metadata.Info{
			InfoHash:    [20]byte{0x1, 0x2, 0x3, 0x4, 0x5, 0x6, 0x7, 0x8, 0x9, 0xa, 0xb, 0xc, 0xd, 0xe, 0xf, 0x10, 0x11, 0x12, 0x13, 0x14},
			PieceLength: 16384,
		},
	}
	peerID := [20]byte{0x99, 0x88, 0x77}
	pool := NewPool(10)
	storage := &storage.Storage{}
	state := &state.State{Bitfield: bitfield.Bitfield{0b11100000}, Left: 1000}

	manager := NewManager(m, state, peerID, pool, storage)

	assert.NotNil(t, manager)
	assert.Equal(t, m.Info.InfoHash, manager.infoHash)
	assert.Equal(t, peerID, manager.peerID)
	assert.Equal(t, state, manager.state)
	assert.Equal(t, m.Info.PieceLength, manager.pieceLength)
	assert.Equal(t, storage, manager.storage)
	assert.NotNil(t, manager.connectedPeers)
	assert.Equal(t, 0, len(manager.connectedPeers))
}

func TestInboundConnection(t *testing.T) {
	// Create a pipe to simulate inbound connection
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	m := &metadata.Metadata{
		Info: metadata.Info{
			InfoHash:    InfoHash,
			PieceLength: 16384,
		},
	}
	pool := NewPool(1)
	state := &state.State{Bitfield: bitfield.Bitfield{0b11100000}, Left: 1000}
	manager := NewManager(m, state, PeerID, pool, &storage.Storage{})

	// Set up channels
	workChan := make(chan *piece.PieceWork, 10)
	failChan := make(chan *piece.PieceWork, 10)
	inboundedChan := make(chan net.Conn)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_ = manager.Run(ctx, workChan, failChan, inboundedChan)

	go func() {
		_, _ = handshake.Read(server)
		_, err := message.Read(server)
		require.NoError(t, err)
	}()

	inboundedChan <- client

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, manager.countPeers())

	<-ctx.Done()
}

func TestOutboundConnection(t *testing.T) {
	// Create a pipe to simulate outbound connection
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	m := &metadata.Metadata{
		Info: metadata.Info{
			InfoHash:    InfoHash,
			PieceLength: 16384,
		},
	}

	addr := ln.Addr().(*net.TCPAddr)
	peer := Peer{
		Addr: net.JoinHostPort(addr.IP.String(), strconv.Itoa(addr.Port)),
	}

	pool := NewPool(1)
	pool.PushMany([]Peer{peer})

	manager := NewManager(m, &state.State{Bitfield: bitfield.Bitfield{0b11100000}, Left: 1000}, PeerID, pool, &storage.Storage{})
	manager.poolInterval = 100 * time.Millisecond

	// Set up channels
	workChan := make(chan *piece.PieceWork, 10)
	failChan := make(chan *piece.PieceWork, 10)
	inboundedChan := make(chan net.Conn)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_ = manager.Run(ctx, workChan, failChan, inboundedChan)

	go func() {
		conn, err := ln.Accept()
		require.NoError(t, err)
		defer conn.Close()

		_, err = handshake.Read(conn)

		ch := handshake.Handshake{
			InfoHash: InfoHash,
			PeerID:   [20]byte{0x66, 0x55, 0x44},
		}

		ch.Write(conn)
		message.Read(conn)

		assert.Equal(t, 1, manager.countPeers())

		cancel()
	}()

	<-ctx.Done()
}
