package op_txproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
	"github.com/ethereum-optimism/optimism/op-service/testlog"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/stretchr/testify/require"
)

func TestAuthHandlerMissingAuth(t *testing.T) {
	var authContext *AuthContext
	log := testlog.Logger(t, log.LevelInfo)
	handler := authHandler{log: log, headerKey: "auth", next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authContext = AuthFromContext(r.Context())
	})}

	rr := httptest.NewRecorder()
	r, _ := http.NewRequest("GET", "/", nil)

	handler.ServeHTTP(rr, r)
	require.Nil(t, authContext)
}

func TestAuthHandlerBadHeader(t *testing.T) {
	var authContext *AuthContext
	log := testlog.Logger(t, log.LevelInfo)
	handler := authHandler{log: log, headerKey: "auth", next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authContext = AuthFromContext(r.Context())
	})}

	rr := httptest.NewRecorder()
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("auth", "foobarbaz")

	handler.ServeHTTP(rr, r)
	require.NotNil(t, authContext)
	require.Zero(t, authContext.Caller)
	require.Equal(t, misformattedAuthErr, authContext.Err)
}

func TestAuthHandlerBadSignature(t *testing.T) {
	var authContext *AuthContext
	log := testlog.Logger(t, log.LevelInfo)
	handler := authHandler{log: log, headerKey: "auth", next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authContext = AuthFromContext(r.Context())
	})}

	rr := httptest.NewRecorder()
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("auth", fmt.Sprintf("%s:%s", common.HexToAddress("a"), "0x123"))

	handler.ServeHTTP(rr, r)
	require.NotNil(t, authContext)
	require.Zero(t, authContext.Caller)
	require.Equal(t, invalidSignatureErr, authContext.Err)
}

func TestAuthHandlerMismatchedCaller(t *testing.T) {
	var authContext *AuthContext
	log := testlog.Logger(t, log.LevelInfo)
	handler := authHandler{log: log, headerKey: "auth", next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authContext = AuthFromContext(r.Context())
	})}

	rr := httptest.NewRecorder()
	body := bytes.NewBufferString("body")
	r, _ := http.NewRequest("GET", "/", body)

	privKey, _ := crypto.GenerateKey()
	sig, _ := crypto.Sign(accounts.TextHash(body.Bytes()), privKey)
	r.Header.Set("auth", fmt.Sprintf("%s:%s", common.HexToAddress("a"), common.Bytes2Hex(sig)))

	handler.ServeHTTP(rr, r)
	require.NotNil(t, authContext)
	require.Zero(t, authContext.Caller)
	require.Equal(t, mismatchedRecoveredSignerErr, authContext.Err)
}

func TestAuthHandlerSetContext(t *testing.T) {
	var authContext *AuthContext
	ctxHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authContext = AuthFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	log := testlog.Logger(t, log.LevelInfo)
	handler := authHandler{log: log, headerKey: DefaultAuthHeaderKey, next: ctxHandler}

	rr := httptest.NewRecorder()
	body := bytes.NewBufferString("body")
	r, _ := http.NewRequest("GET", "/", body)

	privKey, _ := crypto.GenerateKey()
	sig, _ := crypto.Sign(accounts.TextHash(body.Bytes()), privKey)
	addr := crypto.PubkeyToAddress(privKey.PublicKey)
	r.Header.Set(DefaultAuthHeaderKey, fmt.Sprintf("%s:%s", addr, common.Bytes2Hex(sig)))

	handler.ServeHTTP(rr, r)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Nil(t, authContext.Err)
	require.Equal(t, addr, authContext.Caller)
}

type AuthAwareRPC struct{}

func (a *AuthAwareRPC) Run(ctx context.Context) error {
	authContext := AuthFromContext(ctx)
	if authContext == nil || authContext.Err != nil {
		return errors.New("failed")
	}
	return nil
}

func TestAuthHandlerRpcMiddleware(t *testing.T) {
	log := testlog.Logger(t, log.LevelInfo)
	apis := []rpc.API{{Namespace: "test", Service: &AuthAwareRPC{}}}
	rpcServer := oprpc.NewServer("127.0.0.1", 0, "", oprpc.WithAPIs(apis), oprpc.WithMiddleware(AuthMiddleware(log, "auth")))
	require.NoError(t, rpcServer.Start())
	t.Cleanup(func() { _ = rpcServer.Stop() })

	url := fmt.Sprintf("http://%s", rpcServer.Endpoint())
	clnt, err := rpc.Dial(url)
	require.NoError(t, err)
	defer clnt.Close()

	// passthrough auth (default handler does not deny)
	err = clnt.CallContext(context.Background(), nil, "rpc_modules")
	require.Nil(t, err)

	// denied with no header
	err = clnt.CallContext(context.Background(), nil, "test_run")
	require.NotNil(t, err)

	// denied with bad auth header
	clnt.SetHeader("auth", "foobar")
	err = clnt.CallContext(context.Background(), nil, "test_run")
	require.NotNil(t, err)
}

func TestAuthHandlerRequestBodyLimit(t *testing.T) {
	var body []byte
	bodyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	log := testlog.Logger(t, log.LevelInfo)
	handler := authHandler{log: log, headerKey: "auth", next: bodyHandler}

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
