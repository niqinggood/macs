package app

import (
	"strings"
	"testing"
)

func TestWechatStorageRelativePath(t *testing.T) {
	got, ok := wechatStorageRelativePath(`E:\deploy`, `E:\deploy\.openkitty\storage\showcase\article.md`)
	if !ok || got != ".openkitty/storage/showcase/article.md" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	if _, ok := wechatStorageRelativePath(`E:\deploy`, `E:\outside\article.md`); ok {
		t.Fatal("outside path should be rejected")
	}
}

func TestWechatCustomOutputPipelineOverridesArtifactContract(t *testing.T) {
	base := (&GatewayService{}).defaultPipelineSet()["wechat-article"]
	gw := &GatewayService{config: &Config{Workspace: `E:\deploy`}}
	gw.pipelineExec = &PipelineExecutor{pipelines: map[string]Pipeline{"wechat-article": base}}
	mgr := &AIEmployeeManager{gateway: gw}
	got, ok := mgr.wechatArticleCustomOutputPipeline("wechat-article", `输出到 E:\deploy\.openkitty\storage\showcase\a.md 和 E:\deploy\.openkitty\storage\showcase\a.html`)
	if !ok || len(got.Steps) < 2 {
		t.Fatalf("custom pipeline unavailable: %#v", got)
	}
	delivery := got.Steps[len(got.Steps)-1]
	if delivery.ArtifactContract == nil || len(delivery.ArtifactContract.Paths) != 2 || delivery.ArtifactContract.Paths[0] != ".openkitty/storage/showcase/a.md" || delivery.ArtifactContract.Paths[1] != ".openkitty/storage/showcase/a.html" {
		t.Fatalf("paths = %#v", delivery.ArtifactContract)
	}
	for _, want := range []string{"md_path must be exactly", "html_path must be exactly", "first 12 hexadecimal"} {
		if !strings.Contains(delivery.Prompt, want) {
			t.Fatalf("delivery prompt missing %q", want)
		}
	}
}
