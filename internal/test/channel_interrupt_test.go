package app

import "testing"

func TestTryInjectChannelInterruptSendsToRunningSession(t *testing.T) {
	sessionID := "test_interrupt_session"
	interruptChannels.Delete(sessionID)
	ch := registerSessionInterrupt(sessionID)
	defer interruptChannels.Delete(sessionID)

	if !tryInjectChannelInterrupt(&GatewayService{}, sessionID, "qq", "user-1", "extra context") {
		t.Fatal("expected interrupt injection to active session")
	}

	select {
	case got := <-ch:
		if got != "extra context" {
			t.Fatalf("injected message = %q, want extra context", got)
		}
	default:
		t.Fatal("expected injected message in channel")
	}
}

func TestUnregisterSessionInterruptKeepsNewerChannel(t *testing.T) {
	sessionID := "test_interrupt_replace"
	interruptChannels.Delete(sessionID)
	oldCh := registerSessionInterrupt(sessionID)
	newCh := registerSessionInterrupt(sessionID)
	defer interruptChannels.Delete(sessionID)

	unregisterSessionInterrupt(sessionID, oldCh)
	if current, ok := interruptChannels.Load(sessionID); !ok || current != newCh {
		t.Fatal("old unregister should not remove newer interrupt channel")
	}

	unregisterSessionInterrupt(sessionID, newCh)
	if _, ok := interruptChannels.Load(sessionID); ok {
		t.Fatal("new unregister should remove current interrupt channel")
	}
}
