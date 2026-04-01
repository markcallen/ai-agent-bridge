/**
 * BridgeGrpcClient — Node.js gRPC wrapper for the ai-agent-bridge daemon.
 *
 * Loads the proto file dynamically at runtime (no code generation required).
 * Mirrors the Go bridgeclient API surface.
 */

import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";
import * as path from "path";
import {
  AttachEventType,
  BridgeClientOptions,
  Logger,
  ProtoAttachSessionEvent,
  ProtoGetSessionResponse,
  ProtoHealthResponse,
  ProtoListProvidersResponse,
  ProtoListSessionsResponse,
  ProtoResizeSessionResponse,
  ProtoStartSessionResponse,
  ProtoStopSessionResponse,
  ProtoWriteInputResponse,
  SessionInfo,
  SessionStatus,
} from "./types";

// Resolve the proto file regardless of where the package is installed.
//
// Strategy: walk up from __dirname looking for a directory that contains
// "proto/bridge/v1/bridge.proto".  The build script copies the monorepo
// proto/ tree into the package root so it travels with the installed package.
// In a monorepo source checkout the package root is two levels above the
// repo's proto/, so that is checked as a fallback.
function resolveProto(): { protoPath: string; includeDir: string } {
  const fs = require("fs") as typeof import("fs");
  let dir = __dirname;
  for (let i = 0; i < 10; i++) {
    const candidate = path.join(dir, "proto/bridge/v1/bridge.proto");
    if (fs.existsSync(candidate)) {
      return { protoPath: candidate, includeDir: path.join(dir, "proto") };
    }
    dir = path.dirname(dir);
  }
  throw new Error(
    "Cannot locate bridge.proto. " +
      "Run `npm run build` inside packages/bridge-client-node to bundle it."
  );
}

const { protoPath: PROTO_PATH, includeDir: PROTO_INCLUDE_DIR } = resolveProto();

const PROTO_OPTIONS: protoLoader.Options = {
  keepCase: true,
  longs: Number,
  enums: String,
  defaults: true,
  oneofs: true,
  includeDirs: [PROTO_INCLUDE_DIR],
};

// ---------------------------------------------------------------------------
// Proto enum conversion helpers
// ---------------------------------------------------------------------------

const ATTACH_EVENT_TYPE_MAP: Record<string, AttachEventType> = {
  ATTACH_EVENT_TYPE_UNSPECIFIED: "unspecified",
  ATTACH_EVENT_TYPE_ATTACHED: "attached",
  ATTACH_EVENT_TYPE_OUTPUT: "output",
  ATTACH_EVENT_TYPE_REPLAY_GAP: "replay_gap",
  ATTACH_EVENT_TYPE_SESSION_EXIT: "session_exit",
  ATTACH_EVENT_TYPE_ERROR: "error",
};

const ATTACH_EVENT_TYPE_BY_NUMBER: Record<number, AttachEventType> = {
  0: "unspecified",
  1: "attached",
  2: "output",
  3: "replay_gap",
  4: "session_exit",
  5: "error",
};

const SESSION_STATUS_MAP: Record<string, SessionStatus> = {
  SESSION_STATUS_UNSPECIFIED: "unspecified",
  SESSION_STATUS_STARTING: "starting",
  SESSION_STATUS_RUNNING: "running",
  SESSION_STATUS_ATTACHED: "attached",
  SESSION_STATUS_STOPPING: "stopping",
  SESSION_STATUS_STOPPED: "stopped",
  SESSION_STATUS_FAILED: "failed",
};

const SESSION_STATUS_BY_NUMBER: Record<number, SessionStatus> = {
  0: "unspecified",
  1: "starting",
  2: "running",
  3: "attached",
  4: "stopping",
  5: "stopped",
  6: "failed",
};

function toAttachEventType(raw: string | number): AttachEventType {
  if (typeof raw === "string") {
    return ATTACH_EVENT_TYPE_MAP[raw] ?? "unspecified";
  }
  return ATTACH_EVENT_TYPE_BY_NUMBER[raw] ?? "unspecified";
}

function toSessionStatus(raw: string | number): SessionStatus {
  if (typeof raw === "string") {
    return SESSION_STATUS_MAP[raw] ?? "unspecified";
  }
  return SESSION_STATUS_BY_NUMBER[raw] ?? "unspecified";
}

function toLong(v: number | { toNumber(): number }): number {
  return typeof v === "object" ? v.toNumber() : v;
}

function toTimestampString(ts?: {
  seconds: number | { toNumber(): number };
  nanos?: number;
}): string {
  if (!ts) return new Date(0).toISOString();
  const secs =
    typeof ts.seconds === "object" ? ts.seconds.toNumber() : ts.seconds;
  return new Date(secs * 1000).toISOString();
}

function toSessionInfo(r: ProtoGetSessionResponse): SessionInfo {
  return {
    sessionId: r.session_id,
    projectId: r.project_id,
    provider: r.provider,
    status: toSessionStatus(r.status),
    createdAt: toTimestampString(r.created_at as Parameters<typeof toTimestampString>[0]),
    stoppedAt: r.stopped_at
      ? toTimestampString(r.stopped_at as Parameters<typeof toTimestampString>[0])
      : undefined,
    error: r.error || undefined,
  };
}

// ---------------------------------------------------------------------------
// Parsed attach event type (returned from the async generator)
// ---------------------------------------------------------------------------

export interface AttachEvent {
  type: AttachEventType;
  seq: number;
  sessionId: string;
  /** Raw output bytes from the agent PTY/stdio */
  payload: Buffer;
  replay: boolean;
  oldestSeq: number;
  lastSeq: number;
  exitRecorded: boolean;
  exitCode: number;
  error: string;
  cols: number;
  rows: number;
  timestamp: string;
}

function toAttachEvent(raw: ProtoAttachSessionEvent): AttachEvent {
  return {
    type: toAttachEventType(raw.type),
    seq: toLong(raw.seq),
    sessionId: raw.session_id,
    payload: Buffer.isBuffer(raw.payload)
      ? raw.payload
      : Buffer.from(raw.payload ?? []),
    replay: raw.replay,
    oldestSeq: toLong(raw.oldest_seq),
    lastSeq: toLong(raw.last_seq),
    exitRecorded: raw.exit_recorded,
    exitCode: raw.exit_code,
    error: raw.error,
    cols: raw.cols,
    rows: raw.rows,
    timestamp: toTimestampString(raw.timestamp as Parameters<typeof toTimestampString>[0]),
  };
}

// ---------------------------------------------------------------------------
// gRPC service stub type
// ---------------------------------------------------------------------------

interface BridgeServiceStub {
  StartSession(
    req: object,
    metadata: grpc.Metadata,
    cb: grpc.requestCallback<ProtoStartSessionResponse>
  ): grpc.ClientUnaryCall;
  StopSession(
    req: object,
    metadata: grpc.Metadata,
    cb: grpc.requestCallback<ProtoStopSessionResponse>
  ): grpc.ClientUnaryCall;
  GetSession(
    req: object,
    metadata: grpc.Metadata,
    cb: grpc.requestCallback<ProtoGetSessionResponse>
  ): grpc.ClientUnaryCall;
  ListSessions(
    req: object,
    metadata: grpc.Metadata,
    cb: grpc.requestCallback<ProtoListSessionsResponse>
  ): grpc.ClientUnaryCall;
  AttachSession(
    req: object,
    metadata: grpc.Metadata
  ): grpc.ClientReadableStream<ProtoAttachSessionEvent>;
  WriteInput(
    req: object,
    metadata: grpc.Metadata,
    cb: grpc.requestCallback<ProtoWriteInputResponse>
  ): grpc.ClientUnaryCall;
  ResizeSession(
    req: object,
    metadata: grpc.Metadata,
    cb: grpc.requestCallback<ProtoResizeSessionResponse>
  ): grpc.ClientUnaryCall;
  Health(
    req: object,
    metadata: grpc.Metadata,
    cb: grpc.requestCallback<ProtoHealthResponse>
  ): grpc.ClientUnaryCall;
  ListProviders(
    req: object,
    metadata: grpc.Metadata,
    cb: grpc.requestCallback<ProtoListProvidersResponse>
  ): grpc.ClientUnaryCall;
}

// ---------------------------------------------------------------------------
// BridgeGrpcClient
// ---------------------------------------------------------------------------

export interface StartSessionResult {
  sessionId: string;
  status: SessionStatus;
  createdAt: string;
}

export interface StopSessionResult {
  status: SessionStatus;
}

export interface WriteInputResult {
  accepted: boolean;
  bytesWritten: number;
}

export interface ResizeSessionResult {
  applied: boolean;
}

export interface HealthResult {
  status: string;
  providers: Array<{ provider: string; available: boolean; error: string }>;
}

export interface ProviderInfoResult {
  provider: string;
  available: boolean;
  binary: string;
  version: string;
}

export class BridgeGrpcClient {
  private readonly stub: BridgeServiceStub;
  private readonly metadata: grpc.Metadata;
  private readonly logger: Logger;

  constructor(options: BridgeClientOptions) {
    const { bridgeAddr, credentials, metadata, channelOptions, logger } = options;
    this.logger = logger ?? {
      info: (msg, ...a) => console.info(msg, ...a),
      warn: (msg, ...a) => console.warn(msg, ...a),
      error: (msg, ...a) => console.error(msg, ...a),
      debug: (msg, ...a) => console.debug(msg, ...a),
    };

    const packageDef = protoLoader.loadSync(PROTO_PATH, PROTO_OPTIONS);
    const grpcObject = grpc.loadPackageDefinition(packageDef);

    // Navigate: grpcObject.bridge.v1.BridgeService
    const bridgePkg = grpcObject["bridge"] as Record<string, unknown>;
    const v1Pkg = bridgePkg["v1"] as Record<string, unknown>;
    const ServiceCtor = v1Pkg["BridgeService"] as typeof grpc.Client;

    const creds =
      (credentials as grpc.ChannelCredentials) ??
      grpc.credentials.createInsecure();

    this.stub = new ServiceCtor(
      bridgeAddr,
      creds,
      channelOptions ?? {}
    ) as unknown as BridgeServiceStub;

    this.metadata = new grpc.Metadata();
    if (metadata) {
      for (const [k, v] of Object.entries(metadata)) {
        this.metadata.set(k, v);
      }
    }
  }

  /** Close the underlying gRPC channel. */
  close(): void {
    this.logger.debug("Closing gRPC channel");
    (this.stub as unknown as grpc.Client).close();
  }

  // ---------------------------------------------------------------------------
  // Unary RPC helpers
  // ---------------------------------------------------------------------------

  private unary<TReq, TResp>(
    method: (
      req: TReq,
      metadata: grpc.Metadata,
      cb: grpc.requestCallback<TResp>
    ) => grpc.ClientUnaryCall,
    req: TReq
  ): Promise<TResp> {
    return new Promise((resolve, reject) => {
      method.call(this.stub, req, this.metadata, (err: grpc.ServiceError | null, resp: TResp | undefined) => {
        if (err) return reject(err);
        resolve(resp!);
      });
    });
  }

  // ---------------------------------------------------------------------------
  // Session lifecycle
  // ---------------------------------------------------------------------------

  async startSession(opts: {
    projectId: string;
    sessionId?: string;
    repoPath: string;
    provider: string;
    agentOpts?: Record<string, string>;
    initialCols?: number;
    initialRows?: number;
  }): Promise<StartSessionResult> {
    const resp = await this.unary<object, ProtoStartSessionResponse>(
      this.stub.StartSession,
      {
        project_id: opts.projectId,
        session_id: opts.sessionId ?? "",
        repo_path: opts.repoPath,
        provider: opts.provider,
        agent_opts: opts.agentOpts ?? {},
        initial_cols: opts.initialCols ?? 0,
        initial_rows: opts.initialRows ?? 0,
      }
    );
    return {
      sessionId: resp.session_id,
      status: toSessionStatus(resp.status),
      createdAt: toTimestampString(resp.created_at as Parameters<typeof toTimestampString>[0]),
    };
  }

  async stopSession(opts: {
    sessionId: string;
    force?: boolean;
  }): Promise<StopSessionResult> {
    const resp = await this.unary<object, ProtoStopSessionResponse>(
      this.stub.StopSession,
      {
        session_id: opts.sessionId,
        force: opts.force ?? false,
      }
    );
    return { status: toSessionStatus(resp.status) };
  }

  async getSession(sessionId: string): Promise<SessionInfo> {
    const resp = await this.unary<object, ProtoGetSessionResponse>(
      this.stub.GetSession,
      { session_id: sessionId }
    );
    return toSessionInfo(resp);
  }

  async listSessions(projectId?: string): Promise<SessionInfo[]> {
    const resp = await this.unary<object, ProtoListSessionsResponse>(
      this.stub.ListSessions,
      { project_id: projectId ?? "" }
    );
    return (resp.sessions ?? []).map(toSessionInfo);
  }

  // ---------------------------------------------------------------------------
  // Attach — server-streaming
  // ---------------------------------------------------------------------------

  /**
   * Attach to a running session and stream raw output events as an async
   * generator.  The `clientId` is used for subsequent `writeInput` and
   * `resizeSession` calls — generate a UUID before calling and reuse it.
   *
   * The generator yields `AttachEvent` objects until the stream ends or the
   * AbortSignal fires.
   *
   * @example
   * const clientId = crypto.randomUUID();
   * const ac = new AbortController();
   * for await (const ev of client.attachSession({ sessionId, clientId, signal: ac.signal })) {
   *   if (ev.type === "output") process.stdout.write(ev.payload);
   * }
   */
  async *attachSession(opts: {
    sessionId: string;
    clientId: string;
    afterSeq?: number;
    signal?: AbortSignal;
  }): AsyncGenerator<AttachEvent> {
    const stream = this.stub.AttachSession(
      {
        session_id: opts.sessionId,
        client_id: opts.clientId,
        after_seq: opts.afterSeq ?? 0,
      },
      this.metadata
    );

    const abort = () => stream.destroy();
    opts.signal?.addEventListener("abort", abort);

    try {
      for await (const raw of stream) {
        yield toAttachEvent(raw as ProtoAttachSessionEvent);
      }
    } finally {
      opts.signal?.removeEventListener("abort", abort);
      stream.destroy();
    }
  }

  // ---------------------------------------------------------------------------
  // Input and resize
  // ---------------------------------------------------------------------------

  /**
   * Send raw input bytes to the session PTY.
   * Accepts a `Buffer` or a `string` (which is UTF-8 encoded to bytes).
   * For interactive PTY sessions pass `\r` (not `\n`) to send Enter.
   */
  async writeInput(opts: {
    sessionId: string;
    clientId: string;
    data: Buffer | string;
  }): Promise<WriteInputResult> {
    const data =
      typeof opts.data === "string"
        ? Buffer.from(opts.data, "utf8")
        : opts.data;
    const resp = await this.unary<object, ProtoWriteInputResponse>(
      this.stub.WriteInput,
      {
        session_id: opts.sessionId,
        client_id: opts.clientId,
        data,
      }
    );
    return { accepted: resp.accepted, bytesWritten: resp.bytes_written };
  }

  /** Resize the session PTY to the given dimensions. */
  async resizeSession(opts: {
    sessionId: string;
    clientId: string;
    cols: number;
    rows: number;
  }): Promise<ResizeSessionResult> {
    const resp = await this.unary<object, ProtoResizeSessionResponse>(
      this.stub.ResizeSession,
      {
        session_id: opts.sessionId,
        client_id: opts.clientId,
        cols: opts.cols,
        rows: opts.rows,
      }
    );
    return { applied: resp.applied };
  }

  // ---------------------------------------------------------------------------
  // Health and discovery
  // ---------------------------------------------------------------------------

  async health(): Promise<HealthResult> {
    const resp = await this.unary<object, ProtoHealthResponse>(
      this.stub.Health,
      {}
    );
    return {
      status: resp.status,
      providers: (resp.providers ?? []).map((p) => ({
        provider: p.provider,
        available: p.available,
        error: p.error,
      })),
    };
  }

  async listProviders(): Promise<ProviderInfoResult[]> {
    const resp = await this.unary<object, ProtoListProvidersResponse>(
      this.stub.ListProviders,
      {}
    );
    return (resp.providers ?? []).map((p) => ({
      provider: p.provider,
      available: p.available,
      binary: p.binary,
      version: p.version,
    }));
  }
}
