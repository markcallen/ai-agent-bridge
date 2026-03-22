/**
 * useBridgeSession — React hook for the ai-agent-bridge WebSocket protocol.
 *
 * Manages a WebSocket connection to the bridge WebSocket adapter (Node.js or Go)
 * and exposes methods for session lifecycle and event streaming.
 *
 * @example
 * function App() {
 *   const bridge = useBridgeSession("ws://localhost:3000/api/bridge");
 *
 *   const start = () => {
 *     // startSession enqueues the message; the session_started response sets
 *     // bridge.sessionId. Call streamEvents once sessionId is set.
 *     bridge.startSession({
 *       projectId: "my-project",
 *       repoPath: "/repos/my-app",
 *       provider: "claude",
 *     });
 *   };
 *
 *   return (
 *     <div>
 *       <p>Status: {bridge.status}</p>
 *       {bridge.events.map((ev) => <p key={ev.seq}>{ev.text}</p>)}
 *       <button onClick={start}>Start</button>
 *       <button onClick={() => bridge.sendInput({ sessionId: bridge.sessionId!, text: "hello" })}>
 *         Send
 *       </button>
 *     </div>
 *   );
 * }
 */

import { useCallback, useEffect, useRef, useState } from "react";
import type {
  ClientMessage,
  EventMsg,
  EventType,
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
  eventType: EventType;
  stream: string;
  text: string;
  done: boolean;
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
  /** Accumulated events received via stream_events */
  events: BridgeEvent[];
  /** Start a new agent session */
  startSession(opts: {
    projectId: string;
    sessionId?: string;
    repoPath: string;
    provider: string;
    agentOpts?: Record<string, string>;
  }): void;
  /** Send text input to a running session */
  sendInput(opts: {
    sessionId: string;
    text: string;
    idempotencyKey?: string;
  }): void;
  /** Stop a session */
  stopSession(opts: { sessionId: string; force?: boolean }): void;
  /** Subscribe to event streaming for a session */
  streamEvents(opts: {
    sessionId: string;
    afterSeq?: number;
    subscriberId?: string;
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

  const updateStatus = useCallback(
    (s: ConnectionStatus) => {
      setStatus(s);
      onStatusChange?.(s);
    },
    [onStatusChange]
  );

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

      onMessage?.(msg);

      switch (msg.type) {
        case "session_started":
          setSessionId(msg.sessionId);
          break;
        case "event": {
          const e = msg as EventMsg;
          setEvents((prev: BridgeEvent[]) => [
            ...prev,
            {
              seq: e.seq,
              sessionId: e.sessionId,
              eventType: e.eventType,
              stream: e.stream,
              text: e.text,
              done: e.done,
              error: e.error,
            },
          ]);
          break;
        }
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
  }, [url, autoReconnect, reconnectDelayMs, maxReconnectDelayMs, onMessage, updateStatus]);

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
    }) => {
      sendRaw({ type: "start_session", ...opts });
    },
    [sendRaw]
  );

  const sendInput = useCallback(
    (opts: { sessionId: string; text: string; idempotencyKey?: string }) => {
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

  const streamEvents = useCallback(
    (opts: {
      sessionId: string;
      afterSeq?: number;
      subscriberId?: string;
    }) => {
      sendRaw({ type: "stream_events", ...opts });
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
    streamEvents,
    listSessions,
    getSession,
    health,
    listProviders,
    clearEvents,
  };
}

// Re-export status type for consumers
export type { SessionStatus };
