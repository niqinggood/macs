# Template and Rendering Contract

Treat the template source and the rendering engine as separate decisions. A deck
can use no template, a registered OpenKitty template family, a user deck as a
style reference, or a user PPTX as a strict native template. None of those labels
alone decides the renderer.

## Presentation Brief

Before choosing a route, freeze a compact internal brief with:

- communication job and audience;
- source evidence and required claims;
- narrative arc and page-count range;
- editability requirement: native elements, grouped SVG, or image-backed;
- speed/quality priority;
- template intent and template path or family when present;
- visual rationale derived from topic, brand, or source material.

Do not create a planning file merely to record this brief unless recovery across
many rounds is useful.

## Template Intent

Choose exactly one intent:

1. `strict-existing`: preserve a user-supplied PPTX's masters, layouts,
   placeholders, typography, brand chrome, canvas, and page markers. Inspect the
   PPTX first. Fill inherited placeholders only unless an explicit object-level
   map permits more. Never route this intent through PPTMaster or an unrelated
   visual system.
2. `style-reference`: use a supplied deck only as evidence for palette,
   typography, density, image treatment, grid rhythm, and shape language. A
   task-specific renderer or PPTMaster may rebuild the deck, but the result
   must be described as a restyle, not strict template preservation. A style
   reference is never a slide-layout library: do not reuse its page order,
   slide-by-slide geometry, or composition-family sequence.
3. `registered-family`: use an OpenKitty template family. Select its native or
   SVG implementation by the requested editing fidelity and visual ambition.
4. `none`: create a topic-led visual system. Do not fall back to a universal
   business palette or repeated report chrome.

If the user's wording distinguishes strict use ("use this template") from style
reference ("make it feel like this"), proceed without asking. Ask only when the
difference would materially change the deliverable and cannot be inferred.

## Content-first architecture for style references

For `style-reference`, use two independent passes in this order:

1. From the new source only, freeze one row per slide with `narrative job`,
   `takeaway`, `evidence form`, `composition family`, and `focal anchor`. Choose
   the composition because it best explains that page's claim. A medium
   audience-facing deck should normally use several genuinely different
   families, with no adjacent content pages sharing the same body skeleton.
2. Inspect the reference and extract a compact design-DNA allowlist: palette,
   font pairing and scale, margins/grid, rules and borders, density, chart
   treatment, and image treatment. Apply those tokens to the already-frozen
   architecture. Do not map new slide 4 to reference slide 4, search the
   reference for a matching page to copy, or preserve the reference's sequence
   after changing only words and colors.

The distinction is intentional: consistency comes from design tokens and
spacing; page variety comes from the new content's narrative jobs. Cover and
closing may echo the reference's visual world, but body geometry remains
content-derived.

## Renderer Responsibilities

| Renderer | Best fit | Do not use for |
| --- | --- | --- |
| `base-ppt` | fast recurring reports, simple native blocks, compatible native template placeholders | bespoke audience-facing stories or uninspected strict templates |
| task-specific native renderer | page-specific editable compositions, evidence-led analysis, diagrams, charts, decision decks | a request that explicitly requires the full upstream PPTMaster workflow |
| PPTMaster | premium SVG-first visual storytelling, source-to-deck transformation, registered SVG template families | strict preservation of an existing native PPTX master |
| HTML/canvas route | animation or maximum packed visual fidelity | element-level native PowerPoint editing unless disclosed |

PPTMaster is eligible for autonomous selection when visual storytelling is the
dominant requirement, the content is frozen, strict native-template fidelity is
not required, grouped/native SVG editing is acceptable, and the task is not an
explicit fast draft. An explicit PPTMaster request remains a hard override.

## Registered Template Family

A reusable family should expose one manifest and optional engine-specific
implementations:

```text
templates/<family>/
  template.yaml
  tokens.json
  preview.png
  native/template.pptx
  native/layout_map.json
  svg/design_spec.md
  svg/*.svg
```

The manifest records audience/use cases, canvas, fonts, palette, density,
supported layout families, editability, and available renderers. Do not pretend
that an SVG template and a native PPTX master have identical editing semantics.

## Existing PPTX Gate

Use the template inspector supplied by `base-ppt` before strict generation. Its
manifest is evidence, not a route decision. Confirm that the selected layouts
have suitable placeholders and that the intended content fits them. If they do
not, either obtain an explicit object map or report that strict filling cannot
express the requested pages. Never silently overlay generic headers, cards, or
footers and call that template fidelity.

## Unified Handoff and QA

Every renderer receives the same frozen brief and slide jobs. Every final deck
must expose its renderer, template intent, source paths, and output path in the
internal handoff. Run the same structural audit, deterministic render, and real
visual inspection regardless of renderer. File existence, slide count, palette
percentages, and pixel statistics do not prove aesthetic quality.

For `style-reference`, run the structural audit with the reference path:

```powershell
python "<Runtime Directory>\scripts\audit_pptx.py" "<output.pptx>" --style-reference "<reference.pptx>"
```

Treat layout-cloning failure as a real quality failure. Correct page geometry
and focal anchors; changing copy, palette, or section labels is not a fix. Use
`--allow-repeated-layouts` only for an explicitly recurring or fixed-schema
report, never to bypass a style-reference cloning result.
