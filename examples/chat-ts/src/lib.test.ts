import test from "node:test";
import assert from "node:assert/strict";
import { buildCredentials, normalizeTTYInput, parseArgs } from "./lib";

test("parseArgs applies defaults and keeps the repo path", () => {
  const parsed = parseArgs(["node", "chat-ts", "/tmp/repo"]);

  assert.deepEqual(parsed, {
    target: "127.0.0.1:9445",
    project: "dev",
    provider: "claude",
    cacert: "",
    cert: "",
    key: "",
    jwtKey: "",
    jwtIssuer: "dev",
    jwtAudience: "bridge",
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
    "--jwt-key",
    "jwt.key",
    "--jwt-issuer",
    "issuer-1",
    "--jwt-audience",
    "bridge-api",
    "/repo",
  ]);

  assert.equal(parsed.target, "bridge.internal:9445");
  assert.equal(parsed.project, "proj-1");
  assert.equal(parsed.provider, "codex");
  assert.equal(parsed.cacert, "ca.pem");
  assert.equal(parsed.cert, "client.pem");
  assert.equal(parsed.key, "client.key");
  assert.equal(parsed.jwtKey, "jwt.key");
  assert.equal(parsed.jwtIssuer, "issuer-1");
  assert.equal(parsed.jwtAudience, "bridge-api");
  assert.equal(parsed.repoPath, "/repo");
});

test("buildCredentials composes mTLS and JWT call credentials", async () => {
  const creds = buildCredentials({
    cacert: "../../certs/ca-bundle.crt",
    cert: "../../certs/dev-client.crt",
    key: "../../certs/dev-client.key",
    jwtKey: "../../certs/jwt-signing.key",
    jwtIssuer: "dev",
    jwtAudience: "bridge",
    project: "dev",
  }) as unknown as {
    channelCredentials: object;
    callCredentials: {
      metadataGenerator: (
        options: object,
        callback: (err: Error | null, metadata?: { get: (key: string) => string[] }) => void
      ) => void;
    };
  };

  assert.ok(creds.channelCredentials);
  assert.ok(creds.callCredentials);

  const metadata = await new Promise<{ get: (key: string) => string[] }>(
    (resolve, reject) => {
      creds.callCredentials.metadataGenerator({}, (err, md) => {
        if (err) {
          reject(err);
          return;
        }
        resolve(md as { get: (key: string) => string[] });
      });
    }
  );

  const authHeader = metadata.get("authorization")[0];
  assert.match(authHeader, /^Bearer [A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/);
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
