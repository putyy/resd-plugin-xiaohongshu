// Synchronous Goja hooks: no DOM, fetch, Node APIs or cross-call state.
var API_HOST = "edith.xiaohongshu.com";
var DETAIL_PATH = "/api/sns/web/v1/feed";
var IMAGE_HOST = "sns-webpic-qc.xhscdn.com";
var VIDEO_HOSTS = ["sns-video-v6.xhscdn.com", "sns-video-qc.xhscdn.com", "sns-bak-v1.xhscdn.com"];

// Generic suppression covers the site, independently of which media variants
// the extractor understands. Match DNS boundaries, never a substring/referer.
function isSiteHost(value) {
  if (typeof value !== "string") return false;
  var host = value.toLowerCase().replace(/:\d+$/, "");
  return /^(?:[a-z0-9-]+\.)*(?:xiaohongshu|xhscdn)\.com$/.test(host);
}

function object(v) { return v && typeof v === "object" && !Array.isArray(v) ? v : {}; }
function list(v, limit) { return Array.isArray(v) ? v.slice(0, limit) : []; }
function text(v, limit) { return typeof v === "string" ? v.trim().slice(0, limit) : ""; }
function number(v) { return typeof v === "number" && isFinite(v) && v > 0 ? v : 0; }
function parseJSON(v) {
  if (typeof v !== "string" || !v || v.length > 1048576) return null;
  try { return JSON.parse(v); } catch (_) { return null; }
}

// Goja has no browser URL API. Reject credentials, non-default ports and controls;
// preserve the path and signed query byte-for-byte.
function parseURL(value) {
  if (typeof value !== "string" || value.length > 8192 || /[\s\\\x00-\x1f\x7f]/.test(value)) return null;
  var m = /^(https?):\/\/([a-z0-9.-]+)(?::([0-9]+))?(\/[^?#]*)?(\?[^#]*)?$/i.exec(value);
  if (!m) return null;
  var scheme = m[1].toLowerCase();
  if (m[3] && m[3] !== (scheme === "https" ? "443" : "80")) return null;
  return {scheme: scheme, host: m[2].toLowerCase(), path: m[4] || "/", query: m[5] || ""};
}
function mediaURL(value, image) {
  var url = parseURL(value);
  if (!url || (image ? url.host !== IMAGE_HOST : VIDEO_HOSTS.indexOf(url.host) < 0)) return "";
  // Observed playback upgrades the same URLs without changing signatures.
  return "https://" + url.host + url.path + url.query;
}
function aliasURL(value) {
  var url = parseURL(value);
  if (!url) return "";
  // Normalize byte-range queries only for correlation, never for download.
  var query = url.query.slice(1).split("&").filter(function (part) {
    return part && !/^range=\d+-\d*$/i.test(part);
  }).join("&");
  return "https://" + url.host + url.path + (query ? "?" + query : "");
}
function header(headers, name) {
  var keys = Object.keys(object(headers));
  for (var i = 0; i < keys.length; i++) {
    if (keys[i].toLowerCase() === name) return text(list(headers[keys[i]], 1)[0], 512);
  }
  return "";
}
function downloadHeaders(observation) {
  var headers = {Referer: "https://www.xiaohongshu.com/"};
  var agent = header(observation.request.headers, "user-agent");
  if (agent && !/[\r\n]/.test(agent)) headers["User-Agent"] = agent;
  return headers;
}
function imageURL(image) {
  image = object(image);
  var preferred = list(image.info_list, 12).filter(function (info) {
    return object(info).image_scene === "WB_DFT";
  })[0];
  return mediaURL(image.url_default, true) || mediaURL(object(preferred).url, true) || mediaURL(image.url, true);
}
function imageFormat(url) {
  var path = object(parseURL(url)).path || "";
  var match = /(?:\.|_)(jpe?g|png|webp|avif)(?:_|$)/i.exec(path);
  var format = match ? match[1].toLowerCase().replace("jpeg", "jpg") : "";
  return {extension: format ? "." + format : "", mime: format ? "image/" + (format === "jpg" ? "jpeg" : format) : ""};
}
function register(api, groupKey, urls, image) {
  var aliases = [];
  list(urls, 256).forEach(function (url) {
    var safe = mediaURL(url, image);
    if (safe) aliases.push(aliasURL(safe));
  });
  if (aliases.length) api.correlate.register({groupKey: groupKey, trackId: "known", role: "primary", aliases: aliases});
}
function streamURLs(stream) { return [stream.master_url].concat(list(stream.backup_urls, 4)); }
function streams(value) {
  var result = [];
  Object.keys(object(value)).slice(0, 12).forEach(function (key) {
    list(value[key], 24).forEach(function (stream) {
      stream = object(stream);
      if (mediaURL(stream.master_url, false)) result.push(stream);
    });
  });
  return result;
}
function isMP4(stream) {
  var format = text(stream.format, 12).toLowerCase();
  return format === "mp4" || format === "fmp4" || /\.mp4$/i.test(object(parseURL(stream.master_url)).path || "");
}
function hasAudio(stream) { return number(stream.audio_channels) > 0 && number(stream.audio_duration) > 0; }
function bestVideo(values) {
  return values.slice().sort(function (a, b) {
    return number(b.height) - number(a.height) || number(b.width) - number(a.width) ||
      number(b.avg_bitrate) - number(a.avg_bitrate) || number(b.weight) - number(a.weight);
  })[0];
}
function selectAudio(video, audios) {
  var preferred = String(object(video.opaque1).prefer_audio || "");
  var duration = number(video.video_duration) || number(video.duration);
  return audios.filter(function (audio) {
    return text(audio.audio_codec, 20).toLowerCase() === "aac" && number(audio.audio_channels) > 0 &&
      number(audio.audio_duration) > 0 && (!duration || Math.abs(audio.audio_duration - duration) <= 2000);
  }).sort(function (a, b) {
    return Number(String(b.stream_type) === preferred) - Number(String(a.stream_type) === preferred) ||
      number(b.default_stream) - number(a.default_stream) || number(b.audio_bitrate) - number(a.audio_bitrate);
  })[0];
}
function track(stream, id, role, headers) {
  return {id: id, role: role, executor: "http-file", url: mediaURL(stream.master_url, false),
    extension: role === "audio" ? ".m4a" : ".mp4", mime: role === "audio" ? "audio/mp4" : "video/mp4",
    size: Math.floor(number(stream.size)), width: Math.floor(number(stream.width)), height: Math.floor(number(stream.height)),
    quality: number(stream.height) ? String(stream.height) + "p" : "", headers: headers};
}
function metadata(note) {
  var result = {"xiaohongshu.noteId": note.note_id};
  var author = text(object(note.user).nickname, 160);
  if (author) result.author = author;
  if (number(note.time) && Math.floor(note.time) === note.time && note.time <= 253402300799999) result.publishedAt = note.time;
  return result;
}
function title(note) {
  return text(note.title, 240) || text(text(note.desc, 500).split(/\r?\n/)[0], 120) || "小红书 " + note.note_id;
}

function emitVideo(note, observation, api) {
  var video = object(note.video);
  var media = object(parseJSON(video.media_v2));
  var legacy = object(video.media);
  if (number(object(media.video).drm_type) || number(object(legacy.video).drm_type)) return false;
  var videos = streams(media.stream).concat(streams(legacy.stream)).filter(isMP4);
  var audios = streams(media.audio_stream);
  var selected = bestVideo(videos.filter(function (v) { return hasAudio(v) || !!selectAudio(v, audios); }));
  if (!selected) return false; // Never publish an incomplete video-only stream.
  var audio = hasAudio(selected) ? null : selectAudio(selected, audios);
  var headers = downloadHeaders(observation);
  var cover = imageURL(list(note.image_list, 1)[0]);
  var meta = metadata(note);
  meta["xiaohongshu.split"] = !!audio;
  meta["xiaohongshu.height"] = number(selected.height);
  meta.collectionIndex = 1;
  var resource = {groupKey: "xhs:" + note.note_id + ":video", parentGroupKey: "xhs:" + note.note_id, kind: "media.video", primaryType: "video",
    title: title(note), coverUrl: cover, tracks: [track(selected, "video", "video", headers)],
    requiredTracks: ["video"], capabilities: ["download", "open", "copy"], metadata: meta,
    // The host's duration field is an integer; a fractional value causes Go's
    // candidate decoder to reject the entire emitted resource.
    technical: {mime: "video/mp4", container: "mp4", duration: Math.floor((number(selected.video_duration) || number(selected.duration)) / 1000)}};
  if (audio) {
    resource.tracks.push(track(audio, "audio", "audio", headers));
    resource.requiredTracks.push("audio");
    resource.traits = ["multiTrack", "mergeRequired"];
  }
  // Use an audio-bearing progressive rendition for preview, otherwise the cover.
  var preview = bestVideo(videos.filter(function (v) { return hasAudio(v) && text(v.format, 12).toLowerCase() === "mp4"; }));
  if (preview) {
    var previewID = preview.master_url === selected.master_url ? "video" : "preview";
    if (previewID === "preview") resource.tracks.push(track(preview, "preview", "preview", headers));
    resource.preview = {renderer: "video", mode: "proxy", mime: "video/mp4", trackId: previewID};
  } else if (cover) {
    var format = imageFormat(cover);
    resource.tracks.push({id: "preview", role: "preview", url: cover, mime: format.mime, extension: format.extension, headers: headers});
    resource.preview = {renderer: "image", mode: "direct", mime: format.mime, trackId: "preview"};
  }
  if (resource.preview) resource.capabilities.push("preview");
  api.emit({groupKey: resource.parentGroupKey, kind: "media.collection", primaryType: "collection",
    title: title(note), coverUrl: cover, capabilities: ["download"], metadata: metadata(note)});
  api.emit(resource);
  videos.concat(audios).forEach(function (s) { register(api, resource.groupKey, streamURLs(s), false); });
  if (cover) register(api, resource.groupKey, [cover], true);
  return true;
}

function emitGallery(note, observation, api) {
  var children = [];
  var group = "xhs:" + note.note_id;
  var headers = downloadHeaders(observation);
  list(note.image_list, 100).forEach(function (image, index) {
    image = object(image);
    var url = imageURL(image);
    if (!url) return;
    var format = imageFormat(url);
    var key = group + ":image:" + (index + 1);
    var childTitle = title(note) + " " + ("00" + (index + 1)).slice(-3);
    var meta = metadata(note);
    meta["xiaohongshu.imageIndex"] = index + 1;
    meta.collectionIndex = index * 2 + 1;
    children.push({groupKey: key, parentGroupKey: group, kind: "media.image", primaryType: "image",
      title: childTitle, coverUrl: url, metadata: meta,
      tracks: [{id: "image", role: "image", url: url, mime: format.mime, extension: format.extension,
        width: Math.floor(number(image.width)), height: Math.floor(number(image.height)), headers: headers}],
      requiredTracks: ["image"], capabilities: ["download", "preview", "open", "copy"],
      preview: {renderer: "image", mode: "direct", mime: format.mime, trackId: "image"}});
    register(api, key, [url, image.url_pre].concat(list(image.info_list, 12).map(function (info) { return object(info).url; })), true);
    if (image.live_photo !== true) return;
    // Export the motion file separately, not as an Apple paired Live Photo.
    var motion = bestVideo(streams(image.stream).filter(function (s) {
      return /\.mp4$/i.test(object(parseURL(s.master_url)).path || "");
    }));
    if (!motion) return;
    var motionKey = group + ":live:" + (index + 1);
    var motionMeta = metadata(note);
    motionMeta["xiaohongshu.livePhoto"] = true;
    motionMeta["xiaohongshu.imageIndex"] = index + 1;
    motionMeta.collectionIndex = index * 2 + 2;
    children.push({groupKey: motionKey, parentGroupKey: group, kind: "media.video", primaryType: "video",
      title: childTitle + " Live", coverUrl: url, metadata: motionMeta,
      tracks: [track(motion, "video", "video", headers)], requiredTracks: ["video"],
      capabilities: ["download", "preview", "open", "copy"],
      preview: {renderer: "video", mode: "proxy", mime: "video/mp4", trackId: "video"}});
    register(api, motionKey, streamURLs(motion), false);
  });
  if (!children.length) return false;
  api.emit({groupKey: group, kind: "media.collection", primaryType: "collection", traits: ["gallery"],
    title: title(note), coverUrl: children[0].coverUrl, capabilities: ["download"], metadata: metadata(note)});
  children.forEach(function (child) { api.emit(child); });
  return true;
}

function onObservation(observation, api) {
  var result = {decision: "continue"};
  if (!observation || observation.stage !== "response" || !observation.request || !observation.response) return result;
  // The host's request hostname also drives Manifest matching. Suppression
  // must not depend on URL parsing, metadata arrival, signatures or extraction.
  if (!isSiteHost(observation.request.host)) return result;
  result.handled = true;
  var url = parseURL(observation.request.url);
  if (!url) return result;
  if (url.host !== API_HOST || url.path !== DETAIL_PATH) return result;
  // This is a business endpoint even if it is incorrectly labelled as binary.
  result.handled = true;
  var response = observation.response;
  var contentType = text(response.contentType, 160).toLowerCase();
  if (response.statusCode !== 200) { api.log("Detail skipped: HTTP " + response.statusCode); return result; }
  if (response.truncated) { api.log("Detail skipped: truncated body"); return result; }
  if (!response.body) { api.log("Detail skipped: empty body"); return result; }
  if (!/^(application\/json|text\/json|application\/octet-stream)(?:\s*;|$)/.test(contentType)) {
    api.log("Detail skipped: unsupported response MIME"); return result;
  }
  var payload = object(parseJSON(response.body));
  if (payload.success !== true || payload.code !== 0) { api.log("Detail skipped: invalid JSON or unsuccessful payload"); return result; }
  var items = list(object(payload.data).items, 2);
  // Observed modal responses contain one note. Never capture a whole future feed.
  if (items.length !== 1) { api.log("Detail skipped: expected one work"); return result; }
  var item = object(items[0]);
  var note = object(item.note_card);
  if (typeof note.note_id !== "string" || !/^[a-f0-9]{24}$/i.test(note.note_id) ||
      (item.id && item.id !== note.note_id) || item.ignore === true) {
    api.log("Detail skipped: invalid work identity"); return result;
  }
  var emitted = note.type === "video" ? emitVideo(note, observation, api) :
    note.type === "normal" ? emitGallery(note, observation, api) : false;
  if (emitted) {
    api.log("Captured Xiaohongshu " + note.type + " detail");
    emitCaption(note, api);
  } else api.log("Detail skipped: no usable media for this work");
  return result;
}

function createDownloadPlan(input) {
  var resource = object(input.resource);
  if (resource.primaryType === "collection" || resource.kind === "media.collection") return null;
  var values = list(resource.tracks, 16);
  var find = function (id) { return values.filter(function (v) { return v.id === id; })[0]; };
  if (resource.kind === "document.text") {
    var caption = find("text");
    var noteID = object(resource.metadata)["xiaohongshu.noteId"];
    if (!caption || !validCaptionKey(caption.captureKey, noteID)) throw new Error("Reopen the work to capture its caption again");
    return {inputs: [{id: "text", executor: "capture-file", captureKey: caption.captureKey, extension: ".txt"}],
      output: {input: "text", extension: ".txt", mime: "text/plain; charset=utf-8"}};
  }
  var primary = find(resource.kind === "media.image" ? "image" : "video");
  if (!primary || !mediaURL(primary.url, resource.kind === "media.image")) throw new Error("Reopen the work to capture its media again");
  var selected = [primary];
  // Host merges retain old tracks; only the current detail's explicit mode wins.
  if (object(resource.metadata)["xiaohongshu.split"] === true) {
    var audio = find("audio");
    if (!audio || !mediaURL(audio.url, false)) throw new Error("Audio is missing; reopen the work");
    selected.push(audio);
  }
  var plan = {inputs: selected.map(function (v) {
    return {id: v.id, executor: "http-file", url: v.url, headers: v.headers || {}, extension: v.extension || ""};
  }), output: {input: primary.id, extension: primary.extension || "", mime: primary.mime || ""}};
  if (selected.length === 2) {
    plan.pipeline = [{id: "merged", executor: "builtin.media.mux", inputs: [primary.id, "audio"], options: {extension: ".mp4"}}];
    plan.output = {input: "merged", extension: ".mp4", mime: "video/mp4"};
  }
  // Preview resolution uses download inputs. This auxiliary file remains a
  // temporary input; only the selected media/mux output is saved to the user.
  var previewTrack = find("preview");
  if (resource.preview && resource.preview.trackId === "preview" && previewTrack &&
      (mediaURL(previewTrack.url, false) || mediaURL(previewTrack.url, true))) {
    plan.inputs.push({id: "preview", executor: "http-file", url: previewTrack.url,
      headers: previewTrack.headers || {}, extension: previewTrack.extension || ""});
  }
  return plan;
}

function validCaptionKey(key, id) {
  return typeof id === "string" && /^[a-f0-9]{24}$/i.test(id) && typeof key === "string" &&
    (/^saved:[a-f0-9]{32}$/.test(key) ||
      (key.indexOf("xhs:" + id + ":caption:") === 0 && /^[a-f0-9]{32}$/.test(key.slice(("xhs:" + id + ":caption:").length))));
}

function emitCaption(note, api) {
  if (typeof note.desc !== "string" || !note.desc.trim() || note.desc.length > 16384) {
    api.log("Caption skipped: empty or oversized description");
    return;
  }
  if (!api.capture || typeof api.capture.save !== "function") {
    api.log("Caption unavailable: update the host for api.capture.save support");
    return;
  }
  var file;
  try {
    // The host encodes strings as UTF-8 and completes the stream-file before
    // returning. No DOM, page bridge or HTML response is needed.
    file = api.capture.save(title(note) + "\n\n" + note.desc);
  } catch (_) {
    api.log("Caption capture failed: host cache write unavailable");
    return;
  }
  api.emit({groupKey: "xhs:" + note.note_id + ":text", parentGroupKey: "xhs:" + note.note_id,
    kind: "document.text", primaryType: "document", title: title(note) + " 文案",
    metadata: {"xiaohongshu.noteId": note.note_id, collectionIndex: 1000},
    tracks: [{id: "text", role: "primary", executor: "capture-file", captureKey: file.captureKey,
      extension: ".txt", mime: "text/plain; charset=utf-8", size: file.size}],
    requiredTracks: ["primary"], capabilities: ["download", "preview"],
    preview: {renderer: "text", mode: "proxy", mime: "text/plain; charset=utf-8", trackId: "text"}});
  api.log("Caption TXT captured from detail");
}

function refreshResource(input) {
  return {status: "recaptureRequired", resource: input.resource,
    message: "请重新打开对应的小红书作品 / Reopen the Xiaohongshu work to refresh its media URLs"};
}
