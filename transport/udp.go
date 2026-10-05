package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// defaultWriteTimeout is the write deadline used when the context has none.
const defaultWriteTimeout = 100 * time.Millisecond

// ErrUnsupportedNetwork is returned by NewPool for networks it cannot dial.
var ErrUnsupportedNetwork = errors.New("transport: unsupported network")

const networkUnixgram = "unixgram"

// Conn is the datagram connection abstraction used by the pool.
// Both *net.UDPConn and *net.UnixConn satisfy it.
type Conn interface {
	Write([]byte) (int, error)
	SetWriteDeadline(time.Time) error
	Close() error
}

// UDPConnPool manages a pool of datagram connections for high-throughput sending
type UDPConnPool struct {
	network string
	address string
	addr    *net.UDPAddr
	timeout time.Duration

	// Pool of connections
	pool chan Conn

	// Pool configuration
	maxConns int

	// State management
	mu     sync.RWMutex
	closed bool

	// Metrics
	activeConns atomic.Int32
	totalSent   atomic.Uint64
	totalErrors atomic.Uint64
}

// NewUDPConnPool creates a new UDP connection pool.
// It is a thin wrapper around NewPool("udp", ...) with the default write timeout.
func NewUDPConnPool(address string, poolSize int) (*UDPConnPool, error) {
	return NewPool("udp", address, poolSize, defaultWriteTimeout)
}

// NewPool creates a connection pool for the given network ("udp", "udp4",
// "udp6" or "unixgram"; unixgram is unavailable on Windows and yields
// ErrUnsupportedNetwork there). timeout is the write deadline applied when the context carries none;
// a non-positive timeout selects the 100ms default.
func NewPool(network, address string, poolSize int, timeout time.Duration) (*UDPConnPool, error) {
	switch network {
	case "udp", "udp4", "udp6", "unixgram":
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedNetwork, network)
	}

	if poolSize <= 0 {
		poolSize = 4 // Default to 4 connections
	}
	if timeout <= 0 {
		timeout = defaultWriteTimeout
	}

	var addr *net.UDPAddr
	if network != networkUnixgram {
		var err error
		addr, err = net.ResolveUDPAddr(network, address)
		if err != nil {
			return nil, fmt.Errorf("resolve UDP address: %w", err)
		}
	}

	pool := &UDPConnPool{
		network:  network,
		address:  address,
		addr:     addr,
		timeout:  timeout,
		pool:     make(chan Conn, poolSize),
		maxConns: poolSize,
	}

	// Pre-allocate connections
	for i := 0; i < poolSize; i++ {
		conn, err := pool.createConnection()
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("create connection %d: %w", i, err)
		}
		pool.pool <- conn
	}

	return pool, nil
}

// createConnection creates a new UDP connection with optimized settings
func (p *UDPConnPool) createConnection() (Conn, error) {
	if p.network == networkUnixgram {
		conn, err := dialUnixgram(p.address)
		if err != nil {
			return nil, err
		}
		p.activeConns.Add(1)
		return conn, nil
	}

	udpConn, err := net.DialUDP(p.network, nil, p.addr)
	if err != nil {
		return nil, err
	}

	// Set write buffer size for performance (1MB)
	if err := udpConn.SetWriteBuffer(1024 * 1024); err != nil {
		_ = udpConn.Close()
		return nil, fmt.Errorf("set write buffer: %w", err)
	}

	p.activeConns.Add(1)
	return udpConn, nil
}

// Get retrieves a connection from the pool
func (p *UDPConnPool) Get(ctx context.Context) (Conn, error) {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return nil, fmt.Errorf("connection pool is closed")
	}
	p.mu.RUnlock()

	select {
	case conn := <-p.pool:
		return conn, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Put returns a connection to the pool
func (p *UDPConnPool) Put(conn Conn) {
	if conn == nil {
		return
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		conn.Close()
		p.activeConns.Add(-1)
		return
	}

	select {
	case p.pool <- conn:
		// Successfully returned to pool
	default:
		// Pool is full, close this connection
		conn.Close()
		p.activeConns.Add(-1)
	}
}

// Send sends data via UDP using a connection from the pool
func (p *UDPConnPool) Send(ctx context.Context, data []byte) error {
	conn, err := p.Get(ctx)
	if err != nil {
		p.totalErrors.Add(1)
		return err
	}
	defer p.Put(conn)

	// Set write deadline from context or default
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(p.timeout)
	}

	if err := conn.SetWriteDeadline(deadline); err != nil {
		p.totalErrors.Add(1)
		return fmt.Errorf("set deadline: %w", err)
	}

	// Write to UDP (best effort)
	n, err := conn.Write(data)
	if err != nil {
		p.totalErrors.Add(1)
		return fmt.Errorf("UDP write: %w", err)
	}

	if n != len(data) {
		p.totalErrors.Add(1)
		return fmt.Errorf("incomplete write: wrote %d of %d bytes", n, len(data))
	}

	p.totalSent.Add(1)
	return nil
}

// SendBatch sends multiple data packets in sequence
func (p *UDPConnPool) SendBatch(ctx context.Context, dataList [][]byte) error {
	if len(dataList) == 0 {
		return nil
	}

	conn, err := p.Get(ctx)
	if err != nil {
		p.totalErrors.Add(1)
		return err
	}
	defer p.Put(conn)

	// Set write deadline from context or default
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(p.timeout)
	}

	if err := conn.SetWriteDeadline(deadline); err != nil {
		p.totalErrors.Add(1)
		return fmt.Errorf("set deadline: %w", err)
	}

	var lastErr error
	successCount := 0

	for _, data := range dataList {
		if len(data) == 0 {
			continue
		}

		_, err := conn.Write(data)
		if err != nil {
			lastErr = err
			p.totalErrors.Add(1)
			continue
		}

		successCount++
		p.totalSent.Add(1)
	}

	if successCount == 0 && lastErr != nil {
		return fmt.Errorf("all writes failed: %w", lastErr)
	}

	return nil
}

// Close closes all connections in the pool
func (p *UDPConnPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}

	p.closed = true
	close(p.pool)

	// Close all connections
	for conn := range p.pool {
		conn.Close()
		p.activeConns.Add(-1)
	}

	return nil
}

// Stats returns statistics about the connection pool
func (p *UDPConnPool) Stats() PoolStats {
	return PoolStats{
		ActiveConns: int(p.activeConns.Load()),
		TotalSent:   p.totalSent.Load(),
		TotalErrors: p.totalErrors.Load(),
		PoolSize:    p.maxConns,
		Address:     p.address,
	}
}

// PoolStats contains statistics about a UDP connection pool
type PoolStats struct {
	ActiveConns int
	TotalSent   uint64
	TotalErrors uint64
	PoolSize    int
	Address     string
}
