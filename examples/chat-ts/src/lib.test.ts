import test from "node:test";
import assert from "node:assert/strict";
import { normalizeTTYInput, parseArgs } from "./lib";

test("parseArgs applies defaults and keeps the repo path", () => {
  const parsed = parseArgs(["node", "chat-ts", "/tmp/repo"]);

  assert.deepEqual(parsed, {
    target: "127.0.0.1:9445",
    project: "dev",
    provider: "claude",
    cacert: "",
    cert: "",
    key: "",
    repoPath: "/tmp/repo",
  });
});

test("parseArgs reads explicit flags", () => {
  const parsed = parseArgs([
    "node",
    "chat-ts",
    "--target",
    "bridge.internal:9445",
    "--project",
    "proj-1",
    "--provider",
    "codex",
    "--cacert",
    "ca.pem",
    "--cert",
    "client.pem",
    "--key",
    "client.key",
    "/repo",
  ]);

  assert.equal(parsed.target, "bridge.internal:9445");
  assert.equal(parsed.project, "proj-1");
  assert.equal(parsed.provider, "codex");
  assert.equal(parsed.cacert, "ca.pem");
  assert.equal(parsed.cert, "client.pem");
  assert.equal(parsed.key, "client.key");
  assert.equal(parsed.repoPath, "/repo");
});

test("parseArgs requires a repo path", () => {
  assert.throws(() => parseArgs(["node", "chat-ts"]), /usage: chat-ts/);
});

test("normalizeTTYInput converts line feeds to carriage returns", () => {
  const input = Buffer.from("hello\nworld");
  const output = normalizeTTYInput(input);

  assert.equal(output.toString("utf8"), "hello\rworld");
  assert.notStrictEqual(output, input);
  assert.equal(input.toString("utf8"), "hello\nworld");
});

test("normalizeTTYInput returns the original buffer when no line feeds exist", () => {
  const input = Buffer.from("already\rformatted");
  const output = normalizeTTYInput(input);

  assert.strictEqual(output, input);
});
