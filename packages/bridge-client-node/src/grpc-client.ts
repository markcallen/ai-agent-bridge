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
  BridgeClientOptions,
  EventType,
  Logger,
  ProtoGetSessionResponse,
  ProtoHealthResponse,
  ProtoListProvidersResponse,
  ProtoListSessionsResponse,
  ProtoSendInputResponse,
  ProtoSessionEvent,
  ProtoStartSessionResponse,
  ProtoStopSessionResponse,
  SessionInfo,
  SessionStatus,
} from "./types";

// Resolve the proto file relative to the package root regardless of whether
// we are running from src/ (ts-node / tsx) or dist/src/ (compiled output).
// Walk up from __dirname until we find a directory containing package.json,
// then resolve the proto path relative to the repo root (two levels up).
function resolveProtoPath(): string {
  let dir = __dirname;
  for (let i = 0; i < 5; i++) {
    if (require("fs").existsSync(path.join(dir, "package.json"))) {
      // dir is the package root (packages/bridge-client-node)
      return path.resolve(dir, "../../proto/bridge/v1/bridge.proto");
    }
    dir = path.dirname(dir);
  }
  // Fallback: assume we're in src/ at dev time
  return path.resolve(__dirname, "../../../proto/bridge/v1/bridge.proto");
}

const PROTO_PATH = resolveProtoPath();

const PROTO_OPTIONS: protoLoader.Options = {
  keepCase: true,
  longs: Number,
  enums: String,
  defaults: true,
  oneofs: true,
  includeDirs: [path.resolve(__dirname, "../../../")],
};

// ---------------------------------------------------------------------------
// Proto enum conversion helpers
// ---------------------------------------------------------------------------

const EVENT_TYPE_MAP: Record<string, EventType> = {
  EVENT_TYPE_UNSPECIFIED: "unspecified",
  EVENT_TYPE_SESSION_STARTED: "session_started",
  EVENT_TYPE_SESSION_STOPPED: "session_stopped",
  EVENT_TYPE_SESSION_FAILED: "session_failed",
  EVENT_TYPE_STDOUT: "stdout",
  EVENT_TYPE_STDERR: "stderr",
  EVENT_TYPE_INPUT_RECEIVED: "input_received",
  EVENT_TYPE_BUFFER_OVERFLOW: "buffer_overflow",
  EVENT_TYPE_AGENT_READY: "agent_ready",
  EVENT_TYPE_RESPONSE_COMPLETE: "response_complete",
};

const SESSION_STATUS_MAP: Record<string, SessionStatus> = {
  SESSION_STATUS_UNSPECIFIED: "unspecified",
  SESSION_STATUS_STARTING: "starting",
  SESSION_STATUS_RUNNING: "running",
  SESSION_STATUS_STOPPING: "stopping",
  SESSION_STATUS_STOPPED: "stopped",
  SESSION_STATUS_FAILED: "failed",
};

function toEventType(raw: string | number): EventType {
  if (typeof raw === "string") {
    return EVENT_TYPE_MAP[raw] ?? "unspecified";
  }
  const name = Object.keys(EVENT_TYPE_MAP)[raw];
  return name ? EVENT_TYPE_MAP[name] : "unspecified";
}

function toSessionStatus(raw: string | number): SessionStatus {
  if (typeof raw === "string") {
    return SESSION_STATUS_MAP[raw] ?? "unspecified";
  }
  const name = Object.keys(SESSION_STATUS_MAP)[raw];
  return name ? SESSION_STATUS_MAP[name] : "unspecified";
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
// Parsed event type (returned from the async generator)
// ---------------------------------------------------------------------------

export interface SessionEvent {
  seq: number;
  sessionId: string;
  projectId: string;
  provider: string;
  eventType: EventType;
  stream: string;
  text: string;
  done: boolean;
  error: string;
  timestamp: string;
}

function toSessionEvent(raw: ProtoSessionEvent): SessionEvent {
  return {
    seq:
      typeof raw.seq === "object" && raw.seq !== null && "toNumber" in raw.seq
        ? (raw.seq as { toNumber(): number }).toNumber()
        : (raw.seq as number),
    sessionId: raw.session_id,
    projectId: raw.project_id,
    provider: raw.provider,
    eventType: toEventType(raw.type),
    stream: raw.stream,
    text: raw.text,
    done: raw.done,
    error: raw.error,
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
  SendInput(
    req: object,
    metadata: grpc.Metadata,
    cb: grpc.requestCallback<ProtoSendInputResponse>
  ): grpc.ClientUnaryCall;
  StreamEvents(
    req: object,
    metadata: grpc.Metadata
  ): grpc.ClientReadableStream<ProtoSessionEvent>;
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

export interface SendInputResult {
  accepted: boolean;
  seq: number;
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
    const { bridgeAddr, credentials, metadata, logger } = options;
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
      creds
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
  }): Promise<StartSessionResult> {
    const resp = await this.unary<object, ProtoStartSessionResponse>(
      this.stub.StartSession,
      {
        project_id: opts.projectId,
        session_id: opts.sessionId ?? "",
        repo_path: opts.repoPath,
        provider: opts.provider,
        agent_opts: opts.agentOpts ?? {},
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
  // Input
  // ---------------------------------------------------------------------------

  async sendInput(opts: {
    sessionId: string;
    text: string;
    idempotencyKey?: string;
  }): Promise<SendInputResult> {
    const resp = await this.unary<object, ProtoSendInputResponse>(
      this.stub.SendInput,
      {
        session_id: opts.sessionId,
        text: opts.text,
        idempotency_key: opts.idempotencyKey ?? "",
      }
    );
    const seq =
      typeof resp.seq === "object" && resp.seq !== null && "toNumber" in resp.seq
        ? (resp.seq as { toNumber(): number }).toNumber()
        : (resp.seq as number);
    return { accepted: resp.accepted, seq };
  }

  // ---------------------------------------------------------------------------
  // Event streaming — async generator
  // ---------------------------------------------------------------------------

  /**
   * Stream events for a session as an async generator.
   *
   * The generator yields `SessionEvent` objects until the stream ends or the
   * AbortSignal fires. Callers should wrap in a try/finally to cancel.
   *
   * @example
   * const ac = new AbortController();
   * for await (const ev of client.streamEvents({ sessionId, signal: ac.signal })) {
   *   console.log(ev);
   * }
   */
  async *streamEvents(opts: {
    sessionId: string;
    afterSeq?: number;
    subscriberId?: string;
    signal?: AbortSignal;
  }): AsyncGenerator<SessionEvent> {
    const stream = this.stub.StreamEvents(
      {
        session_id: opts.sessionId,
        after_seq: opts.afterSeq ?? 0,
        subscriber_id: opts.subscriberId ?? "",
      },
      this.metadata
    );

    const abort = () => stream.destroy();
    opts.signal?.addEventListener("abort", abort);

    try {
      for await (const raw of stream) {
        yield toSessionEvent(raw as ProtoSessionEvent);
      }
    } finally {
      opts.signal?.removeEventListener("abort", abort);
      stream.destroy();
    }
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
