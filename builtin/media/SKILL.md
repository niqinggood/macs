---
name: media
description: Media capture, OCR, speech recognition, video/audio/image-note processing, AI music generation (MelodyLab/Suno)
version: 1.0.0
author: OpenKitty
status: stable
triggers:
  - camera
  - OCR
  - speech
  - audio
  - capture
  - record
  - screenshot
  - transcribe
  - video-note
  - wx-video-account-notes
  - 视频笔记
  - 视频号
  - 图文动态
  - 拍照
  - 识别
  - 语音
  - 录音
---

# Media Skills

Media input tools — capture images/audio, extract text from images, and transcribe speech.

## Sub-skills

### media-capture
**Use when**: User wants to take a photo (camera), capture a screenshot, or record audio. This is the INPUT tool — it captures raw media for further processing.

Examples: "拍个照" / "截图" / "录一段音频"

### ocr-document
**Use when**: User provides an image or scanned document and wants text extracted from it. Supports Chinese and English. Use after `media-capture` or when the user already has an image file.

Examples: "识别这张图片里的文字" / "Extract text from this scanned PDF"

### funasr-speech
**Use when**: User provides an audio file (or wants to record) and needs Chinese speech transcribed to text. Uses FunASR for high-accuracy Chinese ASR. For non-Chinese speech, consider alternatives.

Examples: "把这段录音转成文字" / "Transcribe this meeting recording"

### video-note
**Use when**: User provides a video, audio, image group, screenshot set, or platform video link and wants OCR/ASR extraction plus a structured Markdown note. This is the orchestration layer: source resolution → frames/audio/OCR/ASR → `note_materials.json` → final note.

Examples: "处理这个视频号链接并生成笔记" / "把这段课程视频整理成笔记" / "把这些图文动态提取成素材"

### wx-video-account-notes
**Use when**: User provides an exact `https://weixin.qq.com/sph/...` link and asks to extract, download, transcribe, summarize, or analyze its actual media. This dedicated route takes priority over generic `video-note` and ordinary `web_fetch`.

Examples: "提取这个视频号的内容" / "下载并转写这个视频号" / "把视频号口播整理成文章"

## Decision Guide

| User says | Use |
|-----------|-----|
| "拍照/截图/录音" | `media-capture` |
| "识别图片/PDF 里的文字" | `ocr-document` |
| "语音转文字" | `funasr-speech` |
| "Extract text from image" | `ocr-document` |
| "weixin.qq.com/sph/ 视频号链接" | `wx-video-account-notes` |
| "其他短视频/课程视频整理笔记" | `video-note` |

Common pipeline: `media-capture` → `ocr-document` (for images) or `media-capture` → `funasr-speech` (for audio).

## IRON RULES

1. **Tool safety**: NEVER use curl or raw CLI commands for external API calls. Use Python (`write_file` + `code_check run`) instead.
2. **Read before act**: Always research/read existing context before creating output. Don't work blind.
3. **Quality over speed**: Every output must be complete, accurate, and well-structured. If you can't do it right, explain what's missing rather than fabricating.
4. **Output to `.openkitty/storage/`**: All generated files go to `.openkitty/storage/`. Never leave artifacts in the project root.
5. **No fabricated data**: Don't invent numbers, citations, names, or facts. If uncertain, state your uncertainty clearly.

## Quality Standards

| ✅ Good | ❌ Bad |
|---------|--------|
| Well-structured with clear headings and logical flow | Flat text with no organization |
| Specific, actionable, data-backed | Vague generalities, no specifics |
| Original analysis and insight | Copy-paste from sources without value-add |
| Proper Markdown formatting | Plain text dump |

Output: `.openkitty/storage/`
