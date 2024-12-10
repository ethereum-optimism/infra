package integration_tests

import (
	"fmt"
	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/stretchr/testify/require"
	"net/http"
	"os"
	"testing"
)

func TestFilterRpcRouting(t *testing.T) {
	backend1 := NewMockBackend(nil)
	backend2 := NewMockBackend(nil)
	defer backend1.Close()
	defer backend2.Close()

	require.NoError(t, os.Setenv("NODE1_URL", backend1.URL()))
	require.NoError(t, os.Setenv("NODE2_URL", backend2.URL()))

	config := ReadConfig("filter_rpc_routing")
	client := NewProxydClient("http://127.0.0.1:8545")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	filterId := "0x11414222354634635214124"
	newFilterResponse := fmt.Sprintf(`{"jsonrpc":"2.0","result":"%s","id":1}`, filterId)
	getFilterChangesResponse1 := `{"jsonrpc":"2.0","result":["0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"],"id":1}`
	getFilterChangesResponse2 := `{"jsonrpc":"2.0","result":["0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"],"id":1}`

	responseQueue := make(chan string, 3)
	responseQueue <- newFilterResponse
	responseQueue <- getFilterChangesResponse1
	responseQueue <- getFilterChangesResponse2

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SingleResponseHandler(200, <-responseQueue)(w, r)
	})

	backend1.SetHandler(handler)
	backend2.SetHandler(handler)

	res, statusCode, err := client.SendRPC("eth_newBlockFilter", nil)
	require.NoError(t, err)
	require.Equal(t, 200, statusCode)

	var selectedBackend *MockBackend
	if len(backend1.Requests()) > 0 {
		selectedBackend = backend1
	} else {
		selectedBackend = backend2
	}

	require.Equal(t, 1, len(selectedBackend.Requests()))
	RequireEqualJSON(t, []byte(newFilterResponse), res)

	res, statusCode, err = client.SendRPC("eth_getFilterChanges", []interface{}{filterId})

	require.Equal(t, 2, len(selectedBackend.Requests()))
	RequireEqualJSON(t, []byte(getFilterChangesResponse1), res)

	res, statusCode, err = client.SendRPC("eth_getFilterChanges", []interface{}{filterId})

	require.Equal(t, 3, len(selectedBackend.Requests()))
	RequireEqualJSON(t, []byte(getFilterChangesResponse2), res)
}
