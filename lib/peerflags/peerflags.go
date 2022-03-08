package peerflags

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"

	connmgr "github.com/libp2p/go-libp2p-connmgr"
	"github.com/libp2p/go-libp2p-core/crypto"
	mbase "github.com/multiformats/go-multibase"
	"github.com/spf13/viper"
	"github.com/textileio/cli"
	"github.com/textileio/go-libp2p-pubsub-rpc/peer"
)

// Flags defines daemon flags for github.com/textileio/go-libp2p-pubsub-rpc/peer.
var Flags = []cli.Flag{
	{
		Name:        "fake-mode",
		DefValue:    false,
		Description: "Avoid owner wallet-address verification. Use only for test purposes.",
	},
	{
		Name:        "private-key",
		DefValue:    "",
		Description: "Libp2p private key",
	},
	{
		Name: "listen-multiaddr",
		DefValue: []string{
			"/ip4/0.0.0.0/tcp/4001",
			"/ip4/0.0.0.0/udp/4001/quic",
		},
		Description: "Libp2p listen multiaddr",
	},
	{
		Name: "bootstrap-multiaddr",
		DefValue: []string{
			// staging auctioneer 0
			"/ip4/34.83.3.108/tcp/4001/p2p/12D3KooWGDBaVz45c5d9VEtF4eM7Pgj71DSzB3HHAfpjc8fb5EGe",
			"/ip4/34.83.3.108/udp/4001/quic/p2p/12D3KooWGDBaVz45c5d9VEtF4eM7Pgj71DSzB3HHAfpjc8fb5EGe",
			// staging auctioneer 1
			"/ip4/34.105.101.67/tcp/4001/p2p/12D3KooW9wsxrkCx6CnsWb1gBxZWAjzVK5Hif9FLXKoQZYLewXoD",
			"/ip4/34.105.101.67/udp/4001/quic/p2p/12D3KooW9wsxrkCx6CnsWb1gBxZWAjzVK5Hif9FLXKoQZYLewXoD",
			// staging ipfs node 0
			"/ip4/34.83.36.118/tcp/4001/p2p/12D3KooWQSf4SMyWPSqLN23KxhcLWYhshWb34pYv65cr85jGpNrR",
			// staging ipfs node 1
			"/ip4/34.82.221.249/tcp/4001/p2p/12D3KooWBGyJbDmjjvEzfsgb3nE9JsfcYRqPxpnQagxbb1PyxBrb",
			// staging ipfs node 2
			"/ip4/34.83.88.62/tcp/4001/p2p/12D3KooWHpxr8BTd3R6kAqvtfn77PKW7WRqJ4cbnrT59K2rU44WM",
		},
		Description: "Libp2p bootstrap peer multiaddr",
	},
	{
		Name:        "announce-multiaddr",
		DefValue:    []string{},
		Description: "Libp2p annouce multiaddr",
	},
	{
		Name:        "conn-low",
		DefValue:    256,
		Description: "Libp2p connection manager low water mark",
	},
	{
		Name:        "conn-high",
		DefValue:    512,
		Description: "Libp2p connection manager high water mark",
	},
	{
		Name:        "quic",
		DefValue:    false,
		Description: "Enable the QUIC transport",
	},
	{
		Name:        "nat",
		DefValue:    false,
		Description: "Enable NAT port mapping",
	},
	{
		Name:        "mdns",
		DefValue:    false,
		Description: "Enable MDNS peer discovery",
	},
}

// GetConfig returns a Config from a *viper.Viper instance.
func GetConfig(v *viper.Viper, repoPathEnv, defaultRepoPath string, isAuctioneer bool) (peer.Config, error) {
	if v.GetString("private-key") == "" {
		return peer.Config{}, fmt.Errorf("--private-key is required. Run 'init' to generate a new keypair")
	}

	_, key, err := mbase.Decode(v.GetString("private-key"))
	if err != nil {
		return peer.Config{}, fmt.Errorf("decoding private key: %v", err)
	}
	priv, err := crypto.UnmarshalPrivateKey(key)
	if err != nil {
		return peer.Config{}, fmt.Errorf("unmarshaling private key: %v", err)
	}

	repoPath := os.Getenv(repoPathEnv)
	if repoPath == "" {
		repoPath = defaultRepoPath
	}

	connMan, err := connmgr.NewConnManager(
		v.GetInt("conn-low"),
		v.GetInt("conn-high"),
	)
	if err != nil {
		return peer.Config{}, fmt.Errorf("creating conn manager: %s", err)
	}

	return peer.Config{
		RepoPath:                 repoPath,
		PrivKey:                  priv,
		ListenMultiaddrs:         cli.ParseStringSlice(v, "listen-multiaddr"),
		AnnounceMultiaddrs:       cli.ParseStringSlice(v, "announce-multiaddr"),
		BootstrapAddrs:           cli.ParseStringSlice(v, "bootstrap-multiaddr"),
		ConnManager:              connMan,
		EnableQUIC:               v.GetBool("quic"),
		EnableNATPortMap:         v.GetBool("nat"),
		EnableMDNS:               v.GetBool("mdns"),
		EnablePubSubPeerExchange: isAuctioneer,
		EnablePubSubFloodPublish: true,
	}, nil
}

// WriteConfig writes a *viper.Viper config to file.
// The file is written to a path in pathEnv env var if set, otherwise to defaultPath.
func WriteConfig(v *viper.Viper, repoPathEnv, defaultRepoPath string) (string, error) {
	repoPath := os.Getenv(repoPathEnv)
	if repoPath == "" {
		repoPath = defaultRepoPath
	}
	cf := filepath.Join(repoPath, "config")
	if err := os.MkdirAll(filepath.Dir(cf), os.ModePerm); err != nil {
		return "", fmt.Errorf("making config directory: %v", err)
	}

	// Bail if config already exists
	if _, err := os.Stat(cf); err == nil {
		return "", fmt.Errorf("%s already exists", cf)
	}

	if v.GetString("private-key") == "" {
		priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
		if err != nil {
			return "", fmt.Errorf("generating private key: %v", err)
		}
		key, err := crypto.MarshalPrivateKey(priv)
		if err != nil {
			return "", fmt.Errorf("marshaling private key: %v", err)
		}
		keystr, err := mbase.Encode(mbase.Base64, key)
		if err != nil {
			return "", fmt.Errorf("encoding private key: %v", err)
		}
		v.Set("private-key", keystr)
	}

	v.Set("listen-multiaddr", cli.ParseStringSlice(v, "listen-multiaddr"))
	v.Set("bootstrap-multiaddr", cli.ParseStringSlice(v, "bootstrap-multiaddr"))
	v.Set("announce-multiaddr", cli.ParseStringSlice(v, "announce-multiaddr"))

	if err := v.WriteConfigAs(cf); err != nil {
		return "", fmt.Errorf("error writing config: %v", err)
	}
	v.SetConfigFile(cf)
	if err := v.ReadInConfig(); err != nil {
		cli.CheckErrf("reading configuration: %s", err)
	}
	return cf, nil
}
