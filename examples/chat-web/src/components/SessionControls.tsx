const PROVIDERS = ["claude", "opencode", "codex", "gemini"] as const;

interface Props {
  provider: string;
  repoPath: string;
  sessionId: string | null;
  onProviderChange: (p: string) => void;
  onRepoPathChange: (p: string) => void;
  onStart: () => void;
  onStop: () => void;
}

export function SessionControls({
  provider,
  repoPath,
  sessionId,
  onProviderChange,
  onRepoPathChange,
  onStart,
  onStop,
}: Props) {
  const hasSession = sessionId !== null;

  return (
    <div className="flex items-center gap-3 px-4 py-2 bg-gray-850 border-b border-gray-700 flex-wrap">
      {/* Provider selector */}
      <select
        value={provider}
        onChange={(e) => onProviderChange(e.target.value)}
        disabled={hasSession}
        className="bg-gray-700 text-gray-100 text-sm rounded px-2 py-1.5 border border-gray-600 focus:outline-none focus:border-blue-500 disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {PROVIDERS.map((p) => (
          <option key={p} value={p}>
            {p}
          </option>
        ))}
      </select>

      {/* Repo path */}
      <input
        type="text"
        value={repoPath}
        onChange={(e) => onRepoPathChange(e.target.value)}
        onKeyDown={(e) => { if (e.key === "Enter" && !hasSession && repoPath) onStart(); }}
        disabled={hasSession}
        placeholder="/path/to/repo"
        className="flex-1 min-w-48 bg-gray-700 text-gray-100 text-sm rounded px-3 py-1.5 border border-gray-600 placeholder-gray-500 focus:outline-none focus:border-blue-500 disabled:opacity-50 disabled:cursor-not-allowed"
      />

      {/* Session ID badge */}
      {sessionId && (
        <span className="text-xs text-gray-500 font-mono truncate max-w-40" title={sessionId}>
          {sessionId.slice(0, 8)}…
        </span>
      )}

      {/* Start / Stop */}
      {!hasSession ? (
        <button
          onClick={onStart}
          disabled={!repoPath}
          className="px-4 py-1.5 text-sm font-medium rounded bg-blue-600 hover:bg-blue-500 disabled:bg-gray-700 disabled:text-gray-500 disabled:cursor-not-allowed transition-colors"
        >
          Start
        </button>
      ) : (
        <button
          onClick={onStop}
          className="px-4 py-1.5 text-sm font-medium rounded bg-red-700 hover:bg-red-600 transition-colors"
        >
          Stop
        </button>
      )}
    </div>
  );
}
