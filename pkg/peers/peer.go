package peers

import (
	"encoding/binary"
	"errors"
	"net"
)

type Peer struct {
	IP   net.IP
	Port uint16
}

func Unmarshal(buf []byte) (Peer, error) {
	if len(buf) != 6 {
		return Peer{}, errors.New("invalid peer address")
	}

	return Peer{
		IP:   net.IP(buf[:4]),
		Port: binary.BigEndian.Uint16(buf[4:]),
	}, nil
}
