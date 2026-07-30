import test from "node:test";
import assert from "node:assert/strict";
import { SSEParser, consumeSSE, parseSSEBlock } from "./file-terminal-stream.js";

test("SSE parser handles fragmented events and keepalives", () => {
  const events = [];
  const parser = new SSEParser(event => events.push(event));
  parser.push(": keepalive\n\nevent: term");
  parser.push("inal\nid: 3\ndata: {\"next_offset\":3}");
  parser.push("\n\n");
  assert.deepEqual(events, [{event:"terminal", id:"3", data:'{"next_offset":3}'}]);
});

test("SSE parser joins multiple data lines", () => {
  assert.deepEqual(parseSSEBlock("event: error\ndata: first\ndata: second"), {
    event: "error",
    id: "",
    data: "first\nsecond",
  });
});

test("SSE parser ignores blocks without data", () => {
  assert.equal(parseSSEBlock("event: terminal\nid: 1"), null);
  assert.equal(parseSSEBlock(": keepalive"), null);
});

test("SSE consumer cancels and releases the reader when a handler fails", async () => {
  let canceled = false;
  let released = false;
  let reads = 0;
  const response = {
    body: {
      getReader() {
        return {
          async read() {
            reads += 1;
            if (reads === 1) return {done:false, value:new TextEncoder().encode("event: terminal\ndata: {}\n\n")};
            return {done:true, value:undefined};
          },
          async cancel() { canceled = true; },
          releaseLock() { released = true; },
        };
      },
    },
  };
  await assert.rejects(() => consumeSSE(response, () => { throw new Error("handler failed"); }), /handler failed/);
  assert.equal(canceled, true);
  assert.equal(released, true);
});
