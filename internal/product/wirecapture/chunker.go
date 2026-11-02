package wirecapture

import (
	"crypto/sha256"
	"encoding/binary"
)

const (
	minChunkBytes = 2 << 10
	maxChunkBytes = 32 << 10
	boundaryBits  = 12
)

var gearTable = func() [256]uint64 {
	var table [256]uint64
	for index := range table {
		digest := sha256.Sum256([]byte{'a', 'i', '-', 'w', 'i', 'r', 'e', byte(index)})
		table[index] = binary.BigEndian.Uint64(digest[:8])
	}
	return table
}()

func splitChunks(data []byte, visit func([]byte)) {
	for len(data) > 0 {
		size := chunkLength(data)
		visit(data[:size])
		data = data[size:]
	}
}

func chunkLength(data []byte) int {
	limit := min(len(data), maxChunkBytes)
	if limit <= minChunkBytes {
		return limit
	}
	var rolling uint64
	for index := 0; index < limit; index++ {
		rolling = rolling<<1 + gearTable[data[index]]
		if index >= minChunkBytes && rolling>>(64-boundaryBits) == 0 {
			return index + 1
		}
	}
	return limit
}
