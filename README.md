# 小红书

[中文](README.md) | [English](README-EN.md)

[res-downloader](https://github.com/putyy/res-downloader) 的小红书作品插件。

## 功能

- 支持打开作品后识别视频、单图、图集和 Live Photo。
- 视频优先下载最高可用画质，支持音视频合并。
- 支持按作品合集下载，也可单独下载图片、视频或 TXT 文案。

## 安装

发布后可在 `res-downloader` 的“插件管理”页面安装。也可以下载对应版本的源码 ZIP，通过“从压缩包安装”导入。

## 设置

- **启用日志**：默认关闭，用于排查抓取问题。

## 注意事项

- 分轨视频下载需要在应用中配置 FFmpeg。
- 视频预览画质可能低于下载画质，部分视频仅显示封面。
- Live Photo 保存为独立的图片和视频文件，不生成苹果相册的配对实况照片。
- 链接过期或文案文件丢失时，刷新页面并重新打开作品抓取。
- 不支持批量抓取首页推荐、字幕和 DRM 内容；空文案或超过 16384 字符的文案不生成 TXT。

## 开发与校验

在 `res-downloader` 项目根目录执行：

```bash
node --check ./plugins/resd-plugin-xiaohongshu/main.js
go run main.go plugin lint ./plugins/resd-plugin-xiaohongshu
for fixturePath in ./plugins/resd-plugin-xiaohongshu/fixtures/*.json; do
  go run main.go plugin replay ./plugins/resd-plugin-xiaohongshu "$fixturePath" || break
done
go test ./plugins/resd-plugin-xiaohongshu/tests
go run main.go plugin pack ./plugins/resd-plugin-xiaohongshu
```

安装包位于 `dist/plugin.zip`。所有 fixture 均为脱敏虚构数据。接口范围、离线检查和人工验收步骤见 [开发说明](tests/README.md)。
