package integration_tests

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"testing"

	"github.com/alicebob/miniredis"
	"github.com/ethereum-optimism/infra/proxyd"
	ms "github.com/ethereum-optimism/infra/proxyd/tools/mockserver/handler"
	"github.com/stretchr/testify/require"
)

// TestConsensusHARedisURLFromEnv asserts that consensus_ha_redis.url honours the
// "$VAR" form, the same way the top-level [redis].url does. Without env
// expansion the literal "$REDIS_URL" reaches redis.ParseURL and proxyd fails to
// start.
func TestConsensusHARedisURLFromEnv(t *testing.T) {
	redis, err := miniredis.Run()
	require.NoError(t, err)
	defer redis.Close()

	node1 := NewMockBackend(nil)
	defer node1.Close()
	node2 := NewMockBackend(nil)
	defer node2.Close()

	dir, err := os.Getwd()
	require.NoError(t, err)
	responses := path.Join(dir, "testdata/consensus_responses.yml")

	h1 := ms.MockedHandler{Overrides: []*ms.MethodTemplate{}, Autoload: true, AutoloadFile: responses}
	h2 := ms.MockedHandler{Overrides: []*ms.MethodTemplate{}, Autoload: true, AutoloadFile: responses}
	node1.SetHandler(http.HandlerFunc(h1.Handler))
	node2.SetHandler(http.HandlerFunc(h2.Handler))

	require.NoError(t, os.Setenv("NODE1_URL", node1.URL()))
	require.NoError(t, os.Setenv("NODE2_URL", node2.URL()))
	require.NoError(t, os.Setenv("REDIS_URL", fmt.Sprintf("redis://127.0.0.1:%s", redis.Port())))

	config := ReadConfig("consensus_ha")
	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()
}
