package content

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"mac/internal/projectctx"
)

func TestResolveVisionPathUsesCurrentTenantWorkspace(t *testing.T) {
	workspace := t.TempDir()
	ctx := projectctx.WithResolvedWorkspace(context.Background(), workspace)
	got := ResolveVisionPath(ctx, ".openkitty/storage/preview/contact_sheet.jpg")
	want := filepath.Join(workspace, ".openkitty", "storage", "preview", "contact_sheet.jpg")
	if !strings.EqualFold(got, filepath.Clean(want)) {
		t.Fatalf("relative vision path resolved outside the current workspace: got %q want %q", got, want)
	}
}

func TestResolveVisionPathPreservesAbsolutePath(t *testing.T) {
	absolute := filepath.Join(t.TempDir(), "slide.png")
	ctx := projectctx.WithResolvedWorkspace(context.Background(), t.TempDir())
	if got := ResolveVisionPath(ctx, absolute); !strings.EqualFold(got, filepath.Clean(absolute)) {
		t.Fatalf("absolute vision path changed: got %q want %q", got, absolute)
	}
}
