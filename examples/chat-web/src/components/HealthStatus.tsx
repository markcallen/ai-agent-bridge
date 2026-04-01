import type { HealthResponseMsg, ProvidersListMsg } from "@ai-agent-bridge/client-node";
import type { ConnectionStatus } from "@ai-agent-bridge/client-node/react";

interface Props {
  connectionStatus: ConnectionStatus;
  health: HealthResponseMsg | null;
  providers: ProvidersListMsg["providers"];
}

const CONNECTION_COLORS: Record<ConnectionStatus, string> = {
  connected: "bg-green-500",
  connecting: "bg-yellow-500 animate-pulse",
  disconnected: "bg-gray-500",
  error: "bg-red-500",
};

export function HealthStatus({ connectionStatus, health, providers }: Props) {
  return (
    <div className="flex items-center gap-3 px-4 py-2 bg-gray-800 border-b border-gray-700 text-sm flex-wrap">
      {/* WebSocket connection */}
      <div className="flex items-center gap-1.5">
        <span className={`w-2 h-2 rounded-full ${CONNECTION_COLORS[connectionStatus]}`} />
        <span className="text-gray-400 capitalize">{connectionStatus}</span>
      </div>

      {/* Bridge health */}
      {health && (
        <div className="flex items-center gap-1.5">
          <span className="text-gray-600">|</span>
          <span className="text-gray-400">bridge</span>
          <span
            className={`text-xs font-medium px-1.5 py-0.5 rounded ${
              health.status === "ok" ? "bg-green-900 text-green-300" : "bg-red-900 text-red-300"
            }`}
          >
            {health.status}
          </span>
        </div>
      )}

      {/* Provider availability */}
      {providers.length > 0 && (
        <>
          <span className="text-gray-600">|</span>
          {providers.map((p) => (
            <div key={p.provider} className="flex items-center gap-1">
              <span
                className={`w-1.5 h-1.5 rounded-full ${
                  p.available ? "bg-green-400" : "bg-red-400"
                }`}
              />
              <span className={p.available ? "text-gray-300" : "text-gray-500"}>
                {p.provider}
              </span>
              {p.version && (
                <span className="text-gray-600 text-xs">{p.version}</span>
              )}
            </div>
          ))}
        </>
      )}
    </div>
  );
}
