package main

import (
	"encoding/binary"
	"fmt"
	"io"
)

const maxFrameSize = 8 << 20

func readFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}

	size := binary.LittleEndian.Uint32(header[:])
	if size > maxFrameSize {
		return nil, fmt.Errorf("frame too large: %d", size)
	}

	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeFrame(w io.Writer, payload []byte) error {
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(payload)))

	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, payload)
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
