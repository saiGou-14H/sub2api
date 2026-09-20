package service

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

var prismDocumentProxyDialer = &coderOpenAIWSClientDialer{proxyClients: make(map[string]*openAIWSProxyClientEntry)}

// AttachProjectFile mounts an uploaded blob by its request-side node ID. The
// upload response's fileUuid is a separate blob identifier used by thumbnails.
func (t *OpenAIPrismTransport) AttachProjectFile(ctx context.Context, account *Account, token, projectID, nodeID, fileName string, sandboxTokens ...string) (string, error) {
	if t.attachFile != nil {
		return t.attachFile(ctx, account, token, projectID, nodeID, fileName)
	}
	if nodeID == "" || fileName == "" || fileName == "." || fileName == ".." || strings.ContainsAny(fileName, "/\\\x00\r\n") {
		return "", errors.New("Prism attachment requires a node id and a single filename")
	}
	session, err := t.openPrismDocument(ctx, account, token, projectID)
	if err != nil {
		return "", err
	}
	defer session.close()
	conn, syncCtx, secrets, doc := session.conn, session.ctx, session.secrets, session.document
	rootID, exists, err := doc.rootFolder(nodeID, fileName)
	initialize := false
	if err != nil {
		if !doc.canInitialize() {
			return "", prismYProtocolError(err)
		}
		rootID = uuid.NewString()
		initialize = true
	}
	if exists {
		return t.waitAttachedFile(ctx, account, token, projectID, fileName, sandboxTokens...)
	}
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	client := uint64(binary.LittleEndian.Uint32(random[:]))
	for {
		if _, exists := doc.clocks[client]; !exists {
			break
		}
		client = (client + 1) & 0xffffffff
	}
	update := prismEncodeYFile(client, nodeID, fileName, rootID)
	expectedClock := uint64(6)
	if initialize {
		update = prismEncodeYNewProjectFile(client, nodeID, fileName, rootID)
		expectedClock = 13
	}
	if err := conn.Write(syncCtx, websocket.MessageBinary, prismYSyncFrame(2, update)); err != nil {
		return "", prismSafeError(err, secrets)
	}
	// A fresh sync-step-1 with an empty vector asks the server for its current
	// complete document, proving our new structs were integrated remotely.
	if err := conn.Write(syncCtx, websocket.MessageBinary, prismYSyncFrame(0, []byte{0})); err != nil {
		return "", prismSafeError(err, secrets)
	}
	verified, err := prismReadDocumentSync(syncCtx, conn)
	if err != nil {
		return "", prismSafeError(err, secrets)
	}
	remote, err := prismReadYUpdate(verified)
	if err != nil {
		return "", prismYProtocolError(err)
	}
	_, exists, err = remote.rootFolder(nodeID, fileName)
	if err != nil {
		return "", prismYProtocolError(err)
	}
	if !exists || remote.clocks[client] < expectedClock {
		return "", errors.New("Prism did not acknowledge the attached file")
	}
	return t.waitAttachedFile(ctx, account, token, projectID, fileName, sandboxTokens...)
}

func (t *OpenAIPrismTransport) waitAttachedFile(ctx context.Context, account *Account, token, projectID, fileName string, sandboxTokens ...string) (string, error) {
	sandboxToken := ""
	if len(sandboxTokens) > 0 {
		sandboxToken = sandboxTokens[0]
	}
	if sandboxToken == "" {
		state := t.OpenAIPrismSessionState()
		if state.ProjectID == projectID {
			sandboxToken = state.SandboxToken
		}
	}
	if sandboxToken == "" {
		return "", errors.New("Prism attachment sandbox state is unavailable")
	}
	ready, err := t.WaitForSync(ctx, account, token, sandboxToken, 10000)
	if err != nil {
		return "", err
	}
	var readiness struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(ready, &readiness) != nil || readiness.Status != "synced" {
		return "", errors.New("Prism sandbox did not synchronize the attached file")
	}
	return fileName, nil
}

func prismReadDocumentSync(ctx context.Context, conn *websocket.Conn) ([]byte, error) {
	totalBytes := 0
	for count := 0; count < 1024; count++ {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		totalBytes += len(data)
		if totalBytes > 32<<20 {
			return nil, errors.New("Prism document synchronization exceeds byte limit")
		}
		if kind != websocket.MessageBinary {
			continue
		}
		r := &prismYReader{data: data}
		message := r.uint()
		if r.err != nil {
			return nil, r.err
		}
		if message != 0 {
			continue
		}
		subtype := r.uint()
		payload := r.bytes()
		if r.err != nil {
			return nil, r.err
		}
		if r.pos != len(data) {
			return nil, errors.New("trailing Prism synchronization frame bytes")
		}
		switch subtype {
		case 0:
			// Our local document is empty until synchronization. An empty update
			// correctly answers the peer's state-vector request.
			if err := conn.Write(ctx, websocket.MessageBinary, prismYSyncFrame(1, []byte{0, 0})); err != nil {
				return nil, err
			}
		case 1:
			return payload, nil
		}
	}
	return nil, errors.New("Prism document synchronization exceeded message limit")
}
