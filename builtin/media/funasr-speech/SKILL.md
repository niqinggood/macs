---
name: funasr-speech
description: Speech recognition via FunASR (Alibaba Damo Academy). 50+ languages, punctuation restoration, emotion detection. Requires funasr-server running. Trigger when user needs voice transcription, meeting notes, or speech-to-text.
read_when:
  - Transcribing audio or speech to text
  - Voice recognition or speech-to-text
  - Meeting recording transcription
  - Converting voice memos to text
  - 语音转文字
  - 会议录音转写
  - 语音识别
tags:
  - speech
  - asr
  - transcription
  - voice
  - funasr
version: 1.0.0
---

# FunASR Speech Recognition Skill

## Prerequisites

```bash
# Docker一键启动(推荐, 零配置)
docker run -d -p 8000:10095 \
  registry.cn-hangzhou.aliyuncs.com/funasr_repo/funasr:funasr-runtime-sdk-online-cpu-0.1.13

# 或: pip安装
pip install funasr-server
python -c "from funasr_server import FunASR; FunASR().start()"
```

## Usage

```bash
python skills/funasr-speech/transcribe.py <audio_file> [--language zh] [--server http://localhost:8000]
```

## Supported Languages

zh (Chinese), en (English), ja (Japanese), ko (Korean), yue (Cantonese), auto

## Features

- 50+ languages, auto-detection
- Punctuation restoration
- Emotion detection
- CPU-viable (17x realtime on CPU)
