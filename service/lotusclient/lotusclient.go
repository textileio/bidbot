package lotusclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/filecoin-project/go-jsonrpc"
	"github.com/filecoin-project/lotus/api"
	"github.com/ipfs/go-cid"
	ma "github.com/multiformats/go-multiaddr"
	dns "github.com/multiformats/go-multiaddr-dns"
	"github.com/textileio/go-libp2p-pubsub-rpc/finalizer"
	golog "github.com/textileio/go-log/v2"
)

var (
	log = golog.Logger("bidbot/lotus")

	requestTimeout = time.Second * 10
)

// LotusClient provides access to Lotus for importing deal data.
type LotusClient interface {
	io.Closer
	HealthCheck() error
	CurrentSealingSectors() (int, error)
	ImportData(pcid cid.Cid, file string) error
}

// Client provides access to Lotus for importing deal data.
type Client struct {
	cmin          api.StorageMiner
	cmkt          api.StorageMiner
	fakeMode      bool
	finalizeEarly bool

	ctx       context.Context
	finalizer *finalizer.Finalizer
}

// New returns a new *LotusClient.
func New(maddr string, authToken string, marketMaddr string, marketAuthToken string,
	connRetries int, fakeMode bool, finalizeEarly bool) (*Client, error) {
	fin := finalizer.NewFinalizer()
	ctx, cancel := context.WithCancel(context.Background())
	fin.Add(finalizer.NewContextCloser(cancel))

	lc := &Client{
		fakeMode:      fakeMode,
		finalizeEarly: finalizeEarly,
		ctx:           ctx,
		finalizer:     fin,
	}
	if lc.fakeMode {
		return lc, nil
	}

	builder, err := newBuilder(maddr, authToken, connRetries)
	if err != nil {
		return nil, fmt.Errorf("building lotus client: %w", err)
	}
	cmin, closer, err := builder(lc.ctx)
	if err != nil {
		return nil, fmt.Errorf("starting lotus client: %w", err)
	}
	fin.AddFn(closer)

	var cmkt api.StorageMiner
	if marketMaddr != "" {
		builder, err = newBuilder(marketMaddr, marketAuthToken, connRetries)
		if err != nil {
			return nil, fmt.Errorf("building lotus market client: %w", err)
		}
		cmkt, closer, err = builder(lc.ctx)
		if err != nil {
			return nil, fmt.Errorf("starting lotus market client: %w", err)
		}
		fin.AddFn(closer)
	} else {
		cmkt = cmin
	}

	client := &Client{
		cmin:      cmin,
		cmkt:      cmkt,
		fakeMode:  fakeMode,
		ctx:       ctx,
		finalizer: fin,
	}
	if err := client.HealthCheck(); err != nil {
		return nil, fmt.Errorf("lotus client fails health check: %w", err)
	}
	return client, nil
}

// Close the client.
func (c *Client) Close() error {
	return c.finalizer.Cleanup(nil)
}

// HealthCheck checks if the lotus client is healthy enough so bidbot can
// participate in bidding.
func (c *Client) HealthCheck() error {
	if c.fakeMode {
		return nil
	}
	ctx, cancel := context.WithTimeout(c.ctx, requestTimeout)
	defer cancel()
	start := time.Now()
	_, err := c.cmkt.MarketGetAsk(ctx)
	log.Infof("MarketGetAsk call took %v (err: %v)", time.Since(start), err)
	return err
}

// CurrentSealingSectors returns the total number of sectors considered to be in sealing.
func (c *Client) CurrentSealingSectors() (int, error) {
	// these are the sector states mapping to sstProving
	// https://github.com/filecoin-project/lotus/blob/v1.15.1/extern/storage-sealing/sector_state.go#L109
	// which are the only states considered not in sealing
	// https://github.com/filecoin-project/lotus/blob/v1.15.1/extern/storage-sealing/stats.go#L65
	// hardcode here to avoid importing tons of dependencies.
	notSealingStates := []string{"Proving", "Removed", "Removing",
		"Terminating", "TerminateWait", "TerminateFinality", "TerminateFailed", "Available"}

	if c.finalizeEarly {
		// some additional states can be mapped to sstProving with the FinalizeEarly option
		// https://github.com/filecoin-project/lotus/blob/v1.13.0/extern/storage-sealing/sector_state.go#L115
		notSealingStates = append(notSealingStates, "SubmitCommit", "CommitWait",
			"SubmitCommitAggregate", "CommitAggregateWait", "SubmitReplicaUpdate", "ReplicaUpdateWait")
	}

	ctx, cancel := context.WithTimeout(c.ctx, requestTimeout)
	defer cancel()
	start := time.Now()
	sectorsByState, err := c.cmin.SectorsSummary(ctx)
	log.Debugf("SectorsSummary() call took %.2f seconds", time.Since(start).Seconds())
	if err != nil {
		return 0, fmt.Errorf("getting lotus sectors summary: %s", err)
	}
	total := 0
	for _, i := range sectorsByState {
		total += i
	}
	notSealing := 0
	for _, s := range notSealingStates {
		notSealing += sectorsByState[api.SectorState(s)]
	}
	return total - notSealing, nil
}

// ImportData imports deal data into Lotus.
func (c *Client) ImportData(pcid cid.Cid, file string) error {
	if c.fakeMode {
		return nil
	}
	ctx, cancel := context.WithTimeout(c.ctx, requestTimeout)
	defer cancel()
	if err := c.cmkt.MarketImportDealData(ctx, pcid, file); err != nil {
		return fmt.Errorf("calling storage miner deals import data: %w", err)
	}
	return nil
}

type clientBuilder func(ctx context.Context) (*api.StorageMinerStruct, func(), error)

func newBuilder(maddrs string, authToken string, connRetries int) (clientBuilder, error) {
	maddr, err := ma.NewMultiaddr(maddrs)
	if err != nil {
		return nil, fmt.Errorf("parsing multiaddress: %w", err)
	}
	addr, err := tcpAddrFromMultiAddr(maddr)
	if err != nil {
		return nil, fmt.Errorf("getting tcp address from multiaddress: %w", err)
	}
	headers := http.Header{
		"Authorization": []string{"Bearer " + authToken},
	}

	return func(ctx context.Context) (*api.StorageMinerStruct, func(), error) {
		var api api.StorageMinerStruct
		var closer jsonrpc.ClientCloser
		var err error
		for i := 0; i < connRetries; i++ {
			if ctx.Err() != nil {
				return nil, nil, fmt.Errorf("canceled by context")
			}
			closer, err = jsonrpc.NewMergeClient(context.Background(), "ws://"+addr+"/rpc/v0", "Filecoin",
				[]interface{}{
					&api.CommonStruct.Internal,
					&api.NetStruct.Internal,
					&api.Internal,
				}, headers)
			if err == nil {
				break
			}
			log.Warnf("failed to connect to Lotus API %s, retrying...", err)
			time.Sleep(time.Second * 10)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("couldn't connect to Lotus API: %s", err)
		}

		return &api, closer, nil
	}, nil
}

func tcpAddrFromMultiAddr(maddr ma.Multiaddr) (string, error) {
	if maddr == nil {
		return "", fmt.Errorf("invalid address")
	}

	var ip string
	if _, err := maddr.ValueForProtocol(ma.P_DNS4); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		maddrs, err := dns.Resolve(ctx, maddr)
		if err != nil {
			return "", fmt.Errorf("resolving dns: %s", err)
		}
		for _, m := range maddrs {
			if ip, err = getIPFromMaddr(m); err == nil {
				break
			}
		}
	} else {
		ip, err = getIPFromMaddr(maddr)
		if err != nil {
			return "", fmt.Errorf("getting ip from maddr: %s", err)
		}
	}

	tcp, err := maddr.ValueForProtocol(ma.P_TCP)
	if err != nil {
		return "", fmt.Errorf("getting port from maddr: %s", err)
	}
	return fmt.Sprintf("%s:%s", ip, tcp), nil
}

func getIPFromMaddr(maddr ma.Multiaddr) (string, error) {
	if ip, err := maddr.ValueForProtocol(ma.P_IP4); err == nil {
		return ip, nil
	}
	if ip, err := maddr.ValueForProtocol(ma.P_IP6); err == nil {
		return fmt.Sprintf("[%s]", ip), nil
	}
	return "", fmt.Errorf("no ip in multiaddr")
}
