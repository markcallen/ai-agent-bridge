const test = require("node:test");
const assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");
const path = require("node:path");

const { createNextJsBridgeRoute } = require("./dist/src/nextjs.js");
const { BridgeGrpcClient } = require("./dist/src/index.js");

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

test("BridgeGrpcClient accepts ChannelCredentials from a different grpc-js install", () => {
  const foreignGrpc = require(
    require.resolve("@grpc/grpc-js", {
      paths: [path.resolve(__dirname, "../../examples/chat-ts")],
    })
  );

  assert.doesNotThrow(() => {
    const client = new BridgeGrpcClient({
      bridgeAddr: "127.0.0.1:9445",
      credentials: foreignGrpc.credentials.createSsl(),
      logger: {
        info() {},
        warn() {},
        error() {},
        debug() {},
      },
    });
    client.close();
  });
});
