package activation

import (
	"context"
	"sync"

	"github.com/spacemeshos/go-spacemesh/log"
	"github.com/spacemeshos/go-spacemesh/p2p/service"
	"github.com/spacemeshos/go-spacemesh/priorityq"
)

// AtxProtocol is the protocol id for broadcasting atxs over gossip
const AtxProtocol = "AtxGossip"

type atxProcessor interface {
	HandleGossipAtx(context.Context, service.GossipMessage, service.Fetcher)
}

// ATXListener handles ATX gossip messages.
type ATXListener struct {
	startOnce   sync.Once
	logger      log.Log
	processor   atxProcessor
	fetcher     service.Fetcher
	atxMessages chan service.GossipMessage
}

// NewATXListener returns a instance of ATXListener
func NewATXListener(net service.Service, processor atxProcessor, fetcher service.Fetcher, log log.Log) *ATXListener {
	return &ATXListener{
		processor:   processor,
		fetcher:     fetcher,
		logger:      log,
		atxMessages: net.RegisterGossipProtocol(AtxProtocol, priorityq.Low),
	}
}

// Start starts listening to ATX gossip messages.
func (al *ATXListener) Start(ctx context.Context) {
	al.startOnce.Do(func() {
		go al.loop(ctx)
	})
}

// loop listens to gossip messages until ctx is canceled.
func (al *ATXListener) loop(ctx context.Context) {
	al.logger.Info("start listening to ATX gossip")
	for {
		select {
		case atx := <-al.atxMessages:
			go al.processor.HandleGossipAtx(ctx, atx, al.fetcher)
		case <-ctx.Done():
			al.logger.WithContext(ctx).Info("stopped listening to ATX gossip")
			return
		}
	}
}
