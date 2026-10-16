# Web, RSS, video, and transcription

## URL reading

Call `agent_reach` with `action=read`, `platform=auto`, and the exact user URL. The tool infers known platforms. Generic pages use Jina Reader first and retain OpenKitty direct `web_fetch` as fallback.

## RSS

For a known feed, call `action=trending`, `platform=rss`, `target=<feed URL>`. The native RSS parser remains the fallback and preserves item URLs and publication times.

## YouTube

- Search: `action=search, platform=youtube, query=...`
- Metadata/read: `action=read, platform=youtube, target=<URL>`
- Audio transcription: `action=transcribe, platform=youtube, target=<URL>, provider=auto`

yt-dlp needs a working JavaScript runtime for some YouTube pages. Doctor reports this separately; web fallback can discover pages but cannot replace missing spoken-content evidence.

## Bilibili

Use Bilibili-specific backends and the public API. Do not route Bilibili through yt-dlp because its risk-control failures are not a reliable media path.

## Transcription

`action=transcribe` invokes Agent Reach's Whisper provider chain. It accepts a supported URL or local media path and may require a configured Groq or OpenAI key. A failed transcript must remain an explicit content gap; page title/description is not proof of spoken content.
