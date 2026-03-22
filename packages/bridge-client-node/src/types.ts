/**
 * WebSocket JSON protocol types for the ai-agent-bridge bridge client.
 *
 * The same protocol is used by both the Node.js and Go WebSocket integrations.
 * All messages are JSON-encoded and exchanged over a WebSocket connection.
 */

// ---------------------------------------------------------------------------
// Enums (mirror proto EventType and SessionStatus)
// ---------------------------------------------------------------------------

export type EventType =
  | "session_started"
  | "session_stopped"
  | "session_failed"
  | "stdout"
  | "stderr"
  | "input_received"
  | "buffer_overflow"
  | "agent_ready"
  | "response_complete"
  | "unspecified";

export type SessionStatus =
  | "unspecified"
  | "starting"
  | "running"
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
}

export interface SendInputMsg {
  type: "send_input";
  sessionId: string;
  text: string;
  idempotencyKey?: string;
}

export interface StopSessionMsg {
  type: "stop_session";
  sessionId: string;
  force?: boolean;
}

export interface StreamEventsMsg {
  type: "stream_events";
  sessionId: string;
  afterSeq?: number;
  subscriberId?: string;
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
  | StreamEventsMsg
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

export interface EventMsg {
  type: "event";
  seq: number;
  sessionId: string;
  eventType: EventType;
  stream: string;
  text: string;
  done: boolean;
  error: string;
}

export interface InputAcceptedMsg {
  type: "input_accepted";
  accepted: boolean;
  seq: number;
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
  | EventMsg
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
export interface ProtoSessionEvent {
  seq: number | Long;
  timestamp?: { seconds: number | Long; nanos: number };
  session_id: string;
  project_id: string;
  provider: string;
  type: number; // EventType enum value
  stream: string;
  text: string;
  done: boolean;
  error: string;
}

export interface ProtoStartSessionResponse {
  session_id: string;
  status: number; // SessionStatus enum value
  created_at?: { seconds: number | Long; nanos: number };
}

export interface ProtoStopSessionResponse {
  status: number;
}

export interface ProtoGetSessionResponse {
  session_id: string;
  project_id: string;
  provider: string;
  status: number;
  created_at?: { seconds: number | Long; nanos: number };
  stopped_at?: { seconds: number | Long; nanos: number };
  error: string;
}

export interface ProtoListSessionsResponse {
  sessions: ProtoGetSessionResponse[];
}

export interface ProtoSendInputResponse {
  accepted: boolean;
  seq: number | Long;
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
  /** gRPC target address, e.g. "localhost:50051" */
  bridgeAddr: string;
  /** Optional gRPC channel credentials (default: insecure) */
  credentials?: object;
  /** Optional static gRPC metadata key/value pairs (e.g. for bearer tokens) */
  metadata?: Record<string, string>;
  /** Logger interface (defaults to console) */
  logger?: Logger;
}

export interface Logger {
  info(msg: string, ...args: unknown[]): void;
  warn(msg: string, ...args: unknown[]): void;
  error(msg: string, ...args: unknown[]): void;
  debug(msg: string, ...args: unknown[]): void;
}
