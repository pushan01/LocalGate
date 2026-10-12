import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { Script, createContext } from 'node:vm';
import test from 'node:test';

// Test the production MCP Apps viewer without a browser or any network access.
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

function setup(openai = {}) {
  const elements = { '#items': new Node(), '#status': new Node() };
  const messages = [];
  let onMessage;
  const parent = { postMessage(value) { messages.push(value); } };
  const window = {
    parent,
    openai,
    addEventListener(type, handler) {
      if (type === 'message') onMessage = handler;
    },
  };
  const document = {
    querySelector(selector) {
      assert.ok(elements[selector], 'unexpected widget element ' + selector);
      return elements[selector];
    },
    createElement(type) { return new Node(type); },
  };
  new Script(js, { filename: 'artifact-viewer.html' }).runInContext(
    createContext({
      window, document, URL, Promise, Map,
      fetch() { throw new Error('widget must not fetch remote files'); },
      File: class { constructor() { throw new Error('widget must not create files'); } },
    }),
  );
  const send = data => onMessage({ source: parent, data });
  return { elements, messages, send };
}

test('widget handshakes and renders MCP image plus download link', async () => {
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
  const [card] = elements['#items'].children;
  assert.equal(card.tag, 'article');
  assert.equal(card.children[0].tag, 'img');
  assert.equal(card.children[0].src, 'data:image/png;base64,aGVsbG8=');
  assert.equal(card.children[1].children[0].textContent, 'photo.png');
  assert.equal(card.children[1].children[1].href, 'https://files.example/path?signature=abc');
  assert.equal(card.children[1].children[1].textContent, '下载原文件');
  assert.equal(elements['#status'].hidden, true);
});

test('widget displays canonical ChatGPT MCP result metadata', () => {
  const { elements } = setup({
    toolOutput: { result: { file_name: 'photo.png' } },
    toolResponseMetadata: { mcp_tool_result: { result: { content: [
      { type: 'resource_link', uri: 'https://files.example/signed', name: 'photo.png' },
      { type: 'image', mimeType: 'image/jpeg', data: 'aGVsbG8=' },
    ] } } },
  });
  const [card] = elements['#items'].children;
  assert.equal(card.children[0].src, 'data:image/jpeg;base64,aGVsbG8=');
  assert.equal(card.children[1].children[1].href, 'https://files.example/signed');
});

test('widget falls back to structured fileUrl without native links', () => {
  const { elements } = setup({
    toolOutput: {
      status: 'succeeded', fileUrl: 'https://files.example/export?signature=a',
      fileName: 'structured.png', mimeType: 'image/png',
    },
  });
  const [card] = elements['#items'].children;
  assert.equal(card.children[0].children[0].textContent, 'structured.png');
  assert.equal(card.children[0].children[1].href, 'https://files.example/export?signature=a');
});

test('widget handles non-image downloads and escapes untrusted filenames', () => {
  const { elements, send } = setup();
  const untrusted = '<img src=x onerror=alert(1)>';
  send({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: {
    content: [{ type: 'resource_link', uri: 'https://files.example/doc.pdf', name: untrusted }],
  } });
  const [card] = elements['#items'].children;
  assert.equal(card.children.length, 1);
  assert.equal(card.children[0].children[0].textContent, untrusted);
  assert.equal(card.children[0].children[1].href, 'https://files.example/doc.pdf');
});

test('widget rejects non-HTTP links, unsupported images and unsafe Base64', () => {
  const { elements, send } = setup();
  send({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: {
    content: [
      { type: 'resource_link', uri: 'javascript:alert(1)', name: 'evil' },
      { type: 'image', mimeType: 'image/svg+xml', data: 'eA==' },
      { type: 'image', mimeType: 'image/png', data: 'not base64!!' },
    ],
  } });
  assert.equal(elements['#items'].children.length, 0);
});

test('widget never uploads, persists file IDs, performs roundtrip reads or sends follow-up messages', () => {
  const called = [];
  const { elements, send } = setup({
    toolOutput: {
      fileUrl: 'https://files.example/export', fileName: 'photo.png',
      mimeType: 'image/png',
    },
    uploadFile() { called.push('uploadFile'); },
    getFileDownloadUrl() { called.push('getFileDownloadUrl'); },
    setWidgetState() { called.push('setWidgetState'); },
    sendFollowUpMessage() { called.push('sendFollowUpMessage'); },
  });
  send({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: {
    content: [{ type: 'resource_link', uri: 'https://files.example/export', name: 'photo.png' }],
  } });
  assert.equal(elements['#items'].children.length, 1);
  assert.deepEqual(called, []);
  assert.doesNotMatch(html, /id="checks"|id="followup"|已返回 fileId|Sandbox 文件挂载/);
});
