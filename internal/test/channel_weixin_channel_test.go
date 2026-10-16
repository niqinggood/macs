package app

import (
	"encoding/base64"
	"strings"
	"testing"

	channelcore "mac/internal/channels"
)

func TestWeixinParseChannelConfig(t *testing.T) {
	cfg := channelcore.ParseWeixinChannelConfig(map[string]interface{}{
		"accountId":     " bot@im.bot ",
		"token":         " token ",
		"baseUrl":       "",
		"botAgent":      "OpenKitty/1.0 (test)",
		"allowed_users": "u1@im.wechat, u2@im.wechat u1@im.wechat",
	})

	if cfg.AccountID != "bot@im.bot" {
		t.Fatalf("AccountID = %q", cfg.AccountID)
	}
	if cfg.Token != "token" {
		t.Fatalf("Token = %q", cfg.Token)
	}
	if cfg.BaseURL != channelcore.WeixinDefaultBaseURL {
		t.Fatalf("BaseURL = %q", cfg.BaseURL)
	}
	if len(cfg.AllowedUsers) != 2 || cfg.AllowedUsers[0] != "u1@im.wechat" || cfg.AllowedUsers[1] != "u2@im.wechat" {
		t.Fatalf("AllowedUsers = %#v", cfg.AllowedUsers)
	}
}

func TestWeixinAllowedUsersDefaultIsAdvisory(t *testing.T) {
	ch := channelcore.NewWeixinChannel("weixin", channelcore.WeixinChannelConfig{
		AccountID:    "bot@im.bot",
		Token:        "token",
		AllowedUsers: []string{"owner-from-qr@im.wechat"},
	}, ".")

	if !ch.ShouldAcceptUser("actual-message-user@im.wechat") {
		t.Fatal("expected non-strict allowed_users mismatch to be accepted")
	}
}

func TestWeixinAllowedUsersStrictBlocksMismatch(t *testing.T) {
	ch := channelcore.NewWeixinChannel("weixin", channelcore.WeixinChannelConfig{
		AccountID:     "bot@im.bot",
		Token:         "token",
		AllowedUsers:  []string{"owner-from-qr@im.wechat"},
		StrictAllowed: true,
	}, ".")

	if ch.ShouldAcceptUser("actual-message-user@im.wechat") {
		t.Fatal("expected strict allowed_users mismatch to be blocked")
	}
}

func TestWeixinExtractText(t *testing.T) {
	got := channelcore.WeixinExtractText([]channelcore.WeixinMessageItem{
		{Type: channelcore.WeixinItemTypeVoice, VoiceItem: &channelcore.WeixinVoiceItem{Text: "voice text"}},
	})
	if got != "voice text" {
		t.Fatalf("voice text = %q", got)
	}

	got = channelcore.WeixinExtractText([]channelcore.WeixinMessageItem{
		{Type: channelcore.WeixinItemTypeText, TextItem: &channelcore.WeixinTextItem{Text: "hello"}},
	})
	if got != "hello" {
		t.Fatalf("text = %q", got)
	}
}

func TestNormalizeWeixinQRImagePreservesDataURL(t *testing.T) {
	in := "data:image/png;base64,abc123"
	if got := channelcore.NormalizeWeixinQRImage(in); got != in {
		t.Fatalf("data URL changed: %q", got)
	}
}

func TestNormalizeWeixinQRImageWrapsBase64PNG(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	got := channelcore.NormalizeWeixinQRImage(raw)
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("unexpected PNG data URL: %q", got)
	}
}

func TestNormalizeWeixinQRImageWrapsSVG(t *testing.T) {
	got := channelcore.NormalizeWeixinQRImage("<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>")
	if !strings.HasPrefix(got, "data:image/svg+xml;base64,") {
		t.Fatalf("unexpected SVG data URL: %q", got)
	}
}

func TestNormalizeWeixinQRImageExtractsImgSrc(t *testing.T) {
	in := `<img alt="qr" src="data:image/png;base64,abc123">`
	want := "data:image/png;base64,abc123"
	if got := channelcore.NormalizeWeixinQRImage(in); got != want {
		t.Fatalf("img src = %q", got)
	}
}

func TestNormalizeWeixinQRImageRejectsNonImageBase64(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte("not an image"))
	if got := channelcore.NormalizeWeixinQRImage(raw); got != "" {
		t.Fatalf("expected non-image base64 to be rejected, got %q", got)
	}
}

func TestGenerateWeixinQRScanDataURL(t *testing.T) {
	got := channelcore.GenerateWeixinQRScanDataURL("https://ilinkai.weixin.qq.com/connect?token=abc")
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("unexpected QR data URL: %q", got)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got, prefix))
	if err != nil {
		t.Fatalf("decode QR PNG: %v", err)
	}
	if len(decoded) < 8 || decoded[0] != 0x89 || decoded[1] != 'P' || decoded[2] != 'N' || decoded[3] != 'G' {
		t.Fatalf("QR data URL is not a PNG: % x", decoded[:min(len(decoded), 8)])
	}
}

func TestUpsertWeixinLoginConfig(t *testing.T) {
	cfg := &Config{}
	name := upsertWeixinLoginConfig(cfg, "weixin", &channelcore.WeixinQRWaitResult{
		AccountID: "bot@im.bot",
		Token:     "token",
		BaseURL:   "https://example.test",
		UserID:    "owner@im.wechat",
	})

	if name != "weixin" {
		t.Fatalf("channel name = %q", name)
	}
	if len(cfg.Channels) != 1 {
		t.Fatalf("channels len = %d", len(cfg.Channels))
	}
	ch := cfg.Channels[0]
	if ch.Type != "weixin" || !ch.Enabled {
		t.Fatalf("channel = %#v", ch)
	}
	if ch.Config["account_id"] != "bot@im.bot" || ch.Config["token"] != "token" {
		t.Fatalf("config = %#v", ch.Config)
	}
	allowed, ok := ch.Config["allowed_users"].([]string)
	if !ok || len(allowed) != 1 || allowed[0] != "owner@im.wechat" {
		t.Fatalf("allowed_users = %#v", ch.Config["allowed_users"])
	}
}

func TestChannelConfigEqualForReloadDetectsConfigChange(t *testing.T) {
	oldCfg := ChannelConfig{Name: "weixin", Type: "weixin", Enabled: true, Config: map[string]interface{}{"token": "a"}}
	newCfg := ChannelConfig{Name: "weixin", Type: "weixin", Enabled: true, Config: map[string]interface{}{"token": "b"}}
	if channelConfigEqualForReload(oldCfg, newCfg) {
		t.Fatal("expected config change to be detected")
	}
}
