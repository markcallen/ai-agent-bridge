import type { HealthResponseMsg } from "@ai-agent-bridge/client-node";

interface Props {
  bridgeError: string | null;
  health: HealthResponseMsg | null;
}

export function collectErrorMessages(
  bridgeError: string | null,
  health: HealthResponseMsg | null
): string[] {
  const messages: string[] = [];
  const seen = new Set<string>();

  const addMessage = (message: string | null | undefined) => {
    const trimmed = message?.trim();
    if (!trimmed || seen.has(trimmed)) {
      return;
    }
    seen.add(trimmed);
    messages.push(trimmed);
  };

  addMessage(bridgeError);

  for (const provider of health?.providers ?? []) {
    if (!provider.error?.trim()) {
      continue;
    }
    addMessage(`${provider.provider}: ${provider.error.trim()}`);
  }

  return messages;
}

export function ErrorSummary({ bridgeError, health }: Props) {
  const messages = collectErrorMessages(bridgeError, health);

  if (messages.length === 0) {
    return null;
  }

  return (
    <details className="border-b border-red-900/60 bg-red-950/30 px-4 py-2 text-sm">
      <summary className="flex cursor-pointer list-none items-center gap-2 text-red-100">
        <span className="rounded-full bg-red-900 px-2 py-0.5 text-xs font-semibold uppercase tracking-wide text-red-200">
          {messages.length} error{messages.length === 1 ? "" : "s"}
        </span>
        <span>Show details</span>
      </summary>
      <ul className="mt-2 space-y-1">
        {messages.map((message) => (
          <li
            key={message}
            className="rounded bg-red-950/60 px-3 py-2 font-mono text-xs leading-5 text-red-100"
          >
            {message}
          </li>
        ))}
      </ul>
    </details>
  );
}
