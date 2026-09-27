# Xiaohongshu

[中文](README.md) | [English](README-EN.md)

A Xiaohongshu media plugin for [res-downloader](https://github.com/putyy/res-downloader).

## Features

- Detects videos, single images, galleries, and Live Photos when you open a post.
- Prefers the highest available video quality and supports merging video and audio.
- Downloads posts as collections, or saves images, videos, and TXT captions individually.

## Installation

Once published, the plugin can be installed from Plugin Management in `res-downloader`. You can also download the source ZIP for the desired version and import it using the option to install from an archive.

## Settings

- **Enable logging**: Disabled by default. Enable it to troubleshoot capture issues.

## Notes

- Configure FFmpeg in the application to download videos with separate audio tracks.
- Preview quality may be lower than download quality; some videos only show a cover image.
- Live Photos are saved as separate image and video files, not paired Apple Photos assets.
- If a link expires or a caption file is missing, refresh the page and reopen the post to capture it again.
- Bulk capture of the homepage feed, subtitles, and DRM content are not supported. Empty captions or captions over 16384 characters are not saved as TXT.

## Development and Validation

Run from the `res-downloader` project root:

```bash
node --check ./plugins/resd-plugin-xiaohongshu/main.js
go run main.go plugin lint ./plugins/resd-plugin-xiaohongshu
for fixturePath in ./plugins/resd-plugin-xiaohongshu/fixtures/*.json; do
  go run main.go plugin replay ./plugins/resd-plugin-xiaohongshu "$fixturePath" || break
done
go test ./plugins/resd-plugin-xiaohongshu/tests
go run main.go plugin pack ./plugins/resd-plugin-xiaohongshu
```

The package is written to `dist/plugin.zip`. All fixtures contain fictional, sanitized data. See the [development notes](tests/README.md) for endpoint scope, offline checks and manual acceptance steps.
