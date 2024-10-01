package op_txproxy

import (
	"bytes"
	"context"
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
)

type authHandler struct {
	headerKey string
	next      http.Handler
}

// This middleware detects when authentication information is present on the request. If
// so, it will validate and set the caller in the request context. It does not reject
// if authentication information is missing. It is up to the request handler to do so via
// the missing `AuthContext`
//   - NOTE: only up to the default body limit (5MB) is read when constructing the text hash
//     that is signed over by the caller
func AuthMiddleware(headerKey string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return &authHandler{headerKey, next}
	}
}

type authContextKey struct{}

type AuthContext struct {
	Caller common.Address
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
		http.Error(w, "misformatted auth header", http.StatusBadRequest)
		return
	}

	if r.Body == nil {
		// edge case from unit tests
		r.Body = io.NopCloser(bytes.NewBuffer(nil))
	}

	// Since this middleware runs prior to the server, we need to manually apply the body limit when reading.
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
	if err != nil {
		http.Error(w, "invalid authentication signature", http.StatusBadRequest)
		return
	}

	if caller != crypto.PubkeyToAddress(*sigPubKey) {
		http.Error(w, "mismatched recovered signer", http.StatusBadRequest)
		return
	}

	// Set the authenticated caller in the context
	newCtx := context.WithValue(r.Context(), authContextKey{}, &AuthContext{caller})
	h.next.ServeHTTP(w, r.WithContext(newCtx))
}

func AuthFromContext(ctx context.Context) *AuthContext {
	auth, ok := ctx.Value(authContextKey{}).(*AuthContext)
	if !ok {
		return nil
	}
	return auth
}
