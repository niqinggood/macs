package channelhost

import (
	"fmt"
	"log"
	"os"

	channelcore "mac/internal/channels"
)

type Channel interface {
	channelcore.Transport
	SetRuntime(channelcore.Runtime)
}

type Manager struct {
	manager *channelcore.Manager
	logger  *log.Logger
	runtime channelcore.Runtime
	host    Runtime
}

func NewManager(runtime channelcore.Runtime) *Manager {
	logger := log.New(os.Stdout, "[ChannelManager] ", log.LstdFlags)
	return &Manager{
		manager: channelcore.NewManager(logger), logger: logger,
		runtime: runtime, host: runtimeOf(runtime),
	}
}

func (manager *Manager) RegisterChannel(channel Channel) error {
	if channel == nil {
		return fmt.Errorf("channel is nil")
	}
	channel.SetRuntime(manager.runtime)
	return manager.manager.Register(channel)
}

func (manager *Manager) GetChannel(name string) (Channel, bool) {
	transport, ok := manager.manager.Get(name)
	if !ok {
		return nil, false
	}
	channel, ok := transport.(Channel)
	return channel, ok
}

func (manager *Manager) StartAll() error {
	manager.manager.StartAll()
	return nil
}

func (manager *Manager) StopAll() error {
	manager.manager.StopAll()
	return nil
}

func (manager *Manager) SendToChannel(channelName, recipient, content string) error {
	channel, ok := manager.GetChannel(channelName)
	if !ok {
		return fmt.Errorf("channel %s not found", channelName)
	}
	workspace := ""
	if manager.host != nil {
		workspace = manager.host.ArtifactWorkspace()
	}
	return channelcore.SendContent(workspace, channel, recipient, content, manager.logger)
}
