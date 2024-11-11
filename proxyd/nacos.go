package proxyd

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/nacos-group/nacos-sdk-go/clients"
	"github.com/nacos-group/nacos-sdk-go/common/constant"
	"github.com/nacos-group/nacos-sdk-go/vo"
)

const (
	defaultPort           = 26659
	defaultTimeoutMs      = uint64(5000)
	defaultListenInterval = uint64(10000)
	defaultWeight         = float64(10)
)

// StartNacosClient start nacos client and register rest service in nacos
func StartNacosClient(urls string, namespace string, name string, externalIP string, externalPorts string) {
	serverConfigs, err := getServerConfigs(urls)
	if err != nil {
		log.Error(fmt.Sprintf("failed to resolve nacos server url %s: %s", urls, err.Error()))
		return
	}
	client, err := clients.CreateNamingClient(map[string]interface{}{
		"serverConfigs": serverConfigs,
		"clientConfig": constant.ClientConfig{
			TimeoutMs:           defaultTimeoutMs,
			ListenInterval:      defaultListenInterval,
			NotLoadCacheAtStart: true,
			NamespaceId:         namespace, // same namespace is shared for all server configs
			LogDir:              "/dev/null",
			LogLevel:            "error",
		},
	})
	if err != nil {
		log.Error(fmt.Sprintf("failed to create nacos client. error: %s", err.Error()))
		return
	}

	appNames := strings.Split(name, ",")
	ports := strings.Split(externalPorts, ",")
	if len(appNames) != len(ports) {
		panic(fmt.Sprintf("Nacos: number of app names not equal to number of external addresses."))
	}

	for i := 0; i < len(ports); i++ {
		if externalIP == "127.0.0.1" {
			externalIP = GetLocalIP()
		}

		port, err := strconv.ParseUint(ports[i], 10, 64)
		if err != nil {
			log.Error(fmt.Sprintf("failed to convert port %s error: %s", ports[i], err.Error()))
			return
		}

		// Register on each ip,port instance.
		_, err = client.RegisterInstance(vo.RegisterInstanceParam{
			Ip:          externalIP,
			Port:        port,
			ServiceName: appNames[i],
			Weight:      defaultWeight,
			ClusterName: "DEFAULT",
			Enable:      true,
			Healthy:     true,
			Ephemeral:   true,
			Metadata: map[string]string{
				"preserved.register.source": "GO",
				"app_registry_tag":          strconv.FormatInt(time.Now().Unix(), 10),
			},
		})
		if err != nil {
			log.Error(fmt.Sprintf("failed to register instance in nacos server. error: %s", err.Error()))
			return
		}
	}

	log.Info("register application instance in nacos successfully")
}

// ResolveIPAndPort resolve ip and port from addr
// If 127.0.0.1, the same default port will be used.
func ResolveIPAndPort(addr string) (string, uint64, error) {
	laddr := strings.Split(addr, ":")
	ip := laddr[0]
	if ip == "127.0.0.1" {
		return GetLocalIP(), defaultPort, nil
	}

	port, err := strconv.ParseUint(laddr[1], 10, 64)
	if err != nil {
		return "", 0, err
	}
	return ip, port, nil
}

// GetLocalIP get local ip
func GetLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}

func getServerConfigs(urls string) ([]constant.ServerConfig, error) {
	// nolint
	var configs []constant.ServerConfig
	for _, url := range strings.Split(urls, ",") {
		laddr, serverPort, err := ResolveIPAndPort(url)
		if err != nil {
			return nil, fmt.Errorf("Err parsing host: %+w", err)
		}
		configs = append(configs, constant.ServerConfig{
			IpAddr: laddr,
			Port:   serverPort,
		})
	}
	return configs, nil
}
