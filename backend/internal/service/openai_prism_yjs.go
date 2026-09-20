package service

// This limited Yjs v1 codec reads Prism's content map, appends file nodes and
// applies edits to unambiguous plain Y.Text values using CRDT identities.
// The format is verified against yjs 13.6.27 fixtures; unsupported or ambiguous
// trees fail closed, and unknown concurrent characters are never deleted.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"unicode/utf16"
	"unicode/utf8"
)

type prismYID struct{ client, clock uint64 }
type prismYItem struct {
	id                    prismYID
	length                uint64
	root, key             string
	parent, origin, right *prismYID
	kind                  uint64
	ref                   byte
	text                  string
	value                 any
	deleted               bool
	resolving             bool
}
type prismYRange struct{ client, clock, length uint64 }
type prismYDocument struct {
	items    map[prismYID]*prismYItem
	deleted  []prismYRange
	clocks   map[uint64]uint64
	byClient map[uint64][]*prismYItem
}
type prismYReader struct {
	data []byte
	pos  int
	err  error
	work int
}

func (r *prismYReader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > len(r.data)-r.pos {
		r.err = io.ErrUnexpectedEOF
		return nil
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b
}
func (r *prismYReader) uint() uint64 {
	if r.err != nil {
		return 0
	}
	n, size := binary.Uvarint(r.data[r.pos:])
	if size <= 0 || n > 1<<53-1 {
		r.err = errors.New("invalid Yjs integer")
		return 0
	}
	r.pos += size
	return n
}
func (r *prismYReader) count() int {
	n := r.uint()
	r.work += int(n)
	if n > 10000 || r.work > 50000 {
		r.err = errors.New("Yjs collection exceeds limit")
		return 0
	}
	return int(n)
}
func (r *prismYReader) bytes() []byte { return r.take(int(r.uint())) }
func (r *prismYReader) text() string {
	b := r.bytes()
	if !utf8.Valid(b) {
		r.err = errors.New("invalid Yjs UTF-8")
	}
	return string(b)
}
func (r *prismYReader) id() *prismYID { return &prismYID{r.uint(), r.uint()} }
func (r *prismYReader) any(depth int) any {
	r.work++
	if depth > 32 || r.work > 50000 {
		r.err = errors.New("Yjs value exceeds nesting limit")
		return nil
	}
	b := r.take(1)
	if len(b) == 0 {
		return nil
	}
	switch b[0] {
	case 127, 126:
		return nil
	case 125:
		first := r.take(1)
		if len(first) == 0 {
			return nil
		}
		value := uint64(first[0] & 63)
		shift := uint(6)
		more := first[0]&128 != 0
		for more {
			b := r.take(1)
			if len(b) == 0 {
				return nil
			}
			if shift > 53 {
				r.err = errors.New("invalid Yjs signed integer")
				return nil
			}
			value |= uint64(b[0]&127) << shift
			shift += 7
			more = b[0]&128 != 0
		}
		if first[0]&64 != 0 {
			return -int64(value)
		}
		return int64(value)
	case 124:
		b := r.take(4)
		if len(b) == 4 {
			return math.Float32frombits(binary.BigEndian.Uint32(b))
		}
	case 123:
		b := r.take(8)
		if len(b) == 8 {
			return math.Float64frombits(binary.BigEndian.Uint64(b))
		}
	case 122:
		r.take(8)
	case 121:
		return false
	case 120:
		return true
	case 119:
		return r.text()
	case 118:
		m := make(map[string]any)
		for n := r.count(); n > 0; n-- {
			k := r.text()
			m[k] = r.any(depth + 1)
		}
		return m
	case 117:
		a := make([]any, r.count())
		for i := range a {
			a[i] = r.any(depth + 1)
		}
		return a
	case 116:
		return r.bytes()
	default:
		r.err = errors.New("unsupported Yjs value")
	}
	return nil
}

func prismReadYUpdate(data []byte) (*prismYDocument, error) {
	if len(data) > 16<<20 {
		return nil, errors.New("Yjs update exceeds limit")
	}
	r := &prismYReader{data: data}
	doc := &prismYDocument{items: make(map[prismYID]*prismYItem), clocks: make(map[uint64]uint64), byClient: make(map[uint64][]*prismYItem)}
	for clients := r.count(); clients > 0; clients-- {
		count, client, clock := r.count(), r.uint(), r.uint()
		if _, duplicate := doc.clocks[client]; duplicate {
			return nil, errors.New("duplicate Yjs client block")
		}
		if len(doc.items)+count > 10000 {
			return nil, errors.New("Yjs struct count exceeds limit")
		}
		for ; count > 0; count-- {
			b := r.take(1)
			if len(b) == 0 {
				break
			}
			info := b[0]
			ref := info & 31
			item := &prismYItem{id: prismYID{client, clock}, length: 1, kind: math.MaxUint64, ref: ref}
			if ref == 0 || ref == 10 {
				item.length = r.uint()
				item.deleted = true
			} else {
				if info&128 != 0 {
					item.origin = r.id()
				}
				if info&64 != 0 {
					item.right = r.id()
				}
				if info&192 == 0 {
					if r.uint() == 1 {
						item.root = r.text()
					} else {
						item.parent = r.id()
					}
					if info&32 != 0 {
						item.key = r.text()
					}
				}
				switch ref {
				case 1:
					item.length = r.uint()
					item.deleted = true
				case 2:
					item.length = uint64(r.count())
					for n := uint64(0); n < item.length; n++ {
						r.text()
					}
				case 3:
					r.bytes()
				case 4:
					item.text = r.text()
					item.length = uint64(len(utf16.Encode([]rune(item.text))))
				case 5:
					r.text()
				case 6:
					r.text()
					r.text()
				case 7:
					item.kind = r.uint()
					if item.kind == 3 || item.kind == 5 {
						r.text()
					}
					if item.kind > 6 {
						r.err = errors.New("unsupported Yjs shared type")
					}
				case 8:
					item.length = uint64(r.count())
					for n := uint64(0); n < item.length; n++ {
						item.value = r.any(0)
					}
				case 9:
					r.text()
					r.any(0)
				default:
					r.err = errors.New("unsupported Yjs content")
				}
			}
			if item.length == 0 {
				r.err = errors.New("invalid empty Yjs struct")
			}
			if item.length > 1<<53-1-clock {
				r.err = errors.New("Yjs clock exceeds range")
			}
			doc.items[item.id] = item
			doc.byClient[client] = append(doc.byClient[client], item)
			clock += item.length
		}
		doc.clocks[client] = clock
	}
	for clients := r.count(); clients > 0; clients-- {
		client := r.uint()
		for count := r.count(); count > 0; count-- {
			span := prismYRange{client, r.uint(), r.uint()}
			if span.length > 1<<53-1-span.clock {
				r.err = errors.New("Yjs deletion range exceeds limit")
			}
			doc.deleted = append(doc.deleted, span)
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.pos != len(data) {
		return nil, errors.New("trailing Yjs update bytes")
	}
	deleted := make(map[uint64][]prismYRange)
	for _, span := range doc.deleted {
		deleted[span.client] = append(deleted[span.client], span)
	}
	for client, spans := range deleted {
		sort.Slice(spans, func(i, j int) bool { return spans[i].clock < spans[j].clock })
		merged := make([]prismYRange, 0, len(spans))
		for _, span := range spans {
			if len(merged) > 0 && span.clock <= merged[len(merged)-1].clock+merged[len(merged)-1].length {
				last := &merged[len(merged)-1]
				if end := span.clock + span.length; end > last.clock+last.length {
					last.length = end - last.clock
				}
			} else {
				merged = append(merged, span)
			}
		}
		deleted[client] = merged
	}
	doc.deleted = nil
	for _, spans := range deleted {
		doc.deleted = append(doc.deleted, spans...)
	}
	for client, items := range doc.byClient {
		sort.Slice(items, func(i, j int) bool { return items[i].id.clock < items[j].id.clock })
		spans := deleted[client]
		for _, item := range items {
			index := sort.Search(len(spans), func(i int) bool { return spans[i].clock > item.id.clock }) - 1
			if index >= 0 && item.id.clock < spans[index].clock+spans[index].length {
				item.deleted = true
			}
		}
	}
	return doc, nil
}

func (d *prismYDocument) find(id *prismYID) *prismYItem {
	if id == nil {
		return nil
	}
	if item := d.items[*id]; item != nil {
		return item
	}
	items := d.byClient[id.client]
	index := sort.Search(len(items), func(i int) bool { return items[i].id.clock > id.clock }) - 1
	if index >= 0 && id.clock < items[index].id.clock+items[index].length {
		return items[index]
	}
	return nil
}
func (d *prismYDocument) resolve(item *prismYItem, depth int) error {
	if item.root != "" || item.parent != nil {
		return nil
	}
	if depth > 1000 || item.resolving {
		return errors.New("Yjs parent chain exceeds limit")
	}
	item.resolving = true
	defer func() { item.resolving = false }()
	ancestor := d.find(item.origin)
	if ancestor == nil {
		ancestor = d.find(item.right)
	}
	if ancestor == nil {
		return errors.New("Yjs parent dependency is missing")
	}
	if err := d.resolve(ancestor, depth+1); err != nil {
		return err
	}
	item.root, item.parent, item.key = ancestor.root, ancestor.parent, ancestor.key
	return nil
}

func (d *prismYDocument) files() (map[string]map[string]any, error) {
	entries := make(map[prismYID]string)
	files := make(map[string]map[string]any)
	for _, item := range d.items {
		if item.deleted {
			continue
		}
		if err := d.resolve(item, 0); err != nil {
			return nil, err
		}
		if item.key != "" && item.length != 1 {
			return nil, errors.New("unsupported Yjs map property length")
		}
		if item.root == "content" && item.kind == 1 && item.key != "" {
			if files[item.key] != nil {
				return nil, errors.New("conflicting Yjs file entries")
			}
			entries[item.id] = item.key
			files[item.key] = make(map[string]any)
		}
	}
	for _, item := range d.items {
		if item.deleted || item.parent == nil || item.key == "" {
			continue
		}
		name, ok := entries[*item.parent]
		if !ok {
			continue
		}
		if _, exists := files[name][item.key]; exists {
			return nil, errors.New("conflicting Yjs file properties")
		}
		files[name][item.key] = item.value
	}
	return files, nil
}

func (d *prismYDocument) rootFolder(nodeID, fileName string) (string, bool, error) {
	for _, item := range d.items {
		if !item.deleted && d.resolve(item, 0) == nil && item.root == "settings" && item.key == "deleted" && item.value == true {
			return "", false, errors.New("Prism project document is deleted")
		}
	}
	files, err := d.files()
	if err != nil {
		return "", false, err
	}
	root := ""
	for id, file := range files {
		if file["type"] == "folder" && file["deleted"] == false {
			if _, hasParent := file["inFolder"]; !hasParent {
				if root != "" || file["id"] != id {
					return "", false, errors.New("Prism document has multiple root folders")
				}
				root = id
			}
		}
	}
	if root == "" {
		return "", false, errors.New("Prism document root folder is unavailable")
	}
	exists := false
	if existing := files[nodeID]; existing != nil {
		if existing["id"] == nodeID && existing["filename"] == fileName && existing["type"] == "url" && existing["deleted"] == false && existing["inFolder"] == root {
			exists = true
		} else {
			return "", false, errors.New("Prism file node id is already in use")
		}
	}
	for id, file := range files {
		if id != nodeID && file["inFolder"] == root && file["filename"] == fileName && file["deleted"] == false {
			return "", false, errors.New("Prism project already contains this filename")
		}
	}
	return root, exists, nil
}

func (d *prismYDocument) canInitialize() bool {
	for _, item := range d.items {
		if item.deleted {
			continue
		}
		if d.resolve(item, 0) != nil {
			return false
		}
		if item.root == "content" {
			return false
		}
		if item.root == "settings" && item.key == "deleted" && item.value == true {
			return false
		}
	}
	return true
}

type prismYWriter []byte

func (w *prismYWriter) uint(n uint64)  { *w = binary.AppendUvarint(*w, n) }
func (w *prismYWriter) text(s string)  { w.uint(uint64(len(s))); *w = append(*w, s...) }
func (w *prismYWriter) bytes(b []byte) { w.uint(uint64(len(b))); *w = append(*w, b...) }
func prismEncodeYFile(client uint64, nodeID, fileName, rootID string) []byte {
	var w prismYWriter
	w.uint(1)
	w.uint(6)
	w.uint(client)
	w.uint(0)
	w = append(w, 39)
	w.uint(1)
	w.text("content")
	w.text(nodeID)
	w.uint(1)
	for _, field := range []struct {
		key   string
		value any
	}{{"id", nodeID}, {"filename", fileName}, {"type", "url"}, {"deleted", false}, {"inFolder", rootID}} {
		w = append(w, 40)
		w.uint(0)
		w.uint(client)
		w.uint(0)
		w.text(field.key)
		w.uint(1)
		if value, ok := field.value.(string); ok {
			w = append(w, 119)
			w.text(value)
		} else {
			w = append(w, 121)
		}
	}
	w.uint(0)
	return w
}
func prismEncodeYNewProjectFile(client uint64, nodeID, fileName, rootID string) []byte {
	var w prismYWriter
	w.uint(1)
	w.uint(13)
	w.uint(client)
	w.uint(0)
	writeMap := func(id string, clock uint64, fields []struct {
		key   string
		value any
	}) {
		w = append(w, 39)
		w.uint(1)
		w.text("content")
		w.text(id)
		w.uint(1)
		for _, field := range fields {
			w = append(w, 40)
			w.uint(0)
			w.uint(client)
			w.uint(clock)
			w.text(field.key)
			w.uint(1)
			if value, ok := field.value.(string); ok {
				w = append(w, 119)
				w.text(value)
			} else {
				w = append(w, 121)
			}
		}
	}
	writeMap(rootID, 0, []struct {
		key   string
		value any
	}{{"id", rootID}, {"filename", "root"}, {"type", "folder"}, {"deleted", false}})
	writeMap(nodeID, 5, []struct {
		key   string
		value any
	}{{"id", nodeID}, {"filename", fileName}, {"type", "url"}, {"deleted", false}, {"inFolder", rootID}})
	for _, field := range []struct {
		key  string
		flag byte
	}{{"init", 120}, {"deleted", 121}} {
		w = append(w, 40)
		w.uint(1)
		w.text("settings")
		w.text(field.key)
		w.uint(1)
		w = append(w, field.flag)
	}
	w.uint(0)
	return w
}
func prismYSyncFrame(subtype uint64, payload []byte) []byte {
	var w prismYWriter
	w.uint(0)
	w.uint(subtype)
	w.bytes(payload)
	return w
}

// Keep errors independent of document values, which may contain user content.
func prismYProtocolError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("Prism document synchronization failed: %w", err)
}
