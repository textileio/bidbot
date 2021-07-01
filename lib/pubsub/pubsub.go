package pubsub

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/ipfs/go-cid"
	util "github.com/ipfs/go-ipfs-util"
	cbor "github.com/ipfs/go-ipld-cbor"
	"github.com/libp2p/go-libp2p-core/peer"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	golog "github.com/textileio/go-log/v2"
)

var (
	log = golog.Logger("mpeer/pubsub")

	// ErrResponseNotReceived indicates a response was not received after publishing a message.
	ErrResponseNotReceived = errors.New("response not received")
)

// EventHandler is used to receive topic peer events.
type EventHandler func(from peer.ID, topic string, msg []byte)

// MessageHandler is used to receive topic messages.
type MessageHandler func(from peer.ID, topic string, msg []byte) ([]byte, error)

// Response wraps a message response.
type Response struct {
	// ID is the cid.Cid of the received message.
	ID string
	// From is the peer.ID of the sender.
	From peer.ID
	// Data is the message data.
	Data []byte
	// Err is an error from the sender.
	Err error
}

type internalResponse struct {
	ID   string
	From []byte
	Data []byte
	Err  string
}

func init() {
	cbor.RegisterCborType(internalResponse{})
}

func responseTopic(base string, pid peer.ID) string {
	return path.Join(base, pid.String(), "_response")
}

// Topic provides a nice interface to a libp2p pubsub topic.
type Topic struct {
	ps             *pubsub.PubSub
	host           peer.ID
	eventHandler   EventHandler
	messageHandler MessageHandler

	resChs   map[cid.Cid]chan internalResponse
	resTopic *Topic

	t *pubsub.Topic
	h *pubsub.TopicEventHandler
	s *pubsub.Subscription

	ctx    context.Context
	cancel context.CancelFunc

	lk sync.Mutex
}

// NewTopic returns a new topic for the host.
func NewTopic(ctx context.Context, ps *pubsub.PubSub, host peer.ID, topic string, subscribe bool) (*Topic, error) {
	t, err := newTopic(ctx, ps, host, topic, subscribe)
	if err != nil {
		return nil, fmt.Errorf("creating topic: %v", err)
	}
	t.resTopic, err = newTopic(ctx, ps, host, responseTopic(topic, host), true)
	if err != nil {
		return nil, fmt.Errorf("creating response topic: %v", err)
	}
	t.resTopic.eventHandler = t.resEventHandler
	t.resTopic.messageHandler = t.resMessageHandler
	t.resChs = make(map[cid.Cid]chan internalResponse)
	return t, nil
}

func newTopic(ctx context.Context, ps *pubsub.PubSub, host peer.ID, topic string, subscribe bool) (*Topic, error) {
	top, err := ps.Join(topic)
	if err != nil {
		return nil, fmt.Errorf("joining topic: %v", err)
	}

	handler, err := top.EventHandler()
	if err != nil {
		return nil, fmt.Errorf("getting topic handler: %v", err)
	}

	var sub *pubsub.Subscription
	if subscribe {
		sub, err = top.Subscribe()
		if err != nil {
			return nil, fmt.Errorf("subscribing to topic: %v", err)
		}
	}

	t := &Topic{
		ps:   ps,
		host: host,
		t:    top,
		h:    handler,
		s:    sub,
	}
	t.ctx, t.cancel = context.WithCancel(ctx)

	go t.watch()
	if t.s != nil {
		go t.listen()
	}

	return t, nil
}

// Close the topic.
func (t *Topic) Close() error {
	t.lk.Lock()
	defer t.lk.Unlock()
	t.cancel()
	t.h.Cancel()
	if t.s != nil {
		t.s.Cancel()
	}
	if err := t.t.Close(); err != nil {
		return err
	}
	if t.resTopic != nil {
		return t.resTopic.Close()
	}
	return nil
}

// SetEventHandler sets a handler func that will be called with peer events.
func (t *Topic) SetEventHandler(handler EventHandler) {
	t.lk.Lock()
	defer t.lk.Unlock()
	t.eventHandler = handler
}

// SetMessageHandler sets a handler func that will be called with topic messages.
// A subscription is required for the handler to be called.
func (t *Topic) SetMessageHandler(handler MessageHandler) {
	t.lk.Lock()
	defer t.lk.Unlock()
	t.messageHandler = handler
}

// Publish data. See PublishOptions for option details.
func (t *Topic) Publish(
	ctx context.Context,
	data []byte,
	opts ...PublishOption,
) (<-chan Response, error) {
	args := defaultOptions
	for _, op := range opts {
		if err := op(&args); err != nil {
			return nil, fmt.Errorf("applying option: %v", err)
		}
	}

	var respCh chan internalResponse
	var msgID cid.Cid
	if !args.ignoreResponse {
		msgID = cid.NewCidV1(cid.Raw, util.Hash(data))
		respCh = make(chan internalResponse)
		t.lk.Lock()
		t.resChs[msgID] = respCh
		t.lk.Unlock()
	}

	if err := t.t.Publish(ctx, data, args.pubOpts...); err != nil {
		return nil, fmt.Errorf("publishing to main topic: %v", err)
	}

	resultCh := make(chan Response)
	if respCh != nil {
		go func() {
			defer func() {
				t.lk.Lock()
				delete(t.resChs, msgID)
				t.lk.Unlock()
				close(resultCh)
			}()
			select {
			case <-ctx.Done():
				if !args.multiResponse {
					resultCh <- Response{
						Err: ErrResponseNotReceived,
					}
				}
				return
			case r := <-respCh:
				res := Response{
					ID:   r.ID,
					From: peer.ID(r.From),
					Data: r.Data,
				}
				if r.Err != "" {
					res.Err = errors.New(r.Err)
				}
				resultCh <- res
				if !args.multiResponse {
					return
				}
			}
		}()
	} else {
		close(resultCh)
	}

	return resultCh, nil
}

func (t *Topic) watch() {
	for {
		e, err := t.h.NextPeerEvent(t.ctx)
		if err != nil {
			break
		}
		var msg string
		switch e.Type {
		case pubsub.PeerJoin:
			msg = "JOINED"
		case pubsub.PeerLeave:
			msg = "LEFT"
		default:
			continue
		}
		t.lk.Lock()
		if t.eventHandler != nil {
			t.eventHandler(e.Peer, t.t.String(), []byte(msg))
		}
		t.lk.Unlock()
	}
}

func (t *Topic) listen() {
	for {
		msg, err := t.s.Next(t.ctx)
		if err != nil {
			break
		}
		if msg.ReceivedFrom.String() == t.host.String() {
			continue
		}
		t.lk.Lock()

		if t.messageHandler != nil {
			data, err := t.messageHandler(msg.ReceivedFrom, t.t.String(), msg.Data)
			if !strings.Contains(t.t.String(), "/_response") {
				// This is a normal message; respond with data and error
				go func() {
					msgID := cid.NewCidV1(cid.Raw, util.Hash(msg.Data))
					t.publishResponse(msg.ReceivedFrom, msgID, data, err)
				}()
			} else if err != nil {
				log.Errorf("response message handler: %v", err)
			}
		}
		t.lk.Unlock()
	}
}

func (t *Topic) publishResponse(from peer.ID, id cid.Cid, data []byte, e error) {
	topic, err := newTopic(t.ctx, t.ps, t.host, responseTopic(t.t.String(), from), false)
	if err != nil {
		log.Errorf("creating response topic: %v", err)
		return
	}
	defer func() {
		if err := topic.Close(); err != nil {
			log.Errorf("closing response topic: %v", err)
		}
	}()
	topic.SetEventHandler(t.resEventHandler)

	res := internalResponse{
		ID: id.String(),
		// From: Set on the receiver end using validated data from the received pubsub message
		Data: data,
	}
	if e != nil {
		res.Err = e.Error()
	}
	msg, err := cbor.DumpObject(&res)
	if err != nil {
		log.Errorf("encoding response: %v", err)
		return
	}

	if err := topic.t.Publish(t.ctx, msg); err != nil {
		log.Errorf("publishing response: %v", err)
	}
}

func (t *Topic) resEventHandler(from peer.ID, topic string, msg []byte) {
	log.Debugf("%s response peer event: %s %s", topic, from, msg)
}

func (t *Topic) resMessageHandler(from peer.ID, topic string, msg []byte) ([]byte, error) {
	var res internalResponse
	if err := cbor.DecodeInto(msg, &res); err != nil {
		return nil, fmt.Errorf("decoding response: %v", err)
	}
	id, err := cid.Decode(res.ID)
	if err != nil {
		return nil, fmt.Errorf("decoding response id: %v", err)
	}
	res.From = []byte(from)

	log.Debugf("%s response from %s: %s", topic, from, res.ID)
	t.lk.Lock()
	ch := t.resChs[id]
	t.lk.Unlock()
	if ch != nil {
		select {
		case ch <- res:
		default:
		}
	}
	return nil, nil // no response to a response
}
