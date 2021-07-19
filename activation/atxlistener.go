package activation

import (
	"context"
	"sync"

	"github.com/spacemeshos/go-spacemesh/log"
	"github.com/spacemeshos/go-spacemesh/p2p/service"
	"github.com/spacemeshos/go-spacemesh/priorityq"
	"gopkg.in/tomb.v2"
)

// AtxProtocol is the protocol id for broadcasting atxs over gossip
const AtxProtocol = "AtxGossip"

type atxProcessor interface {
	HandleGossipAtx(context.Context, service.GossipMessage, service.Fetcher)
}

// ATXListener handles ATX gossip messages.
type ATXListener struct {
	startOnce   sync.Once
	tomb        tomb.Tomb
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
func (al *ATXListener) Start() {
	al.startOnce.Do(func() {
		al.tomb.Go(al.loop)
	})
}

// Stop shutdown the listener gracefully.
func (al *ATXListener) Stop() error {
	al.logger.Info("stopping ATX gossip processing")
	al.tomb.Kill(nil)
	err := al.tomb.Wait()
	al.logger.With().Info("stopped AX gossip processing with err", log.Err(err))
	return err
}

// loop listens to gossip messages until ctx is canceled.
func (al *ATXListener) loop() error {
	al.logger.Info("start listening to ATX gossip")
	for {
		select {
		case atx := <-al.atxMessages:
			al.tomb.Go(func() error {
				al.processor.HandleGossipAtx(al.tomb.Context(nil), atx, al.fetcher)
				return nil
			})
		case <-al.tomb.Dying():
			al.logger.Info("stopped listening to ATX gossip")
			return nil
		}
	}
}
