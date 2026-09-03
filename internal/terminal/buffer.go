package terminal

import "sync"

type Buffer struct {
	mu       sync.RWMutex
	data     []byte
	capacity int
}

func NewBuffer(capacity int) *Buffer {
	if capacity < 0 {
		capacity = 0
	}
	return &Buffer{capacity: capacity}
}

func (b *Buffer) Write(data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.capacity == 0 || len(data) >= b.capacity {
		if b.capacity == 0 {
			b.data = nil
			return
		}
		b.data = append(b.data[:0], data[len(data)-b.capacity:]...)
		return
	}

	overflow := len(b.data) + len(data) - b.capacity
	if overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, data...)
}

func (b *Buffer) Bytes() []byte {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]byte(nil), b.data...)
}
