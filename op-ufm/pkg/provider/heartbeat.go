package provider

import (
	"context"
	"time"

	"github.com/ethereum-optimism/optimism/op-ufm/pkg/metrics"
	"github.com/ethereum-optimism/optimism/op-ufm/pkg/metrics/clients"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/log"
	"github.com/pkg/errors"
)

// Heartbeat polls for expected in-flight transactions
func (p *Provider) Heartbeat(ctx context.Context) {
	log.Debug("heartbeat",
		"provider", p.name,
		"count", len(p.txPool.Transactions))

	metrics.RecordTransactionsInFlight(p.config.Network, len(p.txPool.Transactions))

	// let's exclude transactions already seen by this provider, or originated by it
	expectedTransactions := make([]*TransactionState, 0, len(p.txPool.Transactions))
	alreadySeen := 0
	for _, st := range p.txPool.Transactions {
		if st.ProviderSource == p.name {
			continue
		}
		if _, exist := st.SeenBy[p.name]; exist {
			alreadySeen++
			continue
		}
		expectedTransactions = append(expectedTransactions, st)
	}

	if len(expectedTransactions) == 0 {
		log.Debug("no expected txs",
			"count", len(p.txPool.Transactions),
			"provider", p.name,
			"alreadySeen", alreadySeen)
		return
	}

	client, err := clients.Dial(p.name, p.config.URL)
	if err != nil {
		log.Error("cant dial to provider",
			"provider", p.name,
			"url", p.config.URL,
			"err", err)
	}

	log.Debug("checking in-flight tx",
		"count", len(p.txPool.Transactions),
		"provider", p.name,
		"alreadySeen", alreadySeen)
	for _, st := range expectedTransactions {
		hash := st.Hash.Hex()

		_, isPending, err := client.TransactionByHash(ctx, st.Hash)
		if err != nil && !errors.Is(err, ethereum.NotFound) {
			log.Error("cant check transaction",
				"provider", p.name,
				"hash", hash,
				"url", p.config.URL,
				"err", err)
			continue
		}

		log.Debug("got transaction",
			"provider", p.name,
			"hash", hash,
			"isPending", isPending)

		// mark transaction as seen by this provider
		st.M.Lock()
		latency := time.Since(st.SentAt)
		if st.FirstSeen.IsZero() {
			st.FirstSeen = time.Now()
			metrics.RecordFirstSeenLatency(st.ProviderSource, p.name, latency)
			log.Info("transaction first seen",
				"hash", hash,
				"firstSeenLatency", latency,
				"providerSource", st.ProviderSource,
				"providerSeen", p.name)
		}
		if _, exist := st.SeenBy[p.name]; !exist {
			st.SeenBy[p.name] = time.Now()
			metrics.RecordProviderToProviderLatency(st.ProviderSource, p.name, latency)
		}
		st.M.Unlock()

		// check if transaction have been seen by all providers
		p.txPool.M.Lock()
		if len(st.SeenBy) == p.txPool.Expected {
			log.Debug("transaction seen by all",
				"hash", hash,
				"expected", p.txPool.Expected,
				"seenBy", len(st.SeenBy))
			delete(p.txPool.Transactions, st.Hash.Hex())
		}
		p.txPool.M.Unlock()
	}
}
