package op_txproxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	defaultBodyLimit = 5 * 1024 * 1024 // default in op-geth

	DefaultAuthHeaderKey = "X-Optimism-Signature"

	// errs
	misformattedAuthErr          = errors.New("misformatted auth header")
	invalidAuthSignatureErr      = errors.New("invalid auth signature")
	mismatchedRecoveredSignerErr = errors.New("mismatched recovered signer")
)

type authHandler struct {
	headerKey string
	next      http.Handler
}

// This middleware detects when authentication information is present on the request. If
// so, it will validate and set the caller in the request context. It does not reject any
// requests and leaves it up to the request handler to do so.
//  1. Missing Auth Header: AuthContext is missing from context
//  2. Failed Validation: AuthContext is set with a populated Err
//  3. Passed Validation: AuthContext is set with the authenticated caller
//
// note: only up to the default body limit (5MB) is read when constructing the text hash
func AuthMiddleware(headerKey string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return &authHandler{headerKey, next}
	}
}

type authContextKey struct{}

type AuthContext struct {
	Caller common.Address
	Err    error
}

// ServeHTTP serves JSON-RPC requests over HTTP, implements http.Handler
func (h *authHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get(h.headerKey)
	if authHeader == "" {
		h.next.ServeHTTP(w, r)
		return
	}
	authElems := strings.Split(authHeader, ":")
	if len(authElems) != 2 {
		newCtx := context.WithValue(r.Context(), authContextKey{}, &AuthContext{common.Address{}, misformattedAuthErr})
		h.next.ServeHTTP(w, r.WithContext(newCtx))
		return
	}

	if r.Body == nil { // edge case from unit tests
		r.Body = io.NopCloser(bytes.NewBuffer(nil))
	}

	// Since this middleware runs prior to the server, we need to manually apply the body limit when
	// reading. We reject if we fail to read since this is an issue with this request
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, int64(defaultBodyLimit)))
	if err != nil {
		http.Error(w, "unable to parse request body", http.StatusInternalServerError)
		return
	}

	r.Body = struct {
		io.Reader
		io.Closer
	}{
		io.MultiReader(bytes.NewReader(bodyBytes), r.Body),
		r.Body,
	}

	txtHash := accounts.TextHash(bodyBytes)
	caller, signature := common.HexToAddress(authElems[0]), common.FromHex(authElems[1])
	sigPubKey, err := crypto.SigToPub(txtHash, signature)
	if sigPubKey == nil || err != nil {
		newCtx := context.WithValue(r.Context(), authContextKey{}, &AuthContext{common.Address{}, invalidAuthSignatureErr})
		h.next.ServeHTTP(w, r.WithContext(newCtx))
		return
	}

	if caller != crypto.PubkeyToAddress(*sigPubKey) {
		newCtx := context.WithValue(r.Context(), authContextKey{}, &AuthContext{common.Address{}, mismatchedRecoveredSignerErr})
		h.next.ServeHTTP(w, r.WithContext(newCtx))
		return
	}

	// Set the authenticated caller in the context
	newCtx := context.WithValue(r.Context(), authContextKey{}, &AuthContext{caller, nil})
	h.next.ServeHTTP(w, r.WithContext(newCtx))
}

func AuthFromContext(ctx context.Context) *AuthContext {
	auth, ok := ctx.Value(authContextKey{}).(*AuthContext)
	if !ok {
		return nil
	}
	return auth
}
