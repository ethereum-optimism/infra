package proxyd

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"
)

// Phase constants for the four main timing phases
const (
	PhaseRecvToUpstreamStart     = "recv_to_upstream_start"
	PhaseUpstreamRoundtrip       = "upstream_roundtrip"
	PhaseProxyRecvToDownstream   = "proxy_recv_to_downstream_start"
	PhaseDownstreamSend          = "downstream_send"
)

// Step name constants for sub-processes
const (
	StepReadDownstreamBody  = "read_downstream_body"
	StepParseRequest        = "parse_request"
	StepRouteAndValidate    = "route_and_validate"
	StepCacheGet            = "cache_get"
	StepUpstreamForward     = "upstream_forward"
	StepCachePut            = "cache_put"
	StepUpstreamAcquireSlot = "upstream_acquire_slot"
	StepUpstreamHttpDo      = "upstream_http_do"
	StepUpstreamReadBody    = "upstream_read_body"
	StepUpstreamUnmarshal   = "upstream_unmarshal"
	StepDownstreamEncode    = "downstream_encode"
)

type contextKeyTiming struct{}

// TimingStep represents a single timing measurement within a request
type TimingStep struct {
	Seq        int               `json:"seq"`
	Name       string            `json:"name"`
	Parent     string            `json:"parent,omitempty"`
	DurationMs int64             `json:"duration_ms"`
	Meta       map[string]any    `json:"meta,omitempty"`
	StartTime  time.Time         `json:"-"`
}

// RPCTiming tracks timing information for an RPC request
type RPCTiming struct {
	mu           sync.Mutex
	RequestStart time.Time
	Steps        []TimingStep
	nextSeq      int

	// Phase markers for computing the four main durations
	UpstreamStartTime    time.Time
	UpstreamEndTime      time.Time
	DownstreamStartTime  time.Time
	DownstreamEndTime    time.Time

	// Request metadata
	IsBatch       bool
	BatchSize     int
	ServedBy      string
	CacheHitCount int
	CacheMissCount int
	Status        string
	Error         string
}

// NewRPCTiming creates a new timing tracker
func NewRPCTiming() *RPCTiming {
	return &RPCTiming{
		RequestStart: time.Now(),
		Steps:        make([]TimingStep, 0),
		Status:       "ok",
	}
}

// StartStep begins timing a named step and returns a function to end it
func (t *RPCTiming) StartStep(name, parent string, meta map[string]any) func() {
	t.mu.Lock()
	seq := t.nextSeq
	t.nextSeq++
	startTime := time.Now()
	t.mu.Unlock()

	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.Steps = append(t.Steps, TimingStep{
			Seq:        seq,
			Name:       name,
			Parent:     parent,
			DurationMs: time.Since(startTime).Milliseconds(),
			Meta:       meta,
			StartTime:  startTime,
		})
	}
}

// RecordStep records a completed step with known duration
func (t *RPCTiming) RecordStep(name, parent string, durationMs int64, meta map[string]any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Steps = append(t.Steps, TimingStep{
		Seq:        t.nextSeq,
		Name:       name,
		Parent:     parent,
		DurationMs: durationMs,
		Meta:       meta,
	})
	t.nextSeq++
}

// MarkUpstreamStart marks the time when upstream forwarding begins
func (t *RPCTiming) MarkUpstreamStart() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.UpstreamStartTime = time.Now()
}

// MarkUpstreamEnd marks the time when upstream forwarding completes
func (t *RPCTiming) MarkUpstreamEnd() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.UpstreamEndTime = time.Now()
}

// MarkDownstreamStart marks the time when downstream response sending begins
func (t *RPCTiming) MarkDownstreamStart() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.DownstreamStartTime = time.Now()
}

// MarkDownstreamEnd marks the time when downstream response sending completes
func (t *RPCTiming) MarkDownstreamEnd() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.DownstreamEndTime = time.Now()
}

// SetBatchInfo sets batch-related metadata
func (t *RPCTiming) SetBatchInfo(isBatch bool, batchSize int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.IsBatch = isBatch
	t.BatchSize = batchSize
}

// SetServedBy sets the backend that served the request
func (t *RPCTiming) SetServedBy(servedBy string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ServedBy = servedBy
}

// IncrCacheHit increments the cache hit counter
func (t *RPCTiming) IncrCacheHit() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.CacheHitCount++
}

// IncrCacheMiss increments the cache miss counter
func (t *RPCTiming) IncrCacheMiss() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.CacheMissCount++
}

// SetStatus sets the request status
func (t *RPCTiming) SetStatus(status string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = status
}

// SetError sets the error message
func (t *RPCTiming) SetError(err string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Error = err
	if t.Status == "ok" {
		t.Status = "error"
	}
}

// ComputePhaseDurations computes the four main phase durations in milliseconds
func (t *RPCTiming) ComputePhaseDurations() (recvToUpstream, upstreamRoundtrip, proxyRecvToDownstream, downstreamSend int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()

	// Phase 1: From request received to upstream start
	if !t.UpstreamStartTime.IsZero() {
		recvToUpstream = t.UpstreamStartTime.Sub(t.RequestStart).Milliseconds()
	}

	// Phase 2: Upstream roundtrip (from upstream start to upstream end)
	if !t.UpstreamStartTime.IsZero() && !t.UpstreamEndTime.IsZero() {
		upstreamRoundtrip = t.UpstreamEndTime.Sub(t.UpstreamStartTime).Milliseconds()
	}

	// Phase 3: From request received to downstream start (total processing before sending)
	if !t.DownstreamStartTime.IsZero() {
		proxyRecvToDownstream = t.DownstreamStartTime.Sub(t.RequestStart).Milliseconds()
	}

	// Phase 4: Downstream send duration
	if !t.DownstreamStartTime.IsZero() {
		endTime := t.DownstreamEndTime
		if endTime.IsZero() {
			endTime = now
		}
		downstreamSend = endTime.Sub(t.DownstreamStartTime).Milliseconds()
	}

	return
}

// EmitLog outputs the aggregate timing log
func (t *RPCTiming) EmitLog(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()

	totalMs := time.Since(t.RequestStart).Milliseconds()
	recvToUpstream, upstreamRoundtrip, proxyRecvToDownstream, downstreamSend := int64(0), int64(0), int64(0), int64(0)

	if !t.UpstreamStartTime.IsZero() {
		recvToUpstream = t.UpstreamStartTime.Sub(t.RequestStart).Milliseconds()
	}
	if !t.UpstreamStartTime.IsZero() && !t.UpstreamEndTime.IsZero() {
		upstreamRoundtrip = t.UpstreamEndTime.Sub(t.UpstreamStartTime).Milliseconds()
	}
	if !t.DownstreamStartTime.IsZero() {
		proxyRecvToDownstream = t.DownstreamStartTime.Sub(t.RequestStart).Milliseconds()
	}
	if !t.DownstreamStartTime.IsZero() && !t.DownstreamEndTime.IsZero() {
		downstreamSend = t.DownstreamEndTime.Sub(t.DownstreamStartTime).Milliseconds()
	}

	// Serialize steps to JSON for structured logging
	stepsJSON, _ := json.Marshal(t.Steps)

	logArgs := []any{
		"req_id", GetReqID(ctx),
		"auth", GetAuthCtx(ctx),
		"is_batch", t.IsBatch,
		"batch_size", t.BatchSize,
		"served_by", t.ServedBy,
		"status", t.Status,
		"total_ms", totalMs,
		"t_recv_to_upstream_start_ms", recvToUpstream,
		"t_upstream_roundtrip_ms", upstreamRoundtrip,
		"t_proxy_recv_to_downstream_start_ms", proxyRecvToDownstream,
		"t_downstream_send_ms", downstreamSend,
		"cache_hit_count", t.CacheHitCount,
		"cache_miss_count", t.CacheMissCount,
		"steps", string(stepsJSON),
	}

	if t.Error != "" {
		logArgs = append(logArgs, "error", t.Error)
	}

	log.Info("rpc request timing", logArgs...)
}

// WithTiming adds an RPCTiming to the context
func WithTiming(ctx context.Context, t *RPCTiming) context.Context {
	return context.WithValue(ctx, contextKeyTiming{}, t)
}

// GetTiming retrieves the RPCTiming from context
func GetTiming(ctx context.Context) *RPCTiming {
	if t, ok := ctx.Value(contextKeyTiming{}).(*RPCTiming); ok {
		return t
	}
	return nil
}
