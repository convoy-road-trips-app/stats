package exporters

import (
	"context"
	"errors"
	"io"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/convoy-road-trips-app/stats/models"
)

// DefaultBufferSize is the target size of a Buffer's memory buffers when
// Buffer.BufferSize is zero.
const DefaultBufferSize = 1024

// Buffer serializes metrics with a Serializer into pooled memory buffers and
// writes the result to an io.Writer once a buffer has reached BufferSize. Each
// serialized packet is followed by a newline unless it already ends in one,
// so the output is line-oriented for the StatsD, DogStatsD and EMF
// serializers.
//
// It is the standalone, writer-backed counterpart of the UDP exporters: it
// needs no client, and it also implements models.Exporter (Export is Handle,
// Shutdown is Flush), so it can be passed to stats.WithExporter. The zero
// value is not usable: set Dst and Serializer before first use, and do not
// change fields afterwards. A Buffer is safe for concurrent use; writes to Dst
// are serialized, so Dst need not be.
type Buffer struct {
	// Dst receives the serialized metrics. It must not be nil.
	Dst io.Writer
	// Serializer renders the metrics. It must not be nil.
	Serializer Serializer
	// BufferSize is the size at which a buffer is written out. Zero means
	// DefaultBufferSize. A metric larger than the size is still written, as
	// one oversized chunk.
	BufferSize int
	// BufferPoolSize is the number of buffers, which bounds how many
	// goroutines can append without waiting for one another. Zero means
	// 2 x GOMAXPROCS.
	BufferPoolSize int

	once    sync.Once
	offset  atomic.Uint64
	buffers []pooledBuffer
	writeMu sync.Mutex
}

var _ models.Exporter = (*Buffer)(nil)

// pooledBuffer is one memory buffer; lock is a spin flag, 1 when held.
type pooledBuffer struct {
	lock atomic.Uint32
	data []byte
	_    [32]byte // padding against false sharing
}

func (p *pooledBuffer) acquire() bool { return p.lock.CompareAndSwap(0, 1) }
func (p *pooledBuffer) release()      { p.lock.Store(0) }

var errBufferConfig = errors.New("exporters: Buffer needs a Dst and a Serializer")

// Handle serializes metrics into a buffer and writes the buffer to Dst when it
// reaches BufferSize. It returns the serialization or write error; data of a
// failed write is dropped. The metrics are not modified or retained.
func (b *Buffer) Handle(metrics ...*models.Metric) error {
	if len(metrics) == 0 {
		return nil
	}
	if b.Dst == nil || b.Serializer == nil {
		return errBufferConfig
	}
	packets, err := b.Serializer.Serialize(metrics)
	if err != nil {
		return err
	}
	b.prepare()

	buf := b.acquireBuffer()
	defer buf.release()
	for _, p := range packets {
		buf.data = append(buf.data, p...)
		if n := len(p); n == 0 || p[n-1] != '\n' {
			buf.data = append(buf.data, '\n')
		}
	}
	if len(buf.data) >= b.size() {
		return b.drain(buf)
	}
	return nil
}

// Flush writes every buffer that holds data to Dst, however small. Buffers
// that other goroutines are filling at that moment are skipped; they are
// written when they fill up or on a later Flush.
func (b *Buffer) Flush() error {
	if b.Dst == nil {
		return errBufferConfig
	}
	b.prepare()
	var errs []error
	for i := range b.buffers {
		buf := &b.buffers[i]
		if !buf.acquire() {
			continue
		}
		if err := b.drain(buf); err != nil {
			errs = append(errs, err)
		}
		buf.release()
	}
	return errors.Join(errs...)
}

// Name returns the exporter name.
func (b *Buffer) Name() string { return "buffer" }

// Export is Handle followed by Flush, so a Buffer used as a models.Exporter
// has written everything it accepted to Dst when Export returns, which is what
// a client's Flush relies on. The pipeline already batches what it passes to
// Export; call Handle directly for size-based batching.
func (b *Buffer) Export(_ context.Context, metrics []*models.Metric) error {
	if err := b.Handle(metrics...); err != nil {
		return err
	}
	return b.Flush()
}

// Shutdown flushes the buffers. It does not close Dst.
func (b *Buffer) Shutdown(context.Context) error { return b.Flush() }

// drain writes buf to Dst and empties it. The caller holds buf.
func (b *Buffer) drain(buf *pooledBuffer) error {
	if len(buf.data) == 0 {
		return nil
	}
	b.writeMu.Lock()
	_, err := b.Dst.Write(buf.data)
	b.writeMu.Unlock()
	buf.data = buf.data[:0]
	return err
}

func (b *Buffer) prepare() {
	b.once.Do(func() {
		n := b.BufferPoolSize
		if n <= 0 {
			n = 2 * runtime.GOMAXPROCS(0)
		}
		size := b.size()
		b.buffers = make([]pooledBuffer, n)
		for i := range b.buffers {
			b.buffers[i].data = make([]byte, 0, size+size/4)
		}
	})
}

func (b *Buffer) size() int {
	if b.BufferSize > 0 {
		return b.BufferSize
	}
	return DefaultBufferSize
}

// acquireBuffer spins over the pool until it holds a free buffer.
func (b *Buffer) acquireBuffer() *pooledBuffer {
	n := uint64(len(b.buffers))
	for tries := uint64(1); ; tries++ {
		buf := &b.buffers[b.offset.Add(1)%n]
		if buf.acquire() {
			return buf
		}
		if tries%n == 0 {
			runtime.Gosched()
		}
	}
}
