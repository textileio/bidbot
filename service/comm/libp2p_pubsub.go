package comm

import (
	"context"
	"fmt"

	core "github.com/libp2p/go-libp2p-core/peer"
	pb "github.com/textileio/bidbot/gen/v1"
	"github.com/textileio/bidbot/lib/auction"
	rpc "github.com/textileio/go-libp2p-pubsub-rpc"
	"github.com/textileio/go-libp2p-pubsub-rpc/finalizer"
	"github.com/textileio/go-libp2p-pubsub-rpc/peer"
	golog "github.com/textileio/go-log/v2"
	"google.golang.org/protobuf/proto"
)

var (
	log = golog.Logger("bidbot/comm")
)

// Comm represents the communication channel with auctioneer.
type Comm interface {
	Subscribe(bootstrap bool, eh MessageHandler) error
	Close() error
	ID() core.ID
	Info() (*peer.Info, error)
	PublishBid(ctx context.Context, topicName string, bid *pb.Bid) ([]byte, error)
	PublishBidbotEvent(ctx context.Context, event *pb.BidbotEvent)
}

// MessageHandler handles messages from auctioneer.
type MessageHandler interface {
	AuctionsHandler(core.ID, *pb.Auction) error
	WinsHandler(*pb.WinningBid) error
	ProposalsHandler(*pb.WinningBidProposal) error
}

// Libp2pPubsub communicates with auctioneer via libp2p pubsub.
type Libp2pPubsub struct {
	peer        *peer.Peer
	ctx         context.Context
	started     bool
	finalizer   *finalizer.Finalizer
	eventsTopic *rpc.Topic
}

// NewLibp2pPubsub creates a Comm backed by libp2p pubsub.
func NewLibp2pPubsub(ctx context.Context, conf peer.Config) (*Libp2pPubsub, error) {
	fin := finalizer.NewFinalizer()
	p, err := peer.New(conf)
	if err != nil {
		return nil, fin.Cleanupf("creating peer: %v", err)
	}
	fin.Add(p)

	events, err := p.NewTopic(ctx, auction.BidbotEventsTopic(p.Host().ID()), false)
	if err != nil {
		return nil, fin.Cleanupf("creating bidbot events topic: %v", err)
	}
	fin.Add(events)

	return &Libp2pPubsub{
		peer:        p,
		ctx:         ctx,
		finalizer:   fin,
		eventsTopic: events,
	}, nil
}

// Subscribe subcribes several topics published by autioneer and forwards the messages to the handler.
func (ps *Libp2pPubsub) Subscribe(bootstrap bool, h MessageHandler) error {
	if ps.started {
		return nil
	}
	// Bootstrap against configured addresses
	if bootstrap {
		ps.peer.Bootstrap()
	}

	// Subscribe to the global auctions topic
	auctions, err := ps.peer.NewTopic(ps.ctx, auction.Topic, true)
	if err != nil {
		return ps.finalizer.Cleanupf("creating auction topic: %v", err)
	}
	auctions.SetEventHandler(ps.eventHandler)
	auctions.SetMessageHandler(func(from core.ID, topic string, msg []byte) ([]byte, error) {
		log.Debugf("%s received auction from %s", topic, from)
		a := &pb.Auction{}
		if err := proto.Unmarshal(msg, a); err != nil {
			return nil, fmt.Errorf("unmarshaling message: %v", err)
		}
		return nil, h.AuctionsHandler(from, a)
	})
	ps.finalizer.Add(auctions)

	// Subscribe to our own wins topic
	wins, err := ps.peer.NewTopic(ps.ctx, auction.WinsTopic(ps.peer.Host().ID()), true)
	if err != nil {
		return ps.finalizer.Cleanupf("creating wins topic: %v", err)
	}
	wins.SetEventHandler(ps.eventHandler)
	wins.SetMessageHandler(func(from core.ID, topic string, msg []byte) ([]byte, error) {
		log.Debugf("%s received win from %s", topic, from)
		wb := &pb.WinningBid{}
		if err := proto.Unmarshal(msg, wb); err != nil {
			return nil, fmt.Errorf("unmarshaling message: %v", err)
		}
		return nil, h.WinsHandler(wb)
	})
	ps.finalizer.Add(wins)

	// Subscribe to our own proposals topic
	props, err := ps.peer.NewTopic(ps.ctx, auction.ProposalsTopic(ps.peer.Host().ID()), true)
	if err != nil {
		return ps.finalizer.Cleanupf("creating proposals topic: %v", err)
	}
	props.SetEventHandler(ps.eventHandler)
	props.SetMessageHandler(func(from core.ID, topic string, msg []byte) ([]byte, error) {
		log.Debugf("%s received win from %s", topic, from)
		proposal := &pb.WinningBidProposal{}
		if err := proto.Unmarshal(msg, proposal); err != nil {
			return nil, fmt.Errorf("unmarshaling message: %v", err)
		}
		err := h.ProposalsHandler(proposal)
		if err != nil {
			log.Errorf("handling proposal: %v", err)
		}
		return nil, err
	})
	ps.finalizer.Add(props)

	log.Info("subscribed to the deal auction feed")
	ps.started = true
	return nil
}

// Close closes the communication channel.
func (ps *Libp2pPubsub) Close() error {
	log.Info("Libp2pPubsub was shutdown")
	return ps.finalizer.Cleanup(nil)
}

// PublishBid publishes the bid to the given topic name.
func (ps *Libp2pPubsub) PublishBid(ctx context.Context, topicName string, bid *pb.Bid) ([]byte, error) {
	topic, err := ps.peer.NewTopic(ps.ctx, topicName, false)
	if err != nil {
		return nil, fmt.Errorf("creating bids topic: %v", err)
	}
	defer func() {
		if err := topic.Close(); err != nil {
			log.Errorf("closing bids topic: %v", err)
		}
	}()
	topic.SetEventHandler(ps.eventHandler)

	msg, err := proto.Marshal(bid)
	if err != nil {
		return nil, fmt.Errorf("marshaling bid: %v", err)
	}
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

// PublishBidbotEvent publishes the given event.
func (ps *Libp2pPubsub) PublishBidbotEvent(ctx context.Context, event *pb.BidbotEvent) {
	msg, err := proto.Marshal(event)
	if err != nil {
		log.Warnf("marshaling bidbot event: %v", err)
		return
	}
	if _, err := ps.eventsTopic.Publish(ctx, msg); err != nil {
		log.Warnf("publishing bidbot event: %v", err)
	}
}

// ID returns the peer ID of the channel.
func (ps *Libp2pPubsub) ID() core.ID {
	return ps.peer.Host().ID()
}

// Info returns the peer Info of the channel.
func (ps *Libp2pPubsub) Info() (*peer.Info, error) {
	return ps.peer.Info()
}

func (ps *Libp2pPubsub) eventHandler(from core.ID, topic string, msg []byte) {
	log.Debugf("%s peer event: %s %s", topic, from, msg)
}
