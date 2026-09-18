package service

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

type OpenAIPrismDeltaSync struct {
	Status string                     `json:"status"`
	Files  []OpenAIPrismDeltaFileSync `json:"files"`
}

type OpenAIPrismDeltaFileSync struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type prismDeltaFile struct {
	FilePath               string  `json:"file_path"`
	Status                 string  `json:"status"`
	Diff                   string  `json:"diff"`
	DiffError              *string `json:"diff_error"`
	DiffTruncated          bool    `json:"diff_truncated"`
	BaseRenderHashMismatch bool    `json:"base_render_hash_mismatch"`
	BinaryBodyB64          *string `json:"binary_body_b64"`
	BinaryMimeType         *string `json:"binary_mime_type"`
	BinaryFetchError       *string `json:"binary_fetch_error"`
}

type prismDeltaExpectation struct {
	Path   string
	NodeID string
	Text   string
	Client uint64
	Clock  uint64
}

func prismDeltaPathValid(name string) bool {
	if name == "" || len(name) > 4096 || strings.ContainsAny(name, "\\\x00\r\n:") || strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func prismDeltaPreflight(file prismDeltaFile) string {
	if !prismDeltaPathValid(file.FilePath) {
		return "invalid_path"
	}
	if file.FilePath == "AGENTS.md" {
		return "ignored_agent_instructions"
	}
	if file.Status != "added" && file.Status != "modified" {
		return "unsupported_status"
	}
	if file.BinaryBodyB64 != nil || file.BinaryMimeType != nil || file.BinaryFetchError != nil || strings.EqualFold(path.Ext(file.FilePath), ".pdf") {
		return "unsupported_binary"
	}
	if file.DiffTruncated || file.BaseRenderHashMismatch || (file.DiffError != nil && *file.DiffError != "") {
		return "incomplete_diff"
	}
	if len(file.Diff) > 16<<20 || !strings.Contains(file.Diff, "\n@@ ") {
		return "invalid_diff"
	}
	validHeader := strings.HasPrefix(file.Diff, "--- render/"+file.FilePath+"\n+++ codex/"+file.FilePath+"\n") || strings.HasPrefix(file.Diff, "--- a/"+file.FilePath+"\n+++ b/"+file.FilePath+"\n") || strings.HasPrefix(file.Diff, "--- /dev/null\n+++ b/"+file.FilePath+"\n")
	if !validHeader {
		return "invalid_diff"
	}
	return ""
}

// Called after the model turn completed. Synchronization failures are reported
// as data, never as a retryable model error; the original deltas remain saved.
func (t *OpenAIPrismTransport) syncDeltaFiles(ctx context.Context, account *Account, token, projectID, sandboxToken string, raw json.RawMessage, secrets []string) OpenAIPrismDeltaSync {
	result := OpenAIPrismDeltaSync{Status: "not_required", Files: []OpenAIPrismDeltaFileSync{}}
	var files []prismDeltaFile
	if len(raw) == 0 || string(raw) == "null" {
		return result
	}
	if json.Unmarshal(raw, &files) != nil || len(files) > 1000 {
		result.Status = "unsynced"
		result.Files = append(result.Files, OpenAIPrismDeltaFileSync{Status: "unsynced", Reason: "invalid_delta"})
		return result
	}
	eligible := make([]int, 0)
	seen := make(map[string]int)
	for index, file := range files {
		entry := OpenAIPrismDeltaFileSync{Path: prismReplaceSecrets(file.FilePath, secrets), Status: "unsynced", Reason: prismDeltaPreflight(file)}
		if entry.Reason == "ignored_agent_instructions" {
			entry.Status = "skipped"
		}
		if entry.Reason == "" && (prismReplaceSecrets(file.Diff, secrets) != file.Diff || entry.Path != file.FilePath) {
			entry.Reason = "sensitive_content"
		}
		result.Files = append(result.Files, entry)
		if previous, exists := seen[file.FilePath]; exists {
			result.Files[previous].Status = "unsynced"
			result.Files[previous].Reason = "duplicate_path"
			result.Files[index].Status = "unsynced"
			result.Files[index].Reason = "duplicate_path"
		} else {
			seen[file.FilePath] = index
		}
	}
	for index, entry := range result.Files {
		if entry.Reason == "" {
			if len(eligible) == 128 {
				result.Files[index].Reason = "synchronization_limit"
			} else {
				eligible = append(eligible, index)
			}
		}
	}
	if len(eligible) > 0 {
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		session, err := t.openPrismDocument(ctx, account, token, projectID)
		if err != nil {
			for _, index := range eligible {
				result.Files[index].Reason = "synchronization_failed"
			}
		} else {
			defer session.close()
			for offset, index := range eligible {
				if session.ctx.Err() != nil || session.readBytes > 64<<20 {
					for _, pending := range eligible[offset:] {
						result.Files[pending].Reason = "synchronization_limit"
					}
					break
				}
				client, err := prismNewYClient(session.document)
				if err != nil {
					result.Files[index].Reason = "synchronization_failed"
					continue
				}
				update, expected, err := prismBuildDeltaUpdate(session.document, client, files[index])
				if err != nil {
					result.Files[index].Reason = prismDeltaReason(err)
					continue
				}
				if len(update) > 0 {
					err = session.conn.Write(session.ctx, websocket.MessageBinary, prismYSyncFrame(2, update))
					if err == nil {
						err = session.conn.Write(session.ctx, websocket.MessageBinary, prismYSyncFrame(0, []byte{0}))
					}
					var verified []byte
					if err == nil {
						verified, err = prismReadDocumentSync(session.ctx, session.conn)
					}
					if err == nil {
						session.readBytes += len(verified)
						session.document, err = prismReadYUpdate(verified)
					}
				}
				if err != nil {
					for _, pending := range eligible[offset:] {
						result.Files[pending].Reason = "synchronization_failed"
					}
					break
				}
				if err = prismVerifyDelta(session.document, expected); err != nil {
					result.Files[index].Reason = "readback_conflict"
					continue
				}
				result.Files[index].Status = "synced"
				result.Files[index].Reason = ""
			}
			anySynced := false
			for _, entry := range result.Files {
				anySynced = anySynced || entry.Status == "synced"
			}
			if anySynced {
				_, err := t.waitAttachedFile(ctx, account, token, projectID, "", sandboxToken)
				if err != nil {
					for index := range result.Files {
						if result.Files[index].Status == "synced" {
							result.Files[index].Status = "unsynced"
							result.Files[index].Reason = "sandbox_not_synced"
						}
					}
				}
			}
		}
	}
	synced, unsynced := 0, 0
	for _, entry := range result.Files {
		if entry.Status == "synced" {
			synced++
		}
		if entry.Status == "unsynced" {
			unsynced++
		}
	}
	switch {
	case synced > 0 && unsynced > 0:
		result.Status = "partial"
	case unsynced > 0:
		result.Status = "unsynced"
	case synced > 0:
		result.Status = "synced"
	}
	return result
}

type prismDeltaBuildError string

func (e prismDeltaBuildError) Error() string { return string(e) }
func prismDeltaReason(err error) string {
	var known prismDeltaBuildError
	if errors.As(err, &known) {
		return string(known)
	}
	return "ambiguous_document"
}

func prismNewYClient(doc *prismYDocument) (uint64, error) {
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return 0, err
	}
	client := uint64(binary.LittleEndian.Uint32(random[:]))
	for {
		if _, exists := doc.clocks[client]; !exists {
			return client, nil
		}
		client = (client + 1) & 0xffffffff
	}
}

func prismBuildDeltaUpdate(doc *prismYDocument, client uint64, delta prismDeltaFile) ([]byte, prismDeltaExpectation, error) {
	expected := prismDeltaExpectation{Path: delta.FilePath, Client: client}
	if reason := prismDeltaPreflight(delta); reason != "" {
		return nil, expected, prismDeltaBuildError(reason)
	}
	if _, exists := doc.clocks[client]; exists {
		return nil, expected, errors.New("Yjs client already exists")
	}
	files, err := doc.files()
	if err != nil {
		return nil, expected, err
	}
	root, err := prismDeltaRoot(doc, files)
	initialize := false
	if err != nil {
		if !doc.canInitialize() {
			return nil, expected, err
		}
		root = uuid.NewString()
		initialize = true
	}
	builder := prismYNodeBuilder{client: client}
	if initialize {
		builder.node(root, "root", "folder", "", "", false)
		files[root] = map[string]any{"id": root, "filename": "root", "type": "folder", "deleted": false}
	}
	parent := root
	parts := strings.Split(delta.FilePath, "/")
	for index, name := range parts {
		node, err := prismDeltaChild(files, parent, name)
		if err != nil {
			return nil, expected, err
		}
		last := index == len(parts)-1
		if !last {
			if node != "" {
				if files[node]["type"] != "folder" {
					return nil, expected, prismDeltaBuildError("path_conflict")
				}
			} else {
				node = uuid.NewString()
				builder.node(node, name, "folder", parent, "", false)
				files[node] = map[string]any{"id": node, "filename": name, "type": "folder", "deleted": false, "inFolder": parent}
			}
			parent = node
			continue
		}
		if node != "" {
			if delta.Status == "added" {
				return nil, expected, prismDeltaBuildError("path_conflict")
			}
			if files[node]["type"] != "text" {
				return nil, expected, prismDeltaBuildError("unsupported_file_type")
			}
			text, err := doc.textContent(node)
			if err != nil {
				return nil, expected, err
			}
			result, err := prismApplyUnifiedDiff(text.Text, delta.Diff, delta.FilePath)
			if err != nil {
				return nil, expected, prismDeltaBuildError("baseline_mismatch")
			}
			if len(utf16.Encode([]rune(result))) > 1<<20 {
				return nil, expected, prismDeltaBuildError("text_too_large")
			}
			update, err := prismEncodeYTextEdit(client, text, result)
			expected.NodeID, expected.Text = node, result
			if len(update) > 0 {
				parsed, parseErr := prismReadYUpdate(update)
				if parseErr != nil {
					return nil, expected, parseErr
				}
				expected.Clock = parsed.clocks[client]
			}
			return update, expected, err
		}
		if delta.Status != "added" {
			return nil, expected, prismDeltaBuildError("missing_baseline")
		}
		result, err := prismApplyUnifiedDiff("", delta.Diff, delta.FilePath)
		if err != nil {
			return nil, expected, prismDeltaBuildError("invalid_diff")
		}
		if len(utf16.Encode([]rune(result))) > 1<<20 {
			return nil, expected, prismDeltaBuildError("text_too_large")
		}
		node = uuid.NewString()
		builder.node(node, name, "text", parent, result, true)
		expected.NodeID, expected.Text = node, result
	}
	if initialize {
		builder.setting("init", true)
		builder.setting("deleted", false)
	}
	expected.Clock = builder.clock
	return builder.update(), expected, nil
}

func prismDeltaRoot(doc *prismYDocument, files map[string]map[string]any) (string, error) {
	for _, item := range doc.items {
		if !item.deleted && doc.resolve(item, 0) == nil && item.root == "settings" && item.key == "deleted" && item.value == true {
			return "", errors.New("Prism project is deleted")
		}
	}
	root := ""
	for id, file := range files {
		if file["type"] == "folder" && file["deleted"] == false {
			if _, parent := file["inFolder"]; !parent {
				if root != "" || file["id"] != id {
					return "", errors.New("ambiguous Prism root folder")
				}
				root = id
			}
		}
	}
	if root == "" {
		return "", errors.New("Prism root folder unavailable")
	}
	return root, nil
}

func prismDeltaChild(files map[string]map[string]any, parent, name string) (string, error) {
	match := ""
	for id, file := range files {
		if file["inFolder"] != parent || file["filename"] != name {
			continue
		}
		if file["deleted"] == true {
			continue
		}
		if file["deleted"] != false || file["id"] != id || match != "" {
			return "", errors.New("ambiguous Prism file path")
		}
		match = id
	}
	return match, nil
}

func prismVerifyDelta(doc *prismYDocument, expected prismDeltaExpectation) error {
	files, err := doc.files()
	if err != nil {
		return err
	}
	parent, err := prismDeltaRoot(doc, files)
	if err != nil {
		return err
	}
	parts := strings.Split(expected.Path, "/")
	for index, part := range parts {
		parent, err = prismDeltaChild(files, parent, part)
		if err != nil || parent == "" {
			return errors.New("Prism file was not acknowledged")
		}
		if index < len(parts)-1 && files[parent]["type"] != "folder" {
			return errors.New("Prism folder changed during synchronization")
		}
	}
	if parent != expected.NodeID || files[parent]["type"] != "text" || doc.clocks[expected.Client] < expected.Clock {
		return errors.New("Prism file update was not acknowledged")
	}
	text, err := doc.textContent(parent)
	if err != nil {
		return err
	}
	if text.Text != expected.Text {
		return errors.New("Prism file changed during synchronization")
	}
	return nil
}

type prismYNodeBuilder struct {
	client, clock, count uint64
	body                 prismYWriter
}

func (b *prismYNodeBuilder) node(id, name, kind, parent, text string, hasText bool) {
	nodeClock := b.clock
	b.body = append(b.body, 39)
	b.body.uint(1)
	b.body.text("content")
	b.body.text(id)
	b.body.uint(1)
	b.clock++
	b.count++
	fields := []struct {
		key   string
		value any
	}{{"id", id}, {"filename", name}, {"type", kind}, {"deleted", false}}
	if parent != "" {
		fields = append(fields, struct {
			key   string
			value any
		}{"inFolder", parent})
	}
	for _, field := range fields {
		b.property(nodeClock, field.key, field.value)
	}
	if hasText {
		textClock := b.clock
		b.body = append(b.body, 39)
		b.body.uint(0)
		b.body.uint(b.client)
		b.body.uint(nodeClock)
		b.body.text("content")
		b.body.uint(2)
		b.clock++
		b.count++
		if text != "" {
			prismWriteYText(&b.body, prismYID{b.client, textClock}, nil, nil, text)
			b.clock += uint64(len(utf16.Encode([]rune(text))))
			b.count++
		}
	}
}
func (b *prismYNodeBuilder) property(parent uint64, key string, value any) {
	b.body = append(b.body, 40)
	b.body.uint(0)
	b.body.uint(b.client)
	b.body.uint(parent)
	b.body.text(key)
	b.body.uint(1)
	b.value(value)
	b.clock++
	b.count++
}
func (b *prismYNodeBuilder) setting(key string, value bool) {
	b.body = append(b.body, 40)
	b.body.uint(1)
	b.body.text("settings")
	b.body.text(key)
	b.body.uint(1)
	b.value(value)
	b.clock++
	b.count++
}
func (b *prismYNodeBuilder) value(value any) {
	if text, ok := value.(string); ok {
		b.body = append(b.body, 119)
		b.body.text(text)
	} else if value == true {
		b.body = append(b.body, 120)
	} else {
		b.body = append(b.body, 121)
	}
}
func (b *prismYNodeBuilder) update() []byte {
	var w prismYWriter
	w.uint(1)
	w.uint(b.count)
	w.uint(b.client)
	w.uint(0)
	w = append(w, b.body...)
	w.uint(0)
	return w
}
