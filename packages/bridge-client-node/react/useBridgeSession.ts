/**
 * useBridgeSession — React hook for the ai-agent-bridge WebSocket protocol.
 *
 * Manages a WebSocket connection to the bridge WebSocket adapter (Node.js or Go)
 * and exposes methods for session lifecycle and event streaming.
 *
 * @example
 * function App() {
 *   const bridge = useBridgeSession("ws://localhost:3000/api/bridge");
 *   const clientId = useRef(crypto.randomUUID());
 *
 *   const start = () => {
 *     bridge.startSession({
 *       projectId: "my-project",
 *       repoPath: "/repos/my-app",
 *       provider: "claude",
 *     });
 *   };
 *
 *   // After session starts, attach to receive output:
 *   useEffect(() => {
 *     if (bridge.sessionId) {
 *       bridge.attachSession({ sessionId: bridge.sessionId, clientId: clientId.current });
 *     }
 *   }, [bridge.sessionId]);
 *
 *   return (
 *     <div>
 *       <p>Status: {bridge.status}</p>
 *       {bridge.events.map((ev, i) => <pre key={i}>{ev.text}</pre>)}
 *       <button onClick={start}>Start</button>
 *       <button onClick={() => bridge.sendInput({ sessionId: bridge.sessionId!, clientId: clientId.current, text: "hello\r" })}>
 *         Send
 *       </button>
 *     </div>
 *   );
 * }
 */

import { useCallback, useEffect, useRef, useState } from "react";
import type {
  AttachEventType,
  ClientMessage,
  ListSessionsMsg,
  ServerMessage,
  SessionInfo,
  SessionStatus,
} from "../src/types";

// ---------------------------------------------------------------------------
// Types exposed by the hook
// ---------------------------------------------------------------------------

export type ConnectionStatus =
  | "connecting"
  | "connected"
  | "disconnected"
  | "error";

export interface BridgeEvent {
  seq: number;
  sessionId: string;
  eventType: AttachEventType;
  /** Decoded UTF-8 text from the payload bytes */
  text: string;
  /** Raw base64-encoded payload */
  payloadB64: string;
  replay: boolean;
  oldestSeq: number;
  lastSeq: number;
  exitRecorded: boolean;
  exitCode: number;
  error: string;
}

export interface UseBridgeSessionOptions {
  /**
   * Automatically reconnect on disconnect.  Defaults to `true`.
   */
  autoReconnect?: boolean;
  /**
   * Initial reconnect delay in milliseconds.  Defaults to 1000.
   */
  reconnectDelayMs?: number;
  /**
   * Maximum reconnect delay in milliseconds.  Defaults to 30_000.
   */
  maxReconnectDelayMs?: number;
  /**
   * Called with every server message received.
   */
  onMessage?: (msg: ServerMessage) => void;
  /**
   * Called when the connection status changes.
   */
  onStatusChange?: (status: ConnectionStatus) => void;
}

export interface UseBridgeSessionReturn {
  /** Current WebSocket connection status */
  status: ConnectionStatus;
  /** Last error message, if any */
  error: string | null;
  /** The most recently started session ID */
  sessionId: string | null;
  /** Accumulated output events received via attach_session */
  events: BridgeEvent[];
  /** Start a new agent session */
  startSession(opts: {
    projectId: string;
    sessionId?: string;
    repoPath: string;
    provider: string;
    agentOpts?: Record<string, string>;
    initialCols?: number;
    initialRows?: number;
  }): void;
  /** Send text input to a running session (clientId must match the attach call) */
  sendInput(opts: { sessionId: string; clientId: string; text: string }): void;
  /** Stop a session */
  stopSession(opts: { sessionId: string; force?: boolean }): void;
  /** Attach to a session to start receiving output events */
  attachSession(opts: {
    sessionId: string;
    clientId: string;
    afterSeq?: number;
  }): void;
  /** Send a resize notification */
  resizeSession(opts: {
    sessionId: string;
    clientId: string;
    cols: number;
    rows: number;
  }): void;
  /** List sessions, optionally filtered by project */
  listSessions(opts?: { projectId?: string }): void;
  /** Get info about a specific session */
  getSession(sessionId: string): void;
  /** Check bridge health */
  health(): void;
  /** List available providers */
  listProviders(): void;
  /** Clear accumulated events */
  clearEvents(): void;
  /** Sessions list from the most recent list_sessions response */
  sessions: SessionInfo[];
}

// ---------------------------------------------------------------------------
// Helper: base64 decode in browser and Node environments
// ---------------------------------------------------------------------------

function decodeBase64(b64: string): string {
  if (typeof Buffer !== "undefined") {
    return Buffer.from(b64, "base64").toString("utf8");
  }
  return atob(b64);
}

// ---------------------------------------------------------------------------
// Hook implementation
// ---------------------------------------------------------------------------

export function useBridgeSession(
  url: string,
  options: UseBridgeSessionOptions = {}
): UseBridgeSessionReturn {
  const {
    autoReconnect = true,
    reconnectDelayMs = 1000,
    maxReconnectDelayMs = 30_000,
    onMessage,
    onStatusChange,
  } = options;

  const [status, setStatus] = useState<ConnectionStatus>("disconnected");
  const [error, setError] = useState<string | null>(null);
  const [events, setEvents] = useState<BridgeEvent[]>([]);
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [sessions, setSessions] = useState<SessionInfo[]>([]);

  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectDelayRef = useRef(reconnectDelayMs);
  const mountedRef = useRef(true);
  const pendingRef = useRef<ClientMessage[]>([]);

  // Keep callback refs current on every render so they never need to be deps.
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage;
  const onStatusChangeRef = useRef(onStatusChange);
  onStatusChangeRef.current = onStatusChange;

  const updateStatus = useCallback((s: ConnectionStatus) => {
    setStatus(s);
    onStatusChangeRef.current?.(s);
  }, []); // stable — reads callback via ref

  const sendRaw = useCallback((msg: ClientMessage) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(msg));
    } else {
      // Queue message to send on reconnect
      pendingRef.current.push(msg);
    }
  }, []);

  const connect = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.close();
    }

    updateStatus("connecting");
    const ws = new WebSocket(url);
    wsRef.current = ws;

    ws.onopen = () => {
      if (!mountedRef.current) return;
      reconnectDelayRef.current = reconnectDelayMs;
      updateStatus("connected");
      setError(null);

      // Flush queued messages
      const pending = pendingRef.current.splice(0);
      for (const msg of pending) {
        ws.send(JSON.stringify(msg));
      }
    };

    ws.onmessage = (ev) => {
      if (!mountedRef.current) return;
      let msg: ServerMessage;
      try {
        msg = JSON.parse(ev.data as string) as ServerMessage;
      } catch {
        return;
      }

      onMessageRef.current?.(msg);

      switch (msg.type) {
        case "session_started":
          setSessionId(msg.sessionId);
          break;
        case "session_stopped":
          setSessionId(null);
          setEvents([]);
          break;
        case "attach_event":
          setEvents((prev: BridgeEvent[]) => [
            ...prev,
            {
              seq: msg.seq,
              sessionId: msg.sessionId,
              eventType: msg.eventType,
              text: decodeBase64(msg.payloadB64),
              payloadB64: msg.payloadB64,
              replay: msg.replay,
              oldestSeq: msg.oldestSeq,
              lastSeq: msg.lastSeq,
              exitRecorded: msg.exitRecorded,
              exitCode: msg.exitCode,
              error: msg.error,
            },
          ]);
          break;
        case "sessions_list":
          setSessions(msg.sessions);
          break;
        case "error":
          setError(`${msg.code}: ${msg.message}`);
          break;
        default:
          break;
      }
    };

    ws.onerror = () => {
      if (!mountedRef.current) return;
      updateStatus("error");
      setError("WebSocket connection error");
    };

    ws.onclose = () => {
      if (!mountedRef.current) return;
      wsRef.current = null;

      if (autoReconnect) {
        updateStatus("connecting");
        reconnectTimerRef.current = setTimeout(() => {
          if (!mountedRef.current) return;
          reconnectDelayRef.current = Math.min(
            reconnectDelayRef.current * 2,
            maxReconnectDelayMs
          );
          connect();
        }, reconnectDelayRef.current);
      } else {
        updateStatus("disconnected");
      }
    };
  }, [url, autoReconnect, reconnectDelayMs, maxReconnectDelayMs, updateStatus]);

  useEffect(() => {
    mountedRef.current = true;
    connect();
    return () => {
      mountedRef.current = false;
      if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current);
      wsRef.current?.close();
    };
  }, [connect]);

  // ---------------------------------------------------------------------------
  // Public API
  // ---------------------------------------------------------------------------

  const startSession = useCallback(
    (opts: {
      projectId: string;
      sessionId?: string;
      repoPath: string;
      provider: string;
      agentOpts?: Record<string, string>;
      initialCols?: number;
      initialRows?: number;
    }) => {
      sendRaw({ type: "start_session", ...opts });
    },
    [sendRaw]
  );

  const sendInput = useCallback(
    (opts: { sessionId: string; clientId: string; text: string }) => {
      sendRaw({ type: "send_input", ...opts });
    },
    [sendRaw]
  );

  const stopSession = useCallback(
    (opts: { sessionId: string; force?: boolean }) => {
      sendRaw({ type: "stop_session", ...opts });
    },
    [sendRaw]
  );

  const attachSession = useCallback(
    (opts: { sessionId: string; clientId: string; afterSeq?: number }) => {
      sendRaw({ type: "attach_session", ...opts });
    },
    [sendRaw]
  );

  const resizeSession = useCallback(
    (opts: {
      sessionId: string;
      clientId: string;
      cols: number;
      rows: number;
    }) => {
      sendRaw({ type: "resize_session", ...opts });
    },
    [sendRaw]
  );

  const listSessions = useCallback(
    (opts?: { projectId?: string }) => {
      const msg: ListSessionsMsg = {
        type: "list_sessions",
        projectId: opts?.projectId,
      };
      sendRaw(msg);
    },
    [sendRaw]
  );

  const getSession = useCallback(
    (id: string) => {
      sendRaw({ type: "get_session", sessionId: id });
    },
    [sendRaw]
  );

  const health = useCallback(() => {
    sendRaw({ type: "health" });
  }, [sendRaw]);

  const listProviders = useCallback(() => {
    sendRaw({ type: "list_providers" });
  }, [sendRaw]);

  const clearEvents = useCallback(() => {
    setEvents([]);
  }, []);

  return {
    status,
    error,
    sessionId,
    events,
    sessions,
    startSession,
    sendInput,
    stopSession,
    attachSession,
    resizeSession,
    listSessions,
    getSession,
    health,
    listProviders,
    clearEvents,
  };
}

// Re-export status type for consumers
export type { SessionStatus };
