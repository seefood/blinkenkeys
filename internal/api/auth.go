package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

var (
	errMissingToken = errors.New("api: missing bearer token")
	errWrongToken   = errors.New("api: invalid bearer token")
)

// RequireToken wraps next with bearer-token auth. If required is false,
// requests pass through unchecked (the Unix-socket listener case, where
// filesystem permissions already gate access). If required is true — always
// the case for the optional TCP listener, never user-configurable to
// disable — a missing or mismatched token fails closed.
func RequireToken(token string, required bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !required {
			next.ServeHTTP(w, r)
			return
		}
		got, ok := bearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, errMissingToken)
			return
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writeError(w, http.StatusForbidden, errWrongToken)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	return strings.TrimPrefix(h, prefix), true
}
