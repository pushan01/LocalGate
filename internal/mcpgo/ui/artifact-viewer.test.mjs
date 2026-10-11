import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { Script, createContext } from 'node:vm';
import test from 'node:test';
import { createHash, webcrypto } from 'node:crypto';

// Lightweight DOM bridge test, without a browser dependency or network calls.
// It verifies that the same HTML embedded in the Go MCP resource creates image
// previews and download links from the MCP Apps tool-result notification.
const html = readFileSync(new URL('./artifact-viewer.html', import.meta.url), 'utf8');
const js = html.match(/<script type="module">([\s\S]*?)<\/script>/)?.[1];
assert.ok(js, 'missing widget script');

class Node {
  constructor(tag = '') {
    this.tag = tag;
    this.children = [];
    this.textContent = '';
    this.className = '';
    this.hidden = false;
  }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = [...nodes]; }
  addEventListener(type, handler) { this[type] = handler; }
}

function setup(openai, extras = {}) {
  const elements = {
    '#items': new Node(), '#status': new Node(),
    '#checks': new Node(), '#followup': new Node(),
  };
  const messages = [];
  let handler;
  const parent = { postMessage(value) { messages.push(value); } };
  const window = {
    parent,
    openai,
    addEventListener(type, callback) {
      if (type === 'message') handler = callback;
    },
  };
  const document = {
    querySelector(selector) { return elements[selector]; },
    createElement(type) { return new Node(type); },
  };
  new Script(js, { filename: 'artifact-viewer.html' }).runInContext(createContext({ window, document, URL, Promise, Map, ...extras }));
  const send = data => handler({ source: parent, data });
  return { elements, messages, send };
}
const tick = () => new Promise(resolve => setTimeout(resolve, 20));
const digest = bytes => createHash('sha256').update(bytes).digest('hex');
const makeFile = (bytes, type = 'image/png') => new Blob([bytes], { type });
const FakeFile = class {
  constructor(parts, name, options) { this.parts = parts; this.name = name; this.type = options.type; }
};

test('widget automatically registers an exported file once when host supports uploadFile', async () => {
  const calls = [];
  const states = [];
  const openai = {
    uploadFile: async (file, options) => { calls.push({ file, options }); return { fileId: 'file-test123' }; },
    getFileDownloadUrl: async ({ fileId }) => ({ downloadUrl: 'https://files.example/chatgpt/' + fileId }),
    setWidgetState: state => states.push(state),
  };
  const { send, elements } = setup(openai, {
    fetch: async () => ({ ok: true, blob: async () => ({ type: 'image/png', size: 3 }) }),
    File: class { constructor(parts, name, options) { this.parts = parts; this.name = name; this.type = options.type; } },
  });
  const params = { content: [{ type: 'resource_link', uri: 'https://files.example/export?signature=test', name: 'photo.png', mimeType: 'image/png' }] };
  send({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params });
  send({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(calls.length, 1);
  assert.equal(calls[0].file.name, 'photo.png');
  assert.equal(calls[0].options.library, true);
  assert.equal(states[0].privateContent.fileId, 'file-test123');
  assert.equal(states.at(-1).privateContent.steps.retrieval.level, '通过');
  assert.equal(states[0].imageIds[0], 'file-test123');
  assert.match(elements['#status'].textContent, /file-test123/);
  assert.equal(elements['#status'].hidden, false);
});

test('widget automatically uploads from structured toolOutput without native resource links', async () => {
  const calls = [];
  const { elements } = setup({
    toolOutput: { status: 'succeeded', fileUrl: 'https://files.example/export?signature=a', fileName: 'output.png', mimeType: 'image/png' },
    uploadFile: async (file, options) => { calls.push({ file, options }); return { fileId: 'file-from-structured' }; },
    setWidgetState() {},
  }, {
    fetch: async () => ({ ok: true, blob: async () => ({ type: 'image/png' }) }),
    File: class { constructor(parts, name, options) { this.name = name; this.type = options.type; } },
  });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(calls.length, 1);
  assert.equal(calls[0].file.name, 'output.png');
  assert.equal(calls[0].options.library, true);
  assert.match(elements['#status'].textContent, /file-from-structured/);
});

test('widget renders canonical ChatGPT MCP response metadata without a notification', () => {
  const { elements } = setup({
    toolOutput: { result: { file_name: 'photo.png' } },
    toolResponseMetadata: {
      mcp_tool_result: { result: { content: [
        { type: 'resource_link', uri: 'https://files.example/signed', name: 'photo.png' },
        { type: 'image', mimeType: 'image/jpeg', data: 'aGVsbG8=' },
      ] } },
    },
  });
  const [card] = elements['#items'].children;
  assert.equal(card.children[0].src, 'data:image/jpeg;base64,aGVsbG8=');
  assert.equal(card.children[1].children[1].href, 'https://files.example/signed');
  assert.equal(elements['#status'].hidden, true);
});

test('widget handshakes and renders an MCP image with a download link', async () => {
  const { elements, messages, send } = setup();
  assert.equal(messages[0].method, 'ui/initialize');
  send({ jsonrpc: '2.0', id: 1, result: { protocolVersion: '2026-01-26' } });
  await Promise.resolve();
  assert.equal(messages[1].method, 'ui/notifications/initialized');
  send({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: {
    content: [
      { type: 'resource_link', uri: 'https://files.example/path?signature=abc', name: 'photo.png' },
      { type: 'image', mimeType: 'image/png', data: 'aGVsbG8=' },
    ],
  } });
  assert.equal(elements['#status'].hidden, true);
  const [card] = elements['#items'].children;
  assert.equal(card.tag, 'article');
  assert.equal(card.children[0].tag, 'img');
  assert.equal(card.children[0].src, 'data:image/png;base64,aGVsbG8=');
  assert.equal(card.children[1].children[0].textContent, 'photo.png');
  const link = card.children[1].children[1];
  assert.equal(link.href, 'https://files.example/path?signature=abc');
  assert.equal(link.textContent, '下载原文件');
});

test('widget handles a PDF with download only and escapes untrusted names', () => {
  const { elements, send } = setup();
  const unsafeName = '<img src=x onerror=alert(1)>';
  send({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: {
    content: [{ type: 'resource_link', uri: 'https://files.example/doc.pdf', name: unsafeName }],
  } });
  const [card] = elements['#items'].children;
  assert.equal(card.children.length, 1);
  assert.equal(card.children[0].children[0].textContent, unsafeName);
  assert.equal(card.children[0].children[1].href, 'https://files.example/doc.pdf');
});

test('widget refuses javascript: download URLs and unsupported image MIME', () => {
  const { elements, send } = setup();
  send({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: {
    content: [
      { type: 'resource_link', uri: 'javascript:alert(1)', name: 'evil' },
      { type: 'image', mimeType: 'image/svg+xml', data: 'eA==' },
    ],
  } });
  assert.equal(elements['#items'].children.length, 0);
});

test('widget verifies LocalGate SHA-256 against a real ChatGPT roundtrip and persists imageIds', async () => {
  const payload = Buffer.from('real-cover-bytes');
  const checksum = digest(payload);
  const snapshots = [];
  const calls = [];
  const sourceUrl = 'https://files.example/export?signature=one';
  const openai = {
    toolOutput: {
      status: 'succeeded',
      result: { export_id: 'lgexp_1', sha256: checksum, size: payload.length },
      fileUrl: sourceUrl, fileName: 'cover.png', mimeType: 'image/png',
    },
    uploadFile: async (file, options) => {
      calls.push({ file, options });
      return { fileId: 'file_roundtrip' };
    },
    getFileDownloadUrl: async () => ({ downloadUrl: 'https://files.example/chatgpt/file_roundtrip' }),
    setWidgetState: value => snapshots.push(structuredClone(value)),
  };
  const { elements } = setup(openai, {
    fetch: async () => ({ ok: true, blob: async () => makeFile(payload) }),
    File: FakeFile, crypto: webcrypto,
  });
  await tick();
  assert.equal(calls.length, 1);
  assert.equal(calls[0].options.library, true);
  const latest = snapshots.at(-1);
  assert.equal(latest.privateContent.exportKey, 'lgexp_1');
  assert.equal(latest.privateContent.steps.source.level, '通过');
  assert.equal(latest.privateContent.steps.upload.level, '通过');
  assert.equal(latest.privateContent.steps.retrieval.level, '通过');
  assert.equal(latest.privateContent.steps.integrity.level, '通过');
  assert.equal(latest.privateContent.steps.sandbox.level, '未验证');
  assert.deepEqual(latest.imageIds, ['file_roundtrip']);
  assert.equal(elements['#checks'].children.length, 6);
  assert.ok(!JSON.stringify(latest).includes(sourceUrl));
});

test('widget blocks upload when LocalGate SHA-256 does not match downloaded file', async () => {
  const payload = Buffer.from('tampered');
  let count = 0;
  const { elements } = setup({
    toolOutput: {
      fileUrl: 'https://files.example/modified', fileName: 'broken.png', mimeType: 'image/png',
      result: { export_id: 'lgexp_bad', size: payload.length, sha256: digest(Buffer.from('original')) },
    },
    uploadFile: async () => { count++; return { fileId: 'unreachable' }; },
  }, {
    fetch: async () => ({ ok: true, blob: async () => makeFile(payload) }),
    File: FakeFile, crypto: webcrypto,
  });
  await tick();
  assert.equal(count, 0);
  assert.match(elements['#status'].textContent, /未上传/);
  assert.match(elements['#checks'].children[0].textContent, /哈希不符/);
});

test('widget detects changed ChatGPT roundtrip bytes even when sizes match', async () => {
  const original = Buffer.from('pixel-A');
  const changed = Buffer.from('pixel-B');
  const snapshots = [];
  const { elements } = setup({
    toolOutput: {
      fileUrl: 'https://files.example/export?signature=first',
      fileName: 'cover.png', mimeType: 'image/png',
      result: { export_id: 'lgexp_changed', size: original.length, sha256: digest(original) },
    },
    uploadFile: async () => ({ fileId: 'file_changed' }),
    getFileDownloadUrl: async () => ({ downloadUrl: 'https://files.example/roundtrip' }),
    setWidgetState: value => snapshots.push(structuredClone(value)),
  }, {
    fetch: async url => ({
      ok: true,
      blob: async () => makeFile(url.includes('roundtrip') ? changed : original),
    }),
    File: FakeFile, crypto: webcrypto,
  });
  await tick();
  assert.equal(snapshots.at(-1).privateContent.steps.integrity.level, '失败');
  assert.match(elements['#checks'].children.find(line => line.textContent.includes('一致性')).textContent, /SHA-256 不一致/);
});

test('widget retains fileId when the host does not provide getFileDownloadUrl', async () => {
  const payload = Buffer.from('abc');
  const snapshots = [];
  setup({
    toolOutput: {
      fileUrl: 'https://files.example/export?signature=test', fileName: 'test.png',
      mimeType: 'image/png', result: { export_id: 'lgexp_no_helper', size: payload.length },
    },
    uploadFile: async () => ({ fileId: 'file_no_helper' }),
    setWidgetState: snapshot => snapshots.push(structuredClone(snapshot)),
  }, {
    fetch: async () => ({ ok: true, blob: async () => makeFile(payload) }),
    File: FakeFile,
  });
  await tick();
  assert.equal(snapshots.at(-1).privateContent.fileId, 'file_no_helper');
  assert.equal(snapshots.at(-1).privateContent.steps.retrieval.level, '不可用');
  assert.equal(snapshots.at(-1).privateContent.steps.integrity.level, '未验证');
});

test('widget preserves successful upload when ChatGPT roundtrip is blocked and offers follow-up', async () => {
  const payload = Buffer.from('cover');
  const stateSnapshots = [];
  const prompts = [];
  const openai = {
    toolOutput: {
      fileUrl: 'https://files.example/export?signature=test', fileName: '封面.png', mimeType: 'image/png',
      result: { export_id: 'lgexp_restore', size: payload.length, sha256: digest(payload) },
    },
    uploadFile: async () => ({ fileId: 'file_restore' }),
    getFileDownloadUrl: async () => ({ downloadUrl: 'https://files.example/download' }),
    setWidgetState: snapshot => stateSnapshots.push(structuredClone(snapshot)),
    sendFollowUpMessage: async message => prompts.push(message),
  };
  const { elements } = setup(openai, {
    fetch: async url => {
      if (url.includes('/download')) throw new Error('CORS denied');
      return { ok: true, blob: async () => makeFile(payload) };
    },
    File: FakeFile, crypto: webcrypto,
  });
  await tick();
  const saved = stateSnapshots.at(-1);
  assert.equal(saved.privateContent.steps.retrieval.level, '未验证');
  assert.equal(saved.privateContent.steps.integrity.level, '未验证');
  assert.equal(elements['#followup'].hidden, false);
  await elements['#followup'].click();
  assert.equal(prompts.length, 1);
  assert.match(prompts[0].prompt, /Sandbox/);
  assert.match(prompts[0].prompt, /file_restore/);
  assert.equal(stateSnapshots.at(-1).privateContent.steps.context.level, '已发起验证');

  let repeatUploads = 0;
  const restored = setup({
    ...openai, widgetState: stateSnapshots.at(-1),
    uploadFile: async () => { repeatUploads++; return { fileId: 'should-not-upload-again' }; },
  }, {
    fetch: async () => ({ ok: true, blob: async () => makeFile(payload) }),
    File: FakeFile, crypto: webcrypto,
  });
  await tick();
  assert.equal(repeatUploads, 0);
  assert.match(restored.elements['#status'].textContent, /file_restore/);
  assert.match(restored.elements['#checks'].children.at(-1).textContent, /未验证/);
});

test('widget reports missing uploadFile without claiming sandbox availability', async () => {
  const { elements } = setup({
    toolOutput: {
      fileUrl: 'https://files.example/export?signature=test', fileName: 'cover.png',
      mimeType: 'image/png', result: { export_id: 'lgexp_no_upload' },
    },
  });
  await tick();
  assert.match(elements['#checks'].children[0].textContent, /不可用/);
  assert.equal(elements['#followup'].hidden, true);
});
