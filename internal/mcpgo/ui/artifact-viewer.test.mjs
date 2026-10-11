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

function setup(openai) {
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
  new Script(js, { filename: 'artifact-viewer.html' }).runInContext(createContext({ window, document, URL, Promise, Map }));
  const send = data => handler({ source: parent, data });
  return { elements, messages, send };
}

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
