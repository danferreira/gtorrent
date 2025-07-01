package peer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/danferreira/gtorrent/internal/bitfield"
	"github.com/danferreira/gtorrent/internal/handshake"
	"github.com/danferreira/gtorrent/internal/message"
	"github.com/danferreira/gtorrent/internal/piece"
)

const BlockSize = 16 * 1024 // 16 KB

type pieceBlock struct {
	index  int
	offset int
	data   []byte
}

type pendingPiece struct {
	pw        *piece.PieceWork
	data      map[int][]byte
	timestamp time.Time
}

func (p *pendingPiece) addBlock(block *pieceBlock) {
	if p.data == nil {
		p.data = make(map[int][]byte)
	}
	p.data[block.offset] = block.data
}

func (p *pendingPiece) isComplete() bool {
	numBlocks := int(math.Ceil(float64(p.pw.Length) / BlockSize))

	return len(p.data) == numBlocks
}

func (p *pendingPiece) getData() []byte {
	keys := slices.Sorted(maps.Keys(p.data))

	var r []byte
	for _, k := range keys {
		r = append(r, p.data[k]...)
	}
	return r
}

type ConnectionConfig struct {
	ReadTimeout           time.Duration
	WriteTimeout          time.Duration
	KeepAliveInterval     time.Duration
	CheckTimeoutsInterval time.Duration
	MaxRequests           int
}

func NewDefaultConnectionConfig() ConnectionConfig {
	return ConnectionConfig{
		ReadTimeout:           10 * time.Second,
		WriteTimeout:          10 * time.Second,
		KeepAliveInterval:     2 * time.Minute,
		CheckTimeoutsInterval: 30 * time.Second,
		MaxRequests:           30,
	}
}

type Connection struct {
	mu      sync.Mutex
	conn    net.Conn
	peer    *Peer
	storage io.ReaderAt

	logger *slog.Logger

	config ConnectionConfig

	bitfieldReceived atomic.Bool
	amChoking        atomic.Bool
	amInterested     atomic.Bool
	peerChoking      atomic.Bool
	peerInterested   atomic.Bool

	peerBitfield bitfield.Bitfield
	ownBitfield  bitfield.Bitfield

	outgoing      chan *message.Message
	workRequested chan struct{} // Signal when ready for work

	pieceLength      int
	inflightRequests atomic.Int32

	pendingPieces map[int]*pendingPiece

	done chan struct{}

	cancel context.CancelFunc

	closeOnce sync.Once
}

func newConnection(p *Peer, pieceLen int, bf bitfield.Bitfield, storage io.ReaderAt) *Connection {
	conn := &Connection{
		peer: p,

		logger: slog.With("peer", p.Addr),

		config: NewDefaultConnectionConfig(),

		ownBitfield: bf,
		pieceLength: pieceLen,
		storage:     storage,

		outgoing:      make(chan *message.Message, 100),
		workRequested: make(chan struct{}, 1),

		pendingPieces: make(map[int]*pendingPiece),
		done:          make(chan struct{}),
	}

	conn.amChoking.Store(true)
	conn.peerChoking.Store(true)
	return conn
}

func (c *Connection) dial(ctx context.Context, infoHash, peerID [20]byte) error {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", c.peer.Addr)
	if err != nil {
		return err
	}

	if err = sendHandshake(conn, infoHash, peerID); err != nil {
		conn.Close()
		return fmt.Errorf("failed to send handshake: %w", err)
	}

	if err := receiveHandshake(conn, infoHash); err != nil {
		conn.Close()
		return fmt.Errorf("failed to receive handshake: %w", err)
	}

	c.logger.Info("peer connected")

	c.conn = conn

	if err := c.sendBitfield(); err != nil {
		c.logger.Error("error sending bitfield", "error", err)
	}

	return nil
}

func (c *Connection) accept(conn net.Conn, infoHash, peerID [20]byte) error {
	if err := sendHandshake(conn, infoHash, peerID); err != nil {
		return fmt.Errorf("failed to send handshake: %w", err)
	}

	c.logger.Info("peer connected")

	c.conn = conn

	if err := c.sendBitfield(); err != nil {
		c.logger.Error("error sending bitfield", "error", err)
	}

	return nil
}

func (c *Connection) start(ctx context.Context, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, downloadedChan chan<- *piece.PieceDownloaded) {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	go func() {
		<-ctx.Done()
		c.closeGracefully()
	}()

	go c.messageReaderWorker(ctx, downloadedChan)
	go c.messageWriterWorker(ctx)
	go c.requestPiecesWorker(ctx, workChan, failChan)
}

func (c *Connection) startSeeding(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	go func() {
		<-ctx.Done()
		c.closeGracefully()
	}()

	go c.messageReaderWorker(ctx, nil)
	go c.messageWriterWorker(ctx)
}

func (c *Connection) messageReaderWorker(ctx context.Context, downloadedChan chan<- *piece.PieceDownloaded) {
	c.logger.Debug("starting message reader")

	for {
		select {
		case <-ctx.Done():
			return
		default:
			if err := c.conn.SetReadDeadline(time.Now().Add(c.config.ReadTimeout)); err != nil {
				c.logger.Error("error setting read deadline", "error", err)
				c.closeGracefully()
				return
			}

			msg, err := message.Read(c.conn)
			if err != nil {
				if isConnectionClosed(err) {
					c.logger.Info("connection closed")
				} else {
					c.logger.Error("failed to read message", "error", err)
				}

				c.closeGracefully()
				return
			}

			if err := c.conn.SetReadDeadline(time.Time{}); err != nil {
				c.logger.Error("error reseting read deadline", "error", err)
				c.closeGracefully()
				return
			}

			if err := c.handleMessage(msg, downloadedChan); err != nil {
				c.logger.Error("failed to handle message", "error", err)
				c.closeGracefully()
				return
			}
		}
	}
}

func (c *Connection) messageWriterWorker(ctx context.Context) {
	c.logger.Debug("starting message writer")

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-c.outgoing:
			// Discard request messages if peer is choking us
			if msg != nil && msg.ID == message.MessageRequest && c.peerChoking.Load() {
				continue
			}

			if err := c.writeMessage(msg); err != nil {
				c.logger.Error("failed to write message", "error", err)
				return
			}
		case <-ticker.C:
			c.sendMessage(nil)
		}
	}
}

func (c *Connection) requestPiecesWorker(ctx context.Context, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork) {
	c.logger.Info("starting request pieces worker")

	idle := time.NewTimer(1 * time.Second)
	timeout := time.NewTimer(c.config.CheckTimeoutsInterval) // Check for timeouts every 30s
	defer idle.Stop()
	defer timeout.Stop()

	c.requestWork()

	for {
		select {
		case <-ctx.Done():
			return

		case <-c.workRequested:
			if c.canRequest() {
				select {
				case pw, ok := <-workChan:
					if !ok { // channel has been closed → download finished
						return // exit the worker
					}

					c.processPieceRequest(pw, failChan)
				default:
					idle.Reset(1 * time.Second)
				}
			} else {
				idle.Reset(1 * time.Second)
			}

		case <-idle.C:
			c.requestWork()
			idle.Reset(1 * time.Second)

		case <-timeout.C:
			c.checkTimeouts(failChan)
			timeout.Reset(c.config.CheckTimeoutsInterval)
		}
	}
}

func (c *Connection) requestWork() {
	select {
	case c.workRequested <- struct{}{}:
	default:
	}
}

func (c *Connection) canRequest() bool {
	inflightRequests := int(c.inflightRequests.Load())
	peerChoking := c.peerChoking.Load()
	amInterested := c.amInterested.Load()
	return !peerChoking && amInterested && inflightRequests < c.config.MaxRequests
}

func (c *Connection) processPieceRequest(pw *piece.PieceWork, fail chan<- *piece.PieceWork) {
	if c.peerBitfield == nil || !c.peerBitfield.HasPiece(pw.Index) {
		c.logger.Error("peer does not have piece", "index", pw.Index)
		fail <- pw
		return
	}

	c.pendingPieces[pw.Index] = &pendingPiece{
		pw:        pw,
		data:      make(map[int][]byte),
		timestamp: time.Now(),
	}

	numBlocks := int(math.Ceil(float64(pw.Length) / BlockSize))

	for i := 0; i < numBlocks; i++ {
		offset := i * BlockSize
		end := offset + BlockSize

		if end > pw.Length {
			end = pw.Length
		}

		blockSize := end - offset

		m := message.NewRequest(pw.Index, offset, blockSize)
		c.sendMessage(&m)

		c.inflightRequests.Add(1)
	}
}

func (c *Connection) handleMessage(m *message.Message, downloadedChan chan<- *piece.PieceDownloaded) error {
	if m == nil {
		c.logger.Debug("keep alive")
		return nil
	}

	switch m.ID {
	case message.MessageChoke:
		c.handleChoke()
		return nil
	case message.MessageUnchoke:
		c.handleUnchoke()
		return nil
	case message.MessageInterested:
		return c.handleInterested()
	case message.MessageNotInterested:
		c.handleNotInterested()
		return nil
	case message.MessageHave:
		index := m.ParseAsHave()
		return c.handleHave(index)
	case message.MessageBitfield:
		bf := bitfield.Bitfield(m.Payload)
		return c.handleBitfield(bf)
	case message.MessageRequest:
		request, err := m.ParseAsRequest()
		if err != nil {
			c.logger.Error("failed to parse request message", "error", err)
			return nil
		}

		return c.handleRequest(request)
	case message.MessagePiece:
		piece := m.AsPiece()
		return c.handlePiece(piece, downloadedChan)
	case message.MessageCancel:
		c.logger.Debug("peer wants to cancel")
	default:
		c.logger.Debug("message ID not implemented yet", "id", m.ID)
	}

	return nil
}

func (c *Connection) handleChoke() {
	c.logger.Debug("peer choked us")
	c.peerChoking.Store(true)
	c.inflightRequests.Store(0)
}

func (c *Connection) handleUnchoke() {
	c.logger.Info("peer unchoked us")
	c.peerChoking.Store(false)
	c.inflightRequests.Store(0)
	amInterested := c.amInterested.Load()

	if amInterested {
		c.requestWork()
	}
}

func (c *Connection) handleInterested() error {
	c.logger.Info("peer is interested in us")
	c.peerInterested.Store(true)

	if c.amChoking.Load() {
		m := &message.Message{
			ID: message.MessageUnchoke,
		}
		c.sendMessage(m)
	}

	c.amChoking.Store(false)

	return nil
}

func (c *Connection) handleNotInterested() {
	c.logger.Debug("peer is not interested")
	c.peerInterested.Store(false)
}

func (c *Connection) handleHave(index int) error {
	c.logger.Debug("peer has a piece", "piece", index)

	c.peerBitfield.SetPiece(index)

	if !c.peerInterested.Load() && !c.ownBitfield.HasPiece(index) {
		m := &message.Message{
			ID: message.MessageInterested,
		}
		c.sendMessage(m)
	}

	return nil
}

func (c *Connection) handleBitfield(bf bitfield.Bitfield) error {
	c.logger.Info("received bitfield from peer")

	if c.bitfieldReceived.Load() {
		return errors.New("received duplicate bitfield")
	}

	c.bitfieldReceived.Store(true)
	c.peerBitfield = bf

	shouldBeInterested := false
	for i := 0; i < len(bf); i++ {
		if bf.HasPiece(i) && !c.ownBitfield.HasPiece(i) {
			shouldBeInterested = true
			break
		}
	}

	if shouldBeInterested {
		c.sendMessage(&message.Message{
			ID: message.MessageInterested,
		})

		c.amInterested.Store(true)
	}

	return nil
}

func (c *Connection) handleRequest(req *message.RequestPayload) error {
	c.logger.Debug("peer asked for a piece", "index", req.Index)

	if c.amChoking.Load() {
		c.logger.Debug("ignoring request from choked peer")
		return nil
	}
	if err := c.validateRequest(req); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}

	buf := make([]byte, req.Length)

	offset := (req.Index * uint32(c.pieceLength)) + req.Begin

	_, err := c.storage.ReadAt(buf, int64(offset))
	if err != nil {
		return fmt.Errorf("failed to read piece data: %w", err)
	}

	m := message.NewPiece(int(req.Index), int(req.Begin), buf)
	c.sendMessage(m)
	return nil
}

func (c *Connection) handlePiece(p *message.PiecePayload, downloadedChan chan<- *piece.PieceDownloaded) error {
	c.logger.Debug("received piece block", "index", p.Index)

	if downloadedChan == nil {
		c.logger.Debug("ignoring in seeding mode", "index", p.Index)
		return nil
	}

	pendingPiece := c.pendingPieces[int(p.Index)]
	if pendingPiece == nil {
		c.logger.Warn("received unrequested piece", "index", p.Index)
		return nil
	}

	pendingPiece.addBlock(&pieceBlock{
		index:  int(p.Index),
		offset: int(p.Begin),
		data:   p.Data,
	})

	c.inflightRequests.Add(-1)

	if pendingPiece.isComplete() {
		// we have all blocks for this piece
		completeData := pendingPiece.getData()
		pw := pendingPiece.pw

		// remove from pending pieces
		c.deletePendingPiece(int(p.Index))

		// send downloaded piece to channel
		downloadedChan <- &piece.PieceDownloaded{
			Index: int(p.Index),
			Data:  completeData,
			PW:    pw,
		}

		c.requestWork()
	}

	return nil
}

func (c *Connection) sendBitfield() error {
	if c.ownBitfield == nil {
		return errors.New("no bitfield to send")
	}

	c.logger.Info("sending bitfield")

	if err := c.writeMessage(&message.Message{
		ID:      message.MessageBitfield,
		Payload: c.ownBitfield,
	}); err != nil {
		return fmt.Errorf("bitfield not send %w", err)
	}

	return nil
}

func (c *Connection) validateRequest(req *message.RequestPayload) error {
	if req.Length <= 0 || req.Length > BlockSize {
		return fmt.Errorf("invalid block length: %d", req.Length)
	}

	// Check if the request goes beyond piece boundary
	maxOffset := c.pieceLength
	if int(req.Begin)+int(req.Length) > maxOffset {
		return fmt.Errorf("request exceeds piece boundary")
	}

	return nil
}

func (c *Connection) sendMessage(msg *message.Message) {
	c.outgoing <- msg
}

func (c *Connection) writeMessage(msg *message.Message) error {
	c.conn.SetWriteDeadline(time.Now().Add(c.config.WriteTimeout))
	defer c.conn.SetWriteDeadline(time.Time{})

	data := msg.Serialize()
	if _, err := c.conn.Write(data); err != nil {
		return err
	}

	return nil
}

func (c *Connection) closeGracefully() {
	c.closeOnce.Do(func() {
		c.logger.Info("closing connection gracefully")
		c.conn.Close()
		c.cancel()
		close(c.done)
	})
}

func (c *Connection) checkTimeouts(fail chan<- *piece.PieceWork) {
	if fail == nil {
		return
	}
	for index, pending := range c.pendingPieces {
		if time.Since(pending.timestamp) > 60*time.Second { // 60 second timeout
			c.logger.Warn("piece request timed out", "index", index)

			c.deletePendingPiece(index)
			select {
			case fail <- pending.pw:
			default:
				c.logger.Warn("failed to send timed out work to fail channel")
			}
		}
	}
}

func (c *Connection) deletePendingPiece(index int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pendingPieces, index)
}

func sendHandshake(conn net.Conn, infoHash, peerID [20]byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fmt.Errorf("failed to set write deadline: %w", err)
	}

	defer conn.SetWriteDeadline(time.Time{})

	h := handshake.Handshake{
		InfoHash: infoHash,
		PeerID:   peerID,
	}

	return h.Write(conn)
}

func receiveHandshake(conn net.Conn, infoHash [20]byte) error {
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fmt.Errorf("failed to set read deadline: %w", err)
	}

	defer conn.SetReadDeadline(time.Time{})

	h, err := handshake.Read(conn)
	if err != nil {
		return err
	}

	if !bytes.Equal(infoHash[:], h.InfoHash[:]) {
		return errors.New("infoHash received on handshake is different")
	}

	return nil
}

func isConnectionClosed(err error) bool {
	if err == io.EOF {
		return true
	}
	if netErr, ok := err.(net.Error); ok {
		return netErr.Timeout()
	}
	return false
}
