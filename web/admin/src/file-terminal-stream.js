export class SSEParser {
  constructor(onEvent) {
    this.onEvent = onEvent;
    this.buffer = "";
  }

  push(chunk) {
    this.buffer += chunk || "";
    while (true) {
      const match = this.buffer.match(/(?:\r\n|\r|\n){2}/);
      if (!match || match.index === undefined) return;
      const block = this.buffer.slice(0, match.index);
      this.buffer = this.buffer.slice(match.index + match[0].length);
      const event = parseSSEBlock(block);
      if (event) this.onEvent(event);
    }
  }
}

export function parseSSEBlock(block) {
  let event = "message";
  let id = "";
  const data = [];
  for (const line of String(block || "").split(/\r\n|\r|\n/)) {
    if (!line || line.startsWith(":")) continue;
    const colon = line.indexOf(":");
    const field = colon < 0 ? line : line.slice(0, colon);
    let value = colon < 0 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "event") event = value || "message";
    else if (field === "id") id = value;
    else if (field === "data") data.push(value);
  }
  if (!data.length) return null;
  return {event, id, data:data.join("\n")};
}

export async function consumeSSE(response, onEvent) {
  if (!response?.body?.getReader) throw new Error("Streaming response body is unavailable");
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  const parser = new SSEParser(onEvent);
  let completed = false;
  try {
    while (true) {
      const {done, value} = await reader.read();
      if (done) break;
      parser.push(decoder.decode(value, {stream:true}));
    }
    parser.push(decoder.decode());
    completed = true;
  } finally {
    if (!completed && typeof reader.cancel === "function") {
      try { await reader.cancel(); } catch {}
    }
    if (typeof reader.releaseLock === "function") reader.releaseLock();
  }
}
