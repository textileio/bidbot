package mdns

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery"
	golog "github.com/textileio/go-log/v2"
)

var log = golog.Logger("mpeer/mdns")

const connTimeout = time.Second * 10

// Start the MDNS discovery.
func Start(ctx context.Context, host host.Host, intervalSecs int) error {
	if intervalSecs <= 0 {
		intervalSecs = 5
	}
	dur := time.Duration(intervalSecs) * time.Second
	service, err := discovery.NewMdnsService(ctx, host, dur, discovery.ServiceTag)
	if err != nil {
		return fmt.Errorf("creating mdns service: %v", err)
	}
	service.RegisterNotifee(&handler{
		ctx:  ctx,
		host: host,
	})
	return nil
}

type handler struct {
	ctx  context.Context
	host host.Host
}

// HandlePeerFound tries to connect to the discovered peer.
func (h *handler) HandlePeerFound(p peer.AddrInfo) {
	log.Infof("connecting to discovered peer: %s", p.ID)
	ctx, cancel := context.WithTimeout(h.ctx, connTimeout)
	defer cancel()
	if err := h.host.Connect(ctx, p); err != nil {
		log.Warnf("failed to connect to peer %s found by discovery: %v", p.ID, err)
	}
}
