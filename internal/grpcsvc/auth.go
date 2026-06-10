package grpcsvc

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/teo-dev/teo/internal/auth"
)

// authedMethods is the set of full gRPC method names that require an
// authenticated principal. The Runs mutation/read RPCs are gated here so the
// gRPC surface enforces the same auth the HTTP RunsHandler does; the internal
// worker-dispatch RPCs (teo.v1.Workers/*) are intentionally left open, matching
// the pre-existing posture for internal traffic.
var authedMethods = map[string]bool{
	"/teo.v1.Runs/CreateRun": true,
	"/teo.v1.Runs/GetRun":    true,
	"/teo.v1.Runs/CancelRun": true,
}

// AuthUnaryInterceptor authenticates unary RPCs whose full method name is in
// authedMethods. It extracts a bearer JWT or a teo_* API key from the
// "authorization" metadata header, validates it with the same primitives the
// HTTP middleware uses, and rejects unauthenticated callers with
// codes.Unauthenticated before the handler runs. On success it attaches the
// resolved *auth.Principal to the context via auth.WithPrincipal so handlers
// (and the shared runsvc.Service via audit) can read it.
//
// jwtIssuer and/or resolveAPIKey may be nil; a credential whose validator is
// absent simply fails to authenticate. RPCs not in authedMethods pass straight
// through (still attaching a principal if a valid credential was supplied).
func AuthUnaryInterceptor(jwtIssuer *auth.JWTIssuer, resolveAPIKey auth.Resolver) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if p := authenticate(ctx, jwtIssuer, resolveAPIKey); p != nil {
			ctx = auth.WithPrincipal(ctx, p)
		} else if authedMethods[info.FullMethod] {
			return nil, status.Error(codes.Unauthenticated, "authentication required")
		}
		return handler(ctx, req)
	}
}

// authenticate parses the authorization metadata header and returns the
// resolved principal, or nil if no valid credential was presented.
func authenticate(ctx context.Context, jwtIssuer *auth.JWTIssuer, resolveAPIKey auth.Resolver) *auth.Principal {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil
	}
	tok := bearerFromMetadata(md)
	if tok == "" {
		return nil
	}
	if strings.HasPrefix(tok, "teo_") {
		dot := strings.LastIndexByte(tok, '.')
		if dot <= 0 || resolveAPIKey == nil {
			return nil
		}
		p, err := resolveAPIKey(ctx, tok[:dot], tok)
		if err != nil || p == nil {
			return nil
		}
		p.IsAPIKey = true
		return p
	}
	if jwtIssuer == nil {
		return nil
	}
	p, err := jwtIssuer.Verify(tok)
	if err != nil {
		return nil
	}
	return p
}

// bearerFromMetadata pulls the credential out of the "authorization" metadata
// key, trimming an optional "Bearer " prefix (case-insensitive).
func bearerFromMetadata(md metadata.MD) string {
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return ""
	}
	tok := vals[0]
	if len(tok) >= 7 && strings.EqualFold(tok[:7], "bearer ") {
		tok = tok[7:]
	}
	return strings.TrimSpace(tok)
}
