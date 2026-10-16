package channels

import (
	"fmt"
	"log"
	"os"
	"sync"
)

// Manager owns the transport registry and lifecycle. Application-specific
// dependency injection and rich-content delivery stay in the composition layer.
type Manager struct {
	channels map[string]Transport
	mu       sync.RWMutex
	logger   *log.Logger
}

func NewManager(logger *log.Logger) *Manager {
	if logger == nil {
		logger = log.New(os.Stdout, "[ChannelManager] ", log.LstdFlags)
	}
	return &Manager{channels: make(map[string]Transport), logger: logger}
}

func (manager *Manager) Register(channel Transport) error {
	if channel == nil {
		return fmt.Errorf("channel is nil")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, exists := manager.channels[channel.Name()]; exists {
		return fmt.Errorf("channel %s already exists", channel.Name())
	}
	manager.channels[channel.Name()] = channel
	manager.logger.Printf("registered channel: %s (%s)", channel.Name(), channel.Type())
	return nil
}

func (manager *Manager) Get(name string) (Transport, bool) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	channel, ok := manager.channels[name]
	return channel, ok
}

func (manager *Manager) StartAll() {
	for _, channel := range manager.snapshot() {
		if err := channel.Start(); err != nil {
			manager.logger.Printf("start channel %s failed: %v", channel.Name(), err)
		}
	}
}

func (manager *Manager) StopAll() {
	for _, channel := range manager.snapshot() {
		if err := channel.Stop(); err != nil {
			manager.logger.Printf("stop channel %s failed: %v", channel.Name(), err)
		}
	}
}

func (manager *Manager) snapshot() []Transport {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	channels := make([]Transport, 0, len(manager.channels))
	for _, channel := range manager.channels {
		channels = append(channels, channel)
	}
	return channels
}
