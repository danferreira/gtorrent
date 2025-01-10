package peers

const ProtocolIdentifier = "BitTorrent protocol"

type Handshake struct {
	InfoHash [20]byte
	PeerID   [20]byte
}

func (h *Handshake) Serialize() []byte {
	buf := make([]byte, len(ProtocolIdentifier)+49)
	buf[0] = byte(len(ProtocolIdentifier))
	curr := 1
	curr += copy(buf[curr:], []byte(ProtocolIdentifier))
	curr += copy(buf[curr:], make([]byte, 8)) //Reserved for Flags
	curr += copy(buf[curr:], h.InfoHash[:])
	curr += copy(buf[curr:], h.PeerID[:])
	return buf
}
