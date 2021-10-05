package comm

import (
	"context"
	"fmt"

	core "github.com/libp2p/go-libp2p-core/peer"
	"github.com/textileio/bidbot/lib/auction"
	"github.com/textileio/go-libp2p-pubsub-rpc/finalizer"
	"github.com/textileio/go-libp2p-pubsub-rpc/peer"
	golog "github.com/textileio/go-log/v2"
)

var (
	log = golog.Logger("bidbot/comm")
)

type Comm interface {
	Start(bootstrap bool) error
	Stop() error
	NewBid(ctx context.Context, topicName string, msg []byte) ([]byte, error)
}

type EventHandlers interface {
	AuctionsHandler(from core.ID, topic string, msg []byte) ([]byte, error)
	WinsHandler(from core.ID, topic string, msg []byte) ([]byte, error)
	ProposalHandler(from core.ID, topic string, msg []byte) ([]byte, error)
}

type Libp2pComm struct {
	peer       *peer.Peer
	ctx        context.Context
	subscribed bool
	eh         EventHandlers
	finalizer  *finalizer.Finalizer
}

func NewLibp2pComm(conf peer.Config, eh EventHandlers) (*Libp2pComm, error) {
	fin := finalizer.NewFinalizer()
	ctx, cancel := context.WithCancel(context.Background())
	fin.Add(finalizer.NewContextCloser(cancel))
	p, err := peer.New(conf)
	if err != nil {
		return nil, fin.Cleanupf("creating peer: %v", err)
	}
	fin.Add(p)

	return &Libp2pComm{
		peer:      p,
		ctx:       ctx,
		eh:        eh,
		finalizer: fin,
	}, nil
}

func (s *Libp2pComm) Start(bootstrap bool) error {
	if s.subscribed {
		return nil
	}

	// Bootstrap against configured addresses
	if bootstrap {
		s.peer.Bootstrap()
	}

	// Subscribe to the global auctions topic
	auctions, err := s.peer.NewTopic(s.ctx, auction.Topic, true)
	if err != nil {
		return fmt.Errorf("creating auction topic: %v", err)
	}
	auctions.SetEventHandler(s.eventHandler)
	auctions.SetMessageHandler(s.eh.AuctionsHandler)

	// Subscribe to our own wins topic
	wins, err := s.peer.NewTopic(s.ctx, auction.WinsTopic(s.peer.Host().ID()), true)
	if err != nil {
		if err := auctions.Close(); err != nil {
			log.Errorf("closing auctions topic: %v", err)
		}
		return fmt.Errorf("creating wins topic: %v", err)
	}
	wins.SetEventHandler(s.eventHandler)
	wins.SetMessageHandler(s.eh.WinsHandler)

	// Subscribe to our own proposals topic
	props, err := s.peer.NewTopic(s.ctx, auction.ProposalsTopic(s.peer.Host().ID()), true)
	if err != nil {
		if err := auctions.Close(); err != nil {
			log.Errorf("closing auctions topic: %v", err)
		}
		if err := wins.Close(); err != nil {
			log.Errorf("closing wins topic: %v", err)
		}
		return fmt.Errorf("creating proposals topic: %v", err)
	}
	props.SetEventHandler(s.eventHandler)
	props.SetMessageHandler(s.eh.ProposalHandler)

	s.finalizer.Add(auctions, wins, props)

	log.Info("subscribed to the deal auction feed")
	s.subscribed = true
	return nil
}

func (c *Libp2pComm) Stop() error {
	log.Info("Libp2pComm was shutdown")
	return c.finalizer.Cleanup(nil)
}

func (s *Libp2pComm) NewBid(ctx context.Context, topicName string, msg []byte) ([]byte, error) {
	topic, err := s.peer.NewTopic(s.ctx, topicName, false)
	if err != nil {
		return nil, fmt.Errorf("creating bids topic: %v", err)
	}
	defer func() {
		if err := topic.Close(); err != nil {
			log.Errorf("closing bids topic: %v", err)
		}
	}()
	topic.SetEventHandler(s.eventHandler)

	res, err := topic.Publish(ctx, msg)
	if err != nil {
		return nil, fmt.Errorf("publishing bid: %v", err)
	}
	r := <-res
	if r.Err != nil {
		return nil, fmt.Errorf("publishing bid; auctioneer returned error: %v", r.Err)
	}

	return r.Data, nil
}

func (s *Libp2pComm) eventHandler(from core.ID, topic string, msg []byte) {
	log.Debugf("%s peer event: %s %s", topic, from, msg)
}
