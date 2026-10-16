package routing

import "testing"

func TestChatroomDeliveryRequested(t *testing.T) {
	if !ChatroomDeliveryRequested("请发布到 Chatroom", []string{"请发布到 Chatroom"}) {
		t.Fatal("explicit delivery was not recognized")
	}
	if ChatroomDeliveryRequested("不要发布到 Chatroom", []string{"不要发布到 Chatroom"}) {
		t.Fatal("negative delivery instruction was ignored")
	}
}

func TestChatroomDeliveryStatusUsesLatestAttempt(t *testing.T) {
	satisfied, attempted := ChatroomDeliveryStatus([]ChatroomDeliveryAttempt{
		{ActionIntent: "post", Succeeded: true, ReceiptValid: true},
		{ActionIntent: "post", Succeeded: false},
	})
	if satisfied || !attempted {
		t.Fatalf("status = satisfied %v, attempted %v", satisfied, attempted)
	}
}

func TestAppendChatroomDeliveryFailureIsIdempotent(t *testing.T) {
	first := AppendChatroomDeliveryFailure("draft", "missing receipt")
	if second := AppendChatroomDeliveryFailure(first, "missing receipt"); second != first {
		t.Fatalf("second append changed output: %q", second)
	}
}
