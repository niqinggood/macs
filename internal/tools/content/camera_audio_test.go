package content

import (
	"context"
	"testing"
)

func TestCameraAndAudioToolMetadata(t *testing.T) {
	camera := NewCameraTool(t.TempDir())
	if camera.Name() != "camera" {
		t.Fatalf("camera name = %q", camera.Name())
	}
	audio := NewAudioTool(t.TempDir())
	if audio.Name() != "audio" {
		t.Fatalf("audio name = %q", audio.Name())
	}
}

func TestCameraToolRequiresStringAction(t *testing.T) {
	tool := NewCameraTool(t.TempDir())
	if _, err := tool.Execute(context.Background(), map[string]interface{}{}); err == nil {
		t.Fatal("camera accepted a missing action")
	}
}
