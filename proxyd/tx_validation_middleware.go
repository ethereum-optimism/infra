package proxyd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

const (
	maxBundleTransactions            = 100
	defaultValidationTimeoutSeconds  = 5
	defaultValidationMaxIdleConns    = 10
	defaultValidationIdleConnTimeout = 30 * time.Second
	defaultValidationMaxConnsPerHost = 10
)

// TxValidationFunc validates a batch of transactions and returns a map of tx hashes to unauthorized status.
// The endpoint is the middleware service URL, and payload is the request body to send.
type TxValidationFunc func(ctx context.Context, endpoint string, payload []byte) (map[string]bool, error)

// TxValidationMiddlewareConfig configures the transaction validation middleware.
type TxValidationMiddlewareConfig struct {
	// Enabled determines whether the middleware is active
	Enabled bool `toml:"enabled"`

	// Endpoint is the URL of the validation middleware service
	Endpoint string `toml:"endpoint"`

	// Methods is the list of RPC methods to apply validation to.
	// Defaults to ["eth_sendRawTransaction", "eth_sendRawTransactionConditional", "eth_sendBundle"] if not specified.
	Methods []string `toml:"methods"`

	// TimeoutSeconds is the timeout for validation HTTP requests. Defaults to 5 seconds.
	TimeoutSeconds int `toml:"timeout_seconds"`

	// FailOpen determines whether transactions should be allowed through if the validation
	// service returns an error. Defaults to true for safety (fail-open).
	FailOpen *bool `toml:"fail_open"`
}

// TxValidationClient is a reusable HTTP client for validation requests.
type TxValidationClient struct {
	client  *http.Client
	timeout time.Duration
}

// NewTxValidationClient creates a new validation client with the given timeout.
func NewTxValidationClient(timeoutSeconds int) *TxValidationClient {
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultValidationTimeoutSeconds
	}
	return &TxValidationClient{
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:    defaultValidationMaxIdleConns,
				MaxConnsPerHost: defaultValidationMaxConnsPerHost,
				IdleConnTimeout: defaultValidationIdleConnTimeout,
			},
		},
		timeout: time.Duration(timeoutSeconds) * time.Second,
	}
}

// Validate performs the HTTP request to the validation middleware service.
// Returns a map of tx hashes to unauthorized status.
func (c *TxValidationClient) Validate(ctx context.Context, endpoint string, payload []byte) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var validationRes txValidationResponse
	if err := json.Unmarshal(body, &validationRes); err != nil {
		return nil, err
	}

	if msg := validationRes.ErrorCode + validationRes.ErrorMessage; msg != "" {
		return nil, ErrInternal
	}
	return validationRes.Unauthorized, nil
}

// txValidationResponse represents the response from the validation middleware.
type txValidationResponse struct {
	Unauthorized map[string]bool `json:"unauthorized"`
	ErrorCode    string          `json:"errorCode"`
	ErrorMessage string          `json:"errorMessage"`
}

// buildValidationPayload builds a batch request payload mapping tx hashes to flattened tx objects with "from".
func buildValidationPayload(txsWithSenders map[string]map[string]interface{}) ([]byte, error) {
	return json.Marshal(txsWithSenders)
}

type TxValidationMethodSet map[string]struct{}

func (s TxValidationMethodSet) Contains(method string) bool {
	_, ok := s[method]
	return ok
}

func NewTxValidationMethodSet(methods []string) TxValidationMethodSet {
	set := make(TxValidationMethodSet, len(methods))
	for _, m := range methods {
		set[m] = struct{}{}
	}
	return set
}

var DefaultTxValidationMethods = []string{
	"eth_sendRawTransaction",
	"eth_sendRawTransactionConditional",
	"eth_sendBundle",
}

func defaultTxValidationMethods() TxValidationMethodSet {
	return NewTxValidationMethodSet(DefaultTxValidationMethods)
}

func validateTransactions(
	ctx context.Context,
	txs []*types.Transaction,
	endpoint string,
	validationFn TxValidationFunc,
	failOpen bool,
) error {
	if len(txs) > maxBundleTransactions {
		log.Warn("bundle contains too many transactions",
			"req_id", GetReqID(ctx),
			"tx_count", len(txs),
			"max_allowed", maxBundleTransactions)
		return ErrInvalidParams(fmt.Sprintf("bundle contains %d transactions, maximum allowed is %d", len(txs), maxBundleTransactions))
	}

	txsWithSenders, err := buildTxsWithSenders(ctx, txs)
	if err != nil {
		return err
	}

	payload, err := buildValidationPayload(txsWithSenders)
	if err != nil {
		log.Error("error building validation payload", "err", err, "req_id", GetReqID(ctx))
		return ErrInternal
	}

	unauthorized, validationErr := validationFn(ctx, endpoint, payload)
	if validationErr != nil {
		if failOpen {
			log.Warn("tx validation service error, allowing transactions through (fail_open=true)",
				"req_id", GetReqID(ctx),
				"error", validationErr,
				"tx_count", len(txs),
			)
			return nil
		}
		log.Warn("tx validation service error, rejecting transactions (fail_open=false)",
			"req_id", GetReqID(ctx),
			"error", validationErr,
			"tx_count", len(txs),
		)
		return ErrInternal
	}

	for txHash, isUnauthorized := range unauthorized {
		if isUnauthorized {
			txData := txsWithSenders[txHash]
			log.Info("transaction rejected by validation middleware",
				"req_id", GetReqID(ctx),
				"from", txData["from"],
				"tx_hash", txHash,
			)
			return ErrTransactionRejected
		}
	}
	return nil
}

// buildTxsWithSenders builds a map of tx hashes to flattened tx objects with "from" field added.
func buildTxsWithSenders(ctx context.Context, txs []*types.Transaction) (map[string]map[string]interface{}, error) {
	result := make(map[string]map[string]interface{}, len(txs))
	for _, tx := range txs {
		from, err := getSender(tx)
		if err != nil {
			log.Debug("could not get sender from transaction for validation", "err", err, "req_id", GetReqID(ctx))
			return nil, ErrInvalidParams(err.Error())
		}

		// Marshal tx to JSON, then unmarshal to map to get all fields
		txJSON, err := tx.MarshalJSON()
		if err != nil {
			log.Debug("could not marshal transaction for validation", "err", err, "req_id", GetReqID(ctx))
			return nil, ErrInternal
		}

		var txMap map[string]interface{}
		if err := json.Unmarshal(txJSON, &txMap); err != nil {
			log.Debug("could not unmarshal transaction for validation", "err", err, "req_id", GetReqID(ctx))
			return nil, ErrInternal
		}

		// Add "from" at the same level as other tx fields
		txMap["from"] = from.Hex()
		result[tx.Hash().Hex()] = txMap
	}
	return result, nil
}

// getSender derives the sender address from a signed transaction.
func getSender(tx *types.Transaction) (common.Address, error) {
	var signer types.Signer
	if tx.ChainId().Sign() == 0 {
		signer = new(types.HomesteadSigner)
	} else {
		signer = types.LatestSignerForChainID(tx.ChainId())
	}
	return types.Sender(signer, tx)
}

func transactionsFromBundleReq(ctx context.Context, req *RPCReq) ([]*types.Transaction, error) {
	var params []json.RawMessage
	if err := json.Unmarshal(req.Params, &params); err != nil {
		log.Debug("error unmarshalling bundle params", "err", err, "req_id", GetReqID(ctx))
		return nil, ErrParseErr
	}
	if len(params) < 1 {
		log.Debug("bundle request missing params", "req_id", GetReqID(ctx))
		return nil, ErrInvalidParams("missing bundle params")
	}
	var bundle struct {
		Txs []string `json:"txs"`
	}
	if err := json.Unmarshal(params[0], &bundle); err != nil {
		log.Debug("error unmarshalling bundle object", "err", err, "req_id", GetReqID(ctx))
		return nil, ErrInvalidParams(err.Error())
	}
	if len(bundle.Txs) == 0 {
		log.Debug("bundle has no txs", "req_id", GetReqID(ctx))
		return nil, ErrInvalidParams("bundle has no txs")
	}
	txs := make([]*types.Transaction, 0, len(bundle.Txs))
	for i, txHex := range bundle.Txs {
		tx, err := decodeSignedTx(ctx, txHex)
		if err != nil {
			log.Debug("failed to decode tx in bundle", "index", i, "err", err, "req_id", GetReqID(ctx))
			return nil, err
		}
		txs = append(txs, tx)
	}
	return txs, nil
}

func decodeSignedTx(ctx context.Context, txHex string) (*types.Transaction, error) {
	var data hexutil.Bytes
	if err := data.UnmarshalText([]byte(txHex)); err != nil {
		log.Debug("error decoding raw tx data", "err", err, "req_id", GetReqID(ctx))
		return nil, ErrInvalidParams(err.Error())
	}

	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(data); err != nil {
		log.Debug("could not unmarshal transaction", "err", err, "req_id", GetReqID(ctx))
		return nil, ErrInvalidParams(err.Error())
	}
	return tx, nil
}
