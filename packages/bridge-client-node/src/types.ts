/**
 * WebSocket JSON protocol types for the ai-agent-bridge bridge client.
 *
 * The same protocol is used by both the Node.js and Go WebSocket integrations.
 * All messages are JSON-encoded and exchanged over a WebSocket connection.
 */

// ---------------------------------------------------------------------------
// Enums (mirror proto AttachEventType and SessionStatus)
// ---------------------------------------------------------------------------

export type AttachEventType =
  | "unspecified"
  | "attached"
  | "output"
  | "replay_gap"
  | "session_exit"
  | "error";

export type SessionStatus =
  | "unspecified"
  | "starting"
  | "running"
  | "attached"
  | "stopping"
  | "stopped"
  | "failed";

// ---------------------------------------------------------------------------
// Shared sub-types
// ---------------------------------------------------------------------------

export interface SessionInfo {
  sessionId: string;
  projectId: string;
  provider: string;
  status: SessionStatus;
  createdAt: string; // ISO 8601
  stoppedAt?: string; // ISO 8601, if stopped
  error?: string;
}

export interface ProviderInfo {
  provider: string;
  available: boolean;
  binary: string;
  version: string;
}

export interface ProviderHealth {
  provider: string;
  available: boolean;
  error?: string;
}

// ---------------------------------------------------------------------------
// Client → Server messages
// ---------------------------------------------------------------------------

export interface StartSessionMsg {
  type: "start_session";
  projectId: string;
  sessionId?: string; // optional; server generates one if omitted
  repoPath: string;
  provider: string;
  agentOpts?: Record<string, string>;
  initialCols?: number;
  initialRows?: number;
}

/** Send text input to a running session. Text is UTF-8 encoded to bytes. */
export interface SendInputMsg {
  type: "send_input";
  sessionId: string;
  clientId: string;
  text: string;
}

export interface StopSessionMsg {
  type: "stop_session";
  sessionId: string;
  force?: boolean;
}

export interface AttachSessionMsg {
  type: "attach_session";
  sessionId: string;
  clientId: string;
  afterSeq?: number;
}

export interface ResizeSessionMsg {
  type: "resize_session";
  sessionId: string;
  clientId: string;
  cols: number;
  rows: number;
}

export interface ListSessionsMsg {
  type: "list_sessions";
  projectId?: string;
}

export interface GetSessionMsg {
  type: "get_session";
  sessionId: string;
}

export interface HealthMsg {
  type: "health";
}

export interface ListProvidersMsg {
  type: "list_providers";
}

export type ClientMessage =
  | StartSessionMsg
  | SendInputMsg
  | StopSessionMsg
  | AttachSessionMsg
  | ResizeSessionMsg
  | ListSessionsMsg
  | GetSessionMsg
  | HealthMsg
  | ListProvidersMsg;

// ---------------------------------------------------------------------------
// Server → Client messages
// ---------------------------------------------------------------------------

export interface SessionStartedMsg {
  type: "session_started";
  sessionId: string;
  status: SessionStatus;
  createdAt: string;
}

/** Output event from an attached session. payload is base64-encoded bytes. */
export interface AttachEventMsg {
  type: "attach_event";
  seq: number;
  sessionId: string;
  eventType: AttachEventType;
  payloadB64: string; // base64-encoded raw bytes
  replay: boolean;
  oldestSeq: number;
  lastSeq: number;
  exitRecorded: boolean;
  exitCode: number;
  error: string;
  cols: number;
  rows: number;
}

export interface InputAcceptedMsg {
  type: "input_accepted";
  accepted: boolean;
  bytesWritten: number;
}

export interface SessionStoppedMsg {
  type: "session_stopped";
  sessionId: string;
  status: SessionStatus;
}

export interface SessionsListMsg {
  type: "sessions_list";
  sessions: SessionInfo[];
}

export interface SessionInfoMsg {
  type: "session_info";
  session: SessionInfo;
}

export interface HealthResponseMsg {
  type: "health_response";
  status: string;
  providers: ProviderHealth[];
}

export interface ProvidersListMsg {
  type: "providers_list";
  providers: ProviderInfo[];
}

export interface ErrorMsg {
  type: "error";
  code: string;
  message: string;
}

export type ServerMessage =
  | SessionStartedMsg
  | AttachEventMsg
  | InputAcceptedMsg
  | SessionStoppedMsg
  | SessionsListMsg
  | SessionInfoMsg
  | HealthResponseMsg
  | ProvidersListMsg
  | ErrorMsg;

// ---------------------------------------------------------------------------
// gRPC proto mirror types (used internally by grpc-client.ts)
// ---------------------------------------------------------------------------

/** Raw proto field names as returned by @grpc/proto-loader */
export interface ProtoAttachSessionEvent {
  type: string | number; // AttachEventType enum value
  seq: number | Long;
  timestamp?: { seconds: number | Long; nanos: number };
  session_id: string;
  payload: Buffer | Uint8Array;
  replay: boolean;
  oldest_seq: number | Long;
  last_seq: number | Long;
  exit_recorded: boolean;
  exit_code: number;
  error: string;
  cols: number;
  rows: number;
}

export interface ProtoStartSessionResponse {
  session_id: string;
  status: number | string; // SessionStatus enum value
  created_at?: { seconds: number | Long; nanos: number };
}

export interface ProtoStopSessionResponse {
  status: number | string;
}

export interface ProtoGetSessionResponse {
  session_id: string;
  project_id: string;
  provider: string;
  status: number | string;
  created_at?: { seconds: number | Long; nanos: number };
  stopped_at?: { seconds: number | Long; nanos: number };
  error: string;
  attached: boolean;
  attached_client_id: string;
  exit_recorded: boolean;
  exit_code: number;
  oldest_seq: number | Long;
  last_seq: number | Long;
  cols: number;
  rows: number;
}

export interface ProtoListSessionsResponse {
  sessions: ProtoGetSessionResponse[];
}

export interface ProtoWriteInputResponse {
  accepted: boolean;
  bytes_written: number;
}

export interface ProtoResizeSessionResponse {
  applied: boolean;
}

export interface ProtoHealthResponse {
  status: string;
  providers: Array<{ provider: string; available: boolean; error: string }>;
}

export interface ProtoListProvidersResponse {
  providers: Array<{
    provider: string;
    available: boolean;
    binary: string;
    version: string;
  }>;
}

/** @grpc/proto-loader Long type placeholder */
export interface Long {
  toNumber(): number;
  toString(): string;
  low: number;
  high: number;
  unsigned: boolean;
}

// ---------------------------------------------------------------------------
// Options types for the Node.js API
// ---------------------------------------------------------------------------

export interface BridgeClientOptions {
  /** gRPC target address, e.g. "localhost:9445" */
  bridgeAddr: string;
  /** Optional gRPC channel credentials (default: insecure) */
  credentials?: object;
  /** Optional static gRPC metadata key/value pairs (e.g. for bearer tokens) */
  metadata?: Record<string, string>;
  /**
   * Optional gRPC channel options passed directly to the gRPC client.
   * Useful for e.g. overriding the TLS server name when connecting via IP:
   *   { "grpc.ssl_target_name_override": "bridge.local" }
   */
  channelOptions?: Record<string, string | number>;
  /** Logger interface (defaults to console) */
  logger?: Logger;
}

export interface Logger {
  info(msg: string, ...args: unknown[]): void;
  warn(msg: string, ...args: unknown[]): void;
  error(msg: string, ...args: unknown[]): void;
  debug(msg: string, ...args: unknown[]): void;
}
