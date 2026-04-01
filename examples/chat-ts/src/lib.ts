import * as fs from "fs";
import * as grpc from "@grpc/grpc-js";

export interface ParsedArgs {
  target: string;
  project: string;
  provider: string;
  cacert: string;
  cert: string;
  key: string;
  repoPath: string;
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
    repoPath: positional[0],
  };
}

export function buildCredentials(
  cacert: string,
  cert: string,
  key: string
): grpc.ChannelCredentials {
  if (cacert && cert && key) {
    return grpc.credentials.createSsl(
      fs.readFileSync(cacert),
      fs.readFileSync(key),
      fs.readFileSync(cert)
    );
  }
  return grpc.credentials.createInsecure();
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
