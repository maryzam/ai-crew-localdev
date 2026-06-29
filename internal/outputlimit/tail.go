package outputlimit

import (
	"strings"
	"sync"
)

type Buffer struct {
	mu        sync.Mutex
	data      []byte
	maxBytes  int
	truncated bool
}

func New(maxBytes int) *Buffer {
	capacity := maxBytes
	if capacity > 64*1024 {
		capacity = 64 * 1024
	}
	return &Buffer{data: make([]byte, 0, capacity), maxBytes: maxBytes}
}

func (b *Buffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	written := len(data)
	if b.maxBytes <= 0 {
		b.truncated = b.truncated || written > 0
		return written, nil
	}
	if len(data) >= b.maxBytes {
		b.data = append(b.data[:0], data[len(data)-b.maxBytes:]...)
		b.truncated = true
		return written, nil
	}
	if overflow := len(b.data) + len(data) - b.maxBytes; overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
		b.truncated = true
	}
	b.data = append(b.data, data...)
	return written, nil
}

func (b *Buffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

func (b *Buffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func (b *Buffer) LastLines(maxLines, maxBytes int) []string {
	if maxLines <= 0 {
		return nil
	}
	data := b.Bytes()
	if maxBytes > 0 && len(data) > maxBytes {
		data = data[len(data)-maxBytes:]
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}
