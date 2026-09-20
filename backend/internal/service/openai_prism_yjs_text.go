package service

import (
	"errors"
	"sort"
	"unicode/utf16"
)

// IDs contains one CRDT identity per UTF-16 unit, as required by Yjs clocks.
// Updates only delete these observed IDs; concurrent, unseen inserts survive.
type prismYText struct {
	Text   string
	IDs    []prismYID
	Parent prismYID
}

func (d *prismYDocument) textContent(nodeID string) (prismYText, error) {
	var result prismYText
	var node, textType *prismYItem
	for _, item := range d.items {
		if item.deleted {
			continue
		}
		if err := d.resolve(item, 0); err != nil {
			return result, err
		}
		if item.root == "content" && item.key == nodeID && item.kind == 1 {
			if node != nil {
				return result, errors.New("ambiguous Yjs file node")
			}
			node = item
		}
	}
	if node == nil {
		return result, errors.New("Yjs file node is unavailable")
	}
	for _, item := range d.items {
		if item.deleted || item.parent == nil || *item.parent != node.id || item.key != "content" {
			continue
		}
		if item.kind != 2 || textType != nil {
			return result, errors.New("ambiguous Yjs text property")
		}
		textType = item
	}
	if textType == nil {
		return result, errors.New("Yjs text property is unavailable")
	}
	result.Parent = textType.id
	type unit struct {
		id       prismYID
		value    uint16
		deleted  bool
		edges    []int
		incoming int
	}
	units := make([]unit, 0)
	indices := make(map[prismYID]int)
	items := make([]*prismYItem, 0)
	deleted := make(map[uint64][]prismYRange)
	for _, span := range d.deleted {
		deleted[span.client] = append(deleted[span.client], span)
	}
	for _, spans := range deleted {
		sort.Slice(spans, func(i, j int) bool { return spans[i].clock < spans[j].clock })
	}
	for _, item := range d.items {
		if item.ref == 0 || item.ref == 10 {
			continue
		}
		if err := d.resolve(item, 0); err != nil {
			return result, err
		}
		if item.parent == nil || *item.parent != textType.id {
			continue
		}
		if item.ref != 4 && item.ref != 1 {
			return result, errors.New("unsupported Yjs text content")
		}
		if item.key != "" || item.length > 1<<20 || len(units)+int(item.length) > 1<<20 {
			return result, errors.New("Yjs text exceeds supported bounds")
		}
		items = append(items, item)
		chars := utf16.Encode([]rune(item.text))
		start := len(units)
		for offset := uint64(0); offset < item.length; offset++ {
			id := prismYID{item.id.client, item.id.clock + offset}
			entry := unit{id: id, deleted: item.ref == 1}
			if item.ref == 4 {
				entry.value = chars[offset]
			}
			indices[id] = len(units)
			units = append(units, entry)
		}
		spans := deleted[item.id.client]
		firstSpan := sort.Search(len(spans), func(i int) bool { return spans[i].clock+spans[i].length > item.id.clock })
		for _, span := range spans[firstSpan:] {
			if span.clock >= item.id.clock+item.length {
				break
			}
			lo, hi := span.clock, span.clock+span.length
			if lo < item.id.clock {
				lo = item.id.clock
			}
			if hi > item.id.clock+item.length {
				hi = item.id.clock + item.length
			}
			for clock := lo; clock < hi; clock++ {
				units[start+int(clock-item.id.clock)].deleted = true
			}
		}
	}
	addEdge := func(from, to prismYID) error {
		i, ok := indices[from]
		if !ok {
			return errors.New("Yjs text origin is unavailable")
		}
		j, ok := indices[to]
		if !ok {
			return errors.New("Yjs text boundary is unavailable")
		}
		if i == j {
			return errors.New("cyclic Yjs text ordering")
		}
		for _, existing := range units[i].edges {
			if existing == j {
				return nil
			}
		}
		units[i].edges = append(units[i].edges, j)
		units[j].incoming++
		return nil
	}
	for _, item := range items {
		first, last := item.id, prismYID{item.id.client, item.id.clock + item.length - 1}
		if item.origin != nil {
			if err := addEdge(*item.origin, first); err != nil {
				return result, err
			}
		}
		if item.right != nil {
			if err := addEdge(last, *item.right); err != nil {
				return result, err
			}
		}
		for offset := uint64(1); offset < item.length; offset++ {
			if err := addEdge(prismYID{item.id.client, item.id.clock + offset - 1}, prismYID{item.id.client, item.id.clock + offset}); err != nil {
				return result, err
			}
		}
	}
	ready := make([]int, 0)
	for index := range units {
		if units[index].incoming == 0 {
			ready = append(ready, index)
		}
	}
	chars := make([]uint16, 0, len(units))
	visited := 0
	for len(ready) > 0 {
		// Concurrent branches require Yjs's full conflict integration algorithm.
		// Never guess their order in this deliberately limited codec.
		if len(ready) != 1 {
			return result, errors.New("ambiguous concurrent Yjs text ordering")
		}
		index := ready[0]
		ready = ready[:0]
		visited++
		entry := units[index]
		if !entry.deleted {
			chars = append(chars, entry.value)
			result.IDs = append(result.IDs, entry.id)
		}
		for _, next := range entry.edges {
			units[next].incoming--
			if units[next].incoming == 0 {
				ready = append(ready, next)
			}
		}
	}
	if visited != len(units) {
		return result, errors.New("cyclic Yjs text ordering")
	}
	result.Text = string(utf16.Decode(chars))
	encoded := utf16.Encode([]rune(result.Text))
	if len(encoded) != len(chars) {
		return result, errors.New("invalid Yjs UTF-16 text")
	}
	for i := range chars {
		if chars[i] != encoded[i] {
			return result, errors.New("invalid Yjs UTF-16 text")
		}
	}
	return result, nil
}

func prismEncodeYTextEdit(client uint64, text prismYText, result string) ([]byte, error) {
	if text.Text == result {
		return nil, nil
	}
	before, after := []rune(text.Text), []rune(result)
	prefix := 0
	for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(before)-prefix && suffix < len(after)-prefix && before[len(before)-suffix-1] == after[len(after)-suffix-1] {
		suffix++
	}
	start := len(utf16.Encode(before[:prefix]))
	end := len(utf16.Encode(before[:len(before)-suffix]))
	if len(text.IDs) != len(utf16.Encode(before)) {
		return nil, errors.New("Yjs text identities do not match text")
	}
	insert := string(after[prefix : len(after)-suffix])
	var origin, right *prismYID
	if end > 0 {
		id := text.IDs[end-1]
		origin = &id
	}
	if end < len(text.IDs) {
		id := text.IDs[end]
		right = &id
	}
	var w prismYWriter
	if insert != "" {
		w.uint(1)
		w.uint(1)
		w.uint(client)
		w.uint(0)
		prismWriteYText(&w, text.Parent, origin, right, insert)
	} else {
		w.uint(0)
	}
	prismWriteYDeleteSet(&w, text.IDs[start:end])
	return w, nil
}

func prismWriteYText(w *prismYWriter, parent prismYID, origin, right *prismYID, text string) {
	info := byte(4)
	if origin != nil {
		info |= 128
	}
	if right != nil {
		info |= 64
	}
	*w = append(*w, info)
	if origin != nil {
		w.uint(origin.client)
		w.uint(origin.clock)
	}
	if right != nil {
		w.uint(right.client)
		w.uint(right.clock)
	}
	if origin == nil && right == nil {
		w.uint(0)
		w.uint(parent.client)
		w.uint(parent.clock)
	}
	w.text(text)
}

func prismWriteYDeleteSet(w *prismYWriter, ids []prismYID) {
	byClient := make(map[uint64][]uint64)
	for _, id := range ids {
		byClient[id.client] = append(byClient[id.client], id.clock)
	}
	clients := make([]uint64, 0, len(byClient))
	for client := range byClient {
		clients = append(clients, client)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i] > clients[j] })
	w.uint(uint64(len(clients)))
	for _, client := range clients {
		clocks := byClient[client]
		sort.Slice(clocks, func(i, j int) bool { return clocks[i] < clocks[j] })
		ranges := make([]prismYRange, 0)
		for _, clock := range clocks {
			if len(ranges) > 0 && ranges[len(ranges)-1].clock+ranges[len(ranges)-1].length == clock {
				ranges[len(ranges)-1].length++
			} else {
				ranges = append(ranges, prismYRange{client, clock, 1})
			}
		}
		w.uint(client)
		w.uint(uint64(len(ranges)))
		for _, span := range ranges {
			w.uint(span.clock)
			w.uint(span.length)
		}
	}
}
