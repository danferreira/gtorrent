package piece

type Bitfield []byte

func (bf Bitfield) HasPiece(index int) bool {
	byteIndex := index / 8
	bitOffset := 7 - (index % 8)

	if byteIndex < 0 || byteIndex >= len(bf) {
		return false
	}

	return bf[byteIndex]&(1<<bitOffset) != 0
}

func (bf Bitfield) SetPiece(index int) {
	byteIndex := index / 8
	bitOffset := 7 - (index % 8)

	if byteIndex < 0 || byteIndex >= len(bf) {
		return
	}

	bf[byteIndex] |= (1 << bitOffset)
}
