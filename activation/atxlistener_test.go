package activation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/spacemeshos/go-spacemesh/log"
	"github.com/spacemeshos/go-spacemesh/p2p/service"
	"github.com/stretchr/testify/assert"
)

type atxProcessorMock struct {
	processedATXs chan service.GossipMessage
}

func (ap *atxProcessorMock) HandleGossipAtx(_ context.Context, msg service.GossipMessage, _ service.Fetcher) {
	ap.processedATXs <- msg
}

func TestATXListener(t *testing.T) {
	gossipProvider := &ServiceMock{ch: make(chan service.GossipMessage)}
	processor := &atxProcessorMock{processedATXs: make(chan service.GossipMessage)}
	al := NewATXListener(gossipProvider, processor, nil, log.NewDefault("atx listener"))

	al.Start()

	gossips := []service.GossipMessage{&mockMsg{}, &mockMsg{}, &mockMsg{}}
	var processed []service.GossipMessage
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		for i := 0; i < len(gossips); i++ {
			select {
			case msg := <-processor.processedATXs:
				processed = append(processed, msg)
			case <-time.After(500 * time.Millisecond):
				assert.Fail(t, "timeout waiting for ATX gossip to be processed")
			}
		}
		wg.Done()
	}()
	for _, msg := range gossips {
		gossipProvider.ch <- msg
	}
	wg.Wait()
	assert.ElementsMatch(t, gossips, processed)

	assert.NoError(t, al.Stop())
}
