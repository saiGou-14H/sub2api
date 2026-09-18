// Regenerate with Node and the official yjs 13.6.27 package. PRISM_YJS_MODULE
// may point to its local installation. Go tests consume the JSON only.
// All documents, identifiers, and text below are synthetic.
const fs = require('fs');
const path = require('path');
const assert = require('assert/strict');
const Y = require(process.env.PRISM_YJS_MODULE || 'yjs');
const b64 = bytes => Buffer.from(bytes).toString('base64');
const snapshot = doc => Y.encodeStateAsUpdate(doc);
const clone = (doc, client) => {
  const copy = new Y.Doc({ gc: false });
  Y.applyUpdate(copy, snapshot(doc));
  copy.clientID = client;
  return copy;
};
const addNode = (doc, fields, text) => {
  const node = new Y.Map();
  for (const [key, value] of Object.entries(fields)) node.set(key, value);
  if (text !== undefined) node.set('content', new Y.Text(text));
  doc.getMap('content').set(fields.id, node);
  return node;
};
const document = text => {
  const doc = new Y.Doc({ gc: false });
  doc.clientID = 500;
  addNode(doc, { id: 'root-fixture', filename: 'root', type: 'folder', deleted: false });
  addNode(doc, { id: 'text-fixture', filename: 'main.tex', type: 'text', deleted: false, inFolder: 'root-fixture' }, text);
  return doc;
};
const textOf = doc => doc.getMap('content').get('text-fixture').get('content');
const fixtures = { generator: 'yjs 13.6.27; synthetic data only', documents: {}, edits: {}, nodes: {} };
const saveDocument = (name, doc) => {
  fixtures.documents[name] = { update: b64(snapshot(doc)), text: textOf(doc).toString() };
};

const simple = document('alpha\nbeta\ngamma\n');
saveDocument('simple', simple);
const interior = clone(simple, 510);
textOf(interior).delete(6, 5);
saveDocument('interior_deletion', interior);
const inserted = clone(simple, 520);
textOf(inserted).insert(6, 'added\n');
saveDocument('interior_insertion', inserted);
const emoji = document('A🙂中B\n');
saveDocument('emoji', emoji);
const sequential = clone(simple, 530);
textOf(sequential).delete(6, 4);
textOf(sequential).insert(6, 'one');
textOf(sequential).delete(6, 3);
textOf(sequential).insert(6, 'two');
saveDocument('sequential_replacement', sequential);
const concurrentA = clone(simple, 540);
const concurrentB = clone(simple, 550);
textOf(concurrentA).insert(6, 'A');
textOf(concurrentB).insert(6, 'B');
Y.applyUpdate(concurrentA, snapshot(concurrentB));
saveDocument('concurrent_insertion', concurrentA);
const nested = clone(simple, 560);
addNode(nested, { id: 'folder-fixture', filename: 'chapters', type: 'folder', deleted: false, inFolder: 'root-fixture' });
addNode(nested, { id: 'nested-fixture', filename: 'intro.tex', type: 'text', deleted: false, inFolder: 'folder-fixture' }, 'intro\n');
addNode(nested, { id: 'agent-fixture', filename: 'AGENTS.md', type: 'text', deleted: false, inFolder: 'root-fixture' }, 'synthetic guidance\n');
addNode(nested, { id: 'binary-fixture', filename: 'diagram.png', type: 'url', deleted: false, inFolder: 'root-fixture' });
saveDocument('nested', nested);
const ambiguous = clone(simple, 570);
addNode(ambiguous, { id: 'duplicate-fixture', filename: 'main.tex', type: 'text', deleted: false, inFolder: 'root-fixture' }, 'different\n');
saveDocument('ambiguous_path', ambiguous);

// Compute the smallest contiguous edit while retaining complete Unicode scalars.
// Yjs indices and deletion ranges are UTF-16 units, so conversion happens after
// comparing scalars; two emoji must never share a lone high surrogate.
const edit = (name, base, expected) => {
  const edited = clone(base, 600);
  const before = textOf(base).toString();
  const oldScalars = Array.from(before), newScalars = Array.from(expected);
  let prefix = 0, suffix = 0;
  while (prefix < oldScalars.length && prefix < newScalars.length && oldScalars[prefix] === newScalars[prefix]) prefix++;
  while (suffix < oldScalars.length - prefix && suffix < newScalars.length - prefix && oldScalars[oldScalars.length - 1 - suffix] === newScalars[newScalars.length - 1 - suffix]) suffix++;
  const start = oldScalars.slice(0, prefix).join('').length;
  const removed = oldScalars.slice(prefix, oldScalars.length - suffix).join('').length;
  const added = newScalars.slice(prefix, newScalars.length - suffix).join('');
  let update;
  edited.once('update', emitted => { update = emitted; });
  edited.transact(() => {
    if (removed) textOf(edited).delete(start, removed);
    if (added) textOf(edited).insert(start, added);
  });
  assert(update, 'the edit must emit an update');
  const verified = clone(base, 610);
  Y.applyUpdate(verified, update);
  assert.equal(textOf(verified).toString(), expected);
  // A concurrent insertion that the edit never saw must survive, even when it
  // lies within the replaced span. Only known IDs may be in the delete set.
  const concurrent = clone(base, 620);
  textOf(concurrent).insert(start + (removed > 2 ? 1 : 0), '[concurrent]');
  Y.applyUpdate(concurrent, update);
  assert(textOf(concurrent).toString().includes('[concurrent]'));
  fixtures.edits[name] = {
    initial: b64(snapshot(base)), before, result: expected,
    update: b64(update), verified: b64(snapshot(verified)),
    concurrent_verified: b64(snapshot(concurrent)), concurrent_text: textOf(concurrent).toString(),
  };
};
edit('replace_middle', simple, 'alpha\nchanged\ngamma\n');
edit('prepend', simple, 'before\nalpha\nbeta\ngamma\n');
edit('append', simple, 'alpha\nbeta\ngamma\nafter\n');
edit('delete_middle', simple, 'alpha\ngamma\n');
edit('delete_all', simple, '');
edit('replace_all', simple, 'new document');
edit('replace_emoji', emoji, 'A🚀中B\n');
edit('replace_after_deletion', interior, 'alpha\nchanged\n');
edit('replace_after_insertion', inserted, 'alpha\nmodified\nbeta\ngamma\n');
const addNodes = (name, initial, initialize, text) => {
  const target = clone(initial, 600);
  let update;
  target.once('update', emitted => { update = emitted; });
  target.transact(() => {
    if (initialize) addNode(target, { id: 'root-fixture', filename: 'root', type: 'folder', deleted: false });
    addNode(target, { id: 'folder-added', filename: 'chapters', type: 'folder', deleted: false, inFolder: 'root-fixture' });
    addNode(target, { id: 'text-added', filename: 'chapter.tex', type: 'text', deleted: false, inFolder: 'folder-added' }, text);
    if (initialize) {
      target.getMap('settings').set('init', true);
      target.getMap('settings').set('deleted', false);
    }
  });
  const checked = clone(initial, 610);
  Y.applyUpdate(checked, update);
  assert.equal(checked.getMap('content').get('text-added').get('content').toString(), text);
  fixtures.nodes[name] = { update: b64(update), verified: b64(snapshot(checked)), text };
};
addNodes('existing_root', simple, false, 'synthetic🚀\n');
addNodes('new_project', new Y.Doc(), true, 'synthetic🚀\n');
addNodes('empty_text', simple, false, '');
fs.writeFileSync(path.join(__dirname, 'prism_yjs_delta_fixtures.json'), JSON.stringify(fixtures, null, 2) + '\n');
console.log(`Generated ${Object.keys(fixtures.documents).length} documents, ${Object.keys(fixtures.edits).length} edits, and ${Object.keys(fixtures.nodes).length} node fixtures; official Yjs replay preserved concurrent text.`);
