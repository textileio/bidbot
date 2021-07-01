package marketpeer

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"time"

	ipfslite "github.com/hsanjuan/ipfs-lite"
	blockstore "github.com/ipfs/go-ipfs-blockstore"
	ipfsconfig "github.com/ipfs/go-ipfs-config"
	format "github.com/ipfs/go-ipld-format"
	"github.com/libp2p/go-libp2p"
	connmgr "github.com/libp2p/go-libp2p-connmgr"
	cconnmgr "github.com/libp2p/go-libp2p-core/connmgr"
	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-peerstore/pstoreds"
	ps "github.com/libp2p/go-libp2p-pubsub"
	quic "github.com/libp2p/go-libp2p-quic-transport"
	"github.com/multiformats/go-multiaddr"
	"github.com/textileio/bidbot/lib/finalizer"
	"github.com/textileio/bidbot/lib/marketpeer/mdns"
	"github.com/textileio/bidbot/lib/pubsub"
	badger "github.com/textileio/go-ds-badger3"
	golog "github.com/textileio/go-log/v2"
)

var log = golog.Logger("mpeer")

// Config defines params for Peer configuration.
type Config struct {
	RepoPath                 string
	PrivKey                  crypto.PrivKey
	ListenMultiaddrs         []string
	AnnounceMultiaddrs       []string
	BootstrapAddrs           []string
	ConnManager              cconnmgr.ConnManager
	EnableQUIC               bool
	EnableNATPortMap         bool
	EnableMDNS               bool
	MDNSIntervalSeconds      int
	EnablePubSubPeerExchange bool
	EnablePubSubFloodPublish bool
}

func setDefaults(conf *Config) {
	if len(conf.ListenMultiaddrs) == 0 {
		conf.ListenMultiaddrs = []string{"/ip4/0.0.0.0/tcp/0"}
	}
	if conf.ConnManager == nil {
		conf.ConnManager = connmgr.NewConnManager(256, 512, time.Second*120)
	}
	if conf.MDNSIntervalSeconds <= 0 {
		conf.MDNSIntervalSeconds = 1
	}
}

// PeerInfo contains public information about the libp2p peer.
type PeerInfo struct {
	ID        peer.ID
	PublicKey string
	Addresses []multiaddr.Multiaddr
}

// Peer wraps libp2p peer components needed to partake in the broker market.
type Peer struct {
	host      host.Host
	peer      *ipfslite.Peer
	ps        *ps.PubSub
	bootstrap []peer.AddrInfo
	finalizer *finalizer.Finalizer
}

// New returns a new Peer.
func New(conf Config) (*Peer, error) {
	setDefaults(&conf)

	listenAddr, err := parseMultiaddrs(conf.ListenMultiaddrs)
	if err != nil {
		return nil, fmt.Errorf("parsing listen addresses: %v", err)
	}
	announceAddrs, err := parseMultiaddrs(conf.AnnounceMultiaddrs)
	if err != nil {
		return nil, fmt.Errorf("parsing announce addresses: %v", err)
	}
	bootstrap, err := ipfsconfig.ParseBootstrapPeers(conf.BootstrapAddrs)
	if err != nil {
		return nil, fmt.Errorf("parsing bootstrap addresses: %v", err)
	}

	opts := []libp2p.Option{
		libp2p.ConnectionManager(conf.ConnManager),
		libp2p.DefaultTransports,
		libp2p.DisableRelay(),
	}
	if len(announceAddrs) != 0 {
		opts = append(opts, libp2p.AddrsFactory(func([]multiaddr.Multiaddr) []multiaddr.Multiaddr {
			return announceAddrs
		}))
	}
	if conf.EnableNATPortMap {
		opts = append(opts, libp2p.NATPortMap())
	}
	if conf.EnableQUIC {
		opts = append(opts, libp2p.Transport(quic.NewTransport))
	}

	fin := finalizer.NewFinalizer()
	ctx, cancel := context.WithCancel(context.Background())
	fin.Add(finalizer.NewContextCloser(cancel))

	// Setup ipfslite peerstore
	repoPath := filepath.Join(conf.RepoPath, "ipfslite")
	if err := os.MkdirAll(repoPath, os.ModePerm); err != nil {
		return nil, fmt.Errorf("making dir: %v", err)
	}
	dstore, err := badger.NewDatastore(repoPath, &badger.DefaultOptions)
	if err != nil {
		return nil, fin.Cleanupf("creating repo: %v", err)
	}
	fin.Add(dstore)
	pstore, err := pstoreds.NewPeerstore(ctx, dstore, pstoreds.DefaultOpts())
	if err != nil {
		return nil, fin.Cleanupf("creating peerstore: %v", err)
	}
	fin.Add(pstore)
	opts = append(opts, libp2p.Peerstore(pstore))

	// Setup libp2p
	lhost, dht, err := ipfslite.SetupLibp2p(ctx, conf.PrivKey, nil, listenAddr, dstore, opts...)
	if err != nil {
		return nil, fin.Cleanupf("setting up libp2p", err)
	}
	fin.Add(lhost, dht)

	// Create ipfslite peer
	lpeer, err := ipfslite.New(ctx, dstore, lhost, dht, nil)
	if err != nil {
		return nil, fin.Cleanupf("creating ipfslite peer", err)
	}
	fin.Add(lpeer)

	// Setup pubsub
	gps, err := ps.NewGossipSub(
		ctx,
		lhost,
		ps.WithPeerExchange(conf.EnablePubSubPeerExchange),
		ps.WithFloodPublish(conf.EnablePubSubFloodPublish),
		ps.WithDirectPeers(bootstrap),
	)
	if err != nil {
		return nil, fin.Cleanupf("starting libp2p pubsub: %v", err)
	}

	log.Infof("marketpeer %s is online", lhost.ID())
	log.Debugf("marketpeer addresses: %v", lhost.Addrs())

	p := &Peer{
		host:      lhost,
		peer:      lpeer,
		ps:        gps,
		bootstrap: bootstrap,
		finalizer: fin,
	}

	if conf.EnableMDNS {
		if err := mdns.Start(ctx, p.host, conf.MDNSIntervalSeconds); err != nil {
			return nil, fin.Cleanupf("enabling mdns: %v", err)
		}

		log.Infof("mdns was enabled (interval=%ds)", conf.MDNSIntervalSeconds)
	}

	return p, nil
}

// Close the peer.
func (p *Peer) Close() error {
	return p.finalizer.Cleanup(nil)
}

// Host returns the peer host.
func (p *Peer) Host() host.Host {
	return p.host
}

// Info returns the peer's public information.
func (p *Peer) Info() (*PeerInfo, error) {
	var pkey string
	if pk := p.host.Peerstore().PubKey(p.host.ID()); pk != nil {
		pkb, err := crypto.MarshalPublicKey(pk)
		if err != nil {
			return nil, fmt.Errorf("marshaling public key: %s", err)
		}
		pkey = base64.StdEncoding.EncodeToString(pkb)
	}

	return &PeerInfo{
		p.host.ID(),
		pkey,
		p.host.Addrs(),
	}, nil
}

// ListPeers returns the peers the market peer currently connects to.
func (p *Peer) ListPeers() []peer.ID {
	return p.ps.ListPeers("")
}

// Bootstrap the market peer against Config.Bootstrap network peers.
// Some well-known network peers are included by default.
func (p *Peer) Bootstrap() {
	p.peer.Bootstrap(p.bootstrap)
	log.Info("peer was bootstapped")
}

// DAGService returns the underlying format.DAGService.
func (p *Peer) DAGService() format.DAGService {
	return p.peer
}

// BlockStore returns the underlying format.DAGService.
func (p *Peer) BlockStore() blockstore.Blockstore {
	return p.peer.BlockStore()
}

// NewTopic returns a new pubsub.Topic using the peer's host.
func (p *Peer) NewTopic(ctx context.Context, topic string, subscribe bool) (*pubsub.Topic, error) {
	return pubsub.NewTopic(ctx, p.ps, p.host.ID(), topic, subscribe)
}

func parseMultiaddrs(strs []string) ([]multiaddr.Multiaddr, error) {
	addrs := make([]multiaddr.Multiaddr, len(strs))
	for i, a := range strs {
		addr, err := multiaddr.NewMultiaddr(a)
		if err != nil {
			return nil, fmt.Errorf("parsing multiaddress: %v", err)
		}
		addrs[i] = addr
	}
	return addrs, nil
}
