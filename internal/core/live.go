package core

import (
	"sync"

	"polar/pkg/contracts"
)

type liveHub struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan contracts.LiveUpdate]struct{}
}

func newLiveHub() *liveHub {
	return &liveHub{subscribers: make(map[string]map[chan contracts.LiveUpdate]struct{})}
}

func (h *liveHub) Subscribe(targetID string) chan contracts.LiveUpdate {
	ch := make(chan contracts.LiveUpdate, 8)
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subscribers[targetID]; !ok {
		h.subscribers[targetID] = make(map[chan contracts.LiveUpdate]struct{})
	}
	h.subscribers[targetID][ch] = struct{}{}
	return ch
}

func (h *liveHub) Unsubscribe(targetID string, ch chan contracts.LiveUpdate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if subs, ok := h.subscribers[targetID]; ok {
		delete(subs, ch)
		if len(subs) == 0 {
			delete(h.subscribers, targetID)
		}
	}
	close(ch)
}

func (h *liveHub) Publish(update contracts.LiveUpdate) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subscribers[update.TargetID] {
		select {
		case ch <- update:
		default:
		}
	}
}
