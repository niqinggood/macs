#!/usr/bin/env python3
"""Prepare video/audio materials for the video-note skill.

This script keeps the video-note workflow skill-owned:
- resolve/download local or platform media
- bootstrap portable ffmpeg/ffprobe into .openkitty/tools when needed
- probe media metadata
- extract 16k mono WAV for ASR
- sample frames for OCR
- write note_materials.json plus audit artifacts
"""

from __future__ import annotations

import argparse
import json
import os
import platform
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request
import zipfile
from pathlib import Path
from typing import Any


FFBINARIES_VERSION = "6.1"
DEFAULT_WECHAT_API = "https://sph.litao.workers.dev/api/fetch_video_profile"


def main() -> int:
    parser = argparse.ArgumentParser(description="Prepare media materials for video-note.")
    parser.add_argument("source", help="Local media path, direct media URL, or supported platform URL")
    parser.add_argument("--output", "-o", default="", help="Output directory under .openkitty/storage by default")
    parser.add_argument("--workspace", default=".", help="OpenKitty workspace root")
    parser.add_argument("--frames", type=int, default=6, help="Representative frame count")
    parser.add_argument("--language", default="zh", help="ASR/OCR language hint")
    parser.add_argument("--wechat-api", default=os.environ.get("WX_VIDEO_ACCOUNT_RESOLVE_API", DEFAULT_WECHAT_API))
    parser.add_argument("--no-platform-resolve", action="store_true", help="Do not resolve platform links")
    parser.add_argument("--no-bootstrap", action="store_true", help="Do not auto-download ffmpeg/ffprobe")
    parser.add_argument("--timeout", type=int, default=int(os.environ.get("OPENKITTY_FFMPEG_DOWNLOAD_TIMEOUT_SEC", "180")))
    args = parser.parse_args()

    workspace = Path(args.workspace).resolve()
    output_dir = Path(args.output).resolve() if args.output else default_output_dir(workspace, args.source)
    output_dir.mkdir(parents=True, exist_ok=True)

    result: dict[str, Any] = {
        "source": {"type": "unknown", "url": "", "files": []},
        "metadata": {"title": "", "author": "", "duration": "", "created_at": ""},
        "ocr_lines": [],
        "asr_lines": [],
        "visual_frames": [],
        "overview_lines": [],
        "summary_lines": [],
        "reusable_lines": [],
        "status": {"resolve": "pending", "download": "pending", "probe": "pending", "audio": "pending", "frames": "pending", "ocr": "pending", "asr": "pending", "vision": "pending"},
        "hints": [],
    }

    raw: dict[str, Any] = {"source": args.source, "fetched_at": time.strftime("%Y-%m-%d %H:%M:%S")}
    try:
        media_path, raw = resolve_source(args, output_dir, raw, result)
        result["status"]["resolve"] = "ok"
        result["status"]["download"] = "ok" if media_path.exists() else "skipped"
    except Exception as exc:
        result["status"]["resolve"] = "failed"
        result["status"]["download"] = "failed"
        result["hints"].append(str(exc))
        write_json(output_dir / "raw.json", raw)
        write_json(output_dir / "note_materials.json", result)
        print(json.dumps({"ok": False, "output": str(output_dir), "error": str(exc)}, ensure_ascii=False, indent=2))
        return 1

    write_json(output_dir / "raw.json", raw)

    ffprobe = find_or_bootstrap_binary(workspace, "ffprobe", not args.no_bootstrap, args.timeout, result)
    ffmpeg = find_or_bootstrap_binary(workspace, "ffmpeg", not args.no_bootstrap, args.timeout, result)

    if ffprobe:
        probe = run_probe(ffprobe, media_path, output_dir, result)
        apply_probe_metadata(probe, result)
    else:
        result["status"]["probe"] = "skipped_missing_ffprobe"

    if ffmpeg:
        if is_video_file(media_path):
            extract_audio(ffmpeg, media_path, output_dir, result)
            extract_frames(ffmpeg, media_path, output_dir, args.frames, result)
        elif is_audio_file(media_path):
            result["source"]["files"].append(relpath(media_path, output_dir))
            result["status"]["audio"] = "source_audio_ready"
            result["status"]["frames"] = "skipped_audio_source"
        else:
            result["status"]["audio"] = "skipped_unsupported_source"
            result["status"]["frames"] = "skipped_unsupported_source"
    else:
        result["status"]["audio"] = "skipped_missing_ffmpeg"
        result["status"]["frames"] = "skipped_missing_ffmpeg"

    write_json(output_dir / "note_materials.json", result)
    print(json.dumps({"ok": True, "output": str(output_dir), "materials": str(output_dir / "note_materials.json"), "status": result["status"]}, ensure_ascii=False, indent=2))
    return 0


def default_output_dir(workspace: Path, source: str) -> Path:
    slug = "".join(ch if ch.isalnum() else "-" for ch in source.rstrip("/").split("/")[-1])[:48] or "media"
    return workspace / ".openkitty" / "storage" / f"video-note-{slug}"


def resolve_source(args: argparse.Namespace, output_dir: Path, raw: dict[str, Any], result: dict[str, Any]) -> tuple[Path, dict[str, Any]]:
    source = args.source
    source_path = Path(source)
    if source_path.exists():
        result["source"] = {"type": "local_video" if is_video_file(source_path) else "local_audio", "url": "", "files": [str(source_path)]}
        return source_path.resolve(), raw

    if source.startswith(("http://", "https://")):
        if "weixin.qq.com/sph/" in source and not args.no_platform_resolve:
            video_url, metadata, raw = resolve_wechat_video(source, args.wechat_api, raw)
            result["source"] = {"type": "platform_link", "url": source, "files": []}
            result["metadata"].update(metadata)
            result["overview_lines"] = [metadata.get("title", "")] if metadata.get("title") else []
            media_path = output_dir / "source.mp4"
            download_file(video_url, media_path, timeout=180)
            result["source"]["files"].append("source.mp4")
            return media_path, raw

        media_path = output_dir / guess_media_name(source)
        download_file(source, media_path, timeout=180)
        result["source"] = {"type": "remote_media", "url": source, "files": [media_path.name]}
        return media_path, raw

    raise ValueError(f"source not found or unsupported: {source}")


def resolve_wechat_video(source: str, api: str, raw: dict[str, Any]) -> tuple[str, dict[str, str], dict[str, Any]]:
    payload = json.dumps({"url": source}).encode("utf-8")
    request = urllib.request.Request(api, data=payload, headers={
        "Content-Type": "application/json; charset=utf-8",
        "Accept": "application/json,text/plain,*/*",
        "User-Agent": user_agent(),
    })
    with urllib.request.urlopen(request, timeout=60) as response:
        parsed = json.loads(response.read().decode("utf-8", errors="replace"))
    raw["adapter"] = {"type": "wechat_video_account", "api": api, "response": parsed}
    data = parsed.get("data") or {}
    feed = data.get("feedInfo") or parsed.get("feedInfo") or {}
    author = (data.get("authorInfo") or {}).get("nickname") or ""
    title = feed.get("description") or ""
    video_url = nested_get(feed, ["h264VideoInfo", "videoUrl"]) or nested_get(feed, ["h265VideoInfo", "videoUrl"]) or feed.get("videoUrl")
    if not video_url:
        raise ValueError("wechat resolver returned no videoUrl")
    metadata = {
        "title": repair_mojibake(title),
        "author": repair_mojibake(author),
        "duration": "",
        "created_at": str(feed.get("createtime") or ""),
    }
    return video_url, metadata, raw


def nested_get(data: dict[str, Any], path: list[str]) -> Any:
    cur: Any = data
    for key in path:
        if not isinstance(cur, dict):
            return None
        cur = cur.get(key)
    return cur


def repair_mojibake(text: str) -> str:
    if not text:
        return text
    try:
        repaired = text.encode("latin1").decode("utf-8")
        if sum(ch in repaired for ch in "的一是在不了有和人") >= sum(ch in text for ch in "的一是在不了有和人"):
            return repaired
    except UnicodeError:
        pass
    return text


def guess_media_name(url: str) -> str:
    name = url.rstrip("/").split("/")[-1].split("?")[0]
    return name if "." in name else "source.mp4"


def download_file(url: str, output: Path, timeout: int) -> None:
    request = urllib.request.Request(url, headers={"User-Agent": user_agent(), "Accept": "*/*"})
    with urllib.request.urlopen(request, timeout=timeout) as response, output.open("wb") as handle:
        shutil.copyfileobj(response, handle)


def find_or_bootstrap_binary(workspace: Path, name: str, bootstrap: bool, timeout: int, result: dict[str, Any]) -> Path | None:
    binary = binary_name(name)
    candidates = []
    env_dir = os.environ.get("OPENKITTY_FFMPEG_DIR")
    if env_dir:
        candidates.append(Path(env_dir) / binary)
    candidates.append(ffmpeg_bin_dir(workspace) / binary)
    path_binary = shutil.which(binary)
    if path_binary:
        candidates.append(Path(path_binary))
    for candidate in candidates:
        if candidate.exists():
            return candidate

    if not bootstrap:
        result["hints"].append(install_hint(workspace, name))
        return None

    for url in archive_urls(name):
        try:
            archive_path = ffmpeg_download_dir(workspace) / f"{name}-{platform_key()}.zip"
            archive_path.parent.mkdir(parents=True, exist_ok=True)
            download_file(url, archive_path, timeout=timeout)
            extracted = extract_binary_from_zip(archive_path, ffmpeg_bin_dir(workspace), name)
            if extracted:
                return extracted
        except Exception as exc:
            result["hints"].append(f"{name} download failed: {url} ({exc})")
            continue

    result["hints"].append(install_hint(workspace, name))
    return None


def archive_urls(name: str) -> list[str]:
    custom = []
    for key in [f"OPENKITTY_{name.upper()}_URL", f"OPENKITTY_FFMPEG_{name.upper()}_URL", "OPENKITTY_FFMPEG_ARCHIVE_URL"]:
        if os.environ.get(key):
            custom.append(os.environ[key])

    system = platform.system().lower()
    machine = platform.machine().lower()
    urls: list[str] = []
    if system == "windows" and machine in {"amd64", "x86_64"}:
        if name == "ffprobe":
            urls.append("https://www.novaexel.com/download/ffprobe-6.1-win-64.zip")
        urls.append(f"https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v{FFBINARIES_VERSION}/{name}-{FFBINARIES_VERSION}-win-64.zip")
    elif system == "darwin" and machine in {"amd64", "x86_64"}:
        urls.append(f"https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v{FFBINARIES_VERSION}/{name}-{FFBINARIES_VERSION}-macos-64.zip")
    elif system == "darwin" and machine in {"arm64", "aarch64"}:
        urls.append(f"https://ffmpeg.martin-riedl.de/redirect/latest/macos/arm64/release/{name}.zip")
    elif system == "linux" and machine in {"amd64", "x86_64"}:
        urls.append(f"https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v{FFBINARIES_VERSION}/{name}-{FFBINARIES_VERSION}-linux-64.zip")
    elif system == "linux" and machine in {"arm64", "aarch64"}:
        urls.append(f"https://github.com/ffbinaries/ffbinaries-prebuilt/releases/download/v{FFBINARIES_VERSION}/{name}-{FFBINARIES_VERSION}-linux-arm-64.zip")
    return custom + urls


def extract_binary_from_zip(archive: Path, bin_dir: Path, name: str) -> Path | None:
    target_name = binary_name(name)
    bin_dir.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(archive) as zip_file:
        for member in zip_file.infolist():
            if Path(member.filename).name != target_name:
                continue
            target = bin_dir / target_name
            with zip_file.open(member) as src, target.open("wb") as dst:
                shutil.copyfileobj(src, dst)
            if os.name != "nt":
                target.chmod(0o755)
            return target
    return None


def ffmpeg_bin_dir(workspace: Path) -> Path:
    return workspace / ".openkitty" / "tools" / "ffmpeg" / platform_key() / "bin"


def ffmpeg_download_dir(workspace: Path) -> Path:
    return workspace / ".openkitty" / "tools" / "ffmpeg" / "_downloads"


def platform_key() -> str:
    system = platform.system().lower()
    if system == "windows":
        os_name = "windows"
    elif system == "darwin":
        os_name = "darwin"
    else:
        os_name = "linux"
    machine = platform.machine().lower()
    arch = "arm64" if machine in {"arm64", "aarch64"} else "amd64"
    return f"{os_name}-{arch}"


def binary_name(name: str) -> str:
    return f"{name}.exe" if os.name == "nt" else name


def install_hint(workspace: Path, name: str) -> str:
    urls = archive_urls(name)
    target = ffmpeg_bin_dir(workspace) / binary_name(name)
    lines = [f"Missing {binary_name(name)}. Download a portable zip, extract it, and place the binary at: {target}"]
    if urls:
        lines.append("Download candidates:")
        lines.extend(f"- {url}" for url in urls)
    lines.append("Alternatively set OPENKITTY_FFMPEG_DIR to a bin directory containing ffmpeg/ffprobe.")
    return "\n".join(lines)


def run_probe(ffprobe: Path, media_path: Path, output_dir: Path, result: dict[str, Any]) -> dict[str, Any]:
    command = [str(ffprobe), "-v", "error", "-show_entries", "format=duration,size,bit_rate", "-show_streams", "-of", "json", str(media_path)]
    completed = subprocess.run(command, text=True, capture_output=True, check=True)
    probe = json.loads(completed.stdout)
    write_json(output_dir / "ffprobe.json", probe)
    result["status"]["probe"] = "ok"
    result["source"]["files"].append("ffprobe.json")
    return probe


def apply_probe_metadata(probe: dict[str, Any], result: dict[str, Any]) -> None:
    fmt = probe.get("format") or {}
    if fmt.get("duration"):
        result["metadata"]["duration"] = str(fmt["duration"])
    streams = probe.get("streams") or []
    result["metadata"]["has_audio"] = any(s.get("codec_type") == "audio" for s in streams)
    result["metadata"]["has_video"] = any(s.get("codec_type") == "video" for s in streams)


def extract_audio(ffmpeg: Path, media_path: Path, output_dir: Path, result: dict[str, Any]) -> None:
    audio_dir = output_dir / "audio"
    audio_dir.mkdir(parents=True, exist_ok=True)
    wav = audio_dir / "source_16k_mono.wav"
    log = output_dir / "ffmpeg_extract_audio.log"
    command = [str(ffmpeg), "-y", "-i", str(media_path), "-vn", "-ac", "1", "-ar", "16000", "-acodec", "pcm_s16le", str(wav)]
    with log.open("w", encoding="utf-8") as handle:
        completed = subprocess.run(command, text=True, stdout=handle, stderr=subprocess.STDOUT)
    if completed.returncode != 0 or not wav.exists():
        result["status"]["audio"] = "failed"
        result["hints"].append(f"audio extraction failed; see {log}")
        return
    result["status"]["audio"] = "ok"
    result["status"]["asr"] = "audio_extracted_pending_transcription"
    result["source"]["files"].extend(["audio/source_16k_mono.wav", "ffmpeg_extract_audio.log"])


def extract_frames(ffmpeg: Path, media_path: Path, output_dir: Path, frame_count: int, result: dict[str, Any]) -> None:
    frames_dir = output_dir / "frames"
    frames_dir.mkdir(parents=True, exist_ok=True)
    pattern = frames_dir / "frame_%03d.jpg"
    count = max(1, min(frame_count, 24))
    command = [str(ffmpeg), "-y", "-i", str(media_path), "-vf", f"fps=1/{max(1, count)}", "-frames:v", str(count), "-q:v", "3", str(pattern)]
    completed = subprocess.run(command, text=True, capture_output=True)
    if completed.returncode != 0:
        result["status"]["frames"] = "failed"
        result["hints"].append(completed.stderr[-1000:])
        return
    frames = sorted(frames_dir.glob("frame_*.jpg"))
    result["visual_frames"] = [str(Path("frames") / frame.name) for frame in frames]
    result["source"]["files"].extend(result["visual_frames"])
    result["status"]["frames"] = "ok" if frames else "empty"
    result["status"]["ocr"] = "frames_extracted_pending_ocr" if frames else "skipped_no_frames"
    result["status"]["vision"] = "frames_extracted"


def is_video_file(path: Path) -> bool:
    return path.suffix.lower() in {".mp4", ".mov", ".mkv", ".webm", ".avi", ".m4v"}


def is_audio_file(path: Path) -> bool:
    return path.suffix.lower() in {".wav", ".mp3", ".m4a", ".aac", ".ogg", ".opus", ".flac"}


def relpath(path: Path, base: Path) -> str:
    try:
        return str(path.relative_to(base))
    except ValueError:
        return str(path)


def write_json(path: Path, data: Any) -> None:
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")


def user_agent() -> str:
    return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) OpenKittyVideoNote/1.0 Safari/537.36"


if __name__ == "__main__":
    raise SystemExit(main())
