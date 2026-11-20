package zipppmd

import "io"

// Writer is a stub for writing ZIP archives. PPMd compression is planned
// but not implemented yet; every method returns ErrNotImplemented so that
// callers can be written against the final API today.
type Writer struct {
	w io.Writer
}

// NewWriter returns a Writer that would write an archive to w.
//
// Deprecated: writing is not implemented yet; Create and Close fail with
// ErrNotImplemented.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// Create adds an entry with the given header to the archive and returns a
// writer for its body. It currently always returns ErrNotImplemented.
func (w *Writer) Create(fh *FileHeader) (io.Writer, error) {
	return nil, ErrNotImplemented
}

// Close finalizes the archive. It currently always returns
// ErrNotImplemented.
func (w *Writer) Close() error {
	return ErrNotImplemented
}
