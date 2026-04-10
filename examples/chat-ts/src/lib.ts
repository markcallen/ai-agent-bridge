import * as fs from "fs";
import * as crypto from "crypto";
import * as grpc from "@grpc/grpc-js";

export interface ParsedArgs {
  target: string;
  project: string;
  provider: string;
  cacert: string;
  cert: string;
  key: string;
  jwtKey: string;
  jwtIssuer: string;
  jwtAudience: string;
  repoPath: string;
}

export interface CredentialsOptions {
  cacert: string;
  cert: string;
  key: string;
  jwtKey: string;
  jwtIssuer: string;
  jwtAudience: string;
  project: string;
}

export function parseArgs(argv: string[]): ParsedArgs {
  const args = argv.slice(2);
  const flags: Record<string, string> = {};
  const positional: string[] = [];

  for (let i = 0; i < args.length; i++) {
    if (args[i].startsWith("--")) {
      const key = args[i].slice(2);
      flags[key] = args[++i] ?? "";
    } else {
      positional.push(args[i]);
    }
  }

  if (positional.length < 1) {
    throw new Error("usage: chat-ts [options] <repo-path>");
  }

  return {
    target: flags["target"] ?? "127.0.0.1:9445",
    project: flags["project"] ?? "dev",
    provider: flags["provider"] ?? "claude",
    cacert: flags["cacert"] ?? "",
    cert: flags["cert"] ?? "",
    key: flags["key"] ?? "",
    jwtKey: flags["jwt-key"] ?? "",
    jwtIssuer: flags["jwt-issuer"] ?? "dev",
    jwtAudience: flags["jwt-audience"] ?? "bridge",
    repoPath: positional[0],
  };
}

function mintJwtBearerToken(opts: {
  jwtKey: string;
  jwtIssuer: string;
  jwtAudience: string;
  project: string;
}): string {
  const privateKey = crypto.createPrivateKey(fs.readFileSync(opts.jwtKey));
  const now = Math.floor(Date.now() / 1000);
  const ttlSeconds = 300;
  const header = Buffer.from(
    JSON.stringify({ alg: "EdDSA", typ: "JWT" })
  ).toString("base64url");
  const payload = Buffer.from(
    JSON.stringify({
      iss: opts.jwtIssuer,
      sub: opts.jwtIssuer,
      aud: [opts.jwtAudience],
      iat: now,
      exp: now + ttlSeconds,
      project_id: opts.project,
    })
  ).toString("base64url");
  const signingInput = `${header}.${payload}`;
  const sig = crypto.sign(null, Buffer.from(signingInput), privateKey);
  return `${signingInput}.${sig.toString("base64url")}`;
}

export function buildCredentials(
  opts: CredentialsOptions
): grpc.ChannelCredentials {
  const transportCreds =
    opts.cacert && opts.cert && opts.key
      ? grpc.credentials.createSsl(
          fs.readFileSync(opts.cacert),
          fs.readFileSync(opts.key),
          fs.readFileSync(opts.cert)
        )
      : grpc.credentials.createInsecure();

  if (!opts.jwtKey) {
    return transportCreds;
  }

  if (!opts.cacert || !opts.cert || !opts.key) {
    throw new Error("JWT auth requires mTLS transport credentials");
  }

  const callCreds = grpc.credentials.createFromMetadataGenerator(
    (_params, callback) => {
      try {
        const token = mintJwtBearerToken(opts);
        const metadata = new grpc.Metadata();
        metadata.add("authorization", `Bearer ${token}`);
        callback(null, metadata);
      } catch (error) {
        callback(error as Error);
      }
    }
  );

  return grpc.credentials.combineChannelCredentials(transportCreds, callCreds);
}

export function currentTTYSize(): { cols: number; rows: number } {
  return {
    cols: process.stdout.columns ?? 120,
    rows: process.stdout.rows ?? 40,
  };
}

export function normalizeTTYInput(data: Buffer): Buffer {
  let hasNewline = false;
  for (let i = 0; i < data.length; i++) {
    if (data[i] === 0x0a) {
      hasNewline = true;
      break;
    }
  }
  if (!hasNewline) return data;
  const out = Buffer.allocUnsafe(data.length);
  for (let i = 0; i < data.length; i++) {
    out[i] = data[i] === 0x0a ? 0x0d : data[i];
  }
  return out;
}
