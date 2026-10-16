package app

import (
	"testing"

	channelcore "mac/internal/channels"
)

type captureReplyChannel struct {
	*BaseChannel
	recipient string
	content   string
	count     int
}

func (c *captureReplyChannel) Start() error                   { return nil }
func (c *captureReplyChannel) Stop() error                    { return nil }
func (c *captureReplyChannel) SetRuntime(channelcore.Runtime) {}

func (c *captureReplyChannel) SendMessage(recipient string, content string) error {
	c.recipient = recipient
	c.content = content
	c.count++
	return nil
}

func TestBaseChannelSendReplyUsesRegisteredChannel(t *testing.T) {
	g := &GatewayService{}
	g.channelManager = NewChannelManager(g)
	base := NewBaseChannel("weixin", "weixin")
	base.SetGateway(g)
	ch := &captureReplyChannel{BaseChannel: base}
	if err := g.channelManager.RegisterChannel(ch); err != nil {
		t.Fatalf("register channel: %v", err)
	}

	base.sendReply("wx-user-1", "msg-1", "hello from openkitty", nil)

	if ch.count != 1 {
		t.Fatalf("send count = %d, want 1", ch.count)
	}
	if ch.recipient != "wx-user-1" {
		t.Fatalf("recipient = %q, want wx-user-1", ch.recipient)
	}
	if ch.content != "hello from openkitty" {
		t.Fatalf("content = %q, want hello from openkitty", ch.content)
	}
}
