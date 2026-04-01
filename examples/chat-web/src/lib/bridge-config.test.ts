import { describe, expect, it } from "vitest";

import { buildBridgeChannelOptions } from "./bridge-config";

describe("buildBridgeChannelOptions", () => {
  it("overrides the TLS target name for the docker bridge service", () => {
    expect(buildBridgeChannelOptions("bridge:9445")).toEqual({
      "grpc.ssl_target_name_override": "bridge.local",
      "grpc.default_authority": "bridge.local",
    });
  });

  it("does not override when the bridge address already matches the certificate", () => {
    expect(buildBridgeChannelOptions("bridge.local:9445")).toBeUndefined();
  });

  it("does not override unrelated hosts", () => {
    expect(buildBridgeChannelOptions("example.test:9445")).toBeUndefined();
  });
});
