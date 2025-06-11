package bitfield

type Bitfield []byte

func (bf Bitfield) HasPiece(index int) bool {
	byteIndex := index / 8
	if byteIndex < 0 || byteIndex >= len(bf) {
		return false
	}

	bitOffset := 7 - (index % 8)
	return bf[byteIndex]&(1<<bitOffset) != 0
}

func (bf Bitfield) SetPiece(index int) {
	byteIndex := index / 8

	if byteIndex < 0 || byteIndex >= len(bf) {
		return
	}

	bitOffset := 7 - (index % 8)
	bf[byteIndex] |= (1 << bitOffset)
}
