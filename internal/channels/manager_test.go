package channels

import "testing"

type managerTestTransport struct{ name string }

func (transport *managerTestTransport) Name() string                     { return transport.name }
func (transport *managerTestTransport) Type() string                     { return "test" }
func (transport *managerTestTransport) Start() error                     { return nil }
func (transport *managerTestTransport) Stop() error                      { return nil }
func (transport *managerTestTransport) SendMessage(string, string) error { return nil }

func TestManagerRejectsDuplicateChannelName(t *testing.T) {
	manager := NewManager(nil)
	if err := manager.Register(&managerTestTransport{name: "primary"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Register(&managerTestTransport{name: "primary"}); err == nil {
		t.Fatal("expected duplicate channel registration to fail")
	}
}
