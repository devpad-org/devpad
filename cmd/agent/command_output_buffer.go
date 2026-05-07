package main

func newCommandOutputBuffer(max int) *commandOutputBuffer {
	if max <= 0 {
		max = maxManagedCommandOutput
	}
	return &commandOutputBuffer{
		max:    max,
		notify: make(chan struct{}),
	}
}

func (b *commandOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	written := len(p)
	end := b.next + int64(written)
	if written >= b.max {
		b.data = append(b.data[:0], p[written-b.max:]...)
		b.start = end - int64(b.max)
		b.next = end
		b.signalLocked()
		return written, nil
	}

	b.data = append(b.data, p...)
	if len(b.data) > b.max {
		drop := len(b.data) - b.max
		copy(b.data, b.data[drop:])
		b.data = b.data[:b.max]
		b.start += int64(drop)
	}
	b.next = end
	b.signalLocked()
	return written, nil
}

func (b *commandOutputBuffer) snapshot(cursor int64, maxBytes int) (output string, effectiveCursor, nextCursor int64, truncated, hasMore bool, notify <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if cursor < b.start {
		cursor = b.start
		truncated = true
	}
	if cursor > b.next {
		cursor = b.next
	}
	if maxBytes <= 0 {
		maxBytes = defaultCommandReadBytes
	}

	offset := int(cursor - b.start)
	available := len(b.data) - offset
	if available < 0 {
		available = 0
	}
	size := available
	if size > maxBytes {
		size = maxBytes
	}

	chunk := b.data[offset : offset+size]
	nextCursor = cursor + int64(size)
	hasMore = nextCursor < b.next
	return string(chunk), cursor, nextCursor, truncated, hasMore, b.notify
}

func (b *commandOutputBuffer) nextCursor() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.next
}

func (b *commandOutputBuffer) signal() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.signalLocked()
}

func (b *commandOutputBuffer) signalLocked() {
	close(b.notify)
	b.notify = make(chan struct{})
}
