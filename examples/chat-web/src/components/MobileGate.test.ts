import { describe, expect, it } from "vitest";

import { shouldUseMobileGate } from "./MobileGate";

describe("shouldUseMobileGate", () => {
  it("blocks narrow mobile browsers", () => {
    expect(
      shouldUseMobileGate({
        userAgent:
          "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
        viewportWidth: 390,
        coarsePointer: true,
      })
    ).toBe(true);
  });

  it("allows desktop mode on mobile devices", () => {
    expect(
      shouldUseMobileGate({
        userAgent:
          "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
        viewportWidth: 1280,
        coarsePointer: true,
      })
    ).toBe(false);
  });

  it("does not block coarse-pointer desktop browsers", () => {
    expect(
      shouldUseMobileGate({
        userAgent:
          "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
        viewportWidth: 1440,
        coarsePointer: true,
      })
    ).toBe(false);
  });
});
