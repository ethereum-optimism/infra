package proxyd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"golang.org/x/sync/errgroup"
)

const (
	maxBundleTransactions            = 100
	defaultValidationTimeoutSeconds  = 5
	defaultValidationMaxIdleConns    = 10
	defaultValidationIdleConnTimeout = 30 * time.Second
	defaultValidationMaxConnsPerHost = 10
)

// TxValidationFunc validates a transaction and returns true if it should be rejected.
// The endpoint is the middleware service URL, and payload is the request body to send.
type TxValidationFunc func(ctx context.Context, endpoint string, payload []byte) (bool, error)

// TxFieldMapping defines how to extract a field from a transaction and map it to the middleware request.
type TxFieldMapping struct {
	// SourceField is the transaction field to extract (e.g., "from", "to", "value", "data")
	SourceField string `toml:"source_field"`
	// TargetField is the field name in the middleware request body
	TargetField string `toml:"target_field"`
}

// TxValidationMiddlewareConfig configures the transaction validation middleware.
type TxValidationMiddlewareConfig struct {
	// Enabled determines whether the middleware is active
	Enabled bool `toml:"enabled"`

	// Endpoint is the URL of the validation middleware service
	Endpoint string `toml:"endpoint"`

	// Methods is the list of RPC methods to apply validation to.
	// Defaults to ["eth_sendRawTransaction", "eth_sendBundle"] if not specified.
	Methods []string `toml:"methods"`

	// FieldMappings defines how to transform the transaction into the middleware request format.
	// If empty, the full transaction object is sent as-is.
	// Example: [{ source_field = "from", target_field = "address" }] will send {"address": "<from_address>"}
	FieldMappings []TxFieldMapping `toml:"field_mappings"`

	// TimeoutSeconds is the timeout for validation HTTP requests. Defaults to 5 seconds.
	TimeoutSeconds int `toml:"timeout_seconds"`
}

// TxValidationClient is a reusable HTTP client for validation requests.
type TxValidationClient struct {
	client  *http.Client
	timeout time.Duration
}

var (
	defaultTxValidationClient     *TxValidationClient
	defaultTxValidationClientOnce sync.Once
)

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

// getDefaultTxValidationClient returns the shared default validation client.
func getDefaultTxValidationClient() *TxValidationClient {
	defaultTxValidationClientOnce.Do(func() {
		defaultTxValidationClient = NewTxValidationClient(defaultValidationTimeoutSeconds)
	})
	return defaultTxValidationClient
}

// Validate performs the HTTP request to the validation middleware service.
func (c *TxValidationClient) Validate(ctx context.Context, endpoint string, payload []byte) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	var validationRes txValidationResponse
	if err := json.Unmarshal(body, &validationRes); err != nil {
		return false, err
	}

	if msg := validationRes.ErrorCode + validationRes.ErrorMessage; msg != "" {
		return false, ErrInternal
	}
	return validationRes.Block, nil
}

// txValidation is the default validation function using the shared client.
func txValidation(ctx context.Context, endpoint string, payload []byte) (bool, error) {
	return getDefaultTxValidationClient().Validate(ctx, endpoint, payload)
}

// txValidationResponse represents the response from the validation middleware.
type txValidationResponse struct {
	Block        bool   `json:"block"`
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
}

// creates the request payload for the validation middleware.
// If fieldMappings is empty, it returns the full transaction as JSON.
// Otherwise, it extracts the specified fields and maps them to the target field names.
func buildValidationPayload(tx *types.Transaction, from common.Address, fieldMappings []TxFieldMapping) ([]byte, error) {
	if len(fieldMappings) == 0 {
		return buildFullTxPayload(tx, from)
	}
	return buildMappedPayload(tx, from, fieldMappings)
}

// creates a payload containing the full transaction details, including 'from' address
func buildFullTxPayload(tx *types.Transaction, from common.Address) ([]byte, error) {
	payload := map[string]interface{}{
		"tx":   tx,
		"from": from.Hex(),
	}
	return json.Marshal(payload)
}

// creates a payload with only the mapped fields.
func buildMappedPayload(tx *types.Transaction, from common.Address, mappings []TxFieldMapping) ([]byte, error) {
	payload := make(map[string]interface{})

	for _, mapping := range mappings {
		value := extractTxField(tx, from, mapping.SourceField)
		if value != nil {
			payload[mapping.TargetField] = value
		}
	}

	return json.Marshal(payload)
}

// extractTxField extracts a field value from the transaction.
func extractTxField(tx *types.Transaction, from common.Address, field string) interface{} {
	switch field {
	case "from":
		return from.Hex()
	case "to":
		if tx.To() != nil {
			return tx.To().Hex()
		}
		return nil
	case "value":
		return tx.Value().String()
	case "data":
		return common.Bytes2Hex(tx.Data())
	case "nonce":
		return tx.Nonce()
	case "gas":
		return tx.Gas()
	case "gasPrice":
		return tx.GasPrice().String()
	case "chainId":
		return tx.ChainId().String()
	case "hash":
		return tx.Hash().Hex()
	case "type":
		return tx.Type()
	case "maxFeePerGas":
		if tx.Type() >= types.DynamicFeeTxType {
			return tx.GasFeeCap().String()
		}
		return nil
	case "maxPriorityFeePerGas":
		if tx.Type() >= types.DynamicFeeTxType {
			return tx.GasTipCap().String()
		}
		return nil
	default:
		return nil
	}
}

// TxValidationMethodSet is a set of methods that should be validated.
type TxValidationMethodSet map[string]struct{}

// Contains checks if the given method should be validated.
func (s TxValidationMethodSet) Contains(method string) bool {
	_, ok := s[method]
	return ok
}

// NewTxValidationMethodSet creates a set from a slice of methods.
func NewTxValidationMethodSet(methods []string) TxValidationMethodSet {
	set := make(TxValidationMethodSet, len(methods))
	for _, m := range methods {
		set[m] = struct{}{}
	}
	return set
}

// defaultTxValidationMethods returns the default set of methods to validate.
func defaultTxValidationMethods() TxValidationMethodSet {
	return TxValidationMethodSet{
		"eth_sendRawTransaction": {},
		"eth_sendBundle":         {},
	}
}

// validateTransactions validates multiple transactions in parallel using errgroup.
func validateTransactions(
	ctx context.Context,
	txs []*types.Transaction,
	endpoint string,
	fieldMappings []TxFieldMapping,
	validationFn TxValidationFunc,
) error {
	if len(txs) > maxBundleTransactions {
		log.Warn("bundle contains too many transactions",
			"req_id", GetReqID(ctx),
			"tx_count", len(txs),
			"max_allowed", maxBundleTransactions)
		return ErrInvalidParams(fmt.Sprintf("bundle contains %d transactions, maximum allowed is %d", len(txs), maxBundleTransactions))
	}

	var g errgroup.Group
	for _, tx := range txs {
		tx := tx // capture loop variable
		g.Go(func() error {
			return validateSingleTransaction(ctx, tx, endpoint, fieldMappings, validationFn)
		})
	}
	return g.Wait()
}

// validateSingleTransaction validates a single transaction against the middleware service.
func validateSingleTransaction(
	ctx context.Context,
	tx *types.Transaction,
	endpoint string,
	fieldMappings []TxFieldMapping,
	validationFn TxValidationFunc,
) error {
	var signer types.Signer
	if tx.ChainId().Sign() == 0 {
		signer = new(types.HomesteadSigner)
	} else {
		signer = types.LatestSignerForChainID(tx.ChainId())
	}

	from, err := types.Sender(signer, tx)
	if err != nil {
		log.Debug("could not get sender from transaction for validation", "err", err, "req_id", GetReqID(ctx))
		return ErrInvalidParams(err.Error())
	}

	payload, err := buildValidationPayload(tx, from, fieldMappings)
	if err != nil {
		log.Error("error building validation payload", "err", err, "req_id", GetReqID(ctx))
		return ErrInternal
	}

	block, validationErr := validationFn(ctx, endpoint, payload)
	if validationErr != nil {
		log.Warn("tx validation service error, allowing transaction through",
			"req_id", GetReqID(ctx),
			"from", from.Hex(),
			"error", validationErr,
			"chain_id", tx.ChainId(),
			"nonce", tx.Nonce(),
			"value", tx.Value(),
			"tx_hash", tx.Hash().Hex(),
		)
		return nil
	}

	if block {
		log.Info("transaction rejected by validation middleware",
			"req_id", GetReqID(ctx),
			"from", from.Hex(),
			"tx_hash", tx.Hash().Hex(),
		)
		return ErrTransactionRejected
	}
	return nil
}

// transactionsFromBundleReq extracts transactions from an eth_sendBundle request.
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

// decodeSignedTx decodes a hex-encoded signed transaction.
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
