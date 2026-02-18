package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type ctxKeyClaims struct{}

// ClaimsFromContext extracts BridgeClaims from a gRPC context.
func ClaimsFromContext(ctx context.Context) (*BridgeClaims, bool) {
	c, ok := ctx.Value(ctxKeyClaims{}).(*BridgeClaims)
	return c, ok
}

// ContextWithClaims stores claims in context.
func ContextWithClaims(ctx context.Context, claims *BridgeClaims) context.Context {
	return context.WithValue(ctx, ctxKeyClaims{}, claims)
}

// UnaryJWTInterceptor returns a gRPC unary interceptor that verifies JWTs.
func UnaryJWTInterceptor(v *JWTVerifier, logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Skip auth for health checks
		if info.FullMethod == "/bridge.v1.BridgeService/Health" {
			return handler(ctx, req)
		}
		claims, err := extractAndVerify(ctx, v)
		if err != nil {
			if logger != nil {
				logger.Warn("auth decision", "result", "deny", "rpc_method", info.FullMethod, "reason", err.Error())
			}
			return nil, err
		}
		if logger != nil {
			logger.Info("auth decision", "result", "allow", "rpc_method", info.FullMethod, "caller_sub", claims.Subject, "project_id", claims.ProjectID)
		}
		return handler(ContextWithClaims(ctx, claims), req)
	}
}

// StreamJWTInterceptor returns a gRPC stream interceptor that verifies JWTs.
func StreamJWTInterceptor(v *JWTVerifier, logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		claims, err := extractAndVerify(ss.Context(), v)
		if err != nil {
			if logger != nil {
				logger.Warn("auth decision", "result", "deny", "rpc_method", info.FullMethod, "reason", err.Error())
			}
			return err
		}
		if logger != nil {
			logger.Info("auth decision", "result", "allow", "rpc_method", info.FullMethod, "caller_sub", claims.Subject, "project_id", claims.ProjectID)
		}
		wrapped := &wrappedStream{
			ServerStream: ss,
			ctx:          ContextWithClaims(ss.Context(), claims),
		}
		return handler(srv, wrapped)
	}
}

func extractAndVerify(ctx context.Context, v *JWTVerifier) (*BridgeClaims, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}

	vals := md.Get("authorization")
	if len(vals) == 0 {
		return nil, status.Error(codes.Unauthenticated, "missing authorization header")
	}

	token, err := parseBearerToken(vals[0])
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}

	claims, err := v.Verify(token)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid token: %v", err)
	}

	return claims, nil
}

func parseBearerToken(authz string) (string, error) {
	parts := strings.SplitN(authz, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", errors.New("expected Bearer <token>")
	}
	return strings.TrimSpace(parts[1]), nil
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }
