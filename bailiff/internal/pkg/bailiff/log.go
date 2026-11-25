package bailiff

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/ethereum-optimism/optimism/op-service/log"
)

func RequestID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

type reqIDKey struct{}

func ReqIDContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, reqIDKey{}, RequestID())
}

func ReqIDLogger(ctx context.Context, l log.Logger) log.Logger {
	reqIDRaw := ctx.Value(reqIDKey{})
	reqID, ok := reqIDRaw.(string)
	if !ok {
		reqID = "unknown"
	}
	return l.New("requestID", reqID)
}
