#!/usr/bin/env python3
"""FunASR Speech-to-Text via OpenAI-compatible API.
Usage: python transcribe.py <audio_file> [--language zh] [--server http://localhost:8000]
"""
import argparse, json, sys
from pathlib import Path

def main():
    parser = argparse.ArgumentParser(description="FunASR Speech Recognition")
    parser.add_argument("audio", help="Audio file path (wav/mp3/m4a/ogg)")
    parser.add_argument("--language", default="zh", help="Language: zh, en, ja, ko, yue, auto")
    parser.add_argument("--server", default="http://localhost:8000", help="FunASR server URL")
    parser.add_argument("--model", default="sensevoice", help="Model: sensevoice, paraformer-zh")
    args = parser.parse_args()

    audio_path = Path(args.audio)
    if not audio_path.exists():
        print(f"ERROR: Audio file not found: {args.audio}", file=sys.stderr)
        sys.exit(1)

    # OpenAI-compatible multipart upload
    import urllib.request
    boundary = "----FunASRFormBoundary"
    body = (
        f'--{boundary}\r\n'
        f'Content-Disposition: form-data; name="file"; filename="{audio_path.name}"\r\n'
        f'Content-Type: audio/wav\r\n\r\n'
    ).encode() + audio_path.read_bytes() + (
        f'\r\n--{boundary}\r\n'
        f'Content-Disposition: form-data; name="model"\r\n\r\n{args.model}\r\n'
        f'--{boundary}\r\n'
        f'Content-Disposition: form-data; name="language"\r\n\r\n{args.language}\r\n'
        f'--{boundary}--\r\n'
    ).encode()

    url = f"{args.server.rstrip('/')}/v1/audio/transcriptions"
    req = urllib.request.Request(url, data=body)
    req.add_header("Content-Type", f"multipart/form-data; boundary={boundary}")

    try:
        resp = urllib.request.urlopen(req, timeout=120)
        result = json.loads(resp.read())
        text = result.get("text", "")
        if text:
            print(f"🎤 {text}")
        else:
            print(f"ERROR: Empty result from FunASR", file=sys.stderr)
            sys.exit(1)
    except Exception as e:
        print(f"ERROR: {e}", file=sys.stderr)
        print("Make sure funasr-server is running: funasr-server --device cpu", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
