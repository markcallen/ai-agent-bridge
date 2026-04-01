const DOCKER_BRIDGE_HOST = "bridge";
const TLS_SERVER_NAME = "bridge.local";

export function buildBridgeChannelOptions(bridgeAddr: string) {
  const [host] = bridgeAddr.split(":", 1);
  if (host !== DOCKER_BRIDGE_HOST) {
    return undefined;
  }

  return {
    "grpc.ssl_target_name_override": TLS_SERVER_NAME,
    "grpc.default_authority": TLS_SERVER_NAME,
  };
}
