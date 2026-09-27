# 开发说明 / Development notes

## 观察依据 / Browser evidence

2026-09-27 在用户授权的 cmux 小红书 `/explore` 页面观察到：点击单图、图集、Live Photo 和视频作品时，请求 `edith.xiaohongshu.com/api/sns/web/v1/feed`，返回单个 `data.items[].note_card`。标题与正文来自 `title` / `desc`，无需解析 HTML。媒体抓取已由用户确认；本次直接生成 TXT 的实现尚待用户验收。

Authorized browser observations covered images, galleries, Live Photos and videos using the single-work detail endpoint. Captions come from JSON fields, not rendered HTML. The user confirmed media capture; direct TXT generation remains unverified in the application.

- 图片使用 `image_list[].url_default` / `WB_DFT`，保留处理后缀及签名。/ Image suffixes and signatures are preserved.
- 视频使用 JSON 字符串 `video.media_v2` 的 `stream` / `audio_stream`，兼容 `video.media.stream`；实际播放观察包含 2160p m4s 与 AAC m4a。/ Observed playback included split 2160p video and AAC audio.
- Live Photo 的 `live_photo` 和 `stream.*[].master_url` 提供独立 MP4 附带视频。/ Motion files are separate MP4 children.
- 只读数据检查曾出现通用头像、素材及 QC 视频域名，用户要求整站屏蔽通用资源；确认抓取后才关闭代理，不能据此推断抓取时未开启。/ Generic suppression reflects the user's explicit request; switching off the proxy after capture is not evidence of an earlier routing failure.

不保存真实作品内容、账户、Cookie、Authorization、签名 URL 或原始抓包。fixture 内 ID、标题、作者、路径和签名全部虚构。/ All fixtures are fictional and sanitized.

## 匹配、权限和文案 / Scope, permissions and captions

域名范围为 `xiaohongshu.com`、`*.xiaohongshu.com`、`xhscdn.com`、`*.xhscdn.com`，按用户要求屏蔽这些站点的通用资源，包括头像、素材、预加载和未关联媒体。其他网站不受影响。仅详情接口读取正文，最大 1 MiB；成功响应必须仅含一个有效作品，媒体 URL 仍使用支持域名的白名单。不更改页面或媒体响应，不主动请求作品，不批量解析首页推荐。

Site-scoped generic suppression is independent of metadata timing. Only the supported detail endpoint reads bodies (1 MiB maximum). Other sites retain generic detection. No response modification, proactive detail requests or bulk homepage extraction is performed.

权限为 `observe-response`、`read-response-body`、`emit-resource`、`media.basic`、`capture-response-body`，无需页面脚本和页面桥接。

The permissions support observation, bounded body reading, resource emission, media muxing and capture storage. Page scripts and page bridging are not required.

在媒体详情成功提取后，同一个 observation 钩子调用 `api.capture.save(title + "\n\n" + desc)`。宿主保存 UTF-8 并返回已完成缓存，插件同步上报 `document.text` 子项，排序为 1000；因此仅勾选合集也保留文案。正文上限 16384 个 JavaScript 字符（UTF-16 代码单元），空正文跳过。接口不可用或缓存失败时保留媒体，输出脱敏日志，不产生无法读取的 TXT。

The same observation saves UTF-8 text and emits a text child after media extraction. Captions sort last and survive collection-only capture filters. Empty or oversized descriptions are skipped. An unavailable API or storage failure preserves media and reports a sanitized diagnostic without publishing a dangling text resource.

每次重新抓取创建新缓存键，资源 groupKey 保持稳定；支持地址更新、清空列表后重抓，避免覆盖正在预览的文件。/ Recapture uses a fresh cache key and stable resource identity, supporting URL updates and capture after clearing the list without overwriting files being previewed.

日志：`Caption TXT captured from detail` 表示缓存已写完并上报 TXT；`Caption unavailable` 表示接口不可用；`Caption capture failed` 表示缓存写入失败；空正文或超限记录 `Caption skipped`。/ Log prefixes distinguish completed capture, an unavailable API, storage failure and skipped descriptions.

## 离线检查 / Offline checks

`fixtures/` 的 8 个 JSON 均为 CLI 回放输入：

| Fixture | 检查内容 / Coverage |
| --- | --- |
| video-split | 4K + AAC、完整 MP4 预览、非整数秒时长及 TXT / split tracks, preview, integer duration, TXT |
| video-qc | QC 视频域名 / observed QC hostname with fictional detail data |
| video-progressive | media_v2 损坏后回退 / legacy stream fallback |
| missing-audio-fallback | 高画质无音轨时选择完整视频 / audio-bearing fallback |
| gallery | 5 张图片和 2 个 Live Photo 视频 / ordered gallery and motion files |
| single-image | 单图合集、标题回退、TXT / single-image collection, title fallback, TXT |
| invalid-details | 错误状态、坏 JSON、截断、DRM、缺音轨、外域 / invalid inputs |
| dedupe-and-refresh | 去重、Range、端口、媒体地址与文案缓存更新 / deduplication and recapture |

CLI 回放使用宿主的临时真实缓存，结束后清理。`contract_test.go` 的媒体单元检查故意不提供缓存，验证媒体独立可用；`TestCaptionFromDetailWithoutPage` 通过完整插件管理器与真实缓存，验证零页面会话、中文/换行/表情字节、字节数、合集过滤、排序、下载计划、重抓、新键及缓存失败。宿主 `TestPluginCaptureSave*` 覆盖二进制、视图偏移、权限、隔离、配额和中止清理。它们都不启动应用、不连接网站、不执行 FFmpeg。

Offline checks are separate from application acceptance. Full-chain tests retain generic suppression and scope checks; capture tests exercise real file bytes and download input resolution without network or browser operations.

命令见 [README](../README.md)。/ Commands are listed in the README.

## 用户人工验收 / Manual acceptance

以下由用户执行，本次尚未验证 / The user must perform these checks:

1. 安装插件 ZIP，打开有正文的图集和视频，检查每个合集都有 TXT。页面不注入插件脚本也应能生成文案。/ Install the plugin ZIP and verify gallery/video TXT without injected page scripts.
2. 预览、单独下载 TXT 和整组下载，核对中文、表情、换行及文件内容；不得导出原始 JSON。/ Verify preview, individual and collection downloads produce caption text, not JSON.
3. 仅勾选合集，连续切换作品、重开同一作品、清空列表后重抓，检查标题对应、子项数量和顺序。/ Verify filtering, identity, ordering and recapture.
4. 展开与收起合集，一级列表不得重复出现子项。/ Verify collections do not duplicate children in the root table.
5. 通用抓取开启时，小红书不出现头像或列表素材，其他站点正常；已有视频、Live Photo 和音轨下载行为保持正常。/ Verify suppression scope and existing media behavior.
6. 宿主开发者用脱敏示例验证二进制文件、权限拒绝、空值/超限、缓存缺失及过期清理，检查预览与下载输出一致。/ Also manually check binary files, permission denial, malformed/oversized input and cache lifecycle.
