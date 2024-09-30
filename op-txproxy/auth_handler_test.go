package op_txproxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/stretchr/testify/require"
)

var pingHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "ping")
})

func TestAuthHandlerMissingAuth(t *testing.T) {
	handler := authHandler{next: pingHandler}

	rr := httptest.NewRecorder()
	r, _ := http.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rr, r)

	// simply forwards the request
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "ping", rr.Body.String())
}

func TestAuthHandlerBadHeader(t *testing.T) {
	handler := authHandler{headerKey: "auth", next: pingHandler}

	rr := httptest.NewRecorder()
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("auth", "foobarbaz")

	handler.ServeHTTP(rr, r)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestAuthHandlerBadSignature(t *testing.T) {
	handler := authHandler{headerKey: "auth", next: pingHandler}

	rr := httptest.NewRecorder()
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("auth", fmt.Sprintf("%s:%s", common.HexToAddress("0xa"), "foobar"))

	handler.ServeHTTP(rr, r)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestAuthHandlerMismatchedCaller(t *testing.T) {
	handler := authHandler{headerKey: "auth", next: pingHandler}

	rr := httptest.NewRecorder()
	r, _ := http.NewRequest("GET", "/", strings.NewReader("body"))

	privKey, _ := crypto.GenerateKey()
	sig, _ := crypto.Sign(accounts.TextHash([]byte("body")), privKey)
	r.Header.Set("auth", fmt.Sprintf("%s:%s", common.HexToAddress("0xa"), sig))

	handler.ServeHTTP(rr, r)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestAuthHandlerSetContext(t *testing.T) {
	var ctx *AuthContext
	ctxHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx = AuthFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := authHandler{headerKey: "auth", next: ctxHandler}

	rr := httptest.NewRecorder()
	body := bytes.NewBufferString("body")
	r, _ := http.NewRequest("GET", "/", body)

	privKey, _ := crypto.GenerateKey()
	sig, _ := crypto.Sign(accounts.TextHash(body.Bytes()), privKey)
	addr := crypto.PubkeyToAddress(privKey.PublicKey)
	r.Header.Set("auth", fmt.Sprintf("%s:%s", addr, common.Bytes2Hex(sig)))

	handler.ServeHTTP(rr, r)
	require.Equal(t, http.StatusOK, rr.Code)

	require.NotNil(t, ctx)
	require.Equal(t, addr, ctx.Caller)
}

func TestAuthHandlerRpcMiddleware(t *testing.T) {
	rpcServer := oprpc.NewServer("127.0.0.1", 0, "", oprpc.WithMiddleware(AuthMiddleware("auth")))
	require.NoError(t, rpcServer.Start())
	t.Cleanup(func() { _ = rpcServer.Stop() })

	url := fmt.Sprintf("http://%s", rpcServer.Endpoint())
	clnt, err := rpc.Dial(url)
	require.NoError(t, err)
	defer clnt.Close()

	// pass without auth (default handler does not deny)
	err = clnt.CallContext(context.Background(), nil, "rpc_modules")
	require.Nil(t, err)

	// denied with bad auth header
	clnt.SetHeader("auth", "foobar")
	err = clnt.CallContext(context.Background(), nil, "rpc_modules")
	require.NotNil(t, err)
}

func TestAuthHandlerRequestBodyLimit(t *testing.T) {
	var body []byte
	bodyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	handler := authHandler{headerKey: "auth", next: bodyHandler}

	// only up to limit is read when validating the request body
	authBody := strings.Repeat("*", defaultBodyLimit)
	excess := strings.Repeat("-", 10)

	rr := httptest.NewRecorder()
	r, _ := http.NewRequest("GET", "/", strings.NewReader(authBody+excess))

	// sign over just the auth body
	privKey, _ := crypto.GenerateKey()
	sig, _ := crypto.Sign(accounts.TextHash([]byte(authBody)), privKey)
	addr := crypto.PubkeyToAddress(privKey.PublicKey)
	r.Header.Set("auth", fmt.Sprintf("%s:%s", addr, common.Bytes2Hex(sig)))

	// Auth handler successfully only parses through the max body limit
	handler.ServeHTTP(rr, r)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body)

	// The next handler has the full request body present
	require.Len(t, body, len(authBody)+len(excess))
}
