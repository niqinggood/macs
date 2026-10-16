---
name: video-note
description: Turn videos, audio files, image posts, screenshots, or platform video links into structured Markdown notes and reusable extraction materials. Use when the user asks to process 视频号/短视频/课程视频/会议录屏/音频/图文动态/图片组, extract OCR or ASR, summarize media, create notes, produce reusable copy, or save a media analysis report. Supports local files first; platform link resolution is optional and must gracefully fall back to asking for uploaded media if unavailable.
---

# Video Note

Use this skill as the lightweight orchestration layer for media-to-notes tasks. It borrows the useful pattern from `reference/wx-video-account-notes-main`: resolve/download when possible, extract visual/audio text, build `note_materials.json`, then write a final Markdown note.

Do not make this WeChat-only. Treat WeChat Video Account links as one optional adapter among local video, audio, images, screenshots, and future platform adapters.

## Inputs

Accept any of:

- Local video: `.mp4`, `.mov`, `.mkv`, `.webm`
- Local audio: `.mp3`, `.wav`, `.m4a`, `.aac`
- Image post / screenshot group: `.png`, `.jpg`, `.jpeg`, `.webp`
- Platform link: `https://weixin.qq.com/sph/...` or other video links if an adapter exists
- Optional output directory, title, note style, and whether to keep raw materials

## Output Contract

Write outputs under `.openkitty/storage/` unless the user specifies another directory:

```text
<output-dir>/
  raw.json                 # metadata, source URL, adapter result, or local file metadata
  note_materials.json      # canonical material package for the final note
  ocr.txt                  # extracted image/frame text, may be empty with status
  asr.txt                  # extracted speech text, may be empty with status
  frames/                  # selected visual reference frames or source images
  ocr_frames/              # optional subtitle/text crops
  audio/                   # optional extracted audio
  <slug>.md                # final Markdown note unless user asks materials only
```

`note_materials.json` should contain:

```json
{
  "source": {"type": "local_video|local_audio|image_post|platform_link", "url": "", "files": []},
  "metadata": {"title": "", "author": "", "duration": "", "created_at": ""},
  "ocr_lines": [],
  "asr_lines": [],
  "visual_frames": [],
  "overview_lines": [],
  "summary_lines": [],
  "reusable_lines": [],
  "status": {"resolve": "", "download": "", "ocr": "", "asr": "", "vision": ""}
}
```

## Workflow

1. **Resolve source**
   - Local files: use directly.
   - Platform links: use an adapter only if configured.
   - WeChat Video Account: use `WX_VIDEO_ACCOUNT_RESOLVE_API` if available; otherwise state that link resolution is unavailable and ask for the video/image file.

2. **Python-first preprocessing**
   - Prefer this skill's own script for media preparation:
     ```bash
     python skills/media/video-note/scripts/video_note_prepare.py "<source>" --output ".openkitty/storage/<task-id>"
     ```
   - The script handles platform resolution, media download, local `ffmpeg/ffprobe` bootstrap, probing, audio extraction, and representative frame extraction.
   - Use Go tools only as lower-level execution helpers or fallback, not as the primary workflow owner.

3. **ASR and OCR**
   - For video: use `audio/source_16k_mono.wav` with `funasr-speech`, then write `asr.txt`.
   - For frames: use `frames/*.jpg` with `ocr-document`, then write `ocr.txt`.
   - For audio: run ASR only.
   - For image post/screenshots: run OCR directly and optionally inspect representative images if vision is available.

4. **Build `note_materials.json`**
   - Keep OCR and ASR separate.
   - Preserve selected frame paths in `visual_frames`.
   - Record failures explicitly in `status`; do not pretend extraction succeeded.

5. **Write Markdown note**
   - Base the note on `note_materials.json`.
   - Use `raw.json`, `ocr.txt`, `asr.txt`, and `visual_frames` only to verify or enrich.
   - Do not fabricate content not present in extracted materials.

## Recommended Note Structure

```md
# 标题

- 来源：
- 作者：
- 素材：
- 处理时间：

## 内容概览

用 4-8 条自然中文说明主题、关键情节、观点、证据、结论。

## 关键要点

提炼可复用的观点、方法、案例、数字、金句。

## 提取到的文案

整理 OCR/ASR 合并后的材料，保留原始信息顺序和重要细节。

## 转写文案

以 ASR 为主，做轻度清洗；不要改写成完全不同的文章。

## 可复用素材

列出可用于文章、PPT、销售话术、短视频脚本或知识库的片段。

## 提取状态

- 链接解析：
- OCR：
- ASR：
- 视觉帧：

## 局限说明

说明哪些来自 OCR、ASR、视觉帧、元数据，以及不确定处。
```

## Adapter Policy

- Link adapters are optional and unstable. Never hard-code a third-party service as guaranteed infrastructure.
- If `WX_VIDEO_ACCOUNT_RESOLVE_API` is configured, a WeChat Video Account adapter may POST `{ "url": share_url }` and parse returned video/image URLs.
- If the adapter fails, return a clear action: “请上传视频文件或图片素材，我可以继续 OCR/ASR/整理笔记。”
- Respect platform terms and user authorization. Do not bypass login, DRM, paywalls, or private content restrictions.

## Tool Routing

- Use `video_note_prepare.py` first for downloads, `ffmpeg/ffprobe` bootstrap, audio extraction, and frame extraction.
- Use `ocr-document` for images and selected frames after preprocessing.
- Use `funasr-speech` for Chinese audio transcription after preprocessing.
- Use existing media/file tools only when the Python preprocessing script is unavailable or fails.
- If no OCR/ASR tool is available, still produce `raw.json`, selected frame paths, status fields, and a partial note.

## Media Binary Bootstrap

- Video/audio preprocessing may require `ffmpeg` and `ffprobe`.
- The runtime should first use `OPENKITTY_FFMPEG_DIR`, then `.openkitty/tools/ffmpeg/<os>-<arch>/bin`, then system `PATH`.
- If missing, the runtime may auto-download platform-specific portable zip packages for Windows, Linux, or macOS.
- Do not write system `PATH`; keep downloaded binaries under `.openkitty/tools/ffmpeg/`.
- Operators can override downloads with `OPENKITTY_FFMPEG_URL`, `OPENKITTY_FFMPEG_FFMPEG_URL`, `OPENKITTY_FFMPEG_FFPROBE_URL`, or `OPENKITTY_FFMPEG_ARCHIVE_URL`.
- `OPENKITTY_FFMPEG_DOWNLOAD_TIMEOUT_SEC` controls per-source timeout; default is 180 seconds.
- If download fails, continue with metadata/link extraction and mark `status.asr` or `status.vision` as skipped instead of failing the whole note.
- If a required binary is still missing, tell the user to download the platform zip, extract it, and place the binary at `.openkitty/tools/ffmpeg/<os>-<arch>/bin/ffmpeg(.exe)` or `.openkitty/tools/ffmpeg/<os>-<arch>/bin/ffprobe(.exe)`.

## Quality Rules

- Keep OCR, ASR, and model-written summaries separate.
- Preserve raw extraction artifacts for auditability.
- Prefer stable CPU paths over fragile GPU-only paths.
- Mark empty or failed extraction as failure, not success.
- Do not dump raw transcript only; produce a readable note plus reusable material.
