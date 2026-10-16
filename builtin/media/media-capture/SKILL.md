---
name: media-capture
description: "Take screenshots, record microphone audio, process/convert images. Use when user asks to capture screen, record voice, crop images, or apply image filters. Tools: screenshot, audio, image_process. Always call tool_detail first."
read_when:
  - Take screenshot or screen capture / 截图、截屏、屏幕截图
  - Record audio or microphone / 录音、录歌、麦克风、录一段
  - Process or manipulate images / 处理图片、裁剪、缩放、转格式
  - Capture desktop or window / 捕获桌面
---

# Media Capture & Processing

## Step 1: Get tool parameters

```
tool_detail("screenshot")
tool_detail("audio")
tool_detail("image_process")
```

## Step 2: Use the tools

- `screenshot` — capture the screen or a specific window. Returns image path for vision analysis.
- `audio` — record audio from microphone. Returns audio file path for transcription.
- `image_process` — resize, crop, convert, or apply filters to images.
