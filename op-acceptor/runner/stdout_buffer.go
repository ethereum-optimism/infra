package runner

import (
	"sync"
)

const defaultStdoutTailBytes = 5 * 1024 * 1024 // 5MB kept in memory per test

// tailBuffer keeps only the last N bytes written to it so we can attach a
// representative snippet of stdout to the TestResult without retaining the
// entire log in memory.
type tailBuffer struct {
	maxBytes int

	mu       sync.Mutex
	total    int64
	contents []byte
	overflow bool
}

func newTailBuffer(maxBytes int) *tailBuffer {
	if maxBytes <= 0 {
		maxBytes = defaultStdoutTailBytes
	}
	return &tailBuffer{
		maxBytes: maxBytes,
		contents: make([]byte, 0, maxBytes),
	}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.total += int64(len(p))
	if len(b.contents)+len(p) <= b.maxBytes {
		b.contents = append(b.contents, p...)
		return len(p), nil
	}

	// Append then trim front to keep the most recent bytes
	b.contents = append(b.contents, p...)
	if len(b.contents) > b.maxBytes {
		b.contents = b.contents[len(b.contents)-b.maxBytes:]
		b.overflow = true
	}
	return len(p), nil
}

func (b *tailBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	cp := make([]byte, len(b.contents))
	copy(cp, b.contents)
	return cp
}

func (b *tailBuffer) TotalBytes() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total
}

func (b *tailBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.overflow || int64(len(b.contents)) < b.total
}
