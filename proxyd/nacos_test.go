package proxyd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nacos-group/nacos-sdk-go/common/constant"
	"github.com/stretchr/testify/assert"
)

func TestServerConfigsFromUrls(t *testing.T) {
	urls := "online-nacosapi.okg.com:80,online-nacosapi2.okg.com:123"
	configs, err := getServerConfigs(urls)

	assert.Nil(t, err)
	assert.Equal(t, configs, []constant.ServerConfig{
		{
			Scheme:      "",
			ContextPath: "",
			IpAddr:      "online-nacosapi.okg.com",
			Port:        80,
		},
		{
			Scheme:      "",
			ContextPath: "",
			IpAddr:      "online-nacosapi2.okg.com",
			Port:        123,
		},
	})
}

func TestManyResolveIPAndPort(t *testing.T) {
	testcases := []struct {
		input         string
		expectedIps   []string
		expectedPorts []uint64
	}{
		{
			// Single declaration should not fail.
			"127.0.0.1:7001",
			[]string{GetLocalIP()},
			[]uint64{defaultPort},
		},
		{
			// Defaults to local IP.
			"127.0.0.1:7001,127.0.0.1:7002",
			[]string{GetLocalIP(), GetLocalIP()},
			[]uint64{defaultPort, defaultPort},
		},
		// {
		// 	// This should panic on start (to warn about misconfiguration)
		// 	"127.0.0.3:7001,127.0.0.2:7002",
		// 	[]string{},
		// 	[]uint64{7001},
		// },
		{
			// Same IPs, different ports.
			"127.0.0.2:7001,127.0.0.2:7002",
			[]string{"127.0.0.2", "127.0.0.2"},
			[]uint64{7001, 7002},
		},
	}

	for _, tc := range testcases {
		addrs := strings.Split(tc.input, ",")

		gotIps := []string{}
		gotPorts := []uint64{}
		for j := 0; j < len(addrs); j++ {
			gotIp, gotPort, err := ResolveIPAndPort(addrs[j])
			assert.Nil(t, err)

			gotIps = append(gotIps, gotIp)
			gotPorts = append(gotPorts, gotPort)
		}

		assert.Equal(t, gotIps, tc.expectedIps,
			fmt.Sprintf("Failed on %s. Expected %v, got %v.", tc.input, tc.expectedIps, gotIps),
		)
		assert.Equal(t, gotPorts, tc.expectedPorts,
			fmt.Sprintf("Failed on %s. Expected %v, got %v.", tc.input, tc.expectedPorts, gotPorts),
		)
	}
}
