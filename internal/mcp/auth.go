package mcp

import (
	"context"
	"crypto/subtle"
	"net/http"
)

// Auth configures bearer authentication for the HTTP transport.
//
// Token is an unscoped token: it may act for any actor. ActorTokens maps a
// token to the single actor it may act as. When both are empty, /mcp is
// unauthenticated, which is appropriate for a local stdio-style deployment.
type Auth struct {
	Token       string
	ActorTokens map[string]string
}

func (a Auth) enabled() bool {
	return a.Token != "" || len(a.ActorTokens) > 0
}

// resolve returns the actor bound to the Authorization header. ok is false
// when no configured token matches; an empty actor with ok=true is unscoped.
func (a Auth) resolve(header string) (string, bool) {
	for token, actor := range a.ActorTokens {
		if token == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(header), []byte("Bearer "+token)) == 1 {
			return actor, true
		}
	}
	if a.Token != "" && subtle.ConstantTimeCompare([]byte(header), []byte("Bearer "+a.Token)) == 1 {
		return "", true
	}
	return "", false
}

type actorContextKey struct{}

// WithActor binds actor to ctx. Tool and resource handlers use it to reject
// requests for a different actor.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorContextKey{}, actor)
}

// ActorFromContext returns the actor bound by the HTTP bearer middleware.
func ActorFromContext(ctx context.Context) (string, bool) {
	actor, ok := ctx.Value(actorContextKey{}).(string)
	if !ok || actor == "" {
		return "", false
	}
	return actor, true
}

func requireBearer(auth Auth, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, ok := auth.resolve(r.Header.Get("Authorization"))
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="boundary-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if actor != "" {
			r = r.WithContext(WithActor(r.Context(), actor))
		}
		next.ServeHTTP(w, r)
	})
}
