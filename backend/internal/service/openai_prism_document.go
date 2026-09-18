package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"
)

type prismDocumentConnection struct {
	conn      *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	secrets   []string
	document  *prismYDocument
	readBytes int
}

func (s *prismDocumentConnection) close() { _ = s.conn.CloseNow(); s.cancel() }

func (t *OpenAIPrismTransport) openPrismDocument(ctx context.Context, account *Account, token, projectID string) (*prismDocumentConnection, error) {
	data, _, err := t.doJSON(ctx, account, token, http.MethodPost, "/api/y", map[string]string{"docId": projectID})
	if err != nil {
		return nil, err
	}
	var document struct {
		URL   string `json:"url"`
		DocID string `json:"docId"`
		Token string `json:"token"`
	}
	if json.Unmarshal(data, &document) != nil || document.DocID != projectID || document.Token == "" {
		return nil, errors.New("Prism attachment document authorization is incomplete")
	}
	if err := t.validateAuthorizationURL(document.URL, true); err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(document.URL)
	if err != nil {
		return nil, errors.New("invalid Prism document endpoint")
	}
	query := endpoint.Query()
	query.Set("token", document.Token)
	endpoint.RawQuery = query.Encode()
	authURL, err := url.Parse(t.baseURL() + "/")
	if err != nil {
		return nil, errors.New("invalid Prism base URL")
	}
	req := &http.Request{URL: authURL, Header: make(http.Header)}
	t.setCookies(req, token, account)
	req.Header.Set("Origin", t.baseURL())
	secrets := prismSensitiveValues(account, token, document.Token)
	for _, cookie := range req.Cookies() {
		secrets = append(secrets, cookie.Value)
	}
	opts := &websocket.DialOptions{HTTPHeader: req.Header, CompressionMode: websocket.CompressionContextTakeover}
	if account != nil && account.Proxy != nil {
		opts.HTTPClient, err = prismDocumentProxyDialer.proxyHTTPClient(account.Proxy.URL())
		if err != nil {
			return nil, prismSafeError(err, secrets)
		}
	}
	clientHTTP := &http.Client{}
	if opts.HTTPClient != nil {
		copyClient := *opts.HTTPClient
		clientHTTP = &copyClient
	}
	clientHTTP.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("Prism document redirects are not permitted")
	}
	opts.HTTPClient = clientHTTP
	// Initial synchronization plus a server state read after the update bounds
	// completion. Caller cancellation still ends the connection immediately.
	syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	success := false
	defer func() {
		if !success {
			cancel()
		}
	}()

	conn, resp, err := websocket.Dial(syncCtx, endpoint.String(), opts)
	if resp != nil {
		t.captureCookies(req, resp, account, token)
	}
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, prismSafeError(err, secrets)
	}

	defer func() {
		if !success {
			_ = conn.CloseNow()
		}
	}()
	conn.SetReadLimit(16 << 20)
	if err := conn.Write(syncCtx, websocket.MessageBinary, prismYSyncFrame(0, []byte{0})); err != nil {
		return nil, prismSafeError(err, secrets)
	}
	initial, err := prismReadDocumentSync(syncCtx, conn)
	if err != nil {
		return nil, prismSafeError(err, secrets)
	}
	doc, err := prismReadYUpdate(initial)
	if err != nil {
		return nil, prismYProtocolError(err)
	}
	success = true
	return &prismDocumentConnection{conn: conn, ctx: syncCtx, cancel: cancel, secrets: secrets, document: doc, readBytes: len(initial)}, nil
}
