package fbs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// HeaderSize is the size in bytes of a frame header.
const HeaderSize = 4

// DefaultMaxFrameSize is the default upper bound for a single payload (64 MiB).
const DefaultMaxFrameSize = 64 << 20

var (
	// ErrFrameTooLarge is returned when a payload exceeds the configured limit.
	ErrFrameTooLarge = errors.New("fbs: frame exceeds maximum size")
	// ErrShortFrame is returned when a frame is truncated.
	ErrShortFrame = errors.New("fbs: truncated frame")
)

// EncodeFrame returns payload prefixed with its little-endian uint32 length.
func EncodeFrame(payload []byte) ([]byte, error) {
	if uint64(len(payload)) > math.MaxUint32 {
		return nil, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, len(payload))
	}

	frame := make([]byte, HeaderSize+len(payload))
	binary.LittleEndian.PutUint32(frame[:HeaderSize], uint32(len(payload)))
	copy(frame[HeaderSize:], payload)
	return frame, nil
}

// Writer writes length-prefixed frames to an underlying writer.
//
// A Writer is not safe for concurrent use.
type Writer struct {
	w io.Writer
}

// NewWriter returns a Writer that frames payloads written to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// WriteFrame writes a single frame. The payload is copied to the underlying
// writer without an intermediate allocation.
func (w *Writer) WriteFrame(payload []byte) error {
	if w.w == nil {
		return errors.New("fbs: nil writer")
	}
	if uint64(len(payload)) > math.MaxUint32 {
		return fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, len(payload))
	}

	var header [HeaderSize]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(payload)))

	if err := writeAll(w.w, header[:]); err != nil {
		return fmt.Errorf("fbs: write header: %w", err)
	}
	if err := writeAll(w.w, payload); err != nil {
		return fmt.Errorf("fbs: write payload: %w", err)
	}
	return nil
}

// Reader reads length-prefixed frames from an underlying reader.
//
// A Reader is not safe for concurrent use.
type Reader struct {
	r   io.Reader
	max uint32
}

// NewReader returns a Reader that accepts payloads of at most maxFrameSize
// bytes. A value <= 0 selects [DefaultMaxFrameSize].
func NewReader(r io.Reader, maxFrameSize int) *Reader {
	max := uint32(DefaultMaxFrameSize)
	if maxFrameSize > 0 {
		if maxFrameSize > math.MaxUint32 {
			max = math.MaxUint32
		} else {
			max = uint32(maxFrameSize)
		}
	}
	return &Reader{r: r, max: max}
}

// MaxFrameSize returns the configured payload limit.
func (r *Reader) MaxFrameSize() int { return int(r.max) }

// ReadFrame reads the next frame. It returns [io.EOF] only when the stream
// ends cleanly at a frame boundary.
func (r *Reader) ReadFrame() ([]byte, error) {
	if r.r == nil {
		return nil, errors.New("fbs: nil reader")
	}

	var header [HeaderSize]byte
	if _, err := io.ReadFull(r.r, header[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("%w: header: %v", ErrShortFrame, err)
		}
		return nil, fmt.Errorf("fbs: read header: %w", err)
	}

	size := binary.LittleEndian.Uint32(header[:])
	if size > r.max {
		return nil, fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, size, r.max)
	}

	payload := make([]byte, size)
	if _, err := io.ReadFull(r.r, payload); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("%w: payload: %v", ErrShortFrame, err)
		}
		return nil, fmt.Errorf("fbs: read payload: %w", err)
	}
	return payload, nil
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(p) {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}
