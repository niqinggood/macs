"""Regression tests for executable composition and exact-artifact visual reviews."""
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest
import zipfile


ROOT = Path(__file__).resolve().parents[2]
SKILL = ROOT / "builtin/presentation/ppt-art-director"


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


design = load("pptx_design", SKILL / "assets/task-pptx/pptx_design.py")
audit = load("aesthetic_review", SKILL / "scripts/validate_aesthetic_qa.py")
visual_plan = load("visual_plan_review", SKILL / "scripts/validate_visual_plan.py")


class CompositionTests(unittest.TestCase):
    def test_regions_fit_and_do_not_overlap(self):
        for family in ("editorial", "spotlight", "comparison", "metric"):
            for mirror in (False, True):
                regions = list(design.layout_regions(family, mirror=mirror).values())
                for region in regions:
                    self.assertGreater(region.w, 0)
                    self.assertGreater(region.h, 0)
                    self.assertGreaterEqual(region.x, 0.62 - 1e-8)
                    self.assertGreaterEqual(region.y, 1.72)
                    self.assertLessEqual(region.x + region.w, 12.71 + 1e-8)
                    self.assertLessEqual(region.y + region.h, 6.78 + 1e-8)
                for i, a in enumerate(regions):
                    for b in regions[i + 1:]:
                        self.assertFalse(a.x < b.x + b.w and a.x + a.w > b.x and a.y < b.y + b.h and a.y + a.h > b.y)

    def test_invalid_geometry_is_rejected(self):
        for kwargs in ({"primary_share": 1}, {"x": -1}, {"gap": 9}, {"w": float("nan")}, {"mirror": "false"}):
            with self.assertRaises(ValueError):
                design.layout_regions("editorial", **kwargs)
        with self.assertRaises(ValueError):
            design.layout_regions("made-up")

    def test_plan_drives_real_geometry(self):
        builder = load("task_builder_test", SKILL / "assets/task-pptx/build_deck.py")
        builder.SLIDE_VISUALS = {2: {"layout_family": "editorial", "primary_share": 0.72, "mirror": True}}
        result = builder.planned_layout(2)
        self.assertGreater(result["primary"].x, result["secondary"].x)
        self.assertGreater(result["primary"].w, result["secondary"].w)
        with self.assertRaises(ValueError):
            builder.planned_layout(3)

    def test_native_chart_preserves_chinese_fonts_and_flat_connectors(self):
        deck = design.new_presentation()
        slide = design.new_slide(deck)
        connector = design.line(slide, 1, 1, 4, 1)
        self.assertFalse(connector.shadow.inherit)
        chart = design.native_chart(slide, 1, 2, 8, 4, ["类别"], [{"name": "数值", "values": [3]}], show_legend=True, show_data_labels=True)
        for font in (chart.font, chart.legend.font, chart.category_axis.tick_labels.font, chart.value_axis.tick_labels.font, chart.plots[0].data_labels.font):
            self.assertEqual(font._element.find(design.qn("a:ea")).get("typeface"), design.DEFAULT_THEME.font_cn)

    def test_malformed_plan_reports_contract_errors_without_crashing(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "plan.json"
            path.write_text(json.dumps({"selected_direction": {"background_rhythm": None}, "slides": [{"slide": 1, "layout_family": {"invalid": True}}]}), encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "layout_family is unsupported"):
                visual_plan.validate(path)


class AestheticEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.deck, self.qa, self.render = root / "deck.pptx", root / "qa.json", root / "render"
        self.render.mkdir()
        with zipfile.ZipFile(self.deck, "w") as archive:
            archive.writestr("ppt/slides/slide1.xml", "<slide/>")
        self.image = self.render / "slide_01.png"
        self.image.write_bytes(b"synthetic image bytes for identity tests")
        result = audit.prepare(self.qa, self.deck, self.render)
        self.assertFalse(result["aesthetic_qa_passed"])

    def reviewed(self):
        qa = json.loads(self.qa.read_text(encoding="utf-8"))
        qa["verdict"] = "pass"
        qa["deck_scores"] = {key: 80 for key in audit.DECK_KEYS}
        qa["slides"][0]["scores"] = {key: 80 for key in audit.SLIDE_KEYS}
        qa["slides"][0]["observations"] = ["Synthetic note for validator test, not actual visual QA."]
        self.qa.write_text(json.dumps(qa), encoding="utf-8")
        return qa

    def test_preparation_is_not_review_and_never_overwrites(self):
        with self.assertRaises(ValueError):
            audit.validate(self.qa, self.deck, self.render)
        with self.assertRaises(FileExistsError):
            audit.prepare(self.qa, self.deck, self.render)
        self.reviewed()
        self.assertTrue(audit.validate(self.qa, self.deck, self.render)["hash_bound"])

    def test_changed_deck_or_image_invalidates_review(self):
        self.reviewed()
        self.image.write_bytes(b"changed image")
        with self.assertRaisesRegex(ValueError, "image_sha256"):
            audit.validate(self.qa, self.deck, self.render)
        with zipfile.ZipFile(self.deck, "a") as archive:
            archive.writestr("docProps/core.xml", "changed deck")
        with self.assertRaisesRegex(ValueError, "deck_sha256"):
            audit.validate(self.qa, self.deck, self.render)

    def test_external_image_boolean_slide_and_object_defect_fail(self):
        for field, value in (("image", "../slide_01.png"), ("slide", True), ("blocking_defects", [{"type": "overlap"}])):
            qa = self.reviewed()
            (self.render.parent / "slide_01.png").write_bytes(self.image.read_bytes())
            qa["slides"][0][field] = value
            self.qa.write_text(json.dumps(qa), encoding="utf-8")
            with self.assertRaises(ValueError):
                audit.validate(self.qa, self.deck, self.render)


if __name__ == "__main__":
    unittest.main()
