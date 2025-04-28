package integration_tests

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"testing"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

const dummyHealthyRes = `{"id": 123, "jsonrpc": "2.0", "result": "dummy"}`

const errResTmpl = `{"error":{"code":%d,"message":"%s"},"id":1,"jsonrpc":"2.0"}`

func convertTxToReqParams(tx *types.Transaction) (string, error) {
	var bytes hexutil.Bytes
	bytes, err := tx.MarshalBinary()
	if err != nil {
		return "", err
	}

	return hexutil.Encode(bytes), nil
}

func fakeInteropReqParams() (string, error) {
	toAddress := common.BytesToAddress([]byte{143, 61, 221, 15, 191, 62, 120, 202, 29, 108, 209, 115, 121, 237, 136, 226, 97, 36, 155, 82})

	v, r, s := big.NewInt(0), big.NewInt(0), big.NewInt(0)
	r.SetString("32221253762185627567561170530332760991541284345642488431105080034438681047063", 10)
	s.SetString("53477774121840563707688019836183722736827235081472376095392631194490753506882", 10)

	fakeTx := types.NewTx(&types.AccessListTx{
		ChainID: big.NewInt(420),
		Nonce:   6,
		Value:   big.NewInt(0),
		To:      &toAddress,
		V:       v,
		R:       r,
		S:       s,
		AccessList: types.AccessList{
			{
				Address: params.InteropCrossL2InboxAddress,
				StorageKeys: []common.Hash{
					common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000"),
					common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
				},
			},
		},
	})

	return convertTxToReqParams(fakeTx)
}

func TestInteropValidationSurvivingPath(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	errResp1 := fmt.Sprintf(errResTmpl, -320600, errors.New("conflicting data"))
	badValidatingBackend1 := NewMockBackend(SingleResponseHandler(429, errResp1))
	defer badValidatingBackend1.Close()

	goodValidatingBackend1 := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodValidatingBackend1.Close()

	url1 := badValidatingBackend1.URL()
	url2 := goodValidatingBackend1.URL()

	require.NoError(t, os.Setenv("VALIDATING_BACKEND_RPC_URL_1", url1))
	require.NoError(t, os.Setenv("VALIDATING_BACKEND_RPC_URL_2", url2))

	config := ReadConfig("interop_validation")
	config.InteropValidationConfig.Urls = []string{url1, url2}
	config.SenderRateLimit.Limit = math.MaxInt // Don't perform rate limiting in this test since we're only testing interop validation.
	client := NewProxydClient("http://127.0.0.1:8545")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	fakeInteropReqParams, err := fakeInteropReqParams()
	require.NoError(t, err)

	// running the same request 5 times to avoid lucky flakes of the request never getting routed to the bad backend
	for i := 0; i < 5; i++ {
		res1, code1, err := client.SendRequest(makeSendRawTransaction(fakeInteropReqParams))
		require.NoError(t, err, "iteration %d", i)
		require.Equal(t, 200, code1, "iteration %d: response observed: %s", i, string(res1))
		RequireEqualJSON(t, []byte(dummyHealthyRes), res1)
	}
}

func TestInteropValidationBadPath(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	errResp1 := fmt.Sprintf(errResTmpl, -320600, errors.New("conflicting data"))
	badValidatingBackend1 := NewMockBackend(SingleResponseHandler(409, errResp1))
	defer badValidatingBackend1.Close()

	errResp2 := fmt.Sprintf(errResTmpl, -321501, errors.New("data corruption"))
	badValidatingBackend2 := NewMockBackend(SingleResponseHandler(400, errResp2))
	defer badValidatingBackend2.Close()

	require.NoError(t, os.Setenv("VALIDATING_BACKEND_RPC_URL_1", badValidatingBackend1.URL()))
	require.NoError(t, os.Setenv("VALIDATING_BACKEND_RPC_URL_2", badValidatingBackend2.URL()))

	config := ReadConfig("interop_validation")
	config.SenderRateLimit.Limit = math.MaxInt // Don't perform rate limiting in this test since we're only testing interop validation.
	config.InteropValidationConfig.Urls = []string{badValidatingBackend1.URL(), badValidatingBackend2.URL()}
	client := NewProxydClient("http://127.0.0.1:8545")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	fakeInteropReqParams, err := fakeInteropReqParams()
	require.NoError(t, err)

	res1, code1, err := client.SendRequest(makeSendRawTransaction(fakeInteropReqParams))
	require.NoError(t, err)
	require.Contains(t, []int{409, 400}, code1)
	if code1 == 429 {
		RequireEqualJSON(t, []byte(errResp1), res1)
	} else {
		RequireEqualJSON(t, []byte(errResp2), res1)
	}
}
