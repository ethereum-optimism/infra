package service

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"

	optls "github.com/ethereum-optimism/optimism/op-service/tls"
	"github.com/stretchr/testify/assert"
)

func TestAuthMiddleware_NoCertificate(t *testing.T) {
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NotNil(t, nil, "handler should not have been invoked")
	})
	handler = NewAuthMiddleware()(handler)
	handler = optls.NewPeerTLSMiddleware(handler)
	assert.HTTPStatusCode(t, handler.ServeHTTP, "GET", "/", nil, 401)
}

func TestAuthMiddleware_CertificateMissingDNS(t *testing.T) {
	req, _ := http.NewRequest("GET", "/", nil)
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{}}

	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NotNil(t, nil, "handler should not have been invoked")
	})

	handler = NewAuthMiddleware()(handler)
	handler = optls.NewPeerTLSMiddleware(handler)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	result := rr.Result()
	defer result.Body.Close()
	assert.Equal(t, 401, result.StatusCode)
}

func TestAuthMiddleware_HappyPath(t *testing.T) {
	req, _ := http.NewRequest("GET", "/", nil)
	req.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{{DNSNames: []string{"client.oplabs.co"}}},
	}

	handlerInvoked := false
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientInfo := ClientInfoFromContext(r.Context())
		assert.Equal(t, "client.oplabs.co", clientInfo.ClientName)
		handlerInvoked = true
	})

	handler = NewAuthMiddleware()(handler)
	handler = optls.NewPeerTLSMiddleware(handler)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	result := rr.Result()
	defer result.Body.Close()
	assert.Equal(t, 200, result.StatusCode)
	assert.True(t, handlerInvoked)
}
