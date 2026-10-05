// Package iostats provides counting wrappers and function adapters for I/O.
package iostats

import "io"

// CountReader wraps an io.Reader and counts the bytes it returns.
type CountReader struct {
	R io.Reader
	N int
}

// Read reads from the wrapped reader and adds the returned byte count to N.
func (reader *CountReader) Read(p []byte) (int, error) {
	n, err := reader.R.Read(p)
	reader.N += n
	return n, err
}

// CountWriter wraps an io.Writer and counts the bytes it writes.
type CountWriter struct {
	W io.Writer
	N int
}

// Write writes to the wrapped writer and adds the returned byte count to N.
func (writer *CountWriter) Write(p []byte) (int, error) {
	n, err := writer.W.Write(p)
	writer.N += n
	return n, err
}

// ReaderFunc adapts a function to the io.Reader interface.
type ReaderFunc func([]byte) (int, error)

// Read calls the function.
func (reader ReaderFunc) Read(p []byte) (int, error) {
	return reader(p)
}

// WriterFunc adapts a function to the io.Writer interface.
type WriterFunc func([]byte) (int, error)

// Write calls the function.
func (writer WriterFunc) Write(p []byte) (int, error) {
	return writer(p)
}

// CloserFunc adapts a function to the io.Closer interface.
type CloserFunc func() error

// Close calls the function.
func (closer CloserFunc) Close() error {
	return closer()
}
