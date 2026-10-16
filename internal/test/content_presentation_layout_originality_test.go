package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPPTArtDirectorStyleReferenceContractIsContentFirst(t *testing.T) {
	checks := map[string][]string{
		filepath.Join("builtin", "presentation", "ppt-art-director", "SKILL.md"): {
			"content-first slide architecture",
			"Never treat the reference as a page-layout library",
			"--style-reference",
			"composition family",
			"focal anchor",
		},
		filepath.Join("builtin", "presentation", "ppt-art-director", "references", "template-rendering-contract.md"): {
			"Content-first architecture for style references",
			"design-DNA allowlist",
			"never a slide-layout library",
			"--style-reference",
		},
		filepath.Join("builtin", "presentation", "ppt-art-director", "assets", "task-pptx", "build_deck.py"): {
			"SPEC_PATH",
			"needs_customization",
			"ALLOWED_KINDS",
			"native_chart",
			"editable_table",
		},
	}

	for path, markers := range checks {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, marker := range markers {
			if !strings.Contains(text, marker) {
				t.Fatalf("%s is missing style-reference originality marker %q", path, marker)
			}
		}
	}
}

func TestPPTArtDirectorAuditEnforcesLayoutOriginality(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not installed")
	}
	if output, err := exec.Command(python, "-c", "import pptx").CombinedOutput(); err != nil {
		t.Skipf("python-pptx unavailable: %v: %s", err, output)
	}

	dir := t.TempDir()
	reference := filepath.Join(dir, "reference.pptx")
	clone := filepath.Join(dir, "clone.pptx")
	independent := filepath.Join(dir, "independent.pptx")
	repeated := filepath.Join(dir, "repeated.pptx")

	fixtureScript := `
from pptx import Presentation
from pptx.enum.shapes import MSO_SHAPE
from pptx.util import Inches, Pt
import sys

reference_patterns = [
    [(0.6,1.4,3.7,2.1),(4.8,1.4,3.7,2.1),(9.0,1.4,3.7,2.1),(0.6,4.0,3.7,2.1),(4.8,4.0,3.7,2.1),(9.0,4.0,3.7,2.1)],
    [(0.8,1.35+j*0.86,11.7,0.63) for j in range(6)],
    [(0.7,1.3,5.0,4.8),(6.1,1.3,2.0,1.4),(8.4,1.3,4.3,1.4),(6.1,3.1,3.0,1.4),(9.4,3.1,3.3,1.4),(6.1,4.9,6.6,1.2)],
    [(0.8+j*2.05,1.2+j*0.82,1.5,1.05) for j in range(6)],
    [(0.7,1.3,3.6,1.1),(4.85,1.3,3.6,1.1),(9.0,1.3,3.6,1.1),(1.2,3.2,3.5,2.5),(4.9,3.2,3.5,2.5),(8.6,3.2,3.5,2.5)],
]

independent_patterns = [
    [(0.7,1.4,7.4,4.9)] + [(8.6,1.4+j*0.95,3.8,0.68) for j in range(5)],
    [(0.7,1.4,12.0,1.0)] + [(0.7+j*2.4,2.8,2.0,3.3) for j in range(5)],
    [(0.7,1.4,2.2,4.9)] + [(3.3,1.4+j*1.03,9.4,0.72) for j in range(5)],
    [(0.7,1.4,5.7,1.4),(6.9,1.4,5.7,1.4),(0.7,3.2,2.6,2.9),(3.6,3.2,2.6,2.9),(6.9,3.2,2.6,2.9),(9.9,3.2,2.7,2.9)],
    [(0.7,1.4,3.0,4.9),(4.1,1.4,8.5,2.1),(4.1,3.9,2.5,2.4),(6.9,3.9,2.5,2.4),(9.8,3.9,2.8,2.4),(4.1,6.55,8.5,0.25)],
]

def build(path, patterns, prefix):
    deck = Presentation()
    deck.slide_width = Inches(13.333)
    deck.slide_height = Inches(7.5)
    blank = deck.slide_layouts[6]
    for page in range(7):
        slide = deck.slides.add_slide(blank)
        title = slide.shapes.add_textbox(Inches(0.65), Inches(0.28), Inches(11.8), Inches(0.55))
        title.text = f'{prefix} page {page + 1}'
        title.text_frame.paragraphs[0].font.size = Pt(24)
        if page == 0:
            coords = [(0.8,2.1,7.6,1.4),(9.2,2.1,2.6,2.6)]
        elif page == 6:
            coords = [(1.0,2.0,11.2,1.3),(3.2,4.2,6.8,1.0)]
        else:
            coords = patterns[page - 1]
        for item, (x,y,w,h) in enumerate(coords):
            shape = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(x), Inches(y), Inches(w), Inches(h))
            shape.text = f'{prefix}-{page}-{item} evidence'
            shape.text_frame.paragraphs[0].font.size = Pt(18)
    deck.save(path)

build(sys.argv[1], reference_patterns, 'reference')
build(sys.argv[2], reference_patterns, 'new-copy')
build(sys.argv[3], independent_patterns, 'new-original')
repeated_patterns = [reference_patterns[0], reference_patterns[0], independent_patterns[2], independent_patterns[3], independent_patterns[4]]
build(sys.argv[4], repeated_patterns, 'repeated')
`
	create := exec.Command(python, "-c", fixtureScript, reference, clone, independent, repeated)
	create.Env = append(os.Environ(), "PYTHONUTF8=1")
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create layout-originality fixtures: %v\n%s", err, output)
	}

	audit := filepath.Join("builtin", "presentation", "ppt-art-director", "scripts", "audit_pptx.py")
	if output, err := exec.Command(python, audit, clone).CombinedOutput(); err != nil {
		t.Fatalf("content-valid clone should require the reference comparison to fail: %v\n%s", err, output)
	}

	output, err := exec.Command(python, audit, clone, "--style-reference", reference).CombinedOutput()
	if err == nil {
		t.Fatalf("style-reference layout clone must fail the audience-facing quality gate:\n%s", output)
	}
	if !strings.Contains(string(output), "style-reference layout cloning detected") {
		t.Fatalf("style-reference clone warning lacks diagnosis: %s", output)
	}

	if output, err := exec.Command(python, audit, independent, "--style-reference", reference).CombinedOutput(); err != nil {
		t.Fatalf("content-derived independent layouts were rejected: %v\n%s", err, output)
	}

	output, err = exec.Command(python, audit, repeated).CombinedOutput()
	if err == nil {
		t.Fatalf("repeated audience-facing layouts must fail the quality gate:\n%s", output)
	}
	if !strings.Contains(string(output), "adjacent content slides reuse the same body geometry") {
		t.Fatalf("repeated-layout warning lacks diagnosis: %s", output)
	}

	if output, err := exec.Command(python, audit, repeated, "--allow-repeated-layouts").CombinedOutput(); err != nil {
		t.Fatalf("explicit recurring-layout relaxation should pass with a warning: %v\n%s", err, output)
	}

	output, err = exec.Command(
		python,
		audit,
		clone,
		"--style-reference",
		reference,
		"--allow-repeated-layouts",
	).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "style-reference layout cloning detected") {
		t.Fatalf("recurring-layout flag must not relax style-reference cloning: err=%v\n%s", err, output)
	}
}
