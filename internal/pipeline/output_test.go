package pipeline

import (
	"bytes"
	"strings"
	"testing"
)

func TestOutputBufferKeepsTheEnd(t *testing.T) {
	b := newOutputBuffer(10)
	var all bytes.Buffer
	for _, chunk := range []string{"hello ", "world", "", "0123456789abcdef", "x", "yz", strings.Repeat("q", 9), "!"} {
		b.Write([]byte(chunk))
		all.WriteString(chunk)
		kept, total := b.snapshot()
		want := all.Bytes()
		if len(want) > 10 {
			want = want[len(want)-10:]
		}
		if total != int64(all.Len()) || !bytes.Equal(kept, want) {
			t.Fatalf("after %q: kept %q of %d, want %q of %d", chunk, kept, total, want, all.Len())
		}
	}
}

func TestReadOutputByOffset(t *testing.T) {
	// "é" is two bytes; the kept output starts within one.
	whole := "abcé" + "def"
	kept := []byte(whole[4:]) // the second byte of é onward
	total := int64(len(whole))

	got := readOutput(kept, total, 0, true)
	if !got.Dropped || got.Offset != 5 || string(got.Data) != "def" || got.Next != total {
		t.Fatalf("from 0: %+v %q", got, got.Data)
	}
	got = readOutput(kept, total, 6, true)
	if got.Dropped || got.Offset != 6 || string(got.Data) != "ef" {
		t.Fatalf("from 6: %+v", got)
	}
	got = readOutput(kept, total, 100, true)
	if got.Offset != total || len(got.Data) != 0 || got.Next != total {
		t.Fatalf("past the end: %+v", got)
	}

	// A running job's output that ends partway through a character is read
	// up to it, and the rest comes next time.
	running := []byte("ok é")
	got = readOutput(running[:len(running)-1], int64(len(running)-1), 0, false)
	if string(got.Data) != "ok " || got.Next != 3 {
		t.Fatalf("cut character: %+v %q", got, got.Data)
	}
	got = readOutput(running, int64(len(running)), got.Next, false)
	if string(got.Data) != "é" || got.Next != int64(len(running)) {
		t.Fatalf("rest: %+v %q", got, got.Data)
	}
	// Bytes that are no character at all are passed on, not held back.
	junk := []byte{'a', 0xff, 0xfe}
	if got := readOutput(junk, 3, 0, false); len(got.Data) != 3 {
		t.Fatalf("junk: %+v", got)
	}
}
