import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { Script, createContext } from 'node:vm';
import test from 'node:test';

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
}

function setup(openai, extras = {}) {
  const elements = { '#items': new Node(), '#status': new Node() };
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
  assert.equal(states[0].privateContent.retrievable, true);
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
