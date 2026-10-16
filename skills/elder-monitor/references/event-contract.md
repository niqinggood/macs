# Event contract

Read this reference when normalizing prose, adding a sensor, or troubleshooting a rejected payload.

## Normalization rules

- Produce one event object per person and observation window.
- Use a pseudonymous `person_id`; identity and contact details stay in the caller's protected system.
- Include timezone offsets in `window.start` and `window.end`.
- Express what the sensor observed, not what disease may have caused it.
- Preserve `source`, `confidence`, and an optional opaque `evidence_ref` for every observation.
- Distinguish a person who did not respond from a voice service that failed. Service failure is a `device_health` or integration error, not `no_response`.
- For a voice check, preserve `voice_check_confidence` and `voice_check_service_status`. Use `responded_to_voice_check=null` when service status is `error`; only a completed check may produce `false`.
- Use `count` for repeated observations and `duration_sec` for duration. Do not encode those values only in prose.

## Supported modality mapping

| Raw modality | Normalize to | Notes |
|---|---|---|
| Camera or video model | `posture`, `fall_suspected`, `device_health=blocked` | Do not infer injury, identity, or consciousness from appearance. |
| Audio classifier or speech intent | `distress_vocalization`, `help_request`, `device_health=error` | Preserve count, confidence, and noise uncertainty. |
| Millimeter-wave or motion sensor | `posture`, `motion_level` | Compare with a documented personal baseline when available. |
| Voice check | `no_response` plus normalized context | Service timeout/error is not `no_response`; keep the service state separate. |
| Gateway/camera/microphone status | `device_health` | Coverage loss is an operational alert, not a health observation. |

Do not force an unfamiliar vital-sign, door, bed, medication, smoke, or environmental sensor into these types. First define its physical quantity/unit, upstream alarm semantics, confidence and failure state, then extend the schema, policy, fixtures, and tests together.

## Prose example

Input:

> 老人坐了10分钟，期间检测到10次呻吟声，活动量比平常低，尚未语音确认。

Normalized observations:

```json
[
  {
    "type": "posture",
    "value": "sitting",
    "duration_sec": 600,
    "confidence": 0.96,
    "source": "mmwave"
  },
  {
    "type": "distress_vocalization",
    "value": true,
    "count": 10,
    "confidence": 0.83,
    "source": "audio_classifier"
  },
  {
    "type": "motion_level",
    "value": "below_personal_baseline",
    "confidence": 0.78,
    "source": "motion_sensor"
  }
]
```

The ten-minute sitting duration does not independently trigger an alert. The combination of repeated distress-like vocalization and low motion may.

## Adding a sensor type

Before extending `schemas/event.schema.json`:

1. Define the physical observation and its unit.
2. Define confidence semantics and failure states.
3. Decide whether it is safety-critical or supplementary.
4. Add positive, normal, noisy, stale, and contradictory fixtures.
5. Keep raw vendor payloads in an adapter; do not leak them into the common contract.
6. Add deterministic handling and a golden test before treating the new type as deployable; validation must reject unsupported types instead of silently returning `none`.

