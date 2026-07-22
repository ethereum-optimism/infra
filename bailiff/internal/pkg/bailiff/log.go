package bailiff

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/ethereum/go-ethereum/log"
)

func RequestID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}

func ReqIDContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, "requestID", RequestID())
}

func ReqIDLogger(ctx context.Context, l log.Logger) log.Logger {
	reqIDRaw := ctx.Value("requestID")
	reqID, ok := reqIDRaw.(string)
	if !ok {
		reqID = "unknown"
	}
	return l.New("requestID", reqID)
}
