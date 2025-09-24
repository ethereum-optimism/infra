package preset

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ethereum-optimism/optimism/op-chain-ops/devkeys"
	"github.com/ethereum-optimism/optimism/op-deployer/pkg/deployer/artifacts"
	"github.com/ethereum-optimism/optimism/op-devstack/devtest"
	"github.com/ethereum-optimism/optimism/op-devstack/stack"
	"github.com/ethereum-optimism/optimism/op-devstack/sysgo"
	"github.com/ethereum-optimism/optimism/op-e2e/e2eutils/intentbuilder"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum/go-ethereum/common"
)

// WithMinimalEmbedded composes a Minimal system identical to sysgo.DefaultMinimalSystem,
// except it uses embedded contract artifacts (no local monorepo required).
func WithMinimalEmbedded() stack.CommonOption {
	ids := sysgo.NewDefaultMinimalSystemIDs(sysgo.DefaultL1ID, sysgo.DefaultL2AID)

	opt := stack.Combine[*sysgo.Orchestrator]()

	opt.Add(stack.BeforeDeploy(func(o *sysgo.Orchestrator) {
		o.P().Logger().Info("Setting up")
	}))

	opt.Add(sysgo.WithMnemonicKeys(devkeys.TestMnemonic))

	// Ensure op-deployer has a cache directory to download/extract artifacts into
	opt.Add(stack.BeforeDeploy(func(o *sysgo.Orchestrator) {
		ucdir, err := os.UserCacheDir()
		o.P().Require().NoError(err, "user cache dir")
		cacheDir := filepath.Join(ucdir, "op-acceptor", "op-deployer-cache")
		o.P().Require().NoError(os.MkdirAll(cacheDir, 0o755), "ensure cache dir")
		o.P().Logger().Info("Configured deployer cache dir", "dir", cacheDir)
		// Append pipeline option so ApplyPipeline receives CacheDir
		sysOpt := sysgo.WithDeployerCacheDir(cacheDir)
		stack.ApplyOptionLifecycle(sysgo.WithDeployerPipelineOption(sysOpt), o)
	}))

	// Deployer with artifacts auto-resolution: OP_ACCEPTOR_ARTIFACTS_DIR (dir) ->
	// OP_ACCEPTOR_ARTIFACTS_URL (tgz) -> embedded artifacts
	opt.Add(sysgo.WithDeployer(), sysgo.WithDeployerOptions(withArtifactsAuto(),
		sysgo.WithCommons(ids.L1.ChainID()),
		sysgo.WithPrefundedL2(ids.L1.ChainID(), ids.L2.ChainID()),
	))

	// Core nodes and services (mirror DefaultMinimalSystem wiring)
	opt.Add(sysgo.WithL1Nodes(ids.L1EL, ids.L1CL))
	opt.Add(sysgo.WithL2ELNode(ids.L2EL))
	opt.Add(sysgo.WithL2CLNode(ids.L2CL, ids.L1CL, ids.L1EL, ids.L2EL, sysgo.L2CLSequencer()))
	opt.Add(sysgo.WithBatcher(ids.L2Batcher, ids.L1EL, ids.L2CL, ids.L2EL))
	opt.Add(sysgo.WithProposer(ids.L2Proposer, ids.L1EL, &ids.L2CL, nil))
	opt.Add(sysgo.WithFaucets([]stack.L1ELNodeID{ids.L1EL}, []stack.L2ELNodeID{ids.L2EL}))
	opt.Add(sysgo.WithTestSequencer(ids.TestSequencer, ids.L1CL, ids.L2CL, ids.L1EL, ids.L2EL))
	// Skip L2 challenger in in-memory acceptance preset to avoid monorepo root resolution

	// Export IDs when evaluated
	opt.Add(stack.Finally(func(orch *sysgo.Orchestrator) {
		_ = ids // IDs are static; keep for parity/forward-compat
		// no-op; callers don’t need the IDs struct exported here
		_ = eth.ChainID{}
	}))

	return stack.MakeCommon(opt)
}

// withArtifactsAuto configures contract locators in priority order:
// 1) OP_ACCEPTOR_ARTIFACTS_DIR (pre-extracted forge-artifacts directory)
// 2) OP_ACCEPTOR_ARTIFACTS_URL (tar.gz of forge-artifacts), downloaded & cached
// 3) Embedded artifacts bundled with the module
func withArtifactsAuto() func(p devtest.P, _ devkeys.Keys, builder intentbuilder.Builder) {
	return func(p devtest.P, _ devkeys.Keys, builder intentbuilder.Builder) {
		if dir := os.Getenv("OP_ACCEPTOR_ARTIFACTS_DIR"); dir != "" {
			if locator, err := artifacts.NewFileLocator(dir); err == nil {
				builder.WithL1ContractsLocator(locator)
				builder.WithL2ContractsLocator(locator)
				p.Logger().Info("Using artifacts from directory", "dir", dir)
				return
			} else {
				p.Logger().Warn("Failed to use OP_ACCEPTOR_ARTIFACTS_DIR, falling back", "error", err)
			}
		}

		if url := os.Getenv("OP_ACCEPTOR_ARTIFACTS_URL"); url != "" {
			// Let op-deployer download/extract as part of the pipeline via a URL locator
			locator := artifacts.MustNewLocatorFromURL(url)
			builder.WithL1ContractsLocator(locator)
			builder.WithL2ContractsLocator(locator)
			p.Logger().Info("Using artifacts from URL", "url", url)
			return
		}

		// Fallback: use fixed URL for artifacts
		contentHash := common.HexToHash("8001f021c81159c33c1dcb41cae0cd35ddb1c62ddfa49f04eaf2ae1be98890c9") // Sep 17, 2025, 9:50:12 AM
		url := fmt.Sprintf("https://storage.googleapis.com/oplabs-contract-artifacts/artifacts-v1-%x.tar.gz", contentHash)
		locator := artifacts.MustNewLocatorFromURL(url)
		builder.WithL1ContractsLocator(locator)
		builder.WithL2ContractsLocator(locator)
		p.Logger().Info("Using default artifacts URL", "url", url)
	}
}
