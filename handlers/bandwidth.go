package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

// BandwidthManager enforces a server-wide upload bandwidth cap shared fairly
// across unique client IPs.
//
// Each unique IP receives an equal share of the total cap regardless of how
// many concurrent connections that IP has open. This means a download manager
// opening several parallel connections from the same IP cannot claim more than
// one share of the total budget.
//
// When an IP's last active transfer finishes the share is released and the
// remaining IPs each receive a larger slice. Rebalancing is synchronous and
// happens on every connect/disconnect event.
type BandwidthManager struct {
	mu      sync.Mutex
	limitBps float64            // total cap in bytes/sec (0 = unlimited)
	peers   map[string]*ipState // keyed by remote IP
}

type ipState struct {
	limiter *rate.Limiter
	refs    int // number of active transfers from this IP
}

// NewBandwidthManager creates a manager with the given total cap in bytes per
// second. Pass 0 to disable rate limiting entirely.
func NewBandwidthManager(bytesPerSec float64) *BandwidthManager {
	return &BandwidthManager{
		limitBps: bytesPerSec,
		peers:    make(map[string]*ipState),
	}
}

// join registers a new transfer for the given IP and returns its limiter.
// It rebalances every existing IP's share to account for the new participant.
func (bm *BandwidthManager) join(ip, file string) *rate.Limiter {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	st, exists := bm.peers[ip]
	if !exists {
		// New IP — create a limiter with a placeholder rate; rebalance will set
		// the real value right after.
		st = &ipState{limiter: rate.NewLimiter(1, chunkSize)}
		bm.peers[ip] = st
	}
	st.refs++

	log.Printf("download start  ip=%-15s  streams=%-2d  file=%s", ip, st.refs, file)
	bm.rebalanceLocked()
	return st.limiter
}

// leave decrements the connection count for ip and removes the entry when it
// reaches zero, then rebalances remaining peers.
func (bm *BandwidthManager) leave(ip, file string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	st, ok := bm.peers[ip]
	if !ok {
		return
	}
	st.refs--
	log.Printf("download end    ip=%-15s  streams=%-2d  file=%s", ip, st.refs, file)
	if st.refs <= 0 {
		delete(bm.peers, ip)
	}
	bm.rebalanceLocked()
}

// rebalanceLocked recalculates the per-IP byte rate and applies it to every
// active limiter. Must be called with bm.mu held.
func (bm *BandwidthManager) rebalanceLocked() {
	n := len(bm.peers)
	if n == 0 || bm.limitBps == 0 {
		return
	}
	perIP := bm.limitBps / float64(n)
	lim := rate.Limit(perIP)
	for ip, st := range bm.peers {
		st.limiter.SetLimit(lim)
		// Burst = one chunk so the limiter is always responsive but never
		// allows more than ~one write-buffer worth of free data.
		st.limiter.SetBurst(chunkSize)
		log.Printf("rate rebalance  ip=%-15s  peers=%-2d  alloc=%s", ip, n, formatBits(perIP))
	}
}

// formatBits formats a bytes-per-second value as a human-readable bits-per-second
// string (bps, Kbps, Mbps, Gbps), matching the unit convention users configure with.
func formatBits(bytesPerSec float64) string {
	bps := bytesPerSec * 8
	switch {
	case bps >= 1_000_000_000:
		return fmt.Sprintf("%.2f Gbps", bps/1_000_000_000)
	case bps >= 1_000_000:
		return fmt.Sprintf("%.2f Mbps", bps/1_000_000)
	case bps >= 1_000:
		return fmt.Sprintf("%.2f Kbps", bps/1_000)
	default:
		return fmt.Sprintf("%.0f bps", bps)
	}
}

// Wrap returns an http.Handler that applies bandwidth limiting to h for
// downloads (responses). When the manager has no cap set (0), h is returned
// unchanged with zero overhead.
func (bm *BandwidthManager) Wrap(h http.Handler) http.Handler {
	if bm.limitBps == 0 {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		file := r.URL.Path
		limiter := bm.join(ip, file)
		defer bm.leave(ip, file)

		h.ServeHTTP(&limitedResponseWriter{
			ResponseWriter: w,
			ctx:            r.Context(),
			limiter:        limiter,
		}, r)
	})
}

// chunkSize is the maximum number of bytes written in a single pass through
// the rate limiter. Smaller values give smoother, more accurate limiting;
// 32 KiB is a good balance between accuracy and syscall overhead.
const chunkSize = 32 * 1024

// limitedResponseWriter wraps http.ResponseWriter and throttles Write calls
// through a token-bucket rate limiter.
type limitedResponseWriter struct {
	http.ResponseWriter
	ctx     context.Context
	limiter *rate.Limiter
}

func (lw *limitedResponseWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		// Check if the client has gone away.
		select {
		case <-lw.ctx.Done():
			return total, lw.ctx.Err()
		default:
		}

		n := len(p)
		if n > chunkSize {
			n = chunkSize
		}

		// Block until the limiter grants tokens for this chunk.
		if err := lw.limiter.WaitN(lw.ctx, n); err != nil {
			return total, err
		}

		written, err := lw.ResponseWriter.Write(p[:n])
		total += written
		if err != nil {
			return total, err
		}
		p = p[n:]
	}
	return total, nil
}

// ReadFrom is implemented so that io.Copy (used internally by http.ServeFile
// and zip.Writer) routes through our Write method rather than bypassing it
// via the faster WriteTo/ReadFrom path.
func (lw *limitedResponseWriter) ReadFrom(src io.Reader) (int64, error) {
	buf := make([]byte, chunkSize)
	var total int64
	for {
		select {
		case <-lw.ctx.Done():
			return total, lw.ctx.Err()
		default:
		}

		nr, rerr := src.Read(buf)
		if nr > 0 {
			nw, werr := lw.Write(buf[:nr])
			total += int64(nw)
			if werr != nil {
				return total, werr
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return total, nil
			}
			return total, rerr
		}
	}
}

// Unwrap lets http.ResponseController reach the underlying ResponseWriter.
func (lw *limitedResponseWriter) Unwrap() http.ResponseWriter {
	return lw.ResponseWriter
}

// clientIP extracts the remote IP from the request, stripping the port.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
