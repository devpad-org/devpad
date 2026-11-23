package pipeline

import (
	"slices"
	"sync"
	"time"
	"unicode/utf8"
)

// A job's output is everything it printed, its clone's and its script's,
// and nothing of devpad's: devpad's notes on setting it up and on how it
// ended are kept apart, so the job cannot forge one. Offsets are byte
// positions in the output, from 0, so a client polling a running job asks
// for what came after what it has, and the same offsets keep working once
// the job has finished and only the end of its output is kept. Each read
// also carries every note so far.

// jobLog is what a running job has told so far: its output, of which it
// keeps the end, and devpad's notes on it. It is the job's
// jobrun.Recorder, safe for one writer and any number of readers.
type jobLog struct {
	out *outputBuffer
	now func() time.Time

	mu    sync.Mutex
	notes []Note
}

func newJobLog(limit int, now func() time.Time) *jobLog {
	return &jobLog{out: newOutputBuffer(limit), now: now}
}

func (l *jobLog) Write(p []byte) (int, error) {
	return l.out.Write(p)
}

func (l *jobLog) Note(text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.notes = append(l.notes, Note{At: l.now(), Text: text})
}

// snapshot is a copy of what the job has told so far.
func (l *jobLog) snapshot() JobLog {
	kept, total := l.out.snapshot()
	l.mu.Lock()
	defer l.mu.Unlock()
	return JobLog{Output: kept, Total: total, Notes: slices.Clone(l.notes)}
}

// outputBuffer is a running job's output: it keeps the last limit bytes
// written to it and counts them all. It is safe for one writer and any
// number of readers.
type outputBuffer struct {
	mu    sync.Mutex
	limit int
	// buf holds the end of the output, up to twice limit, so that dropping
	// its start, which copies the rest, happens once per limit bytes
	// written rather than on every write.
	buf   []byte
	total int64
}

func newOutputBuffer(limit int) *outputBuffer {
	return &outputBuffer{limit: limit}
}

// Write keeps the end of p. It never fails, so a job can never stall on it.
func (b *outputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += int64(len(p))
	if len(p) >= b.limit {
		b.buf = append(b.buf[:0], p[len(p)-b.limit:]...)
		return len(p), nil
	}
	if len(b.buf)+len(p) > 2*b.limit {
		keep := b.limit - len(p)
		b.buf = append(b.buf[:0], b.buf[len(b.buf)-keep:]...)
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// snapshot returns what is kept, at most limit bytes, and how much was
// written in all. The bytes are a copy.
func (b *outputBuffer) snapshot() ([]byte, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	kept := b.buf
	if len(kept) > b.limit {
		kept = kept[len(kept)-b.limit:]
	}
	return append([]byte(nil), kept...), b.total
}

// Output is part of a job's output, as a client polling it reads it.
type Output struct {
	// Offset is where Data starts in all the job printed, and Next where to
	// ask from for what comes after it.
	Offset int64
	Data   []byte
	Next   int64
	// Done says the job has finished, so nothing comes after Next.
	Done bool
	// Dropped says output after the offset asked for is no longer kept: it
	// was over MaxJobOutput before the end, or is gone with an old
	// pipeline's.
	Dropped bool
	// Notes are all devpad's notes on the job so far.
	Notes []Note
}

// readOutput returns what is kept of a job's output from offset on, with
// every note. A job still running may be partway through a character, so
// its part ends before one that is cut off and the next read gets it whole.
// A part that starts where the kept output was cut skips to the next whole
// character.
func readOutput(log JobLog, offset int64, done bool) *Output {
	kept, total := log.Output, log.Total
	start := total - int64(len(kept))
	offset = max(offset, 0)
	out := &Output{Done: done, Notes: log.Notes}
	if offset > total {
		offset = total
	}
	if offset < start {
		out.Dropped = true
		offset = start
		for offset < total && !utf8.RuneStart(kept[offset-start]) && offset-start < utf8.UTFMax {
			offset++
		}
	}
	data := kept[offset-start:]
	if !done {
		data = data[:completeRunes(data)]
	}
	out.Offset, out.Data, out.Next = offset, data, offset+int64(len(data))
	return out
}

// completeRunes is how many bytes of b come before a character cut off at
// its end.
func completeRunes(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return i
			}
			return len(b)
		}
	}
	return len(b)
}
