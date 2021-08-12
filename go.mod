module github.com/textileio/bidbot

go 1.16

require (
	github.com/dustin/go-humanize v1.0.0
	github.com/filecoin-project/go-address v0.0.5
	github.com/filecoin-project/go-fil-commcid v0.1.0 // indirect
	github.com/filecoin-project/go-jsonrpc v0.1.4-0.20210217175800-45ea43ac2bec
	github.com/filecoin-project/go-state-types v0.1.1-0.20210506134452-99b279731c48
	github.com/filecoin-project/lotus v1.10.0
	github.com/gogo/status v1.1.0
	github.com/golang/snappy v0.0.3-0.20201103224600-674baa8c7fc3 // indirect
	github.com/google/uuid v1.2.0
	github.com/huin/goupnp v1.0.1-0.20210310174557-0ca763054c88 // indirect
	github.com/ipfs/go-cid v0.0.7
	github.com/ipfs/go-datastore v0.4.5
	github.com/ipfs/go-ipfs-blockstore v1.0.4
	github.com/ipfs/go-ipfs-cmds v0.6.0 // indirect
	github.com/ipfs/go-ipfs-http-client v0.1.0 // indirect
	github.com/ipfs/go-ipfs-util v0.0.2
	github.com/ipfs/go-ipld-cbor v0.0.5
	github.com/ipfs/go-ipld-format v0.2.0
	github.com/ipld/go-car v0.1.1-0.20201119040415-11b6074b6d4d
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/joho/godotenv v1.3.0
	github.com/libp2p/go-libp2p-connmgr v0.2.4
	github.com/libp2p/go-libp2p-core v0.8.5
	github.com/multiformats/go-multiaddr v0.3.3
	github.com/multiformats/go-multiaddr-dns v0.3.1
	github.com/multiformats/go-multibase v0.0.3
	github.com/multiformats/go-multihash v0.0.15
	github.com/oklog/ulid/v2 v2.0.2
	github.com/shirou/gopsutil v2.20.5+incompatible // indirect
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	github.com/spf13/viper v1.8.1
	github.com/stretchr/testify v1.7.0
	github.com/syndtr/goleveldb v1.0.1-0.20210305035536-64b5b1c73954 // indirect
	github.com/textileio/go-datastore-extensions v1.0.1
	github.com/textileio/go-ds-badger3 v0.0.0-20210324034212-7b7fb3be3d1c
	github.com/textileio/go-ds-mongo v0.1.4
	github.com/textileio/go-libp2p-pubsub-rpc v0.0.4
	github.com/textileio/go-log/v2 v2.1.3-gke-1
	github.com/urfave/cli/v2 v2.3.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/runtime v0.21.0
	go.opentelemetry.io/otel/exporters/prometheus v0.21.0
	go.opentelemetry.io/otel/metric v0.21.0
	go.opentelemetry.io/otel/sdk/export/metric v0.21.0
	go.opentelemetry.io/otel/sdk/metric v0.21.0
	go.uber.org/zap v1.18.1
	google.golang.org/grpc v1.39.0
	google.golang.org/protobuf v1.27.1
)

replace github.com/kilic/bls12-381 => github.com/kilic/bls12-381 v0.0.0-20200820230200-6b2c19996391

replace github.com/ipfs/go-ipns => github.com/ipfs/go-ipns v0.0.2
