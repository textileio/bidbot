package pubsub

import (
	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// PublishOptions defines options for Publish.
type PublishOptions struct {
	ignoreResponse bool
	multiResponse  bool
	pubOpts        []pubsub.PubOpt
}

var defaultOptions = PublishOptions{
	ignoreResponse: false,
	multiResponse:  false,
}

// PublishOption defines a Publish option.
type PublishOption func(*PublishOptions) error

// WithIgnoreResponse indicates whether or not Publish will wait for a response(s) from the receiver(s).
// Default: disabled.
func WithIgnoreResponse(enable bool) PublishOption {
	return func(args *PublishOptions) error {
		args.ignoreResponse = enable
		return nil
	}
}

// WithMultiResponse indicates whether or not Publish will wait for multiple responses before returning.
// Default: disabled.
func WithMultiResponse(enable bool) PublishOption {
	return func(args *PublishOptions) error {
		args.multiResponse = enable
		return nil
	}
}

// WithPubOpts sets native pubsub.PubOpt options.
func WithPubOpts(opts ...pubsub.PubOpt) PublishOption {
	return func(args *PublishOptions) error {
		args.pubOpts = opts
		return nil
	}
}
