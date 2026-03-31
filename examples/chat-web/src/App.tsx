import { useEffect, useRef, useState } from "react";
import { useBridgeSession } from "@ai-agent-bridge/client-node/react";
import type { HealthResponseMsg, ProvidersListMsg } from "@ai-agent-bridge/client-node";
import { HealthStatus } from "./components/HealthStatus";
import { SessionControls } from "./components/SessionControls";
import { Terminal, type TerminalHandle } from "./components/Terminal";
import { InputBar } from "./components/InputBar";

const WS_URL = `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/api/bridge`;

export function App() {
  const [provider, setProvider] = useState("claude");
  const [repoPath, setRepoPath] = useState("");
  const [health, setHealth] = useState<HealthResponseMsg | null>(null);
  const [providers, setProviders] = useState<ProvidersListMsg["providers"]>([]);

  // Stable client ID for the lifetime of the page
  const clientId = useRef(crypto.randomUUID());
  const termRef = useRef<TerminalHandle>(null);
  const lastEventIndex = useRef(0);

  const bridge = useBridgeSession(WS_URL, {
    onMessage(msg) {
      if (msg.type === "health_response") setHealth(msg);
      if (msg.type === "providers_list") setProviders(msg.providers);
    },
  });

  // Request health + providers on every (re)connect
  useEffect(() => {
    if (bridge.status === "connected") {
      bridge.health();
      bridge.listProviders();
    }
  }, [bridge.status]);

  // Auto-attach once session is started
  useEffect(() => {
    if (!bridge.sessionId) return;
    lastEventIndex.current = 0;
    bridge.attachSession({
      sessionId: bridge.sessionId,
      clientId: clientId.current,
      afterSeq: 0,
    });
  }, [bridge.sessionId]);

  // Write new attach_event output to the terminal
  useEffect(() => {
    const newEvents = bridge.events.slice(lastEventIndex.current);
    lastEventIndex.current = bridge.events.length;

    for (const ev of newEvents) {
      if (ev.eventType === "output" && ev.payloadB64) {
        const bytes = Uint8Array.from(atob(ev.payloadB64), (c) => c.charCodeAt(0));
        termRef.current?.write(bytes);
      }
    }
  }, [bridge.events]);

  function handleStart() {
    if (!repoPath) return;
    termRef.current?.clear();
    bridge.startSession({
      projectId: "dev",
      repoPath,
      provider,
      initialCols: termRef.current?.cols ?? 120,
      initialRows: termRef.current?.rows ?? 40,
    });
  }

  function handleStop() {
    if (bridge.sessionId) {
      bridge.stopSession({ sessionId: bridge.sessionId, force: true });
    }
  }

  function sendInput(text: string) {
    if (bridge.sessionId) {
      bridge.sendInput({
        sessionId: bridge.sessionId,
        clientId: clientId.current,
        text,
      });
    }
  }

  function handleTerminalResize(cols: number, rows: number) {
    if (bridge.sessionId) {
      bridge.resizeSession({
        sessionId: bridge.sessionId,
        clientId: clientId.current,
        cols,
        rows,
      });
    }
  }

  return (
    <div className="flex flex-col h-screen bg-gray-900 text-gray-100">
      <HealthStatus
        connectionStatus={bridge.status}
        health={health}
        providers={providers}
      />
      <SessionControls
        provider={provider}
        repoPath={repoPath}
        sessionId={bridge.sessionId}
        onProviderChange={setProvider}
        onRepoPathChange={setRepoPath}
        onStart={handleStart}
        onStop={handleStop}
      />
      <div className="flex-1 min-h-0 p-2 bg-gray-900">
        <Terminal
          ref={termRef}
          onData={sendInput}
          onResize={handleTerminalResize}
        />
      </div>
      <InputBar
        disabled={!bridge.sessionId}
        onSubmit={(text) => sendInput(text + "\r")}
      />
    </div>
  );
}
