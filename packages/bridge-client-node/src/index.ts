/**
 * @ai-agent-bridge/client-node — main entry point
 *
 * Node.js bridge client: gRPC → WebSocket adapter for the ai-agent-bridge daemon.
 */

export { BridgeGrpcClient } from "./grpc-client";
export type {
  AttachEvent,
  StartSessionResult,
  StopSessionResult,
  WriteInputResult,
  ResizeSessionResult,
  HealthResult,
  ProviderInfoResult,
} from "./grpc-client";

export { createBridgeWebSocketHandler } from "./websocket-handler";
export type { BridgeWebSocketHandlerOptions } from "./websocket-handler";

export { createNextJsBridgeRoute, customServerSnippet } from "./nextjs";

export type {
  // Protocol messages — Client → Server
  ClientMessage,
  StartSessionMsg,
  SendInputMsg,
  StopSessionMsg,
  AttachSessionMsg,
  ResizeSessionMsg,
  ListSessionsMsg,
  GetSessionMsg,
  HealthMsg,
  ListProvidersMsg,
  // Protocol messages — Server → Client
  ServerMessage,
  SessionStartedMsg,
  AttachEventMsg,
  InputAcceptedMsg,
  SessionStoppedMsg,
  SessionsListMsg,
  SessionInfoMsg,
  HealthResponseMsg,
  ProvidersListMsg,
  ErrorMsg,
  // Shared types
  SessionInfo,
  ProviderInfo,
  ProviderHealth,
  AttachEventType,
  SessionStatus,
  BridgeClientOptions,
  Logger,
} from "./types";
