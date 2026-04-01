import { describe, expect, it } from "vitest";

import type { HealthResponseMsg } from "@ai-agent-bridge/client-node";
import { collectErrorMessages } from "./ErrorSummary";

describe("collectErrorMessages", () => {
  it("includes the websocket error when present", () => {
    expect(collectErrorMessages("WebSocket connection error", null)).toEqual([
      "WebSocket connection error",
    ]);
  });

  it("includes unavailable provider errors from bridge health", () => {
    const health: HealthResponseMsg = {
      type: "health_response",
      status: "degraded",
      providers: [
        { provider: "claude", available: false, error: "missing ANTHROPIC_API_KEY" },
        { provider: "codex", available: true, error: "" },
      ],
    };

    expect(collectErrorMessages(null, health)).toEqual([
      "claude: missing ANTHROPIC_API_KEY",
    ]);
  });

  it("deduplicates repeated errors", () => {
    const health: HealthResponseMsg = {
      type: "health_response",
      status: "degraded",
      providers: [
        { provider: "claude", available: false, error: "missing API key" },
        { provider: "gemini", available: false, error: "missing API key" },
      ],
    };

    expect(collectErrorMessages("missing API key", health)).toEqual([
      "missing API key",
      "claude: missing API key",
      "gemini: missing API key",
    ]);
  });
});
