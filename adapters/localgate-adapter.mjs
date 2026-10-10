#!/usr/bin/env node

import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';

const GEMINI_APP_URL = 'https://gemini.google.com/app';
const GEMINI_DOMAIN = 'gemini.google.com';
const DEFAULT_SUBMISSION_TIMEOUT_SECONDS = 8;
const MAX_ARTIFACT_BYTES = 16 << 20;
const GENERATED_IMAGE_SELECTOR = '.generated-images img, single-image.generated-image img, .generated-image img.image.loaded, .generated-image img';
const browserExecutable = (process.env.LOCALGATE_BROWSER_EXECUTABLE || 'opencli').trim() || 'opencli';
let activeSite = 'gemini';

function browserSession() {
  const override = String(process.env.LOCALGATE_BROWSER_SESSION || '').trim();
  return override || `localgate-${activeSite}-adapter`;
}

function fail(message, detail = '') {
  const text = detail ? `${message}: ${detail}` : message;
  process.stderr.write(`${text}\n`);
  process.exit(1);
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function runBrowser(args, { parseJSON = true } = {}) {
  const result = spawnSync(browserExecutable, ['browser', browserSession(), ...args], {
    encoding: 'utf8',
    maxBuffer: 32 << 20,
    env: process.env,
  });
  if (result.error) {
    throw new Error(`failed to start browser backend ${browserExecutable}: ${result.error.message}`);
  }
  if (result.status !== 0) {
    const stderr = String(result.stderr || '').trim();
    const stdout = String(result.stdout || '').trim();
    throw new Error(stderr || stdout || `browser backend exited with status ${result.status}`);
  }
  const stdout = String(result.stdout || '').trim();
  if (!parseJSON) return stdout;
  if (!stdout) return null;
  try {
    return JSON.parse(stdout);
  } catch (error) {
    throw new Error(`browser backend returned invalid JSON: ${error.message}`);
  }
}

function browserOpen(url) {
  return runBrowser(['open', url, '--window', 'background']);
}

function browserEval(script) {
  return runBrowser(['eval', script]);
}

function browserKeys(key) {
  return runBrowser(['keys', key]);
}

function browserNetworkRaw(since = '15s') {
  return runBrowser(['network', '--since', since, '--raw']);
}

function extractGeminiID(value) {
  const raw = String(value || '').trim();
  if (!raw) return '';
  if (/^[a-zA-Z0-9_-]{6,}$/.test(raw) && !raw.includes('/')) return raw;
  try {
    const url = new URL(raw, GEMINI_APP_URL);
    if (url.hostname !== GEMINI_DOMAIN && !url.hostname.endsWith(`.${GEMINI_DOMAIN}`)) return '';
    const match = url.pathname.match(/^\/app\/([^/?#]+)/);
    return match ? match[1] : '';
  } catch {
    const match = raw.match(/(?:^|\/app\/)([a-zA-Z0-9_-]{6,})(?:[/?#]|$)/);
    return match ? match[1] : '';
  }
}

function conversationURL(value) {
  const id = extractGeminiID(value);
  if (!id) throw new Error('Gemini conversation must be an id or /app/<id> URL');
  return { id, url: `${GEMINI_APP_URL}/${id}` };
}

function normalizeFingerprintText(value) {
  return String(value || '')
    .normalize('NFKC')
    .toLocaleLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, '');
}

function promptMatchesTurn(prompt, turnText) {
  const want = normalizeFingerprintText(prompt);
  const got = normalizeFingerprintText(turnText);
  if (!want || !got) return false;
  if (got === want) return true;
  return got.startsWith(want) && [...got].length - [...want].length <= 32;
}

function parseArgs(argv) {
  const positional = [];
  const flags = new Map();
  for (let i = 0; i < argv.length; i += 1) {
    const item = argv[i];
    if (!item.startsWith('--')) {
      positional.push(item);
      continue;
    }
    const key = item.slice(2);
    const next = argv[i + 1];
    if (next !== undefined && !next.startsWith('--')) {
      flags.set(key, next);
      i += 1;
    } else {
      flags.set(key, 'true');
    }
  }
  return { positional, flags };
}

function boolFlag(value) {
  return ['true', '1', 'yes', 'on'].includes(String(value || '').trim().toLowerCase());
}

function intFlag(value, fallback) {
  const parsed = Number.parseInt(String(value ?? ''), 10);
  return Number.isFinite(parsed) ? parsed : fallback;
}

const snapshotScript = String.raw`
(() => {
  /* LOCALGATE_GEMINI_SNAPSHOT */
  const clean = (value) => String(value || '')
    .replace(/\u00a0/g, ' ')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
  const isVisible = (el) => {
    if (!(el instanceof HTMLElement)) return false;
    const style = window.getComputedStyle(el);
    if (style.display === 'none' || style.visibility === 'hidden') return false;
    const rect = el.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  };
  const composerSelectors = [
    '.ql-editor[contenteditable="true"]',
    '.ql-editor[role="textbox"]',
    '.ql-editor[aria-label*="Gemini"]',
    '[contenteditable="true"][aria-label*="Gemini"]',
    '[aria-label="Enter a prompt for Gemini"]',
    '[aria-label*="prompt for Gemini"]',
  ];
  const composer = composerSelectors
    .flatMap((selector) => Array.from(document.querySelectorAll(selector)))
    .find((node) => node instanceof HTMLElement && isVisible(node)) || null;

  const modelPicker = Array.from(document.querySelectorAll('button, [role="button"]'))
    .find((node) => node instanceof HTMLElement
      && isVisible(node)
      && /(?:open )?mode picker/i.test(clean(node.getAttribute('aria-label')))) || null;
  const model = clean(modelPicker?.innerText || modelPicker?.textContent || '');

  const nodes = Array.from(document.querySelectorAll('.query-text, .model-response-text'))
    .filter((node) => node instanceof HTMLElement && isVisible(node))
    .sort((left, right) => {
      if (left === right) return 0;
      const relation = left.compareDocumentPosition(right);
      if (relation & Node.DOCUMENT_POSITION_FOLLOWING) return -1;
      if (relation & Node.DOCUMENT_POSITION_PRECEDING) return 1;
      return 0;
    });

  const stripUserPrefix = (text) => clean(text)
    .replace(/^(?:You said|You asked|User|你说|你问|您说)\s*/i, '')
    .trim();
  const stripAssistantPrefix = (text) => clean(text)
    .replace(/^(?:Gemini said|Gemini|Assistant|Gemini 回答)\s*/i, '')
    .trim();

  const generatedImages = Array.from(document.querySelectorAll(
    '.generated-images img, single-image.generated-image img, .generated-image img.image.loaded, .generated-image img'
  )).filter((node) => node instanceof HTMLImageElement && node.complete && node.naturalWidth > 0 && node.naturalHeight > 0);
  const imageDescriptors = (root) => {
    if (!(root instanceof HTMLElement)) return [];
    return Array.from(root.querySelectorAll(
      '.generated-images img, single-image.generated-image img, .generated-image img.image.loaded, .generated-image img'
    )).filter((node) => node instanceof HTMLImageElement && node.complete && node.naturalWidth > 0 && node.naturalHeight > 0)
      .map((node) => ({
        Ordinal: generatedImages.indexOf(node),
        Alt: clean(node.alt || '').slice(0, 1000),
        Width: node.naturalWidth,
        Height: node.naturalHeight,
      }))
      .filter((image) => image.Ordinal >= 0);
  };
  const mergeImages = (target, incoming) => {
    const seen = new Set((target || []).map((image) => image.Ordinal));
    const out = Array.isArray(target) ? target : [];
    for (const image of incoming || []) {
      if (seen.has(image.Ordinal)) continue;
      seen.add(image.Ordinal);
      out.push(image);
    }
    return out;
  };

  const turns = [];
  for (const node of nodes) {
    const isUser = node.matches('.query-text');
    const role = isUser ? 'User' : 'Assistant';
    const text = isUser
      ? stripUserPrefix(node.innerText || node.textContent || '')
      : stripAssistantPrefix(node.innerText || node.textContent || '');
    // Gemini image-generation responses can have a real model response with no
    // textual body at all: the response container only contains generated
    // image media. Preserve an empty Assistant turn in that case so LocalGate
    // can reconcile the pending User turn once generation has stopped. Do not
    // treat an arbitrary empty model-response node as completion; require
    // explicit generated-media structure in the same response container.
    const responseRoot = !isUser
      ? node.closest('model-response, response-container, .response-container')
      : null;
    const hasGeneratedMedia = !!(responseRoot && responseRoot.querySelector(
      '.generated-images, single-image.generated-image, .generated-image img, img.image.loaded'
    ));
    const images = !isUser ? imageDescriptors(responseRoot) : [];
    if (!text && !hasGeneratedMedia) continue;
    const previous = turns[turns.length - 1];
    if (previous && previous.Role === role) {
      previous.Images = mergeImages(previous.Images, images);
      if (!text && hasGeneratedMedia) {
        // A text Assistant turn followed by generated media belongs to the same
        // model response. It already provides the completion anchor we need.
        continue;
      }
      if (previous.Text === text) continue;
      if (previous.Text.includes(text)) continue;
      if (text.includes(previous.Text)) {
        previous.Text = text;
        continue;
      }
      previous.Text += '\n' + text;
      continue;
    }
    turns.push({ Role: role, Text: text, Images: images });
  }

  const generating = Array.from(document.querySelectorAll('button, [role="button"]')).some((node) => {
    if (!(node instanceof HTMLElement) || !isVisible(node)) return false;
    const text = clean(node.textContent).toLocaleLowerCase();
    const aria = clean(node.getAttribute('aria-label')).toLocaleLowerCase();
    return text === 'stop response'
      || aria === 'stop response'
      || text === '停止回答'
      || aria === '停止回答'
      || text === '停止生成'
      || aria === '停止生成';
  });

  const signIn = Array.from(document.querySelectorAll('a, button, [role="button"]')).some((node) => {
    if (!(node instanceof HTMLElement) || !isVisible(node)) return false;
    const text = clean(node.textContent).toLocaleLowerCase();
    const aria = clean(node.getAttribute('aria-label')).toLocaleLowerCase();
    const href = node.getAttribute('href') || '';
    return text === 'sign in'
      || aria === 'sign in'
      || text === '登录'
      || aria === '登录'
      || href.includes('accounts.google.com/ServiceLogin');
  });

  // Do not classify quota/challenge state from the conversation-history sidebar.
  // A valid chat title such as "Gemini Spark and API Quotas" would otherwise
  // make every new Gemini conversation look quota-blocked.
  const bodyText = clean((document.body && document.body.innerText) || '');
  const sideNav = document.querySelector('bard-sidenav, side-navigation-content, conversations-list');
  const sideNavText = clean((sideNav && (sideNav.innerText || sideNav.textContent)) || '');
  const blockerText = (sideNavText ? bodyText.replace(sideNavText, ' ') : bodyText).slice(0, 12000);
  let blocker = '';
  if (signIn && !composer) blocker = 'auth';
  else if (/captcha|verify (?:that )?you(?:'re| are) human|验证您是真人|完成验证/i.test(blockerText) && !composer) blocker = 'challenge';
  else if (/too many requests|rate limits?|usage limit|usage cap|quota\s+(?:exceeded|reached|limit|exhausted)|reached (?:your|the) .*?limit|limit (?:reached|exceeded)|达到.*(?:上限|限额)|请求过于频繁/i.test(blockerText) && !generating) blocker = 'quota';

  return {
    url: window.location.href,
    title: document.title || '',
    turns,
    composerHasText: !!(composer && clean(composer.innerText || composer.textContent || '')),
    canSend: !!composer,
    model,
    generating,
    blocker,
  };
})()
`;

function insertPromptScript(text) {
  return String.raw`
((inputText) => {
  /* LOCALGATE_GEMINI_INSERT */
  const isVisible = (el) => {
    if (!(el instanceof HTMLElement)) return false;
    const style = window.getComputedStyle(el);
    if (style.display === 'none' || style.visibility === 'hidden') return false;
    const rect = el.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  };
  const selectors = [
    '.ql-editor[contenteditable="true"]',
    '.ql-editor[role="textbox"]',
    '.ql-editor[aria-label*="Gemini"]',
    '[contenteditable="true"][aria-label*="Gemini"]',
    '[aria-label="Enter a prompt for Gemini"]',
    '[aria-label*="prompt for Gemini"]',
  ];
  const composer = selectors
    .flatMap((selector) => Array.from(document.querySelectorAll(selector)))
    .find((node) => node instanceof HTMLElement && isVisible(node));
  if (!(composer instanceof HTMLElement)) return { ok: false, reason: 'Gemini sign in required or composer not found' };

  composer.focus();
  try {
    const selection = window.getSelection();
    const range = document.createRange();
    range.selectNodeContents(composer);
    range.collapse(false);
    selection?.removeAllRanges();
    selection?.addRange(range);
    composer.textContent = '';
    composer.dispatchEvent(new InputEvent('input', { bubbles: true, data: '', inputType: 'deleteContentBackward' }));
  } catch {}

  let inserted = false;
  try {
    inserted = typeof document.execCommand === 'function'
      ? document.execCommand('insertText', false, inputText)
      : false;
  } catch {}

  if (!inserted) {
    const paragraph = document.createElement('p');
    const lines = String(inputText).split(/\n/);
    for (const [index, line] of lines.entries()) {
      if (index > 0) paragraph.appendChild(document.createElement('br'));
      paragraph.appendChild(document.createTextNode(line));
    }
    composer.replaceChildren(paragraph);
  }

  try {
    composer.dispatchEvent(new InputEvent('beforeinput', { bubbles: true, data: inputText, inputType: 'insertText' }));
    composer.dispatchEvent(new InputEvent('input', { bubbles: true, data: inputText, inputType: 'insertText' }));
  } catch {
    composer.dispatchEvent(new Event('input', { bubbles: true }));
  }
  composer.dispatchEvent(new Event('change', { bubbles: true }));

  const actual = String(composer.innerText || composer.textContent || '').trim();
  return { ok: actual.length > 0, actual };
})(${JSON.stringify(text)})
`;
}

const submitScript = String.raw`
(() => {
  /* LOCALGATE_GEMINI_SUBMIT */
  const isVisible = (el) => {
    if (!(el instanceof HTMLElement)) return false;
    const style = window.getComputedStyle(el);
    if (style.display === 'none' || style.visibility === 'hidden') return false;
    const rect = el.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  };
  const selectors = [
    '.ql-editor[contenteditable="true"]',
    '.ql-editor[role="textbox"]',
    '.ql-editor[aria-label*="Gemini"]',
    '[contenteditable="true"][aria-label*="Gemini"]',
    '[aria-label="Enter a prompt for Gemini"]',
    '[aria-label*="prompt for Gemini"]',
  ];
  const composer = selectors
    .flatMap((selector) => Array.from(document.querySelectorAll(selector)))
    .find((node) => node instanceof HTMLElement && isVisible(node));
  if (!(composer instanceof HTMLElement)) return { ok: false, reason: 'Gemini composer not found' };

  const composerRect = composer.getBoundingClientRect();
  const roots = [
    composer.closest('form'),
    composer.closest('[role="form"]'),
    composer.closest('.input-area-container'),
    composer.closest('.textbox-container'),
    composer.closest('.input-wrapper'),
    composer.parentElement,
    composer.parentElement?.parentElement,
  ].filter(Boolean);

  const excluded = /main menu|主菜单|microphone|麦克风|upload|上传|mode|模式|tools|工具|settings|临时对话|new chat|新对话/i;
  const submitPattern = /send|发送|傳送|submit|提交/i;
  const seen = new Set();
  let best = null;
  let bestScore = -1;
  for (const root of roots) {
    for (const node of root.querySelectorAll('button, [role="button"]')) {
      if (!(node instanceof HTMLElement) || seen.has(node) || !isVisible(node)) continue;
      seen.add(node);
      if (node instanceof HTMLButtonElement && node.disabled) continue;
      if (node.getAttribute('aria-disabled') === 'true') continue;
      const label = ((node.getAttribute('aria-label') || '') + ' ' + ((node.textContent || '').trim())).trim();
      if (excluded.test(label)) continue;
      const rect = node.getBoundingClientRect();
      const verticalDistance = Math.abs((rect.top + rect.bottom) / 2 - (composerRect.top + composerRect.bottom) / 2);
      if (verticalDistance > 160) continue;
      let score = 0;
      if (submitPattern.test(label)) score += 10;
      if (rect.left >= composerRect.right - 160) score += 3;
      if (rect.left >= composerRect.left) score += 1;
      if (rect.width <= 96 && rect.height <= 96) score += 1;
      if (score > bestScore) {
        best = node;
        bestScore = score;
      }
    }
  }
  if (best instanceof HTMLElement && bestScore >= 3) {
    best.click();
    return { ok: true, action: 'button' };
  }
  composer.focus();
  return { ok: true, action: 'enter' };
})()
`;

function readSnapshot() {
  const snapshot = browserEval(snapshotScript);
  if (!snapshot || typeof snapshot !== 'object' || !Array.isArray(snapshot.turns)) {
    throw new Error('Gemini snapshot returned malformed data');
  }
  if (snapshot.blocker === 'auth') throw new Error('Gemini sign in required');
  if (snapshot.blocker === 'challenge') throw new Error('Gemini challenge required');
  if (snapshot.blocker === 'quota') throw new Error('Gemini rate limit or quota reached');
  return snapshot;
}

async function ensureGemini(url) {
  browserOpen(url);
  await sleep(700);
  const snapshot = readSnapshot();
  const current = String(snapshot.url || '');
  try {
    const parsed = new URL(current);
    if (parsed.hostname !== GEMINI_DOMAIN && !parsed.hostname.endsWith(`.${GEMINI_DOMAIN}`)) {
      throw new Error(`Gemini browser navigation landed on unexpected host ${parsed.hostname}`);
    }
  } catch (error) {
    if (error instanceof TypeError) throw new Error(`Gemini browser navigation returned invalid URL ${current}`);
    throw error;
  }
  return snapshot;
}

function normalizeGeminiModelLabel(value) {
  return String(value || '')
    .trim()
    .toLowerCase()
    .replace(/[‐‑‒–—-]+/g, ' ')
    .replace(/\s+/g, ' ');
}

function stripGeminiModelVersion(value) {
  return normalizeGeminiModelLabel(value).replace(/^\d+(?:\.\d+)*\s+/, '');
}

function geminiModelMatches(label, target) {
  const normalizedTarget = normalizeGeminiModelLabel(target);
  if (!normalizedTarget) return false;
  const normalizedLabel = normalizeGeminiModelLabel(label);
  return normalizedLabel === normalizedTarget || stripGeminiModelVersion(label) === normalizedTarget;
}

const openGeminiModelPickerScript = String.raw`
(() => {
  const visible = (node) => {
    if (!(node instanceof HTMLElement)) return false;
    const style = getComputedStyle(node);
    if (style.display === 'none' || style.visibility === 'hidden') return false;
    const rect = node.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  };
  const picker = Array.from(document.querySelectorAll('button, [role="button"]'))
    .find((node) => visible(node) && /(?:open )?mode picker/i.test(String(node.getAttribute('aria-label') || '').trim()));
  if (!(picker instanceof HTMLElement)) return { ok: false, reason: 'Gemini model picker not found' };
  picker.click();
  return { ok: true };
})()
`;

function chooseGeminiModelScript(targetModel) {
  return String.raw`
((requestedModel) => {
  const visible = (node) => {
    if (!(node instanceof HTMLElement)) return false;
    const style = getComputedStyle(node);
    if (style.display === 'none' || style.visibility === 'hidden') return false;
    const rect = node.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  };
  const clean = (value) => String(value || '').replace(/\u00a0/g, ' ').trim();
  const normalize = (value) => clean(value).toLowerCase().replace(/[‐‑‒–—-]+/g, ' ').replace(/\s+/g, ' ');
  const stripVersion = (value) => normalize(value).replace(/^\d+(?:\.\d+)*\s+/, '');
  const target = normalize(requestedModel);
  const rows = Array.from(document.querySelectorAll('[role="menuitem"], [role="option"]'))
    .filter(visible)
    .map((node) => ({
      node,
      label: clean((node.innerText || node.textContent || '').split(/\n/)[0]),
    }))
    .filter((row) => row.label);
  const matches = rows.filter((row) => normalize(row.label) === target || stripVersion(row.label) === target);
  if (matches.length !== 1) {
    return {
      ok: false,
      reason: matches.length === 0 ? 'Gemini model was not found in the mode picker' : 'Gemini model selector is ambiguous',
      available: rows.map((row) => row.label),
    };
  }
  matches[0].node.click();
  return { ok: true, selected: matches[0].label };
})(${JSON.stringify(targetModel)})
`;
}

async function selectGeminiModel(model, currentModel) {
  const requested = String(model || '').trim();
  if (!requested) return String(currentModel || '').trim();
  if (geminiModelMatches(currentModel, requested)) return String(currentModel || '').trim();

  const opened = browserEval(openGeminiModelPickerScript);
  if (!opened || opened.ok !== true) {
    throw new Error(opened?.reason || 'failed to open Gemini model picker');
  }
  await sleep(250);

  const selected = browserEval(chooseGeminiModelScript(requested));
  if (!selected || selected.ok !== true) {
    const available = Array.isArray(selected?.available) && selected.available.length > 0
      ? `; available models: ${selected.available.join(', ')}`
      : '';
    throw new Error(`${selected?.reason || 'failed to choose Gemini model'}${available}`);
  }
  await sleep(300);

  const verified = readSnapshot();
  if (!geminiModelMatches(verified.model, requested)) {
    throw new Error(`Gemini model selection could not be verified: requested ${requested}, observed ${verified.model || '<unknown>'}`);
  }
  return String(verified.model || selected.selected || '').trim();
}

async function submitGemini(argv) {
  const { positional, flags } = parseArgs(argv);
  const prompt = String(positional[0] || '').trim();
  if (!prompt) throw new Error('Gemini prompt is required');

  const fresh = boolFlag(flags.get('new'));
  const conversation = String(flags.get('conversation') || '').trim();
  const requestedModel = String(flags.get('model') || '').trim();
  if (fresh && conversation) throw new Error('Gemini --new cannot be combined with --conversation');
  if (requestedModel && !fresh) throw new Error('Gemini --model requires --new true');
  const timeoutSeconds = intFlag(flags.get('submission-timeout'), DEFAULT_SUBMISSION_TIMEOUT_SECONDS);
  if (timeoutSeconds < 1 || timeoutSeconds > 60) throw new Error('Gemini submission timeout must be between 1 and 60 seconds');

  let before;
  let expectedID = '';
  if (fresh) {
    before = await ensureGemini(GEMINI_APP_URL);
  } else if (conversation) {
    const target = conversationURL(conversation);
    expectedID = target.id;
    before = await ensureGemini(target.url);
    const observedID = extractGeminiID(before.url);
    if (observedID && observedID !== expectedID) {
      throw new Error(`Gemini conversation navigation mismatch: expected ${expectedID}, observed ${observedID}`);
    }
  } else {
    before = await ensureGemini(GEMINI_APP_URL);
  }

  if (!before.canSend) throw new Error('Gemini sign in required or composer unavailable');
  const selectedModel = await selectGeminiModel(requestedModel, before.model);
  if (requestedModel) before = readSnapshot();
  const inserted = browserEval(insertPromptScript(prompt));
  if (!inserted || inserted.ok !== true) {
    throw new Error(inserted?.reason || 'failed to insert prompt into Gemini composer');
  }

  const submit = browserEval(submitScript);
  if (!submit || submit.ok !== true) {
    throw new Error(submit?.reason || 'failed to submit Gemini prompt');
  }
  if (submit.action === 'enter') browserKeys('Enter');

  const deadline = Date.now() + timeoutSeconds * 1000;
  let current = before;
  let confirmed = false;
  while (Date.now() <= deadline) {
    await sleep(500);
    current = readSnapshot();
    const newTurns = current.turns.slice(before.turns.length);
    const hasPromptTurn = newTurns.some((turn) => turn?.Role === 'User' && promptMatchesTurn(prompt, turn?.Text));
    const lastUser = [...current.turns].reverse().find((turn) => turn?.Role === 'User');
    if (hasPromptTurn || (lastUser && promptMatchesTurn(prompt, lastUser.Text))) {
      confirmed = true;
      break;
    }
    if (!current.composerHasText && current.generating) {
      confirmed = true;
      break;
    }
  }
  if (!confirmed) throw new Error('Gemini prompt submission could not be confirmed before timeout');

  let url = String(current.url || '').trim();
  let id = extractGeminiID(url);
  const idDeadline = Date.now() + 5000;
  while (!id && Date.now() <= idDeadline) {
    await sleep(500);
    const settled = readSnapshot();
    url = String(settled.url || '').trim();
    id = extractGeminiID(url);
    current = settled;
  }
  if (!id) throw new Error('Gemini accepted the prompt but no conversation id was observable');
  if (expectedID && id !== expectedID) throw new Error(`Gemini submit changed conversation id from ${expectedID} to ${id}`);

  process.stdout.write(`${JSON.stringify({
    Status: 'Submitted',
    ConversationId: id,
    ConversationUrl: url || `${GEMINI_APP_URL}/${id}`,
    Model: selectedModel || String(current.model || '').trim(),
    Generating: !!current.generating,
  })}\n`);
}

function captureGeneratedImageScript(ordinal) {
  return String.raw`
((targetOrdinal) => {
  /* LOCALGATE_GEMINI_CAPTURE_IMAGE */
  const selector = '.generated-images img, single-image.generated-image img, .generated-image img.image.loaded, .generated-image img';
  const images = Array.from(document.querySelectorAll(selector))
    .filter((node) => node instanceof HTMLImageElement && node.complete && node.naturalWidth > 0 && node.naturalHeight > 0);
  const img = images[targetOrdinal];
  if (!(img instanceof HTMLImageElement)) return { ok: false, reason: 'generated image not found' };
  const canvas = document.createElement('canvas');
  canvas.width = img.naturalWidth;
  canvas.height = img.naturalHeight;
  const ctx = canvas.getContext('2d');
  if (!ctx) return { ok: false, reason: '2d canvas unavailable' };
  try {
    ctx.drawImage(img, 0, 0);
    const dataURL = canvas.toDataURL('image/png');
    return {
      ok: true,
      dataURL,
      alt: String(img.alt || '').slice(0, 1000),
      width: img.naturalWidth,
      height: img.naturalHeight,
    };
  } catch (error) {
    return { ok: false, reason: String(error && error.message ? error.message : error) };
  }
})(${JSON.stringify(ordinal)})
`;
}

function materializeGeminiArtifacts(snapshot, conversationId) {
  const artifactRoot = String(process.env.LOCALGATE_ARTIFACT_DIR || '').trim();
  if (!artifactRoot || snapshot.generating) return new Map();
  const root = path.resolve(artifactRoot);
  const result = new Map();
  for (const [turnOffset, turn] of snapshot.turns.entries()) {
    if (turn?.Role !== 'Assistant' || !Array.isArray(turn.Images) || turn.Images.length === 0) continue;
    const turnIndex = turnOffset + 1;
    const artifacts = [];
    for (const [imageOffset, descriptor] of turn.Images.entries()) {
      try {
        const captured = browserEval(captureGeneratedImageScript(Number(descriptor?.Ordinal)));
        if (!captured || captured.ok !== true || typeof captured.dataURL !== 'string') continue;
        const match = captured.dataURL.match(/^data:(image\/png);base64,([A-Za-z0-9+/=]+)$/);
        if (!match) continue;
        const bytes = Buffer.from(match[2], 'base64');
        if (bytes.length <= 0 || bytes.length > MAX_ARTIFACT_BYTES) continue;
        const sha256 = createHash('sha256').update(bytes).digest('hex');
        const artifactKey = createHash('sha256')
          .update(`${conversationId}:${turnIndex}:${imageOffset}:${sha256}`)
          .digest('hex')
          .slice(0, 24);
        const dir = path.join(root, conversationId, `turn-${turnIndex}`);
        mkdirSync(dir, { recursive: true, mode: 0o700 });
        const fileName = `gemini-${conversationId}-turn-${turnIndex}-image-${imageOffset + 1}.png`;
        const filePath = path.join(dir, `${artifactKey}.png`);
        writeFileSync(filePath, bytes, { mode: 0o600 });
        artifacts.push({
          id: `art_${artifactKey}`,
          type: 'image',
          mime_type: 'image/png',
          file_name: fileName,
          path: filePath,
          size: bytes.length,
          sha256,
          width: Number(captured.width || descriptor?.Width || 0),
          height: Number(captured.height || descriptor?.Height || 0),
          alt: String(captured.alt || descriptor?.Alt || '').slice(0, 1000),
        });
      } catch (error) {
        // Artifact capture must never turn a completed Gemini response back into
        // a stuck/degraded subagent. Preserve status reconciliation even when a
        // browser cannot materialize one particular image.
        process.stderr.write(`Gemini artifact capture skipped: ${String(error?.message || error)}\n`);
      }
    }
    if (artifacts.length > 0) result.set(turnIndex, artifacts);
  }
  return result;
}

async function inspectGemini(argv) {
  const { positional } = parseArgs(argv);
  const target = conversationURL(positional[0]);
  const snapshot = await ensureGemini(target.url);
  const observedID = extractGeminiID(snapshot.url);
  if (observedID && observedID !== target.id) {
    throw new Error(`Gemini inspect conversation mismatch: expected ${target.id}, observed ${observedID}`);
  }
  const artifactsByTurn = materializeGeminiArtifacts(snapshot, target.id);
  process.stdout.write(`${JSON.stringify({
    ConversationId: target.id,
    ConversationUrl: String(snapshot.url || target.url),
    Generating: !!snapshot.generating,
    Turns: snapshot.turns.map((turn, index) => ({
      Index: index + 1,
      Role: String(turn?.Role || 'System'),
      Text: String(turn?.Text || ''),
      Artifacts: artifactsByTurn.get(index + 1) || [],
    })),
  })}\n`);
}

function extractChatGPTConversationID(value) {
  const raw = String(value || '').trim();
  if (!raw) return '';
  if (/^[a-zA-Z0-9_-]{8,}$/.test(raw) && !raw.includes('/')) return raw;
  try {
    const url = new URL(raw, 'https://chatgpt.com');
    if (url.hostname !== 'chatgpt.com' && !url.hostname.endsWith('.chatgpt.com')) return '';
    const match = url.pathname.match(/\/(?:g\/g-p-[^/]+\/)?c\/([a-zA-Z0-9_-]{8,})\/?$/);
    return match ? match[1] : '';
  } catch {
    return '';
  }
}

function normalizeChatGPTProjectID(value) {
  const raw = String(value || '').trim();
  if (!raw) return '';
  let candidate = raw;
  if (/^[a-fA-F0-9]{8,}$/.test(candidate)) return candidate.toLowerCase();
  if (candidate.startsWith('g-p-')) {
    candidate = candidate.slice(4).split('-')[0];
    return /^[a-fA-F0-9]{8,}$/.test(candidate) ? candidate.toLowerCase() : '';
  }
  try {
    const url = new URL(candidate, 'https://chatgpt.com');
    const match = url.pathname.match(/^\/g\/g-p-([a-fA-F0-9]{8,})(?:[-/]|$)/);
    return match ? match[1].toLowerCase() : '';
  } catch {
    return '';
  }
}

function projectIDFromConversationPayload(payload) {
  if (!payload || typeof payload !== 'object') return '';
  for (const value of [payload.gizmo_id, payload.project_id, payload.project?.id, payload.gizmo?.id, payload.conversation_template_id]) {
    const normalized = normalizeChatGPTProjectID(value);
    if (normalized) return normalized;
  }
  return '';
}

function networkEntries(payload) {
  if (Array.isArray(payload)) return payload;
  if (payload && typeof payload === 'object' && Array.isArray(payload.entries)) return payload.entries;
  return [];
}

function observeChatGPTConversationProjectFromNetwork(conversationID, since = '15s') {
  const suffix = `/backend-api/conversation/${conversationID}`;
  const entries = networkEntries(browserNetworkRaw(since));
  let projectID = '';
  for (const entry of entries) {
    if (!entry || Number(entry.status ?? entry.responseStatus ?? 0) !== 200) continue;
    let pathname = '';
    try { pathname = new URL(String(entry.url || '')).pathname; } catch {}
    if (pathname !== suffix) continue;
    const observed = projectIDFromConversationPayload(entry.body);
    if (observed) projectID = observed;
  }
  return projectID;
}

function projectConversationIDs(payload) {
  const items = Array.isArray(payload)
    ? payload
    : Array.isArray(payload?.items)
      ? payload.items
      : Array.isArray(payload?.conversations)
        ? payload.conversations
        : [];
  return items
    .map((item) => String(item?.id || item?.conversation_id || item?.conversationId || '').trim())
    .filter(Boolean);
}

function observeChatGPTProjectListingFromNetwork(projectID, conversationID, since = '15s') {
  const token = `g-p-${projectID}`;
  const prefix = `/backend-api/gizmos/${token}/conversations`;
  const entries = networkEntries(browserNetworkRaw(since));
  for (const entry of entries) {
    if (!entry || Number(entry.status ?? entry.responseStatus ?? 0) !== 200) continue;
    let pathname = '';
    try { pathname = new URL(String(entry.url || '')).pathname; } catch {}
    if (pathname !== prefix) continue;
    if (projectConversationIDs(entry.body).includes(conversationID)) return true;
  }
  return false;
}

function chatGPTAuthStateScript() {
  return String.raw`
(() => {
  const visible = (node) => {
    if (!(node instanceof HTMLElement)) return false;
    const style = getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 0 && rect.height > 0;
  };
  const login = Array.from(document.querySelectorAll('a,button,[role="button"]')).some((node) => {
    if (!(node instanceof HTMLElement) || !visible(node)) return false;
    const text = ((node.textContent || '') + ' ' + (node.getAttribute('aria-label') || '')).trim().toLowerCase();
    return text === 'log in' || text === 'sign in' || text === '登录' || text === '登陆';
  });
  return { url: location.href, login };
})()
`;
}

const openChatGPTOptionsScript = String.raw`
(() => {
  /* LOCALGATE_CHATGPT_PROJECT_MOVE_OPTIONS */
  const button = document.querySelector('[data-testid="conversation-options-button"]');
  if (!(button instanceof HTMLElement)) return { found: false };
  button.click();
  return { found: true };
})()
`;

const openChatGPTProjectSubmenuScript = String.raw`
(() => {
  /* LOCALGATE_CHATGPT_PROJECT_MOVE_SUBMENU */
  const visible = (node) => {
    if (!(node instanceof HTMLElement)) return false;
    const style = getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 0 && rect.height > 0;
  };
  const candidates = Array.from(document.querySelectorAll('[role="menuitem"][data-has-submenu]')).filter(visible);
  let item = null;
  if (candidates.length === 1) item = candidates[0];
  if (!item) {
    item = candidates.find((node) => /move to project|移至项目|移动到项目|移動至專案/i.test((node.textContent || '').trim())) || null;
  }
  if (!(item instanceof HTMLElement)) return { found: false, count: candidates.length };
  item.click();
  return { found: true };
})()
`;

function selectChatGPTProjectScript(projectID) {
  return String.raw`
((expectedProjectID) => {
  /* LOCALGATE_CHATGPT_PROJECT_MOVE_TARGET */
  const visible = (node) => {
    if (!(node instanceof HTMLElement)) return false;
    const style = getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 0 && rect.height > 0;
  };
  const projectIDFromHref = (href) => {
    try {
      const url = new URL(String(href || ''), location.origin);
      const match = url.pathname.match(/^\/g\/g-p-([a-f0-9]{8,})(?:[-/]|$)/i);
      return match ? match[1].toLowerCase() : '';
    } catch {
      return '';
    }
  };
  const candidates = Array.from(document.querySelectorAll('[role="menuitem"][href*="/g/g-p-"]')).filter(visible);
  const item = candidates.find((node) => projectIDFromHref(node.getAttribute('href')) === expectedProjectID);
  if (!(item instanceof HTMLElement)) {
    return { found: false, availableProjectIDs: candidates.map((node) => projectIDFromHref(node.getAttribute('href'))).filter(Boolean) };
  }
  item.click();
  return { found: true };
})(${JSON.stringify(projectID)})
`;
}

async function openChatGPTConversation(conversationID) {
  browserOpen(`https://chatgpt.com/c/${conversationID}`);
  await sleep(900);
  const state = browserEval(chatGPTAuthStateScript());
  if (state?.login) throw new Error('ChatGPT sign in required');
  return state;
}

async function verifyChatGPTProjectMembership(conversationID, projectID) {
  for (let attempt = 0; attempt < 3; attempt += 1) {
    browserOpen(`https://chatgpt.com/g/g-p-${projectID}`);
    await sleep(900);
    if (observeChatGPTProjectListingFromNetwork(projectID, conversationID, '10s')) return projectID;

    browserOpen(`https://chatgpt.com/c/${conversationID}`);
    await sleep(1100);
    const observed = observeChatGPTConversationProjectFromNetwork(conversationID, '10s');
    if (observed === projectID) return observed;
    if (attempt < 2) await sleep(500);
  }
  return '';
}

const chatGPTInspectScript = String.raw`
(() => {
  /* LOCALGATE_CHATGPT_INSPECT */
  const clean = (value) => String(value || '')
    .replace(/\u00a0/g, ' ')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
  const visible = (node) => {
    if (!(node instanceof HTMLElement)) return false;
    const style = getComputedStyle(node);
    if (style.display === 'none' || style.visibility === 'hidden') return false;
    const rect = node.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  };
  const messages = Array.from(document.querySelectorAll('[data-message-author-role]'))
    .filter((node) => node instanceof HTMLElement && visible(node))
    .map((node) => {
      const rawRole = String(node.getAttribute('data-message-author-role') || '').toLowerCase();
      const role = rawRole === 'user' ? 'User' : rawRole === 'assistant' ? 'Assistant' : '';
      return role ? { Role: role, Text: clean(node.innerText || node.textContent || '') } : null;
    })
    .filter((row) => row && row.Text);

  const generating = Array.from(document.querySelectorAll('button, [role="button"]')).some((node) => {
    if (!(node instanceof HTMLElement) || !visible(node)) return false;
    if (node.getAttribute('data-testid') === 'stop-button') return true;
    const label = clean((node.getAttribute('aria-label') || '') + ' ' + (node.textContent || '')).toLowerCase();
    return /stop (?:generating|response)|停止(?:生成|回答)|停止回应|停止回覆/.test(label);
  });

  const signIn = Array.from(document.querySelectorAll('a, button, [role="button"]')).some((node) => {
    if (!(node instanceof HTMLElement) || !visible(node)) return false;
    const label = clean((node.getAttribute('aria-label') || '') + ' ' + (node.textContent || '')).toLowerCase();
    return label === 'log in' || label === 'sign in' || label === '登录' || label === '登陆';
  });
  const bodyText = clean((document.body && document.body.innerText) || '').slice(0, 16000);
  let blocker = '';
  if (signIn && messages.length === 0) blocker = 'auth';
  else if (/captcha|verify (?:that )?you(?:'re| are) human|验证您是真人|完成验证/i.test(bodyText) && messages.length === 0) blocker = 'challenge';
  else if (/too many requests|rate limit|usage limit|usage cap|quota|达到.*(?:上限|限额)|请求过于频繁/i.test(bodyText) && !generating) blocker = 'quota';

  return {
    url: location.href,
    messages,
    generating,
    blocker,
  };
})()
`;

async function inspectChatGPT(argv) {
  const { positional } = parseArgs(argv);
  const conversationID = extractChatGPTConversationID(positional[0]);
  if (!conversationID) throw new Error('ChatGPT inspect conversation id is required');
  browserOpen(`https://chatgpt.com/c/${conversationID}`);
  await sleep(800);
  const snapshot = browserEval(chatGPTInspectScript);
  if (!snapshot || typeof snapshot !== 'object' || !Array.isArray(snapshot.messages)) {
    throw new Error('ChatGPT inspect returned malformed browser data');
  }
  if (snapshot.blocker === 'auth') throw new Error('ChatGPT sign in required');
  if (snapshot.blocker === 'challenge') throw new Error('ChatGPT challenge required');
  if (snapshot.blocker === 'quota') throw new Error('ChatGPT rate limit or quota reached');
  if (snapshot.messages.length === 0) throw new Error('No visible ChatGPT messages were found');
  process.stdout.write(`${JSON.stringify(snapshot.messages.map((message, index) => ({
    Index: index + 1,
    Role: String(message.Role || ''),
    Text: String(message.Text || ''),
    Generating: !!snapshot.generating,
    StableSeconds: 0,
  })))}\n`);
}

async function main() {
  const [site, command, ...rest] = process.argv.slice(2);
  activeSite = site || 'unknown';
  if (site === 'gemini') {
    if (command === 'submit') return submitGemini(rest);
    if (command === 'inspect') return inspectGemini(rest);
    throw new Error(`unsupported LocalGate Gemini adapter command ${command || '<empty>'}`);
  }
  if (site === 'chatgpt') {
    if (command === 'inspect') return inspectChatGPT(rest);
    throw new Error(`unsupported LocalGate ChatGPT adapter command ${command || '<empty>'}`);
  }
  throw new Error(`unsupported LocalGate adapter site ${site || '<empty>'}`);
}

try {
  await main();
} catch (error) {
  fail(error instanceof Error ? error.message : String(error));
}
