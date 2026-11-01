package nfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var (
	ErrXDRUnderflow = errors.New("xdr: unexpected end of buffer")
)

// XDRReader decodes RFC 4506 XDR data streams.
type XDRReader struct {
	buf []byte
	off int
}

func NewXDRReader(buf []byte) *XDRReader {
	return &XDRReader{buf: buf, off: 0}
}

func (r *XDRReader) Remaining() int {
	return len(r.buf) - r.off
}

func (r *XDRReader) ReadUint32() (uint32, error) {
	if r.Remaining() < 4 {
		return 0, ErrXDRUnderflow
	}
	val := binary.BigEndian.Uint32(r.buf[r.off : r.off+4])
	r.off += 4
	return val, nil
}

func (r *XDRReader) ReadUint64() (uint64, error) {
	if r.Remaining() < 8 {
		return 0, ErrXDRUnderflow
	}
	val := binary.BigEndian.Uint64(r.buf[r.off : r.off+8])
	r.off += 8
	return val, nil
}

func (r *XDRReader) ReadFixedBytes(n int) ([]byte, error) {
	pad := (4 - (n % 4)) % 4
	total := n + pad
	if r.Remaining() < total {
		return nil, ErrXDRUnderflow
	}
	res := make([]byte, n)
	copy(res, r.buf[r.off:r.off+n])
	r.off += total
	return res, nil
}

func (r *XDRReader) ReadBytes() ([]byte, error) {
	length, err := r.ReadUint32()
	if err != nil {
		return nil, err
	}
	n := int(length)
	if n < 0 || n > 64*1024*1024 { // 64MB sanity limit
		return nil, fmt.Errorf("xdr: invalid byte array length %d", n)
	}
	return r.ReadFixedBytes(n)
}

func (r *XDRReader) ReadString() (string, error) {
	b, err := r.ReadBytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *XDRReader) ReadBitmap() ([]uint32, error) {
	count, err := r.ReadUint32()
	if err != nil {
		return nil, err
	}
	if count > 16 {
		return nil, fmt.Errorf("xdr: bitmap count %d exceeds limit", count)
	}
	bm := make([]uint32, count)
	for i := uint32(0); i < count; i++ {
		bm[i], err = r.ReadUint32()
		if err != nil {
			return nil, err
		}
	}
	return bm, nil
}

// XDRWriter encodes RFC 4506 XDR data streams.
type XDRWriter struct {
	buf bytes.Buffer
}

func NewXDRWriter() *XDRWriter {
	return &XDRWriter{}
}

func (w *XDRWriter) WriteUint32(v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	w.buf.Write(b[:])
}

func (w *XDRWriter) WriteUint64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	w.buf.Write(b[:])
}

func (w *XDRWriter) WriteFixedBytes(data []byte) {
	w.buf.Write(data)
	pad := (4 - (len(data) % 4)) % 4
	for i := 0; i < pad; i++ {
		w.buf.WriteByte(0)
	}
}

func (w *XDRWriter) WriteBytes(data []byte) {
	w.WriteUint32(uint32(len(data)))
	w.WriteFixedBytes(data)
}

func (w *XDRWriter) WriteString(s string) {
	w.WriteBytes([]byte(s))
}

func (w *XDRWriter) WriteBitmap(bm []uint32) {
	w.WriteUint32(uint32(len(bm)))
	for _, v := range bm {
		w.WriteUint32(v)
	}
}

func (w *XDRWriter) WriteRaw(data []byte) {
	w.buf.Write(data)
}

func (w *XDRWriter) Bytes() []byte {
	return w.buf.Bytes()
}

func (w *XDRWriter) Len() int {
	return w.buf.Len()
}

// ReadRecord reads one RPC TCP record fragment.
func ReadRecord(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	h := binary.BigEndian.Uint32(hdr[:])
	isLast := (h & 0x80000000) != 0
	length := int(h & 0x7FFFFFFF)
	if length > 32*1024*1024 { // 32MB single fragment sanity limit
		return nil, fmt.Errorf("rpc: fragment length %d exceeds 32MB", length)
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	if !isLast {
		// If fragment is not last, concatenate subsequent fragments
		next, err := ReadRecord(r)
		if err != nil {
			return nil, err
		}
		return append(buf, next...), nil
	}
	return buf, nil
}

// WriteRecord writes one complete RPC TCP record fragment with LastFragment bit set.
func WriteRecord(w io.Writer, payload []byte) error {
	var hdr [4]byte
	length := uint32(len(payload))
	binary.BigEndian.PutUint32(hdr[:], 0x80000000|length)
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}
