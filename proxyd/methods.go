package proxyd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/log"
	"golang.org/x/exp/maps"
)

type RPCMethodHandler interface {
	GetRPCMethod(context.Context, *RPCReq) (*RPCRes, error)
	PutRPCMethod(context.Context, *RPCReq, *RPCRes) error
}

type StaticMethodHandler struct {
	cache     Cache
	m         sync.RWMutex
	filterGet func(*RPCReq) bool
	filterPut func(*RPCReq, *RPCRes) bool
}

func (e *StaticMethodHandler) key(req *RPCReq, headersToForward http.Header) (string, error) {
	// signature is the hashed json.RawMessage param contents
	h := sha256.New()
	h.Write(req.Params)

	if len(headersToForward) != 0 {
		headers := maps.Keys(headersToForward)
		slices.Sort(headers)

		checksum := bytes.NewBufferString("")
		for _, h := range headers {
			values, ok := headersToForward[h]
			if !ok {
				return "", ErrAllowedHeaderNotFound
			}

			valuesCopy := slices.Clone(values)
			slices.Sort(valuesCopy)

			checksum.WriteString(h)
			for _, v := range valuesCopy {
				checksum.WriteString(v)
			}
		}

		h.Write(checksum.Bytes())
	}

	signature := fmt.Sprintf("%x", h.Sum(nil))
	return strings.Join([]string{"cache", req.Method, signature}, ":"), nil
}

func (e *StaticMethodHandler) GetRPCMethod(ctx context.Context, req *RPCReq) (*RPCRes, error) {
	if e.cache == nil {
		return nil, nil
	}
	if e.filterGet != nil && !e.filterGet(req) {
		return nil, nil
	}

	e.m.RLock()
	defer e.m.RUnlock()

	headersToForward := GetHeadersToForward(ctx)
	key, err := e.key(req, headersToForward)
	if err != nil {
		log.Error("error generating key for request", "method", req.Method, "err", err)
		return nil, err
	}
	val, err := e.cache.Get(ctx, key)
	if err != nil {
		log.Error("error reading from cache", "key", key, "method", req.Method, "err", err)
		return nil, err
	}
	if val == "" {
		return nil, nil
	}

	var result interface{}
	if err := json.Unmarshal([]byte(val), &result); err != nil {
		log.Error("error unmarshalling value from cache", "key", key, "method", req.Method, "err", err)
		return nil, err
	}
	return &RPCRes{
		JSONRPC: req.JSONRPC,
		Result:  result,
		ID:      req.ID,
	}, nil
}

func (e *StaticMethodHandler) PutRPCMethod(ctx context.Context, req *RPCReq, res *RPCRes) error {
	if e.cache == nil {
		return nil
	}
	// if there is a filter on get, we don't want to cache it because its irretrievable
	if e.filterGet != nil && !e.filterGet(req) {
		return nil
	}
	// response filter
	if e.filterPut != nil && !e.filterPut(req, res) {
		return nil
	}

	e.m.Lock()
	defer e.m.Unlock()

	headersToForward := GetHeadersToForward(ctx)
	key, err := e.key(req, headersToForward)
	if err != nil {
		log.Error("error generating key for request", "method", req.Method, "err", err)
		return err
	}
	value := mustMarshalJSON(res.Result)

	err = e.cache.Put(ctx, key, string(value))
	if err != nil {
		log.Error("error putting into cache", "key", key, "method", req.Method, "err", err)
		return err
	}
	return nil
}
