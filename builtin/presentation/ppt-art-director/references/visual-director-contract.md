# Visual Director contract

`deck_spec.json` owns facts and narrative. `visual_plan.json` owns the visual
world and the evidence-bearing asset plan. Both are required for a substantive
Level-2 deck; neither may be used as decorative paperwork.

## Direction selection

1. Freeze the communication job and audience first.
2. Propose two or three genuinely different directions. Change the material,
   imagery, composition, and rhythm—not merely the accent color.
3. Select exactly one direction and record why it communicates better. When a
   supplied brand or inspected reference fully owns the visual system, set
   `reference_owned=true`; one selected direction is then sufficient.
4. Use the selected palette and typography as executable tokens. The default
   no-reference floor is 50pt deck title, 35pt slide title, 24pt subhead, and
   16pt body.

For premium or beauty-first work, create a small direction preview/contact
sheet before full production whenever the active renderer supports it. The
preview should compare the cover, one evidence page, and one system/diagram
page—not three color swatches.

## Asset planning

Every slide records a narrative job, composition, density, focal anchor, and
one primary asset. Allowed primary asset types are `photo`, `illustration`,
`screenshot`, `chart`, `table`, `diagram`, `map`, `icon-system`, and
`typographic`.

- Photos, illustrations, and screenshots require a real local `path`, an
  attribution or generation `source`, a communication `purpose`, and a crop
  mode. They must be relevant to the claim; generic office stock imagery is a
  failure.
- Charts, tables, diagrams, and maps require a source-bound purpose. Never
  invent numbers to make a page more visual.
- Use icons as a coherent system, not mixed emoji or random glyphs.
- A substantive deck should have a non-typographic proof object on at least
  half its pages. Diagrams and native data graphics count.
- Two consecutive pages may share a composition family; three may not. A
  6–7-page deck needs at least three families, and an 8+-page deck at least
  four.

Run `scripts/validate_visual_plan.py` before the first build. Then make the
task-local renderers consume `SLIDE_VISUALS` and the selected `THEME`. The
builder checks that planned media becomes a real picture and planned native
charts/tables become real editable objects.

## Executable composition regions

For body pages, optionally add `layout_family`, `primary_share` (0.55–0.78,
default 0.68), and `mirror` (boolean) to the page's visual plan. The task
builder exposes `planned_layout(page)`, returning named regions with an
inch-valued `.box` tuple `(x, y, w, h)`. This consumes the actual plan rather
than asking the model to recalculate margins on every page.

| Family | Primary region | Secondary region | Use |
| --- | --- | --- | --- |
| `editorial` | Dominant exhibit | Narrow interpretation column | Photo, chart, annotated screenshot |
| `spotlight` | Wide exhibit above | Concise interpretation below | Mechanism, trend, evidence with a clear conclusion |
| `comparison` | Left exhibit | Right exhibit, plus `conclusion` below | Alternatives with a common scale |
| `metric` | Typographic anchor | Larger explanation or diagram | One sourced measure with its context |

Place native charts, tables, images and text inside these regions. The helper
draws no cards, containers or decorations. Each page still owns its narrative,
focal scale, content and rendering function. Use `layout_regions(...)` directly
for deliberate custom body bounds, or task-local geometry for other families.
Do not force the cover or a supplied template into a body-page region system.

Example inside a page renderer:

```python
regions = planned_layout(page)
native_chart(slide, *regions["primary"].box, categories, series, theme=THEME)
note = regions["secondary"]
text_box(slide, note.x, note.y, note.w, 0.9, finding, size=24, bold=True, theme=THEME)
text_box(slide, note.x, note.y + 1.2, note.w, note.h - 1.2, explanation, size=18, theme=THEME)
```

Use this example with `editorial`, where the interpretation column is tall.
For `spotlight`, keep the lower strip to a short line or two. Preserve the
selected typography and never shrink dense content just to fit a region.
