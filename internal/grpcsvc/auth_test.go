package grpcsvc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/teo-dev/teo/internal/auth"
)

func TestBearerFromMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		md   metadata.MD
		want string
	}{
		{"empty", metadata.MD{}, ""},
		{"bare token", metadata.Pairs("authorization", "tok123"), "tok123"},
		{"Bearer prefix", metadata.Pairs("authorization", "Bearer tok123"), "tok123"},
		{"lowercase bearer", metadata.Pairs("authorization", "bearer tok123"), "tok123"},
		{"surrounding space", metadata.Pairs("authorization", "Bearer   tok123  "), "tok123"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, bearerFromMetadata(tc.md))
		})
	}
}

func TestAuthUnaryInterceptor(t *testing.T) {
	t.Parallel()

	issuer := &auth.JWTIssuer{Secret: []byte("a-32-byte-test-secret-padding-xx"), TTL: time.Hour, Issuer: "teo"}
	valid, err := issuer.Issue("u1", "u1@example.com", []auth.Role{auth.RoleEngineer})
	require.NoError(t, err)

	interceptor := AuthUnaryInterceptor(issuer, nil)

	// handler records whether it ran and what principal it saw.
	var sawPrincipal *auth.Principal
	handler := func(ctx context.Context, _ any) (any, error) {
		sawPrincipal = auth.PrincipalFrom(ctx)
		return "ok", nil
	}

	call := func(md metadata.MD, method string) (any, error) {
		sawPrincipal = nil
		ctx := context.Background()
		if md != nil {
			ctx = metadata.NewIncomingContext(ctx, md)
		}
		return interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, handler)
	}

	const runsCreate = "/teo.v1.Runs/CreateRun"
	const workersPull = "/teo.v1.Workers/PullAssignment"

	t.Run("gated method without metadata is rejected", func(t *testing.T) {
		_, err := call(nil, runsCreate)
		require.Equal(t, codes.Unauthenticated, status.Code(err))
		require.Nil(t, sawPrincipal)
	})

	t.Run("gated method with invalid token is rejected", func(t *testing.T) {
		_, err := call(metadata.Pairs("authorization", "Bearer garbage"), runsCreate)
		require.Equal(t, codes.Unauthenticated, status.Code(err))
	})

	t.Run("gated method with valid JWT passes and attaches principal", func(t *testing.T) {
		out, err := call(metadata.Pairs("authorization", "Bearer "+valid), runsCreate)
		require.NoError(t, err)
		require.Equal(t, "ok", out)
		require.NotNil(t, sawPrincipal)
		require.Equal(t, "u1", sawPrincipal.UserID)
	})

	t.Run("ungated method without auth still passes", func(t *testing.T) {
		out, err := call(nil, workersPull)
		require.NoError(t, err)
		require.Equal(t, "ok", out)
		require.Nil(t, sawPrincipal)
	})

	t.Run("ungated method with valid JWT attaches principal", func(t *testing.T) {
		out, err := call(metadata.Pairs("authorization", "Bearer "+valid), workersPull)
		require.NoError(t, err)
		require.Equal(t, "ok", out)
		require.NotNil(t, sawPrincipal)
	})
}

func TestAuthenticateAPIKeyResolver(t *testing.T) {
	t.Parallel()

	called := false
	resolver := func(_ context.Context, prefix, display string) (*auth.Principal, error) {
		called = true
		require.Equal(t, "teo_ci_abc", prefix)
		require.Equal(t, "teo_ci_abc.secret", display)
		return &auth.Principal{APIKeyID: "k1"}, nil
	}
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", "teo_ci_abc.secret"))
	p := authenticate(ctx, nil, resolver)
	require.True(t, called)
	require.NotNil(t, p)
	require.Equal(t, "k1", p.APIKeyID)
	require.True(t, p.IsAPIKey)
}
