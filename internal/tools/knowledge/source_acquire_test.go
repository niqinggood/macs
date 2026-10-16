package knowledge

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type captureSourceProvider struct {
	params map[string]interface{}
}

func (provider *captureSourceProvider) Execute(_ context.Context, params map[string]interface{}) (string, error) {
	provider.params = params
	return "frozen", nil
}

func TestSourceAcquireRoutesDescriptionVerbatim(t *testing.T) {
	provider := &captureSourceProvider{}
	tool := NewSourceAcquireTool(t.TempDir(), provider)
	const description = "chatroom 有一篇星河缓存转型帖子"
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"description": description,
		"max_results": float64(2),
		"max_pages":   float64(3),
		"output_path": ".openkitty/storage/source.json",
	})
	if err != nil || result != "frozen" {
		t.Fatalf("unexpected provider result: result=%q err=%v", result, err)
	}
	want := map[string]interface{}{
		"query": description, "max_results": float64(2), "max_pages": float64(3),
		"output_path": ".openkitty/storage/source.json",
	}
	if !reflect.DeepEqual(provider.params, want) {
		t.Fatalf("provider arguments changed: got=%#v want=%#v", provider.params, want)
	}
}

func TestSourceAcquireRejectsUnknownProviderWithoutGuessing(t *testing.T) {
	tool := NewSourceAcquireTool(t.TempDir(), &captureSourceProvider{})
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"description": "某个未配置社区里的一篇星河缓存文章",
		"output_path": ".openkitty/storage/source.json",
	})
	if err == nil || !strings.Contains(err.Error(), "no trusted provider matched") {
		t.Fatalf("expected unsupported-provider error, got %v", err)
	}
}

func TestSourceAcquireTraitsAndScope(t *testing.T) {
	workspace := t.TempDir()
	tool := NewSourceAcquireTool(workspace, nil)
	if tool.Name() != "source_acquire" || !tool.IsReadOnly() || !tool.IsNeverParallel() || tool.MaxResultSize() != 16*1024 {
		t.Fatalf("unexpected tool traits")
	}
	got := tool.ScopePath(map[string]interface{}{"output_path": filepath.Join(".openkitty", "storage", "source.json")})
	want := filepath.Join(workspace, ".openkitty", "storage", "source.json")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("ScopePath() = %q, want %q", got, want)
	}
}
