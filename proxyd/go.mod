module github.com/ethereum-optimism/infra/proxyd

go 1.22.0

toolchain go1.23.6

require (
	github.com/BurntSushi/toml v1.4.0
	github.com/alicebob/miniredis v2.5.0+incompatible
	github.com/emirpasic/gods v1.18.1
	github.com/ethereum/go-ethereum v0.0.0-00010101000000-000000000000
	github.com/go-redsync/redsync/v4 v4.10.0
	github.com/golang/snappy v0.0.5-0.20220116011046-fa5810519dcb
	github.com/gorilla/mux v1.8.0
	github.com/gorilla/websocket v1.5.0
	github.com/hashicorp/golang-lru v1.0.2
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.17.0
	github.com/redis/go-redis/v9 v9.2.1
	github.com/rs/cors v1.11.0
	github.com/stretchr/testify v1.10.0
	github.com/syndtr/goleveldb v1.0.1-0.20210819022825-2ae1ddf74ef7
	github.com/xaionaro-go/weightedshuffle v0.0.0-20211213010739-6a74fbc7d24a
	golang.org/x/sync v0.10.0
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/ethereum/go-ethereum => github.com/ethereum-optimism/op-geth v1.101503.4-rc.1

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/VictoriaMetrics/fastcache v1.12.2 // indirect
	github.com/alicebob/gopher-json v0.0.0-20230218143504-906a9b012302 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bits-and-blooms/bitset v1.17.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/consensys/bavard v0.1.22 // indirect
	github.com/consensys/gnark-crypto v0.14.0 // indirect
	github.com/crate-crypto/go-ipa v0.0.0-20240724233137-53bbb0ceb27a // indirect
	github.com/crate-crypto/go-kzg-4844 v1.1.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/deckarep/golang-set/v2 v2.6.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/ethereum/c-kzg-4844 v1.0.0 // indirect
	github.com/ethereum/go-verkle v0.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/gofrs/flock v0.8.1 // indirect
	github.com/gomodule/redigo v1.8.9 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/holiman/bloomfilter/v2 v2.0.3 // indirect
	github.com/holiman/uint256 v1.3.2 // indirect
	github.com/klauspost/compress v1.17.11 // indirect
	github.com/mattn/go-runewidth v0.0.15 // indirect
	github.com/matttproud/golang_protobuf_extensions/v2 v2.0.0 // indirect
	github.com/mmcloughlin/addchain v0.4.0 // indirect
	github.com/naoina/go-stringutil v0.1.0 // indirect
	github.com/naoina/toml v0.1.2-0.20170918210437-9fafd6967416 // indirect
	github.com/olekukonko/tablewriter v0.0.5 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_model v0.5.0 // indirect
	github.com/prometheus/common v0.45.0 // indirect
	github.com/prometheus/procfs v0.12.0 // indirect
	github.com/rivo/uniseg v0.4.4 // indirect
	github.com/shirou/gopsutil v3.21.11+incompatible // indirect
	github.com/supranational/blst v0.3.14 // indirect
	github.com/tklauser/go-sysconf v0.3.12 // indirect
	github.com/tklauser/numcpus v0.6.1 // indirect
	github.com/yuin/gopher-lua v1.1.0 // indirect
	github.com/yusufpapurcu/wmi v1.2.3 // indirect
	golang.org/x/crypto v0.32.0 // indirect
	golang.org/x/exp v0.0.0-20231110203233-9a3e6036ecaa // indirect
	golang.org/x/sys v0.29.0 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
	rsc.io/tmplfunc v0.0.3 // indirect
)
