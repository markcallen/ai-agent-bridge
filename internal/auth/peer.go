package auth

import (
	"context"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// CallerCommonName extracts the Common Name from the mTLS peer certificate
// in the gRPC context. Returns empty string if not available.
func CallerCommonName(ctx context.Context) string {
	return callerCommonName(ctx)
}

func callerCommonName(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil {
		return ""
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return ""
	}
	if len(tlsInfo.State.PeerCertificates) == 0 {
		return ""
	}
	return tlsInfo.State.PeerCertificates[0].Subject.CommonName
}
