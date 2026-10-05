package iostats

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCountReaderCountsSuccessfulBytes(t *testing.T) {
	// Given
	reader := &CountReader{R: strings.NewReader("hello")}
	buffer := make([]byte, 2)

	// When
	n, err := reader.Read(buffer)

	// Then
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if n != 2 || reader.N != 2 {
		t.Fatalf("Read() = (%d, %q), counted %d bytes", n, buffer[:n], reader.N)
	}
}

func TestCountWriterPartialWrite(t *testing.T) {
	// Given
	writer := &CountWriter{W: partialWriter{n: 2}}

	// When
	n, err := writer.Write([]byte("hello"))

	// Then
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 2 || writer.N != 2 {
		t.Fatalf("Write() = (%d, nil), counted %d bytes", n, writer.N)
	}
}

func TestFuncAdapters(t *testing.T) {
	// Given
	reader := ReaderFunc(func(p []byte) (int, error) {
		return copy(p, "read"), nil
	})
	writer := WriterFunc(func(p []byte) (int, error) {
		return len(p), nil
	})
	closed := false
	closer := CloserFunc(func() error {
		closed = true
		return nil
	})
	buffer := make([]byte, 4)

	// When
	readN, readErr := reader.Read(buffer)
	writeN, writeErr := writer.Write(buffer)
	closeErr := closer.Close()

	// Then
	if readN != 4 || readErr != nil || string(buffer) != "read" {
		t.Fatalf("Read() = (%d, %v), buffer = %q", readN, readErr, buffer)
	}
	if writeN != len(buffer) || writeErr != nil {
		t.Fatalf("Write() = (%d, %v)", writeN, writeErr)
	}
	if closeErr != nil || !closed {
		t.Fatalf("Close() = %v, closed = %t", closeErr, closed)
	}
}

func TestCountReaderErrorCountsReturnedBytes(t *testing.T) {
	// Given
	readErr := errors.New("read failed")
	reader := &CountReader{R: errorReader{data: []byte("abc"), err: readErr}}
	buffer := make([]byte, 8)

	// When
	n, err := reader.Read(buffer)

	// Then
	if !errors.Is(err, readErr) {
		t.Fatalf("Read() error = %v, want %v", err, readErr)
	}
	if n != 3 || reader.N != n {
		t.Fatalf("Read() = (%d, %v), counted %d bytes", n, err, reader.N)
	}
}

type partialWriter struct {
	n int
}

func (writer partialWriter) Write(p []byte) (int, error) {
	return writer.n, nil
}

type errorReader struct {
	data []byte
	err  error
}

func (reader errorReader) Read(p []byte) (int, error) {
	n := copy(p, reader.data)
	return n, reader.err
}

var _ io.Reader = ReaderFunc(nil)
var _ io.Writer = WriterFunc(nil)
var _ io.Closer = CloserFunc(nil)
