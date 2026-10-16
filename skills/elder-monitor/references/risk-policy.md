# Risk policy

Read this reference before changing risk levels, thresholds, escalation timing, or rule precedence.

## Policy intent

The policy estimates operational risk and chooses the next monitoring action. It does not diagnose disease or determine treatment.

## Rule precedence

1. Explicit help request at sufficient confidence: `critical`.
2. Suspected fall or floor posture plus confirmed no response: `critical`.
3. High-confidence suspected fall: `high`.
4. Distress cluster plus low motion: `high`.
5. Distress cluster, or smaller distress cluster plus low motion: `medium`.
6. Monitoring coverage loss: `medium` operational alert.
7. Inactivity substantially beyond a documented personal baseline: `low`.
8. Otherwise: `none`.

Higher-precedence rules cannot be downgraded by a model. A model may add evidence or raise a lower-severity result only through a separately reviewed policy path.

## Threshold policy

Values in `rules/risk-policy.json` are software-demo defaults, not clinical thresholds. A real pilot must record:

- who approved each threshold;
- which population and environment it applies to;
- the supporting evidence or pilot data;
- the effective date and policy version;
- expected false-positive and false-negative tradeoffs;
- rollback criteria.

Prefer personal baselines where available. If no baseline exists, return explicit uncertainty and avoid treating a short ordinary activity as dangerous.

## Required operational controls

- Idempotency by `event_id` and policy version.
- Alert cooldown and deduplication in the dispatch layer.
- Separate device-health alerts from person-health risk.
- Record acknowledgement and final human disposition.
- Critical-path offline operation.
- Shadow-mode evaluation before live notifications.

## External references

- [WHO ICOPE](https://www.who.int/publications/i/item/9789241550109)
- [WHO Falls](https://www.who.int/en/news-room/fact-sheets/detail/falls)
- [CDC STEADI](https://www.cdc.gov/steadi/index.html)


