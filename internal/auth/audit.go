package auth

import (
	"context"
	"log/slog"
	"reflect"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryAuditInterceptor logs RPC outcomes with caller and request scope metadata.
func UnaryAuditInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)

		if logger == nil {
			return resp, err
		}
		claims, _ := ClaimsFromContext(ctx)
		projectID := requestStringField(req, "ProjectId")
		sessionID := requestStringField(req, "SessionId")
		if claims != nil && claims.ProjectID != "" && projectID == "" {
			projectID = claims.ProjectID
		}

		fields := []any{
			"rpc_method", info.FullMethod,
			"project_id", projectID,
			"session_id", sessionID,
		}
		if claims != nil {
			fields = append(fields, "caller_sub", claims.Subject)
		}
		if err != nil {
			st, _ := status.FromError(err)
			fields = append(fields, "result", "error", "code", st.Code().String(), "reason", st.Message())
			logger.Warn("rpc audit", fields...)
			return resp, err
		}
		fields = append(fields, "result", "ok", "code", codes.OK.String())
		logger.Info("rpc audit", fields...)
		return resp, err
	}
}

// StreamAuditInterceptor logs stream RPC outcomes with caller metadata.
func StreamAuditInterceptor(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		err := handler(srv, ss)

		if logger == nil {
			return err
		}
		claims, _ := ClaimsFromContext(ss.Context())
		fields := []any{"rpc_method", info.FullMethod}
		if claims != nil {
			fields = append(fields, "caller_sub", claims.Subject, "project_id", claims.ProjectID)
		}
		if err != nil {
			st, _ := status.FromError(err)
			fields = append(fields, "result", "error", "code", st.Code().String(), "reason", st.Message())
			logger.Warn("rpc audit", fields...)
			return err
		}
		fields = append(fields, "result", "ok", "code", codes.OK.String())
		logger.Info("rpc audit", fields...)
		return nil
	}
}

func requestStringField(req any, field string) string {
	if req == nil {
		return ""
	}
	v := reflect.ValueOf(req)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	f := v.FieldByName(field)
	if !f.IsValid() || f.Kind() != reflect.String {
		return ""
	}
	return f.String()
}
