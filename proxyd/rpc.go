package proxyd

import (
	"encoding/json"
	"io"
	"strings"
)

type RPCReq struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      json.RawMessage `json:"id"`
}

type RPCRes struct {
	JSONRPC string
	Result  json.RawMessage
	Error   *RPCErr
	ID      json.RawMessage

	// RawResponse holds the original upstream response bytes for zero-copy passthrough.
	// When set, writeRPCRes will write these bytes directly instead of re-encoding.
	RawResponse []byte
}

type rpcResJSON struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCErr         `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

type nullResultRPCRes struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
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
	Code          int             `json:"code"`
	Message       string          `json:"message"`
	Data          json.RawMessage `json:"data,omitempty"`
	HTTPErrorCode int             `json:"-"`
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
	var rawResult json.RawMessage
	switch typed := result.(type) {
	case nil:
		rawResult = nil
	case json.RawMessage:
		rawResult = typed
	case []byte:
		rawResult = typed
	default:
		rawResult = mustMarshalJSON(typed)
	}

	return &RPCRes{
		JSONRPC: JSONRPCVersion,
		Result:  rawResult,
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
