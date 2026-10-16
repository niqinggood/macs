package skills

import (
	"io/fs"
	"strconv"
	"strings"
	"testing"

	rootassets "mac"
)

func TestPPTMasterBuiltinContainsCompleteUpstreamWorkflow(t *testing.T) {
	root := "builtin/presentation/ppt-master"
	paths := []string{
		root + "/LICENSE", root + "/NOTICE", root + "/requirements.txt", root + "/UPSTREAM_MANIFEST.sha256",
		root + "/skills/ppt-master/SKILL.md", root + "/skills/ppt-master/references/strategist.md",
		root + "/skills/ppt-master/references/shared-standards.md", root + "/skills/ppt-master/references/executor-base.md",
		root + "/skills/ppt-master/references/executor-consultant-top.md", root + "/skills/ppt-master/workflows/create-template.md",
	}
	for _, path := range paths {
		info, err := fs.Stat(rootassets.BuiltinSkillsFS, path)
		if err != nil || info.Size() == 0 {
			t.Fatalf("missing bundled upstream PPT Master file %s: info=%v err=%v", path, info, err)
		}
	}

	notice, err := fs.ReadFile(rootassets.BuiltinSkillsFS, root+"/NOTICE")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"luonghaianh1208/PPTmaster", "LƯƠNG HẢI ANH"} {
		if !strings.Contains(string(notice), want) {
			t.Fatalf("NOTICE missing upstream attribution %q", want)
		}
	}

	router, err := fs.ReadFile(rootassets.BuiltinSkillsFS, root+"/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"overrides the upstream confirmation pauses only",
		"without** `--move`",
		"never move, rename, overwrite, or delete the user's original file",
		"<runtime-dir>/requirements.txt",
		"PPTMaster is a rendering workflow, not a native PPTX template",
		"strong autonomous candidate",
		"`strict-existing` template intent",
		"SVG layout families",
	} {
		if !strings.Contains(string(router), want) {
			t.Fatalf("PPT Master router missing automation safety rule %q", want)
		}
	}
}

func TestEmbeddedPPTMasterMatchesUpstreamManifest(t *testing.T) {
	manifestData, err := fs.ReadFile(rootassets.BuiltinSkillsFS, "builtin/presentation/ppt-master/UPSTREAM_MANIFEST.sha256")
	if err != nil {
		t.Fatal(err)
	}
	manifest := map[string]string{}
	for _, line := range strings.Split(string(manifestData), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			manifest[key] = value
		}
	}
	wantCount, err := strconv.Atoi(manifest["file_count"])
	if err != nil || wantCount <= 0 {
		t.Fatalf("invalid upstream file_count: %q", manifest["file_count"])
	}
	root := "builtin/presentation/ppt-master/skills/ppt-master"
	count := 0
	if err := fs.WalkDir(rootassets.BuiltinSkillsFS, root, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			count++
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	digest, err := EmbeddedFSDirDigest(rootassets.BuiltinSkillsFS, root)
	if err != nil {
		t.Fatal(err)
	}
	if count != wantCount || digest != manifest["tree_sha256"] {
		t.Fatalf("bundled upstream tree mismatch: count=%d want=%d sha256=%s want=%s", count, wantCount, digest, manifest["tree_sha256"])
	}
}

func TestEmbeddedPPTMasterIncludesCompleteUpstreamWorkflow(t *testing.T) {
	root := "builtin/presentation/ppt-master"
	for _, rel := range []string{
		"LICENSE",
		"NOTICE",
		"requirements.txt",
		"skills/ppt-master/SKILL.md",
		"skills/ppt-master/references/strategist.md",
		"skills/ppt-master/references/shared-standards.md",
		"skills/ppt-master/references/executor-base.md",
		"skills/ppt-master/references/executor-consultant-top.md",
		"skills/ppt-master/scripts/finalize_svg.py",
		"skills/ppt-master/scripts/svg_to_pptx.py",
		"skills/ppt-master/workflows/create-template.md",
	} {
		data, err := fs.ReadFile(rootassets.BuiltinSkillsFS, root+"/"+rel)
		if err != nil || len(data) == 0 {
			t.Fatalf("embedded PPT Master missing %s: size=%d err=%v", rel, len(data), err)
		}
	}
	if !EmbeddedSkillHasRuntimeAssets(rootassets.BuiltinSkillsFS, root, SkillMetadata{Name: "ppt-master"}) {
		t.Fatal("embedded PPT Master runtime assets were not detected")
	}
}
