package harness

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	t.Parallel()

	payloads := [][]byte{
		nil,
		{},
		[]byte("a"),
		[]byte("hello, boundary"),
		bytes.Repeat([]byte{0x5a}, 1024),
	}

	var buf bytes.Buffer
	w := NewWriter(&buf)
	for _, p := range payloads {
		if err := w.WriteFrame(p); err != nil {
			t.Fatalf("WriteFrame(%d bytes): %v", len(p), err)
		}
	}

	r := NewReader(&buf, 0)
	for i, want := range payloads {
		got, err := r.ReadFrame()
		if err != nil {
			t.Fatalf("ReadFrame #%d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("ReadFrame #%d = %q, want %q", i, got, want)
		}
	}

	if _, err := r.ReadFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("trailing ReadFrame = %v, want io.EOF", err)
	}
}

func TestEncodeFrameUsesLittleEndianLength(t *testing.T) {
	t.Parallel()

	frame, err := EncodeFrame([]byte{0xaa, 0xbb, 0xcc})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{3, 0, 0, 0, 0xaa, 0xbb, 0xcc}
	if !bytes.Equal(frame, want) {
		t.Fatalf("EncodeFrame = %v, want %v", frame, want)
	}
}

func TestEncodeFrameRoundTrip(t *testing.T) {
	t.Parallel()

	payload := []byte("payload")
	frame, err := EncodeFrame(payload)
	if err != nil {
		t.Fatal(err)
	}

	got, err := NewReader(bytes.NewReader(frame), 0).ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}

func TestReadFrameTruncatedHeader(t *testing.T) {
	t.Parallel()

	_, err := NewReader(bytes.NewReader([]byte{1, 0}), 0).ReadFrame()
	if !errors.Is(err, ErrShortFrame) {
		t.Fatalf("err = %v, want ErrShortFrame", err)
	}
}

func TestReadFrameTruncatedPayload(t *testing.T) {
	t.Parallel()

	// Header announces 5 bytes; only 2 are present.
	data := []byte{5, 0, 0, 0, 0xaa, 0xbb}
	_, err := NewReader(bytes.NewReader(data), 0).ReadFrame()
	if !errors.Is(err, ErrShortFrame) {
		t.Fatalf("err = %v, want ErrShortFrame", err)
	}
}

func TestReadFrameTooLarge(t *testing.T) {
	t.Parallel()

	data := []byte{4, 0, 0, 0, 0xaa, 0xbb, 0xcc, 0xdd}
	_, err := NewReader(bytes.NewReader(data), 3).ReadFrame()
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v, want ErrFrameTooLarge", err)
	}
}

func TestReadFrameDefaultLimit(t *testing.T) {
	t.Parallel()

	r := NewReader(bytes.NewReader(nil), 0)
	if got := r.MaxFrameSize(); got != DefaultMaxFrameSize {
		t.Fatalf("MaxFrameSize = %d, want %d", got, DefaultMaxFrameSize)
	}
}

func TestReadFrameNilReader(t *testing.T) {
	t.Parallel()

	r := &Reader{}
	if _, err := r.ReadFrame(); err == nil {
		t.Fatal("ReadFrame on nil reader returned nil error")
	}
}

func TestWriteFrameShortWrite(t *testing.T) {
	t.Parallel()

	w := NewWriter(zeroWriter{})
	err := w.WriteFrame([]byte("payload"))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("err = %v, want io.ErrShortWrite", err)
	}
}

func TestWriteFrameHandlesPartialWrites(t *testing.T) {
	t.Parallel()

	var sink chunkWriter
	sink.size = 1 // accept one byte per Write call

	payload := []byte("partial writes")
	if err := NewWriter(&sink).WriteFrame(payload); err != nil {
		t.Fatal(err)
	}

	got, err := NewReader(bytes.NewReader(sink.buf.Bytes()), 0).ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}

func TestWriteFrameNilWriter(t *testing.T) {
	t.Parallel()

	w := &Writer{}
	if err := w.WriteFrame([]byte("x")); err == nil {
		t.Fatal("WriteFrame on nil writer returned nil error")
	}
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

type chunkWriter struct {
	buf  bytes.Buffer
	size int
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	if len(p) > w.size {
		p = p[:w.size]
	}
	return w.buf.Write(p)
}

func FuzzReadFrame(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0})
	f.Add([]byte{2, 0, 0, 0, 0xaa, 0xbb})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})

	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewReader(bytes.NewReader(data), 1<<20)
		_, _ = r.ReadFrame()
	})
}
