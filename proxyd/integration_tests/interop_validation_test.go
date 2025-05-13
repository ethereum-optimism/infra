package integration_tests

import (
	"fmt"
	"math"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	supervisorTypes "github.com/ethereum-optimism/optimism/op-supervisor/supervisor/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/types/interoptypes"
	"github.com/ethereum/go-ethereum/crypto"
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

func fakeTxBuilder(txModifiers ...func(tx *types.AccessListTx)) *types.Transaction {
	checksumArgs := supervisorTypes.ChecksumArgs{
		BlockNumber: 3519561,
		Timestamp:   1746536469,
		LogIndex:    1,
		ChainID:     eth.ChainIDFromUInt64(420120003),
		LogHash: supervisorTypes.PayloadHashToLogHash(
			crypto.Keccak256Hash([]byte("Hello, World!")),
			common.HexToAddress("0x7A23c3fC3dA9a5364b97E0e4c47E7777BaE5C8Cd"),
		),
	}

	accessListEntries := supervisorTypes.EncodeAccessList([]supervisorTypes.Access{
		checksumArgs.Access(),
	})

	toAddress := common.HexToAddress("0x8f3Ddd0FBf3e78CA1D6cd17379eD88E261249B53")

	v, r, s := big.NewInt(0), big.NewInt(0), big.NewInt(0)
	r.SetString("32221253762185627567561170530332760991541284345642488431105080034438681047063", 10)
	s.SetString("53477774121840563707688019836183722736827235081472376095392631194490753506882", 10)

	accessListArgument := &types.AccessListTx{
		ChainID: big.NewInt(420120003),
		Nonce:   6,
		Value:   big.NewInt(0),
		To:      &toAddress,
		V:       v,
		R:       r,
		S:       s,
		AccessList: types.AccessList{
			{
				Address:     params.InteropCrossL2InboxAddress,
				StorageKeys: accessListEntries,
			},
		},
	}

	for _, opt := range txModifiers {
		opt(accessListArgument)
	}

	return types.NewTx(accessListArgument)
}

func TestInteropValidation_NormalFlow(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	errResp1 := fmt.Sprintf(errResTmpl, -32000, supervisorTypes.ErrConflict.Error())
	badValidatingBackend1 := NewMockBackend(SingleResponseHandler(409, errResp1))
	defer badValidatingBackend1.Close()

	errResp2 := fmt.Sprintf(errResTmpl, -32000, supervisorTypes.ErrDataCorruption.Error())
	badValidatingBackend2 := NewMockBackend(SingleResponseHandler(400, errResp2))
	defer badValidatingBackend2.Close()

	goodValidatingBackend1 := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodValidatingBackend1.Close()

	badSupervisorUrl1 := badValidatingBackend1.URL()
	badSupervisorUrl2 := badValidatingBackend2.URL()
	goodSupervisorUrl := goodValidatingBackend1.URL()

	config := ReadConfig("interop_validation")
	config.SenderRateLimit.Limit = math.MaxInt // Don't perform rate limiting in this test since we're only testing interop validation.

	expectedErrResp1 := fmt.Sprintf(errResTmpl, -320600, supervisorTypes.ErrConflict.Error())       // although the backend returns -32000, proxyd should correctly map it to -320600
	expectedErrResp2 := fmt.Sprintf(errResTmpl, -321501, supervisorTypes.ErrDataCorruption.Error()) // although the backend returns -32000, proxyd should correctly map it to -321501

	type respDetails struct {
		code         int
		jsonResponse []byte
	}
	type testCase struct {
		name                  string
		strategy              proxyd.InteropValidationStrategy
		urls                  []string
		expectedResp          respDetails
		possibilities         []respDetails
		multiplePossibilities bool
	}
	cases := []testCase{
		{
			name:     "first-supervisor strategy with first url returning error",
			strategy: proxyd.FirstSupervisorStrategy,
			urls:     []string{badSupervisorUrl1, goodSupervisorUrl},
			expectedResp: respDetails{
				code:         409,
				jsonResponse: []byte(expectedErrResp1),
			},
		},
		{
			name:     "default strategy with first url returning success",
			strategy: proxyd.EmptyStrategy,
			urls:     []string{goodSupervisorUrl, badSupervisorUrl1},
			expectedResp: respDetails{
				code:         200,
				jsonResponse: []byte(dummyHealthyRes),
			},
		},
		{
			name:     "multicall strategy with atleast one good url",
			strategy: proxyd.MulticallStrategy,
			urls:     []string{badSupervisorUrl1, goodSupervisorUrl},
			expectedResp: respDetails{
				code:         200,
				jsonResponse: []byte(dummyHealthyRes),
			},
		},
		{
			name:                  "multicall strategy with all bad urls",
			strategy:              proxyd.MulticallStrategy,
			urls:                  []string{badSupervisorUrl1, badSupervisorUrl2},
			multiplePossibilities: true,
			possibilities: []respDetails{
				{
					code:         409, // http code corresponding to supervisorTypes.ErrDataCorruption from interopRPCErrorMap
					jsonResponse: []byte(expectedErrResp1),
				},
				{
					code:         422, // http code corresponding to supervisorTypes.ErrDataCorruption from interopRPCErrorMap
					jsonResponse: []byte(expectedErrResp2),
				},
			},
		},
	}

	fakeInteropReqParams, err := convertTxToReqParams(fakeTxBuilder())
	require.NoError(t, err)

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			config.InteropValidationConfig.Strategy = c.strategy
			config.InteropValidationConfig.Urls = c.urls
			_, shutdown, err := proxyd.Start(config)
			require.NoError(t, err)
			defer shutdown()

			client := NewProxydClient("http://127.0.0.1:8545")
			for i := 0; i < 5; i++ {
				sendRawTransaction := makeSendRawTransaction(fakeInteropReqParams)
				observedResp, observedCode, err := client.SendRequest(sendRawTransaction)
				require.NoError(t, err, "iteration %d", i)

				if c.multiplePossibilities {
					onePossibilityMatched := false
					for _, expectedResp := range c.possibilities {
						if expectedResp.code == observedCode {
							RequireEqualJSON(t, expectedResp.jsonResponse, observedResp)
							onePossibilityMatched = true
							break
						}
					}
					require.True(t, onePossibilityMatched, "could not find any expectated possibility matching the observed response code: observed status code %d", observedCode)
				} else {
					require.Equal(t, c.expectedResp.code, observedCode, "iteration %d: response observed: %s", i, string(observedResp))
					RequireEqualJSON(t, c.expectedResp.jsonResponse, observedResp)
				}
			}
		})
	}
}

func TestInteropValidation_ReqSizeLimit(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	type testCase struct {
		name                   string
		reqSizeLimit           int
		expectedHTTPCode       int
		expectedRpcCode        int
		expectedErrSubStr      string
		expectedCallsToBackend int
	}
	cases := []testCase{
		{
			name:                   "Req params size limit of 1 byte",
			reqSizeLimit:           1,
			expectedHTTPCode:       413,
			expectedRpcCode:        -32021,
			expectedErrSubStr:      "request body too large",
			expectedCallsToBackend: 0,
		},
		{
			name:                   "Req params size limit of 1000 bytes (2000 hex characters)",
			reqSizeLimit:           1000,
			expectedHTTPCode:       200,
			expectedErrSubStr:      "",
			expectedCallsToBackend: 1,
		},
		{
			name:                   "Req params size limit of 0 or not provided",
			expectedHTTPCode:       200,
			expectedErrSubStr:      "",
			expectedCallsToBackend: 1,
		},
	}

	config := ReadConfig("interop_validation")
	config.SenderRateLimit.Limit = math.MaxInt // Don't perform rate limiting in this test since we're only testing interop validation.

	fakeInteropReqParams, err := convertTxToReqParams(fakeTxBuilder())
	require.NoError(t, err)

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			validatingBackend1 := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
			defer validatingBackend1.Close()

			validatingBackend2 := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
			defer validatingBackend2.Close()

			config.InteropValidationConfig.ReqSizeLimit = c.reqSizeLimit
			config.InteropValidationConfig.Urls = []string{validatingBackend1.URL(), validatingBackend2.URL()}

			_, shutdown, err := proxyd.Start(config)
			require.NoError(t, err)
			defer shutdown()

			client := NewProxydClient("http://127.0.0.1:8545")
			sendRawTransaction := makeSendRawTransaction(fakeInteropReqParams)
			observedResp, observedCode, err := client.SendRequest(sendRawTransaction)
			require.NoError(t, err)

			require.Equal(t, c.expectedHTTPCode, observedCode)
			require.Contains(t, string(observedResp), c.expectedErrSubStr)

			if c.expectedRpcCode != 0 {
				require.Contains(t, string(observedResp), fmt.Sprintf("\"code\":%d", c.expectedRpcCode))
			}

			require.Equal(t, len(validatingBackend1.requests), c.expectedCallsToBackend)
		})
	}
}

func TestInteropValidation_AccessListSizeLimit(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	type testCase struct {
		name                   string
		accessListSizeLimit    int
		expectedHTTPCode       int
		expectedRpcCode        int
		expectedErrSubStr      string
		expectedCallsToBackend int
	}
	cases := []testCase{
		{
			name:                   "access list size limit of 1",
			accessListSizeLimit:    1,
			expectedHTTPCode:       413,
			expectedRpcCode:        -32022,
			expectedErrSubStr:      "access list out of bounds",
			expectedCallsToBackend: 0,
		},
		{
			name:                   "access list size limit of 2",
			accessListSizeLimit:    2,
			expectedHTTPCode:       200,
			expectedErrSubStr:      "",
			expectedCallsToBackend: 1,
		},
		{
			name:                   "access list size limit of 0 or not provided",
			expectedHTTPCode:       200,
			expectedErrSubStr:      "",
			expectedCallsToBackend: 1,
		},
	}

	config := ReadConfig("interop_validation")
	config.SenderRateLimit.Limit = math.MaxInt // Don't perform rate limiting in this test since we're only testing interop validation.

	fakeInteropReqParams, err := convertTxToReqParams(fakeTxBuilder())
	require.NoError(t, err)

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			validatingBackend1 := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
			defer validatingBackend1.Close()

			validatingBackend2 := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
			defer validatingBackend2.Close()

			config.InteropValidationConfig.AccessListSizeLimit = c.accessListSizeLimit
			config.InteropValidationConfig.Urls = []string{validatingBackend1.URL(), validatingBackend2.URL()}

			_, shutdown, err := proxyd.Start(config)
			require.NoError(t, err)
			defer shutdown()

			client := NewProxydClient("http://127.0.0.1:8545")
			sendRawTransaction := makeSendRawTransaction(fakeInteropReqParams)
			observedResp, observedCode, err := client.SendRequest(sendRawTransaction)
			require.NoError(t, err)

			require.Equal(t, c.expectedHTTPCode, observedCode)
			require.Contains(t, string(observedResp), c.expectedErrSubStr)

			if c.expectedRpcCode != 0 {
				require.Contains(t, string(observedResp), fmt.Sprintf("\"code\":%d", c.expectedRpcCode))
			}

			require.Equal(t, len(validatingBackend1.requests), c.expectedCallsToBackend)
		})
	}
}

func TestInteropValidation_Deduplication(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	config := ReadConfig("interop_validation")
	config.SenderRateLimit.Limit = math.MaxInt // Don't perform basic rate limiting in this test since we're only testing interop validation.

	config.InteropValidationConfig.AccessListSizeLimit = 2 // only 2 entries are allowed in the access list

	validatingBackend1 := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer validatingBackend1.Close()

	validatingBackend2 := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer validatingBackend2.Close()

	config.InteropValidationConfig.Urls = []string{validatingBackend1.URL(), validatingBackend2.URL()}

	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	client := NewProxydClient("http://127.0.0.1:8545")

	fakeTx := fakeTxBuilder(func(tx *types.AccessListTx) {
		oneAccessListEntry := tx.AccessList[0]

		// duplicate the access list itself with same entries
		tx.AccessList = append(tx.AccessList, oneAccessListEntry)

		// duplicate intra-access list entries
		tx.AccessList[0].StorageKeys = append(tx.AccessList[0].StorageKeys, oneAccessListEntry.StorageKeys...)
	})

	{
		// assert that the (duplicated) access list is now 3x the size of the original
		// and higher than the access list size limit
		fakeTxStorageEntries := interoptypes.TxToInteropAccessList(fakeTx)
		require.Equal(t, len(fakeTxStorageEntries), 6)
		require.Greater(t, len(fakeTxStorageEntries), config.InteropValidationConfig.AccessListSizeLimit)
	}

	fakeInteropReqParams, err := convertTxToReqParams(fakeTx)
	require.NoError(t, err)

	sendRawTransaction := makeSendRawTransaction(fakeInteropReqParams)
	_, observedCode, err := client.SendRequest(sendRawTransaction)

	// yet the call succeeds because the deduplicated access list is still within the access list size limit
	require.NoError(t, err)
	require.Equal(t, 200, observedCode)
	require.Equal(t, len(validatingBackend1.requests), 1)
}

func TestInteropValidation_StaticParseAccessPrevalidationCheck(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	config := ReadConfig("interop_validation")
	config.SenderRateLimit.Limit = math.MaxInt // Don't perform basic rate limiting in this test since we're only testing interop validation.

	config.InteropValidationConfig.AccessListSizeLimit = 2 // only 2 entries are allowed in the access list

	validatingBackend1 := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer validatingBackend1.Close()

	config.InteropValidationConfig.Urls = []string{validatingBackend1.URL()}

	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	client := NewProxydClient("http://127.0.0.1:8545")

	// subroutine for checking the bad path
	// i.e. a request with an invalid access list that is rejected by the ParseAccess check itself
	// without needing to reach the validating backend (supervisor)
	{
		wrongTx := fakeTxBuilder(func(tx *types.AccessListTx) {
			// make the access list's storage keys invalid enough to be failed by the ParseAccess check itself
			tx.AccessList[0].StorageKeys = []common.Hash{
				common.HexToHash("0x123"),
				common.HexToHash("0x456"),
			}
		})

		wrongInteropReqParams, err := convertTxToReqParams(wrongTx)
		require.NoError(t, err)

		sendRawTransactionWithWrongAccessList := makeSendRawTransaction(wrongInteropReqParams)
		observedResp, observedCode, err := client.SendRequest(sendRawTransactionWithWrongAccessList)
		require.NoError(t, err)
		require.Equal(t, 400, observedCode)
		require.Contains(t, string(observedResp), fmt.Sprintf("\"code\":%d", -32602))

		// request failed aptly without needing to reach the validating backend (supervisor)
		require.Equal(t, len(validatingBackend1.requests), 0)
	}

	// subroutine for checking the good path
	// i.e. a request with a valid access list that is validated by the ParseAccess check itself
	// is allowed to the validating backend (supervisor) for potentially other validation checks which can only be performed by the supervisor
	{
		rightTx := fakeTxBuilder()
		rightInteropReqParams, err := convertTxToReqParams(rightTx)
		require.NoError(t, err)

		sendRawTransactionWithRightAccessList := makeSendRawTransaction(rightInteropReqParams)
		_, observedCode, err := client.SendRequest(sendRawTransactionWithRightAccessList)
		require.NoError(t, err)
		require.Equal(t, 200, observedCode)
		require.Equal(t, len(validatingBackend1.requests), 1)
	}

}

func TestInteropValidation_SenderRateLimit(t *testing.T) {
	goodBackend := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer goodBackend.Close()

	require.NoError(t, os.Setenv("GOOD_BACKEND_RPC_URL", goodBackend.URL()))

	config := ReadConfig("interop_validation")
	config.SenderRateLimit.Limit = math.MaxInt // Don't perform basic rate limiting in this test since we're only testing interop validation.

	config.InteropValidationConfig.RateLimit.Enabled = true
	config.InteropValidationConfig.RateLimit.Limit = 1

	validatingBackend1 := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer validatingBackend1.Close()

	validatingBackend2 := NewMockBackend(SingleResponseHandler(200, dummyHealthyRes))
	defer validatingBackend2.Close()

	config.InteropValidationConfig.Urls = []string{validatingBackend1.URL(), validatingBackend2.URL()}

	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	client := NewProxydClient("http://127.0.0.1:8545")
	fakeInteropReqParams, err := convertTxToReqParams(fakeTxBuilder())
	require.NoError(t, err)
	sendRawTransaction := makeSendRawTransaction(fakeInteropReqParams)
	_, observedCode1, err1 := client.SendRequest(sendRawTransaction)
	observedResp2, observedCode2, err2 := client.SendRequest(sendRawTransaction)

	// ensuring the first call succeeded
	require.NoError(t, err1)
	require.Equal(t, 200, observedCode1)

	require.Equal(t, len(validatingBackend1.requests), 1)

	// ensuring the second call failed due to rate limiting
	require.NoError(t, err2)
	require.Equal(t, 429, observedCode2)
	require.Contains(t, string(observedResp2), "sender is over rate limit")
	require.Contains(t, string(observedResp2), fmt.Sprintf("\"code\":%d", -32017))

	// ensuring that the second call didn't contribute to additional validating backend (supervisor) requests
	require.Equal(t, len(validatingBackend1.requests), 1)

	// waiting for the rate limit to reset
	time.Sleep(1100 * time.Millisecond)

	// ensuring the third call succeeds
	_, observedCode3, err3 := client.SendRequest(sendRawTransaction)
	require.NoError(t, err3)
	require.Equal(t, 200, observedCode3)

	// ensuring that this call did contribute to additional validating backend (supervisor) requests due to being within the rate limit
	require.Equal(t, len(validatingBackend1.requests), 2)
}
