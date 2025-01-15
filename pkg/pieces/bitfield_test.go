package pieces

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetPiece(t *testing.T) {
	tests := []struct {
		index  int
		output Bitfield
	}{
		{
			2,
			Bitfield{0b11101010, 0b01010101},
		}, {
			7,
			Bitfield{0b11001011, 0b01010101},
		},
		{
			8,
			Bitfield{0b11001010, 0b11010101},
		},
		{
			16,
			Bitfield{0b11001010, 0b01010101},
		}}

	for _, tt := range tests {
		bf := Bitfield{0b11001010, 0b01010101}
		bf.SetPiece(tt.index)

		assert.Equal(t, tt.output, bf)
	}
}

func TestHasPiece(t *testing.T) {
	bf := Bitfield{0b11001010, 0b01010101}

	output := []bool{true, true, false, false, true, false, true, false, false, true, false, true, false, true, false, true, false, false, false}

	for index, expected := range output {
		got := bf.HasPiece(index)

		assert.Equal(t, expected, got)
	}
}
