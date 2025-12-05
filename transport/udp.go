package transport

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// UDPConnPool manages a pool of UDP connections for high-throughput sending
type UDPConnPool struct {
	address string
	addr    *net.UDPAddr

	// Pool of connections
	pool chan *net.UDPConn

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

// NewUDPConnPool creates a new UDP connection pool
func NewUDPConnPool(address string, poolSize int) (*UDPConnPool, error) {
	if poolSize <= 0 {
		poolSize = 4 // Default to 4 connections
	}

	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, fmt.Errorf("resolve UDP address: %w", err)
	}

	pool := &UDPConnPool{
		address:  address,
		addr:     addr,
		pool:     make(chan *net.UDPConn, poolSize),
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
func (p *UDPConnPool) createConnection() (*net.UDPConn, error) {
	conn, err := net.DialUDP("udp", nil, p.addr)
	if err != nil {
		return nil, err
	}

	// Set write buffer size for performance (1MB)
	if err := conn.SetWriteBuffer(1024 * 1024); err != nil {
		conn.Close()
		return nil, fmt.Errorf("set write buffer: %w", err)
	}

	p.activeConns.Add(1)
	return conn, nil
}

// Get retrieves a connection from the pool
func (p *UDPConnPool) Get(ctx context.Context) (*net.UDPConn, error) {
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
func (p *UDPConnPool) Put(conn *net.UDPConn) {
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
		deadline = time.Now().Add(100 * time.Millisecond)
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
		deadline = time.Now().Add(100 * time.Millisecond)
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
