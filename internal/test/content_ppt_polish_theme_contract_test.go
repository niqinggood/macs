package app

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPPTPolishRequiresGoogleStyleOnlyWhenExplicit(t *testing.T) {
	for _, input := range []string{
		"请增加一个谷歌风的漂亮清爽 PPT 风格",
		"Use a clean Google-style presentation.",
		"请使用清爽白底",
	} {
		if !pptPolishRequiresGoogleStyle(input) {
			t.Fatalf("explicit style was not detected: %q", input)
		}
	}
	for _, input := range []string{
		"做一份漂亮的商务 PPT",
		"黑金咨询风",
		"帮我润色这个演示文稿",
	} {
		if pptPolishRequiresGoogleStyle(input) {
			t.Fatalf("ordinary style was incorrectly forced to Google-clean: %q", input)
		}
	}
}

func TestPPTPolishVisualThemeContractIsInjectedForExplicitGoogleStyle(t *testing.T) {
	step := PipelineStep{Name: "视觉润色", Prompt: "build"}
	exec := &PipelineExecution{
		ID:           "pipe_google",
		PipelineName: "ppt-polish",
		Input:        "做成谷歌风格的清爽白底 PPT",
	}
	prompt := appendPipelineRuntimeContractInstructions("build", exec, []PipelineStep{step}, 0, exec.Input)
	for _, required := range []string{"GOOGLE_CLEAN_THEME", googleCleanPrimaryHex, "silently keeps the default dark consulting theme"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("theme contract missing %q:\n%s", required, prompt)
		}
	}

	exec.Input = "做成正式商务 PPT"
	prompt = appendPipelineRuntimeContractInstructions("build", exec, []PipelineStep{step}, 0, exec.Input)
	if strings.Contains(prompt, "GOOGLE_CLEAN_THEME") {
		t.Fatalf("ordinary deck unexpectedly received Google theme contract:\n%s", prompt)
	}
}

func TestPPTXContainsSRGBColorScansPresentationXML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.pptx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	part, err := writer.Create("ppt/slides/slide1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(`<p:sld xmlns:p="p" xmlns:a="a"><a:srgbClr val="4285F4"/></p:sld>`)); err != nil {
		t.Fatal(err)
	}
	decoy, err := writer.Create("docProps/core.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decoy.Write([]byte(`<color val="EA4335"/>`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	found, err := pptxContainsSRGBColor(path, "4285f4")
	if err != nil || !found {
		t.Fatalf("Google primary was not found: found=%v err=%v", found, err)
	}
	found, err = pptxContainsSRGBColor(path, "EA4335")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("non-presentation decoy color was accepted")
	}
}

func TestValidatePPTPolishRequestedThemeRejectsWrongTheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "theme.pptx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	part, err := writer.Create("ppt/theme/theme1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(`<a:theme xmlns:a="a"><a:srgbClr val="C9A86A"/></a:theme>`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if err := validatePPTPolishRequestedTheme("普通商务 PPT", path); err != nil {
		t.Fatalf("unspecified theme should not be rejected: %v", err)
	}
	err = validatePPTPolishRequestedTheme("谷歌清爽风 PPT", path)
	if err == nil || !strings.Contains(err.Error(), googleCleanPrimaryHex) {
		t.Fatalf("wrong explicit theme was accepted: %v", err)
	}
}
