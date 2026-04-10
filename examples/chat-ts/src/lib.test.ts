import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { buildCredentials, normalizeTTYInput, parseArgs } from "./lib";

const TEST_CA_CERT = `-----BEGIN CERTIFICATE-----
MIIB9zCCAX6gAwIBAgIQBh//PkqN68wf4YDLlhzapDAKBggqhkjOPQQDAzA8MRww
GgYDVQQKExNhaS1hZ2VudC1icmlkZ2UtZGV2MRwwGgYDVQQDExNhaS1hZ2VudC1i
cmlkZ2UtZGV2MB4XDTI2MDQxMDE2MjE1NVoXDTM2MDQxMDE2MjE1NVowPDEcMBoG
A1UEChMTYWktYWdlbnQtYnJpZGdlLWRldjEcMBoGA1UEAxMTYWktYWdlbnQtYnJp
ZGdlLWRldjB2MBAGByqGSM49AgEGBSuBBAAiA2IABH8feoqCs0Qwpbc4/zQyNYYb
USuOXENwXJetwA3CJlac3sNzHmrHTB0aw+a/uG+LsJexwgceKI+MItJLQt8VBf0t
jHrYb7s02PQG/Dgf3Zi1w3rSvJTFYO0r9KdDHgV1zKNFMEMwDgYDVR0PAQH/BAQD
AgEGMBIGA1UdEwEB/wQIMAYBAf8CAQEwHQYDVR0OBBYEFJ1ewuS52GGODA0tQLwd
gbanUtfgMAoGCCqGSM49BAMDA2cAMGQCMFlJuxUiFYKscjBoQ9dKk3z0EZlOUfIe
ApOeazjVhXUZu26xJq1KIWoUP3r4f37xDgIwE60OTarhuK5/7ceSLNw299PBCG6I
Rk1w665f64t0/wzmPBFnjOibJO9ApwZ9h5Ih
-----END CERTIFICATE-----
`;

const TEST_CLIENT_CERT = `-----BEGIN CERTIFICATE-----
MIIB1jCCAVugAwIBAgIRAP4zY93b05ChlT4drKSfH9IwCgYIKoZIzj0EAwMwPDEc
MBoGA1UEChMTYWktYWdlbnQtYnJpZGdlLWRldjEcMBoGA1UEAxMTYWktYWdlbnQt
YnJpZGdlLWRldjAeFw0yNjA0MTAxNjIxNTVaFw0yNjA3MDkxNjIxNTVaMBUxEzAR
BgNVBAMTCmRldi1jbGllbnQwdjAQBgcqhkjOPQIBBgUrgQQAIgNiAAQzJPCmYwrJ
FDIQD1KNRVQ0P8gZs1LK/uPE2BxTfMLypY9M7nrTqCoTnSzs86s7Qo7i8U0O6e9V
lDrDm6Asddz5Pcs2NEPnTiUv1kEYJ0OsfrGla9xW+Msr9vsb0zRaYbmjSDBGMA4G
A1UdDwEB/wQEAwIHgDATBgNVHSUEDDAKBggrBgEFBQcDAjAfBgNVHSMEGDAWgBSd
XsLkudhhjgwNLUC8HYG2p1LX4DAKBggqhkjOPQQDAwNpADBmAjEAmtTvhYmD7uWH
HXXXwXyLyvkEXEDMyfZaIDCFKyAv0EIWWnAQFtrcCDphG9c4arEuAjEAozIhJcpj
j6BkVYRga5geYziSEnVlQB1m8MHjL01VZJ38HvZDxL5kfsRAvyq+1Ei+
-----END CERTIFICATE-----
`;

const TEST_CLIENT_KEY = `-----BEGIN EC PRIVATE KEY-----
MIGkAgEBBDDpromoqM2dmaxoAQ0ilhDH997gVhVkzP7Y7eLtlqgm/SiO/1J14cc4
/OCDJ/VWNEagBwYFK4EEACKhZANiAAQzJPCmYwrJFDIQD1KNRVQ0P8gZs1LK/uPE
2BxTfMLypY9M7nrTqCoTnSzs86s7Qo7i8U0O6e9VlDrDm6Asddz5Pcs2NEPnTiUv
1kEYJ0OsfrGla9xW+Msr9vsb0zRaYbk=
-----END EC PRIVATE KEY-----
`;

const TEST_JWT_KEY = `-----BEGIN PRIVATE KEY-----
MC4CAQAwBQYDK2VwBCIEILUoSzUyrX2nZdXo1+TKHy6GmDSPno8Qh17TDoSMCZAW
-----END PRIVATE KEY-----
`;

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
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "chat-ts-creds-"));
  const cacert = path.join(tmp, "ca.crt");
  const cert = path.join(tmp, "client.crt");
  const key = path.join(tmp, "client.key");
  const jwtKey = path.join(tmp, "jwt.key");
  fs.writeFileSync(cacert, TEST_CA_CERT);
  fs.writeFileSync(cert, TEST_CLIENT_CERT);
  fs.writeFileSync(key, TEST_CLIENT_KEY);
  fs.writeFileSync(jwtKey, TEST_JWT_KEY);

  const creds = buildCredentials({
    cacert,
    cert,
    key,
    jwtKey,
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
