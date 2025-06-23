package torrent

import (
	"context"
	"net"
	"sync"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/peer"
	"github.com/danferreira/gtorrent/internal/piece"
	"github.com/danferreira/gtorrent/internal/state"
	"github.com/danferreira/gtorrent/internal/storage"
	"github.com/danferreira/gtorrent/internal/tracker"
)

type StorageService interface {
	Read(start int, length int) ([]byte, error)
}

type Torrent struct {
	mu sync.Mutex

	metadata *metadata.Metadata
	peerID   [20]byte

	inboundConnections chan net.Conn

	trackerManager *tracker.Manager
	pieceScheduler *piece.Scheduler
	peerManager    *peer.Manager
	pieceCompleter *piece.Completer

	state *state.State

	storage StorageService

	listenPort int

	ctx    context.Context
	cancel context.CancelFunc
}

func NewTorrent(m *metadata.Metadata, peerID [20]byte, listenPort int) (*Torrent, error) {
	pool := peer.NewPool(10)
	trackerManager := tracker.NewManager(tracker.NewTracker(m, peerID, listenPort), pool)

	str, err := storage.NewStorage(m.Info.Files)
	if err != nil {
		return nil, err
	}

	state := state.NewState(m, str)

	peerManager := peer.NewManager(m, state, peerID, pool, str)

	pieceScheduler, err := piece.NewScheduler(m, state.GetBitfield())
	if err != nil {
		return nil, err
	}

	pieceCompleter := piece.NewCompleter(m, state, str)

	inboundConnections := make(chan net.Conn)

	return &Torrent{
		metadata:           m,
		peerID:             peerID,
		trackerManager:     trackerManager,
		pieceScheduler:     pieceScheduler,
		peerManager:        peerManager,
		pieceCompleter:     pieceCompleter,
		inboundConnections: inboundConnections,
		state:              state,
	}, nil
}

func (t *Torrent) Start(parent context.Context) {
	t.ctx, t.cancel = context.WithCancel(parent)

	go t.trackerManager.Run(t.ctx, t.state.Snapshot)
	workChan, failChan := t.pieceScheduler.Run(t.ctx)
	resultChan := t.peerManager.Run(t.ctx, workChan, failChan, t.inboundConnections)
	go t.pieceCompleter.Run(t.ctx, failChan, resultChan)

	t.state.SetStatus(state.Downloading)
}

func (t *Torrent) Stop() {
	if t.cancel != nil {
		t.cancel()
		t.state.SetStatus(state.Stopped)
	}
}
func (t *Torrent) State() *state.State {
	return t.state
}

func (t *Torrent) NewInboundConnection(conn net.Conn) {
	t.inboundConnections <- conn
}
