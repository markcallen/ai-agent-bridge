const test = require("node:test");
const assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");

const { createNextJsBridgeRoute } = require("./dist/src/nextjs.js");

test("createNextJsBridgeRoute rejects websocket upgrades for other paths", () => {
  const req = {
    headers: { upgrade: "websocket" },
    url: "/not-bridge",
  };

  const server = {};
  const res = {
    socket: { server },
    statusCode: null,
    headers: null,
    body: "",
    writeHead(statusCode, headers) {
      this.statusCode = statusCode;
      this.headers = headers;
    },
    end(body = "") {
      this.body = body;
    },
  };

  const route = createNextJsBridgeRoute({
    bridgeAddr: "127.0.0.1:9445",
    logger: {
      info() {},
      warn() {},
      error() {},
      debug() {},
    },
    wssOptions: { noServer: true },
  });

  route(req, res);

  assert.equal(res.statusCode, 404);
  assert.deepEqual(res.headers, { "Content-Type": "text/plain" });
  assert.equal(res.body, "WebSocket endpoint not found.");
  assert.equal(server.bridgeWss instanceof EventEmitter, true);

  server.bridgeWss.close();
});
