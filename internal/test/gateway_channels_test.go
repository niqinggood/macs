package app

import (
	"io"
	"log"
	"testing"

	channelcore "mac/internal/channels"
)

type testChannel struct {
	name    string
	started bool
	stopped bool
}

func (c *testChannel) Name() string                       { return c.name }
func (c *testChannel) Type() string                       { return "test" }
func (c *testChannel) Start() error                       { c.started = true; return nil }
func (c *testChannel) Stop() error                        { c.stopped = true; return nil }
func (c *testChannel) SendMessage(string, string) error   { return nil }
func (c *testChannel) SetGateway(gateway *GatewayService) {}
func (c *testChannel) SetRuntime(channelcore.Runtime)     {}

func TestChannelsDisabledByEnv(t *testing.T) {
	t.Setenv(envDisableChannels, "1")
	if !channelsDisabledByEnv() {
		t.Fatal("expected channels to be disabled")
	}

	t.Setenv(envDisableChannels, "false")
	if channelsDisabledByEnv() {
		t.Fatal("false should not disable channels")
	}
}

func TestUpdateChannelsConfigClearsExistingChannelsWhenDisabledByEnv(t *testing.T) {
	t.Setenv(envDisableChannels, "true")

	gw := &GatewayService{
		config: &Config{Channels: []ChannelConfig{{
			Name:    "old",
			Type:    "test",
			Enabled: true,
		}}},
		logger: log.New(io.Discard, "", 0),
	}
	gw.channelManager = NewChannelManager(gw)
	oldChannel := &testChannel{name: "old"}
	if err := gw.channelManager.RegisterChannel(oldChannel); err != nil {
		t.Fatalf("register test channel: %v", err)
	}

	err := gw.updateChannelsConfig([]ChannelConfig{{
		Name:    "new",
		Type:    "feishu",
		Enabled: true,
	}})
	if err != nil {
		t.Fatalf("updateChannelsConfig: %v", err)
	}
	if !oldChannel.stopped {
		t.Fatal("existing channel should be stopped during update")
	}
	if _, ok := gw.channelManager.GetChannel("old"); ok {
		t.Fatal("existing channel should be cleared when manager is rebuilt")
	}
	if _, ok := gw.channelManager.GetChannel("new"); ok {
		t.Fatal("new external channel should not be loaded when channels are disabled")
	}
}
