package repository

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCodexHarvestTransportProfile(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIHTTP2.Enabled = true
	s := NewHTTPUpstream(cfg).(*httpUpstreamService)
	proxy, err := url.Parse("http://127.0.0.1:8888")
	require.NoError(t, err)
	mode := s.resolveProtocolMode(service.HTTPUpstreamProfileCodexHarvest, proxy.String(), proxy)
	require.Equal(t, upstreamProtocolModeCodexHarvest, mode)
	settings := s.applyProfilePoolSettings(s.resolvePoolSettings(config.ConnectionPoolIsolationAccount, 1000), service.HTTPUpstreamProfileCodexHarvest)
	require.Equal(t, 4, settings.maxConnsPerHost)
	require.Equal(t, 4, settings.maxIdleConns)
	require.Equal(t, 4, settings.maxIdleConnsPerHost)
	require.Equal(t, 30*time.Second, settings.idleConnTimeout)
	require.Equal(t, 10*time.Second, settings.responseHeaderTimeout)
	tr, err := buildUpstreamTransport(settings, proxy, mode)
	require.NoError(t, err)
	defer tr.CloseIdleConnections()
	require.True(t, tr.DisableKeepAlives)
	require.False(t, tr.ForceAttemptHTTP2)
	require.NotNil(t, tr.TLSNextProto)
	require.Empty(t, tr.TLSNextProto)
	require.Equal(t, int64(64<<10), tr.MaxResponseHeaderBytes)
	req := httptest.NewRequest(http.MethodGet, "https://chatgpt.com/backend-api/codex/responses", nil)
	actual, err := tr.Proxy(req)
	require.NoError(t, err)
	require.Equal(t, proxy.String(), actual.String())
	for _, isolation := range []string{config.ConnectionPoolIsolationAccount, config.ConnectionPoolIsolationAccountProxy, config.ConnectionPoolIsolationProxy} {
		for _, businessMode := range []string{upstreamProtocolModeDefault, upstreamProtocolModeOpenAIH1, upstreamProtocolModeOpenAIH2, upstreamProtocolModeLongStreamH2} {
			require.NotEqual(t, buildCacheKey(isolation, proxy.String(), 7, businessMode), buildCacheKey(isolation, proxy.String(), 7, mode))
			require.NotEqual(t, buildPoolKey(settings, businessMode), buildPoolKey(settings, mode))
		}
	}
}

func TestCodexHarvestProxySchemeDialPaths(t *testing.T) {
	for _, tc := range []struct {
		scheme string
		first  byte
	}{{"http", 'C'}, {"https", 0x16}, {"socks5", 5}, {"socks5h", 5}} {
		t.Run(tc.scheme, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			require.NoError(t, listener.(*net.TCPListener).SetDeadline(time.Now().Add(3*time.Second)))
			type observed struct {
				first byte
				err   error
			}
			received := make(chan observed, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					received <- observed{err: err}
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				var first [1]byte
				_, err = io.ReadFull(conn, first[:])
				received <- observed{first: first[0], err: err}
			}()
			s := NewHTTPUpstream(nil).(*httpUpstreamService)
			// Exercise normalization, profile selection, caching and the actual shared
			// ConfigureTransportProxy path. The local proxy rejects after one byte.
			entry, err := s.getClientEntry(tc.scheme+"://"+listener.Addr().String(), 7, 100, service.HTTPUpstreamProfileCodexHarvest, false, false)
			require.NoError(t, err)
			tr := entry.client.Transport.(*http.Transport)
			defer tr.CloseIdleConnections()
			require.True(t, tr.DisableKeepAlives)
			require.False(t, tr.ForceAttemptHTTP2)
			require.NotNil(t, tr.TLSNextProto)
			require.Empty(t, tr.TLSNextProto)
			client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
			resp, err := client.Get("https://codex-probe.invalid/backend-api/codex/responses")
			if resp != nil {
				resp.Body.Close()
			}
			require.Error(t, err)
			got := <-received
			require.NoError(t, got.err)
			require.Equal(t, tc.first, got.first)
		})
	}
}

func TestCodexHarvestDoesNotReuseConnections(t *testing.T) {
	addresses := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addresses <- r.RemoteAddr
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	s := NewHTTPUpstream(nil).(*httpUpstreamService)
	settings := s.applyProfilePoolSettings(defaultPoolSettings(nil), service.HTTPUpstreamProfileCodexHarvest)
	transport, err := buildUpstreamTransport(settings, nil, upstreamProtocolModeCodexHarvest)
	require.NoError(t, err)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	for i := 0; i < 2; i++ {
		resp, err := client.Get(server.URL)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, resp.Body.Close())
		require.NoError(t, err)
	}
	require.NotEqual(t, <-addresses, <-addresses)
	business, err := buildUpstreamTransport(defaultPoolSettings(nil), nil, upstreamProtocolModeOpenAIH1)
	require.NoError(t, err)
	defer business.CloseIdleConnections()
	require.False(t, business.DisableKeepAlives, "business transport must retain its pooling policy")
}

func TestCodexHarvestNeverNegotiatesH2(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 {
			t.Errorf("unexpected HTTP protocol %s", r.Proto)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	s := NewHTTPUpstream(nil).(*httpUpstreamService)
	settings := s.applyProfilePoolSettings(defaultPoolSettings(nil), service.HTTPUpstreamProfileCodexHarvest)
	tr, err := buildUpstreamTransport(settings, nil, upstreamProtocolModeCodexHarvest)
	require.NoError(t, err)
	defer tr.CloseIdleConnections()
	tr.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 1, resp.ProtoMajor)
}
