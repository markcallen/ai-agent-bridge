package auth

import (
	"context"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

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
