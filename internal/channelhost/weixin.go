package channelhost

import channelcore "mac/internal/channels"

type WeixinChannelConfig = channelcore.WeixinChannelConfig

// WeixinChannel binds the protocol transport to the shared host message flow.
type WeixinChannel struct {
	*channelcore.WeixinChannel
	runtime Runtime
}

func NewWeixinChannel(name string, config WeixinChannelConfig, workspace string) *WeixinChannel {
	return &WeixinChannel{WeixinChannel: channelcore.NewWeixinChannel(name, config, workspace)}
}

func NewWeixinChannelFromMap(name string, raw map[string]interface{}, workspace string) *WeixinChannel {
	return NewWeixinChannel(name, channelcore.ParseWeixinChannelConfig(raw), workspace)
}

func (channel *WeixinChannel) SetRuntime(runtime channelcore.Runtime) {
	channel.WeixinChannel.SetRuntime(runtime)
	channel.runtime = runtimeOf(runtime)
	channel.SetMessageHandler(func(userID, recipient, messageID, message string, metadata map[string]interface{}) {
		if channel.runtime != nil {
			channel.runtime.ProcessMessage(channel.Name(), "weixin", userID, recipient, messageID, message, metadata)
		}
	})
}
