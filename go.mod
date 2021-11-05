module github.com/textileio/bidbot

go 1.16

require (
	github.com/dustin/go-humanize v1.0.0
	github.com/facebookgo/ensure v0.0.0-20200202191622-63f1cf65ac4c // indirect
	github.com/facebookgo/stack v0.0.0-20160209184415-751773369052 // indirect
	github.com/facebookgo/subset v0.0.0-20200203212716-c811ad88dec4 // indirect
	github.com/facebookgo/symwalk v0.0.0-20150726040526-42004b9f3222
	github.com/facebookgo/testname v0.0.0-20150612200628-5443337c3a12 // indirect
	github.com/filecoin-project/go-address v0.0.5
	github.com/filecoin-project/go-amt-ipld/v2 v2.1.1-0.20201006184820-924ee87a1349 // indirect
	github.com/filecoin-project/go-jsonrpc v0.1.4-0.20210217175800-45ea43ac2bec
	github.com/filecoin-project/go-state-types v0.1.1-0.20210915140513-d354ccf10379
	github.com/filecoin-project/lotus v1.13.0
	github.com/golang/snappy v0.0.3-0.20201103224600-674baa8c7fc3 // indirect
	github.com/google/uuid v1.3.0
	github.com/ipfs/go-cid v0.1.0
	github.com/ipfs/go-datastore v0.4.6
	github.com/ipfs/go-ipfs-blockstore v1.0.4
	github.com/ipfs/go-ipfs-cmds v0.6.0 // indirect
	github.com/ipfs/go-ipfs-http-client v0.1.0-update-libp2p // indirect
	github.com/ipfs/go-ipfs-util v0.0.2
	github.com/ipfs/go-ipld-cbor v0.0.5
	github.com/ipfs/go-ipld-format v0.2.0
	github.com/ipld/go-car v0.3.2-0.20211001225732-32d0d9933823
	github.com/joho/godotenv v1.3.0
	github.com/libp2p/go-libp2p-connmgr v0.2.4
	github.com/libp2p/go-libp2p-core v0.9.0
	github.com/melbahja/got v0.6.1
	github.com/multiformats/go-multiaddr v0.4.1
	github.com/multiformats/go-multiaddr-dns v0.3.1
	github.com/multiformats/go-multibase v0.0.3
	github.com/multiformats/go-multihash v0.0.16
	github.com/oklog/ulid/v2 v2.0.2
	github.com/shirou/gopsutil v2.20.5+incompatible // indirect
	github.com/spf13/cobra v1.1.3
	github.com/spf13/viper v1.8.1
	github.com/stretchr/objx v0.2.0 // indirect
	github.com/stretchr/testify v1.7.0
	github.com/syndtr/goleveldb v1.0.1-0.20210305035536-64b5b1c73954 // indirect
	github.com/textileio/cli v1.0.2
	github.com/textileio/crypto v0.0.0-20210929130053-08edebc3361a
	github.com/textileio/go-datastore-extensions v1.0.1
	github.com/textileio/go-ds-badger3 v0.0.0-20210324034212-7b7fb3be3d1c
	github.com/textileio/go-libp2p-pubsub-rpc v0.0.6
	github.com/textileio/go-log/v2 v2.1.3-gke-2
	go.opentelemetry.io/otel/metric v0.21.0
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	google.golang.org/protobuf v1.27.1
)

replace github.com/kilic/bls12-381 => github.com/kilic/bls12-381 v0.0.0-20200820230200-6b2c19996391
