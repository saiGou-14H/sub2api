//go:build unit

package repository

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// This is a real TCP transport fault: Redis executes the command, but its reply
// is lost between the server and client. It exercises socket deadlines and
// retry policy, unlike advancing miniredis's logical TTL clock.
func codexReplyBlackhole(t *testing.T, upstream string) (string, *atomic.Bool) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	stopped := make(chan struct{})
	accepted := make(chan struct{})
	var sessions sync.WaitGroup
	blocked := new(atomic.Bool)
	go func() {
		defer close(accepted)
		for {
			down, err := listener.Accept()
			if err != nil {
				return
			}
			sessions.Add(1)
			go func() {
				defer sessions.Done()
				defer down.Close()
				up, err := net.DialTimeout("tcp", upstream, time.Second)
				if err != nil {
					return
				}
				defer up.Close()
				finished := make(chan struct{})
				defer close(finished)
				watcherDone := make(chan struct{})
				go func() {
					defer close(watcherDone)
					select {
					case <-stopped:
						down.Close()
						up.Close()
					case <-finished:
					}
				}()
				copied := make(chan struct{})
				go func() { defer close(copied); _, _ = io.Copy(up, down); up.Close() }()
				defer func() { down.Close(); up.Close(); <-copied }()
				buf := make([]byte, 8192)
				for {
					n, err := up.Read(buf)
					if n > 0 {
						if blocked.Load() {
							<-stopped
							return
						}
						if _, e := down.Write(buf[:n]); e != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { close(stopped); _ = listener.Close(); <-accepted; sessions.Wait() })
	return listener.Addr().String(), blocked
}

func TestCodexTicketObservationBlockedSocketBoundedWithoutRetry(t *testing.T) {
	server := miniredis.RunT(t)
	addr, blocked := codexReplyBlackhole(t, server.Addr())
	client := redis.NewClient(&redis.Options{Addr: addr, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, ContextTimeoutEnabled: false, MaxRetries: 3, DisableIdentity: true})
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Ping(context.Background()).Err())
	cache, _, _, ticket := ticketCacheFixture(t)
	cache.client = client
	blocked.Store(true)
	started := time.Now()
	err := cache.RecordCodexTicketDecision(context.Background(), ticket.Key, "header_set", "ticket_ready", "")
	elapsed := time.Since(started)
	require.Error(t, err)
	require.Less(t, elapsed, 500*time.Millisecond, "100ms socket deadline must bound a host client with 3s read timeout and retries=3")
	require.GreaterOrEqual(t, elapsed, 50*time.Millisecond, "test must reach the blocked socket")
	require.Equal(t, "1", server.HGet(codexObservationKey(ticket.Key), "count"), "lost reply must never replay an increment")
	// A timeout clone must not close the real shared pool.
	blocked.Store(false)
	require.NoError(t, client.Ping(context.Background()).Err())
}

func TestCodexTicketObservationColdHandshakeBounded(t *testing.T) {
	server := miniredis.RunT(t)
	addr, blocked := codexReplyBlackhole(t, server.Addr())
	blocked.Store(true)
	client := redis.NewClient(&redis.Options{Addr: addr, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, ContextTimeoutEnabled: false, MaxRetries: 3, DisableIdentity: true})
	t.Cleanup(func() { _ = client.Close() })
	cache, _, _, ticket := ticketCacheFixture(t)
	cache.client = client
	started := time.Now()
	err := cache.RecordCodexTicketDecision(context.Background(), ticket.Key, "header_set", "ticket_ready", "")
	require.Error(t, err)
	require.Less(t, time.Since(started), 500*time.Millisecond, "first HELLO/AUTH initialization must obey the observation timeout")
	require.False(t, server.Exists(codexObservationKey(ticket.Key)), "failed handshake cannot write telemetry")
}

func TestCodexTicketObservationCanceledContextDoesNotWrite(t *testing.T) {
	cache, mr, _, ticket := ticketCacheFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	require.ErrorIs(t, cache.RecordCodexTicketDecision(ctx, ticket.Key, "header_set", "ticket_ready", ""), context.Canceled)
	require.Less(t, time.Since(start), 500*time.Millisecond)
	require.False(t, mr.Exists(codexObservationKey(ticket.Key)))
}
