# Joys360 / DeepAffex-style health data

Read this reference only for Joys360 responses or DeepAffex point IDs.

## What the inspected endpoint returns

The inspected Joys360 endpoint returned HTTP 200 with this shape:

```json
{
  "header": {"code": 0, "msg": "..."},
  "body": {
    "list": [
      {
        "abbrName": "BP_SYSTOLIC",
        "name": "收缩压",
        "score": "126.4",
        "ovalue": "1264452.0",
        "tabList": "NOTE_MISSING_MEDICAL_INFO,NOTE_DEGRADED_ACCURACY"
      }
    ]
  }
}
```

The sample contained 42 points spanning camera/device-estimated vital signs, anthropometrics, proprietary wellness scores, and model-estimated disease risks. `score` is the rounded display value. `ovalue` is a higher-precision scaled internal value and must not be shown or interpreted as the clinical value.

The identifiers and note codes match the public DeepAffex Points and Measurement documentation. This strongly suggests a wrapper or downstream integration, but it is an inference rather than a verified Joys360 vendor statement.

## Quality notes

Translate and surface every note:

| Code | Meaning | Reporting consequence |
|---|---|---|
| `NOTE_USED_PRED_DEMOG` | Required user profile data was predicted because entered data was missing | Do not treat predicted sex, age, height, or weight as known facts; dependent results have limited reliability |
| `NOTE_MISSING_MEDICAL_INFO` | Medical-history questionnaire data was not supplied | Risk estimates are incomplete and must not drive diagnosis or treatment |
| `NOTE_DEGRADED_ACCURACY` | Signal computation had degraded accuracy | Mark the affected point as limited-quality and request repeat/confirmation |
| `NOTE_SNR_BELOW_THRESHOLD` | Signal-to-noise ratio was below the vendor threshold | Treat dependent signals as unreliable |
| `NOTE_FT_LIVENSSS_FAILED` | First liveness check failed | Treat the measurement as unreliable |
| `NOTE_MODEL_LIVENSSS_FAILED` | Second liveness check failed | Treat the measurement as unreliable |

Do not silently average away or omit a quality warning.

## Point semantics

Use the point ID, not the localized `name`, as the stable key. Some source labels can be “未获取” even when the point ID identifies the metric.

- Vitals/device estimates: `BP_SYSTOLIC` and `BP_DIASTOLIC` (mmHg), `HR_BPM` (beats/min), `BR_BPM` (breaths/min), `TEMPERATURE_SENSOR` (°C), `HRV_SDNN` (ms), `IHB_COUNT` (count).
- Estimated anthropometrics: `HEIGHT` (cm), `WEIGHT` (kg), `BMI_CALC` (kg/m²), `WAIST_CIRCUM` (cm), `WAIST_TO_HEIGHT` (%), `AGE` and `AGE_CVM` (years), `GENDER` (unmapped vendor code).
- Model-risk estimates in percent: `HPT_RISK_PROB`, `DBT_RISK_PROB`, `FLD_RISK_PROB`, `MFBG_RISK_PROB`, `HBA1C_RISK_PROB`, `HDLTC_RISK_PROB`, `TG_RISK_PROB`, `OVERALL_METABOLIC_RISK_PROB`, `BP_CVD`, `BP_HEART_ATTACK`, `BP_STROKE`, and `CVD_MULTI_YEAR_RISK_PROBS`.
- Proprietary/unitless scores: `HEALTH_SCORE`, `VITAL_SCORE`, `PHYSIO_SCORE`, `PHYSICAL_SCORE`, `MENTAL_SCORE`, `RISKS_SCORE`, `MSI`, `SLEEP_QUALITY`, `ANXIETY_INDEX`, and `VITALITY`.
- Metadata/other model points: `SNR` (dB), `CVD_MULTI_YEAR_RISK_YEARS` (years), `ABSI`, `BP_RPP`, and `BP_TAU`.

Do not infer a biological-sex label from `GENDER` numeric codes unless the vendor's current mapping is supplied. Do not apply ordinary laboratory reference ranges to model-risk percentages.

## Interpretation rules

- Label values as “camera/device estimate” or “model risk estimate”, not as a blood test or confirmed vital sign.
- A high `MFBG_RISK_PROB` means the model estimates elevated risk of fasting glucose above its stated threshold; it is not a fasting-glucose reading. Confirm with a standard fasting glucose and/or HbA1c ordered or interpreted by a clinician.
- A high `HBA1C_RISK_PROB` is not an HbA1c percentage. A high `HDLTC_RISK_PROB` or `TG_RISK_PROB` is not a lipid-panel result.
- Blood-pressure estimates require repeat measurement with a validated cuff under standard conditions. Diagnosis requires the repetition/context specified by the applicable guideline.
- Estimated height, weight, waist, BMI, and age should be replaced with direct measurements before clinical interpretation.
- Proprietary wellness scores have no general clinical diagnostic cutoff. Report their direction only if the vendor's current scale is available.
- Do not rank different model-risk percentages by magnitude. They describe different endpoints and may use different horizons or model semantics. The normalizer uses a stable display order only.
- Missing, nonnumeric, conflicting duplicate, or machine-implausible values are quarantined as `待确认`. Broad machine sanity bounds are data-integrity checks, not clinical normal/abnormal thresholds.

## Security

The URL contains an access token in its query string. Never echo it, cite it, persist it in a report, commit it, include it in an error message, or place it in a command/process argument or environment variable. Prefer a protected JSON file. Use `--url-stdin` only through a host channel explicitly designed not to log sensitive stdin; `--url-file` is limited to a protected ephemeral file. The hidden legacy `--url` option is accepted only to return a constant error and is otherwise disabled. The normalizer accepts only HTTPS requests to `www.joys360.com` and the inspected endpoint path, rejects redirects, caps downloads, ignores unknown source fields, and emits no raw query string.

Structured output uses schema `med-guid.joys360.v2`. `result.fact_sha256` identifies the normalized facts, `result.coverage` reports parsed/valid/actionable/invalid/ignored/duplicate counts, and every metric has `value_status` plus a per-metric `quality_level`. Unknown point identifiers are replaced by stable opaque IDs, unknown quality codes are collapsed to `NOTE_UNRECOGNIZED`, and noncanonical source labels are not retained for display. `--format bundle` returns the selected `report`, all deterministic `reports` modes, and the `result` snapshot, so a later mode change selects the saved rendering without refetching or reconstructing facts.

All three rendered modes include a visible TCM initial-differentiation component. With endpoint data alone it is always labeled `C 级（仅设备/模型资料）`: it routes available metabolic/body, mind/sleep, and cardiovascular domains to unranked `待辨方向`, decisive symptom/tongue/pulse questions, and low-risk care principles. It must not call a candidate direction a confirmed syndrome or infer that a vendor score is high/abnormal. When the surrounding conversation supplies symptoms or credible tongue/pulse context, the agent may replace the C-level text with a narrower A/B-level preliminary pattern while preserving the endpoint facts.

The fact hash covers the endpoint payload, not symptoms or medical history supplied in the surrounding conversation. Keep those user-supplied facts in a companion case snapshot. Active red flags override the device-only urgency wording; lead with emergency guidance. For non-emergency context, preserve the endpoint facts but correct any generated “not included” statement when the user has actually supplied that information.

## Vendor references

- DeepAffex point catalog: https://docs.deepaffex.ai/points/introduction.html
- DeepAffex measurement note definitions: https://docs.deepaffex.ai/guide/cloud/6_measurements.html
- Example risk-point definition: https://docs.deepaffex.ai/points/blood_biomarkers/MFBG_RISK_PROB.html

Re-check these pages when exact semantics or ranges matter; vendor models and documentation can change.
