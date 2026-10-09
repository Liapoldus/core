package plugins

import "sync"

// PeerDirectoryBroadcaster fans out a single change signal to every pending
// long-poll waiter. It is deliberately coarse: a wake-up never carries state,
// so a woken poller always re-reads the current snapshot rather than trusting a
// raced delta. Each subscriber receives exactly one pending signal per change.
type PeerDirectoryBroadcaster struct {
	mu          sync.Mutex
	subscribers map[chan struct{}]struct{}
}

// NewPeerDirectoryBroadcaster creates an empty broadcaster.
func NewPeerDirectoryBroadcaster() *PeerDirectoryBroadcaster {
	return &PeerDirectoryBroadcaster{subscribers: make(map[chan struct{}]struct{})}
}

// Subscribe registers a waiter. The returned channel is closed when a change is
// broadcast; callers must invoke the returned cancel function when they stop
// waiting so the subscription does not leak.
func (broadcaster *PeerDirectoryBroadcaster) Subscribe() (<-chan struct{}, func()) {
	if broadcaster == nil {
		return nil, func() {}
	}
	channel := make(chan struct{})
	broadcaster.mu.Lock()
	broadcaster.subscribers[channel] = struct{}{}
	broadcaster.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			broadcaster.mu.Lock()
			if _, exists := broadcaster.subscribers[channel]; exists {
				delete(broadcaster.subscribers, channel)
				close(channel)
			}
			broadcaster.mu.Unlock()
		})
	}
	return channel, cancel
}

// Notify wakes every current waiter. It never blocks on a slow consumer.
func (broadcaster *PeerDirectoryBroadcaster) Notify() {
	if broadcaster == nil {
		return
	}
	broadcaster.mu.Lock()
	for channel := range broadcaster.subscribers {
		delete(broadcaster.subscribers, channel)
		close(channel)
	}
	broadcaster.mu.Unlock()
}