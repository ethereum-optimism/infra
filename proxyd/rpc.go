package proxyd

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

type RPCReq struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      json.RawMessage `json:"id"`
}

type RPCRes struct {
	JSONRPC string
	Result  interface{}
	Error   *RPCErr
	ID      json.RawMessage
}

type rpcResJSON struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *RPCErr         `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

type nullResultRPCRes struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  interface{}     `json:"result"`
	ID      json.RawMessage `json:"id"`
}

func (r *RPCRes) IsError() bool {
	return r.Error != nil
}

func (r *RPCRes) MarshalJSON() ([]byte, error) {
	if r.Result == nil && r.Error == nil {
		return json.Marshal(&nullResultRPCRes{
			JSONRPC: r.JSONRPC,
			Result:  nil,
			ID:      r.ID,
		})
	}

	return json.Marshal(&rpcResJSON{
		JSONRPC: r.JSONRPC,
		Result:  r.Result,
		Error:   r.Error,
		ID:      r.ID,
	})
}

type RPCErr struct {
	Code          int    `json:"code"`
	Message       string `json:"message"`
	Data          string `json:"data,omitempty"`
	HTTPErrorCode int    `json:"-"`
}

func (r *RPCErr) Error() string {
	return r.Message
}

func (r *RPCErr) Clone() *RPCErr {
	return &RPCErr{
		Code:          r.Code,
		Message:       r.Message,
		HTTPErrorCode: r.HTTPErrorCode,
	}
}

func IsValidID(id json.RawMessage) bool {
	// handle the case where the ID is a string
	if strings.HasPrefix(string(id), "\"") && strings.HasSuffix(string(id), "\"") {
		return len(id) > 2
	}

	// technically allows a boolean/null ID, but so does Geth
	// https://github.com/ethereum/go-ethereum/blob/master/rpc/json.go#L72
	return len(id) > 0 && id[0] != '{' && id[0] != '['
}

func ParseRPCReq(body []byte) (*RPCReq, error) {
	req := new(RPCReq)
	if err := json.Unmarshal(body, req); err != nil {
		return nil, ErrParseErr
	}

	return req, nil
}

func ParseBatchRPCReq(body []byte) ([]json.RawMessage, error) {
	batch := make([]json.RawMessage, 0)
	if err := json.Unmarshal(body, &batch); err != nil {
		return nil, err
	}

	return batch, nil
}

func ParseRPCRes(r io.Reader) (*RPCRes, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, wrapErr(err, "error reading RPC response")
	}

	res := new(RPCRes)
	if err := json.Unmarshal(body, res); err != nil {
		return nil, wrapErr(err, "error unmarshalling RPC response")
	}

	return res, nil
}

func ValidateRPCReq(req *RPCReq) error {
	if req.JSONRPC != JSONRPCVersion {
		return ErrInvalidRequest("invalid JSON-RPC version")
	}

	if req.Method == "" {
		return ErrInvalidRequest("no method specified")
	}

	if !IsValidID(req.ID) {
		return ErrInvalidRequest("invalid ID")
	}

	return nil
}

func NewRPCErrorRes(id json.RawMessage, err error) *RPCRes {
	var rpcErr *RPCErr
	if rr, ok := err.(*RPCErr); ok {
		rpcErr = rr
	} else {
		rpcErr = &RPCErr{
			Code:    JSONRPCErrorInternal,
			Message: err.Error(),
		}
	}

	return &RPCRes{
		JSONRPC: JSONRPCVersion,
		Error:   rpcErr,
		ID:      id,
	}
}

func NewRPCRes(id json.RawMessage, result interface{}) *RPCRes {
	return &RPCRes{
		JSONRPC: JSONRPCVersion,
		Result:  result,
		ID:      id,
	}
}

func IsBatch(raw []byte) bool {
	for _, c := range raw {
		// skip insignificant whitespace (http://www.ietf.org/rfc/rfc4627.txt)
		if c == 0x20 || c == 0x09 || c == 0x0a || c == 0x0d {
			continue
		}
		return c == '['
	}
	return false
}

type BlockNumberTracker interface {
	GetLatestBlockNumber() (uint64, bool)
	GetSafeBlockNumber() (uint64, bool)
	GetFinalizedBlockNumber() (uint64, bool)
}

type Range struct {
	FromBlock uint64
	ToBlock   uint64
}

func ParseRange(req *RPCReq, tracker BlockNumberTracker) *Range {
	switch req.Method {
	case "eth_getLogs", "eth_newFilter":
		var p []map[string]interface{}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil
		}
		fromBlock, hasFrom := p[0]["fromBlock"].(string)
		toBlock, hasTo := p[0]["toBlock"].(string)
		if !hasFrom && !hasTo {
			return nil
		}
		// if either fromBlock or toBlock is defined, default the other to "latest" if unset
		if hasFrom && !hasTo {
			toBlock = "latest"
		} else if hasTo && !hasFrom {
			fromBlock = "latest"
		}
		from, fromOk := stringToBlockNumber(fromBlock, tracker)
		to, toOk := stringToBlockNumber(toBlock, tracker)
		if !fromOk || !toOk {
			return nil
		}
		return &Range{
			FromBlock: from,
			ToBlock:   to,
		}
	default:
		return nil
	}
}

func stringToBlockNumber(tag string, tracker BlockNumberTracker) (uint64, bool) {
	switch tag {
	case "latest":
		return tracker.GetLatestBlockNumber()
	case "safe":
		return tracker.GetSafeBlockNumber()
	case "finalized":
		return tracker.GetFinalizedBlockNumber()
	case "earliest":
		return 0, true
	case "pending":
		latest, ok := tracker.GetLatestBlockNumber()
		return latest + 1, ok
	}
	d, err := hexutil.DecodeUint64(tag)
	return d, err == nil
}
