# Aesthetic Critic contract

Structural validity is necessary but does not establish visual quality. After
the exact final PPTX passes structural audit and native rendering, inspect the
contact sheet for deck rhythm and every full-size slide image separately.

For each slide, record 0–100 scores for:

- `hierarchy`: the intended focal point is immediately obvious;
- `composition`: balance, alignment, whitespace, and scale feel deliberate;
- `typography`: readable sizes, line breaks, measure, and emphasis;
- `asset_quality`: images/diagrams/charts are sharp, coherent, and intentional;
- `visual_relevance`: the primary visual proves or explains the claim;
- `density_balance`: neither document-like crowding nor accidental emptiness.

Also score the deck for `coherence`, `rhythm`, `visual_storytelling`, and
`originality`. Passing requires every slide dimension to be at least 65, every
slide average at least 75, every deck score at least 75, exact image coverage,
and no blocking defects. `blocking_defects` includes clipping, overlap,
overflow, unreadable copy, broken charts, missing assets, serialized config,
or a visually misleading claim.

After rendering and individual visual inspection, prepare an unreviewed ledger with `scripts/validate_aesthetic_qa.py
--prepare --qa <new-ledger.json> --deck <final.pptx> --render-dir <final-images>`.
It records `schema_version=2`, the exact deck SHA-256, and the SHA-256 of every
slide image. Its empty scores and `unreviewed` verdict cannot pass validation.
The command refuses to overwrite a prior review. After a rebuild or rerender,
prepare a new ledger path and inspect the new images; do not copy old scores
onto changed images. Keep the previous review as the repair record.

Fill the prepared ledger only from actual vision observations. Run
`scripts/validate_aesthetic_qa.py` against the exact deck and render directory.
If it fails, make one focused correction, rebuild, rerun structural audit,
rerender, reinspect every changed slide, and replace the ledger. A build after
the ledger invalidates it.

Hashes establish which artifacts were reviewed, not whether the reviewer saw
them or whether a judgment is correct. Actual visual inspection remains
required. `blocking_defects` must be empty to pass; object-shaped defects are
not silently ignored. External images and stale image identities are rejected.
