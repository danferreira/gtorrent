package metadata

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTotalLength(t *testing.T) {
	metadata := Metadata{
		Info: Info{
			Files: []FileInfo{
				{
					Length: 1024000,
				},
				{
					Length: 512000,
				},
			},
		},
	}

	assert.Equal(t, 1536000, metadata.Info.TotalLength())
}
