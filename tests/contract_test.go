package xiaohongshu_test

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"res-downloader/internal/capture"
	"strings"
	"testing"

	"res-downloader/internal/config"
	"res-downloader/internal/logging"
	"res-downloader/internal/model"
	"res-downloader/internal/plugin"
	"res-downloader/internal/resource"
)

func runtime(t *testing.T) model.RuntimePlugin {
	t.Helper()
	p, _, err := plugin.LoadOfficialPlugin("..")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func fixture(t *testing.T, name string) []model.Observation {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "fixtures", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var f plugin.PluginFixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f.Observations
}
func handle(t *testing.T, p model.RuntimePlugin, o model.Observation) model.PluginResult {
	t.Helper()
	r, err := p.Handle(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	for i := range r.Resources {
		r.Resources[i].Source.PluginID = p.Manifest().ID
		if err := plugin.ValidateCandidate(&r.Resources[i]); err != nil {
			t.Fatal(err)
		}
	}
	if r.Patch != nil || r.SyntheticResponse != nil || r.Decision != "continue" {
		t.Fatal("must not alter website traffic")
	}
	return r
}
func mediaObservation(raw string) model.Observation {
	u, _ := url.Parse(raw)
	return model.Observation{Stage: model.StageResponse,
		Request:  model.RequestSnapshot{Method: "GET", URL: raw, Host: u.Host, Path: u.Path, Headers: map[string][]string{"Range": {"bytes=0-1023"}}},
		Response: &model.ResponseSnapshot{StatusCode: 206, ContentType: "video/mp4"}}
}
func resolve(t *testing.T, p model.RuntimePlugin, r model.ResourceCandidate) model.DownloadPlan {
	t.Helper()
	plan, ok, err := p.Resolve(context.Background(), r, model.DownloadOptions{})
	if err != nil || !ok {
		t.Fatalf("resolve: %v, %v", ok, err)
	}
	for _, in := range plan.Inputs {
		if in.Executor != "http-file" || !strings.HasPrefix(in.URL, "https://") {
			t.Fatal("unexpected acquisition input")
		}
		if plan.Output.Input == "preview" {
			t.Fatal("preview must not replace the selected video output")
		}
		if in.Headers["Referer"] != "https://www.xiaohongshu.com/" {
			t.Fatal("missing public referer")
		}
	}
	return plan
}

func TestVideoPlans(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inputs int
		height int
	}{
		{"video-split", 3, 2160}, {"video-qc", 3, 2160}, {"video-progressive", 1, 720}, {"missing-audio-fallback", 1, 720},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := runtime(t)
			r := handle(t, p, fixture(t, tc.name)[0])
			if !r.Handled || len(r.Resources) != 2 {
				t.Fatal("expected a collection and one video")
			}
			v := r.Resources[1]
			if v.Tracks[0].Height != tc.height || v.State != "ready" {
				t.Fatal("incorrect quality or readiness")
			}
			if v.Technical.Duration != 120 {
				t.Fatal("fractional source seconds must be converted to an integer host duration")
			}
			if v.Preview == nil || v.Preview.Renderer != "video" || (v.Preview.TrackID != "preview" && v.Preview.TrackID != "video") {
				t.Fatal("expected complete video preview")
			}
			plan := resolve(t, p, v)
			foundPreview := false
			for _, input := range plan.Inputs {
				if input.ID == v.Preview.TrackID {
					foundPreview = true
				}
			}
			if !foundPreview {
				t.Fatal("host cannot resolve preview from plan")
			}
			if len(plan.Inputs) != tc.inputs || plan.Output.Extension != ".mp4" {
				t.Fatal("incorrect plan")
			}
			if tc.inputs == 3 {
				if len(plan.Pipeline) != 1 || plan.Pipeline[0].Executor != "builtin.media.mux" ||
					!reflect.DeepEqual(plan.Pipeline[0].Inputs, []string{"video", "audio"}) ||
					plan.Output.Input != "merged" || !strings.Contains(plan.Inputs[1].URL, "audio-103.m4a") {
					t.Fatal("incorrect audio pairing or mux wiring")
				}
			} else if len(plan.Pipeline) != 0 || plan.Output.Input != "video" {
				t.Fatal("progressive file must not use stale audio")
			}
		})
	}
}

func TestGalleryAndTitleFallback(t *testing.T) {
	p := runtime(t)
	r := handle(t, p, fixture(t, "gallery")[0])
	if len(r.Resources) != 8 || r.Resources[0].Kind != "media.collection" || len(r.Resources[0].Tracks) != 0 {
		t.Fatal("expected parent, five photos and two motion files")
	}
	parent := r.Resources[0].GroupKey
	previousIndex := float64(0)
	imageIndex := 0
	for _, child := range r.Resources[1:] {
		order, ok := child.Metadata["collectionIndex"].(float64)
		if !ok || order <= previousIndex {
			t.Fatal("host collection ordering missing")
		}
		previousIndex = order
		if child.ParentGroupKey != parent {
			t.Fatal("orphan child")
		}
		if child.Kind == "media.image" {
			imageIndex++
			if child.Metadata["xiaohongshu.imageIndex"] != float64(imageIndex) {
				t.Fatal("incorrect image order")
			}
			if strings.Contains(child.Tracks[0].URL, "nd_prv") || child.Tracks[0].Extension != ".jpg" {
				t.Fatal("thumbnail or wrong image format selected")
			}
		}
		plan := resolve(t, p, child)
		if len(plan.Inputs) != 1 || len(plan.Pipeline) != 0 {
			t.Fatal("gallery children are independent files")
		}
	}
	if _, ok, err := p.Resolve(context.Background(), r.Resources[0], model.DownloadOptions{}); err != nil || ok {
		t.Fatal("parent must defer to host collection downloader")
	}
	single := handle(t, p, fixture(t, "single-image")[0])
	if single.Resources[0].Title != "无标题作品的首行" {
		t.Fatal("missing description title fallback")
	}
}

func TestCorrelatedSuppressionAndFallback(t *testing.T) {
	p := runtime(t)
	base := "https://sns-video-v6.xhscdn.com/fixture/video-2160.m4s?sign=fixture&t=4102444800"
	if !handle(t, p, mediaObservation(base)).Handled {
		t.Fatal("media before metadata must suppress generic capture")
	}
	handle(t, p, fixture(t, "video-split")[0])
	cases := []struct {
		url     string
		handled bool
	}{
		{base, true},
		{strings.Replace(base, ".com/", ".com:443/", 1), true},
		{strings.Replace(base, "https:", "http:", 1), true},
		{strings.Replace(strings.Replace(base, "https:", "http:", 1), ".com/", ".com:80/", 1), true},
		{base + "&range=0-1023", true},
		{strings.Replace(base, "video-2160.m4s", "video-720.m4s", 1), true},
		{strings.Replace(base, "video-2160.m4s", "audio-103.m4a", 1), true},
		{"https://sns-bak-v1.xhscdn.com/fixture/video-2160.m4s", true},
		{strings.Replace(base, "sign=fixture", "sign=other", 1), true},
		{base + "&unknown=1", true},
		{strings.Replace(base, "video-2160.m4s", "unrelated.mp4", 1), true},
		{strings.Replace(base, "sns-video-v6.xhscdn.com", "example.com", 1), false},
		{strings.Replace(base, ".com/", ".com:8443/", 1), true},
	}
	for _, tc := range cases {
		r := handle(t, p, mediaObservation(tc.url))
		if r.Handled != tc.handled || len(r.Resources) != 0 {
			t.Fatalf("incorrect suppression for fictional URL %s", tc.url)
		}
	}
	p = runtime(t)
	handle(t, p, fixture(t, "gallery")[0])
	for _, raw := range []string{
		"https://sns-webpic-qc.xhscdn.com/fixture/image-1!nd_dft_wlteh_jpg_3",
		"https://sns-webpic-qc.xhscdn.com/fixture/image-1!nd_prv_wlteh_jpg_3",
		"https://sns-video-v6.xhscdn.com/fixture/live-1.mp4?sign=fixture&t=4102444800",
	} {
		if !handle(t, p, mediaObservation(raw)).Handled {
			t.Fatal("gallery media should be correlated")
		}
	}
}

func TestHostMergeRefreshAndModeChange(t *testing.T) {
	p := runtime(t)
	old := handle(t, p, fixture(t, "video-split")[0]).Resources[1]
	progressive := handle(t, p, fixture(t, "video-progressive")[0]).Resources[1]
	merged := plugin.MergeResourceCandidate(old, progressive)
	if merged.GroupKey != old.GroupKey || len(resolve(t, p, merged).Inputs) != 1 {
		t.Fatal("stale audio used after progressive update")
	}
	seq := fixture(t, "dedupe-and-refresh")
	fresh := handle(t, p, seq[len(seq)-1]).Resources[1]
	merged = plugin.MergeResourceCandidate(merged, fresh)
	plan := resolve(t, p, merged)
	if len(plan.Inputs) != 3 {
		t.Fatal("split mode not restored")
	}
	for _, in := range plan.Inputs {
		if !strings.Contains(in.URL, "sign=fixture-new") {
			t.Fatal("old signed URL survived merge")
		}
	}
	if merged.Metadata["publishedAt"] != float64(1700000000000) {
		t.Fatal("publication time lost")
	}
	refresh, ok, err := p.(model.ResourceRefresher).RefreshResource(context.Background(), merged, model.DownloadOptions{})
	if err != nil || !ok || refresh.Status != "recaptureRequired" {
		t.Fatal("expired URLs must ask for recapture")
	}
	var withoutAudio []model.ResourceTrack
	for _, tr := range merged.Tracks {
		if tr.ID != "audio" {
			withoutAudio = append(withoutAudio, tr)
		}
	}
	merged.Tracks = withoutAudio
	if _, _, err := p.Resolve(context.Background(), merged, model.DownloadOptions{}); err == nil {
		t.Fatal("must reject incomplete split download")
	}
}

func TestMalformedDetailsAndHeaderPrivacy(t *testing.T) {
	p := runtime(t)
	for _, o := range fixture(t, "invalid-details") {
		r := handle(t, p, o)
		if !r.Handled || len(r.Resources) != 0 {
			t.Fatal("invalid business response must not become media")
		}
	}
	o := fixture(t, "video-split")[0]
	o.Request.Headers["Cookie"] = []string{"fixture-private"}
	o.Request.Headers["Authorization"] = []string{"fixture-private"}
	r := handle(t, p, o)
	for _, res := range r.Resources {
		for _, tr := range res.Tracks {
			for k := range tr.Headers {
				if strings.EqualFold(k, "cookie") || strings.EqualFold(k, "authorization") {
					t.Fatal("API credentials leaked to media")
				}
			}
		}
	}
	for _, body := range []string{
		"null", "[]", "{\"code\":0,\"success\":true,\"data\":null}",
		"{\"code\":0,\"success\":true,\"data\":{\"items\":[null]}}",
		"{\"code\":0,\"success\":true,\"data\":{\"items\":[{},{}]}}",
	} {
		o.Response.Body = body
		if len(handle(t, p, o).Resources) != 0 {
			t.Fatal("malformed detail captured")
		}
	}
	o = fixture(t, "video-split")[0]
	o.Response.ContentType = "application/octet-stream"
	if len(handle(t, p, o).Resources) != 2 {
		t.Fatal("valid JSON labelled binary should work")
	}
	o.Response.ContentType = "text/html"
	if len(handle(t, p, o).Resources) != 0 {
		t.Fatal("HTML error page captured")
	}
	o = fixture(t, "video-split")[0]
	o.Request.URL = "https://edith.xiaohongshu.com/api/sns/web/v1/homefeed"
	o.Request.Path = "/api/sns/web/v1/homefeed"
	if r := handle(t, p, o); !r.Handled || len(r.Resources) != 0 {
		t.Fatal("homepage feed must be suppressed without extracting list resources")
	}
}

func TestCaptionFromDetailWithoutPage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "plugins", "official.xiaohongshu")
	if err := os.MkdirAll(dir, 0750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plugin.json", "main.js"} {
		data, err := os.ReadFile(filepath.Join("..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	logger := logging.New(false, "")
	catalog := resource.New(root, &config.Config{}, nil, logger, nil)
	defer catalog.Close()
	// Mirror the official source recorded when this plugin is installed.
	if err := os.WriteFile(filepath.Join(root, "plugin-sources.json"), []byte(`{"official.xiaohongshu":"official"}`), 0600); err != nil {
		t.Fatal(err)
	}
	manager := plugin.NewManager(root, func() plugin.NetworkSettings { return plugin.NetworkSettings{} }, nil, catalog, logger)
	store, err := capture.New(filepath.Join(root, "captures"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// Set after plugins are loaded, as in the application's composition root.
	manager.SetCaptureStore(store)
	catalog.SetCaptureSource(store)
	catalog.RegisterTypes([]string{"collection"})
	catalog.SetTypes([]string{"collection"})
	status, _ := manager.Status("official.xiaohongshu")
	if len(status.Manifest.PageScripts) != 0 || status.Manifest.Permissions.Has("page-bridge") || status.Manifest.Permissions.Has("inject-page-script") {
		t.Fatal("caption still depends on webpage")
	}
	obs := fixture(t, "single-image")[0]
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(obs.Response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	note := payload["data"].(map[string]interface{})["items"].([]interface{})[0].(map[string]interface{})["note_card"].(map[string]interface{})
	note["title"], note["desc"] = "离线标题", "正文第一行\n第二行 😀"
	raw, _ := json.Marshal(payload)
	obs.Response.Body = string(raw)
	var previous string
	for range 2 {
		r := manager.Process(context.Background(), obs)
		if !r.Handled || len(r.Resources) != 3 {
			t.Fatalf("caption not emitted with media: %#v", r.Diagnostics)
		}
		rows := catalog.List()
		if len(rows) != 1 || len(rows[0].Children) != 2 || rows[0].Children[1].Kind != "document.text" {
			t.Fatalf("caption missing from collection: %#v", rows)
		}
		child := rows[0].Children[1].ResourceCandidate
		plan, err := manager.CreateDownloadPlan(context.Background(), child, model.DownloadOptions{})
		if err != nil || len(plan.Inputs) != 1 || plan.Inputs[0].Executor != "capture-file" || plan.Output.Extension != ".txt" {
			t.Fatalf("caption plan: %#v %v", plan, err)
		}
		key := plan.Inputs[0].CaptureKey
		if key == previous {
			t.Fatal("recapture overwrote the previous file")
		}
		previous = key
		reader, _, err := store.OpenComplete(key)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || string(data) != "离线标题\n\n正文第一行\n第二行 😀" || child.Tracks[0].Size != int64(len(data)) {
			t.Fatalf("caption bytes/size mismatch: %v", err)
		}
		catalog.Clear()
	}
	for _, desc := range []string{"", "  ", strings.Repeat("x", 16385)} {
		note["desc"] = desc
		raw, _ = json.Marshal(payload)
		obs.Response.Body = string(raw)
		if r := manager.Process(context.Background(), obs); len(r.Resources) != 2 {
			t.Fatal("empty or oversized caption was emitted")
		}
		catalog.Clear()
	}
	// A cache failure must retain the media collection without a dangling TXT.
	store.Close()
	note["desc"] = "fixture"
	raw, _ = json.Marshal(payload)
	obs.Response.Body = string(raw)
	if r := manager.Process(context.Background(), obs); !r.Handled || len(r.Resources) != 2 {
		t.Fatal("capture failure lost media or emitted an invalid TXT")
	}
}

// These hosts were identified in the user's persisted generic resource rows.
// Use the complete manager so manifest matching and the generic detector are
// exercised, rather than calling the JS hook outside its declared scope.
func TestObservedHostsThroughFullPluginChain(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "plugins", "official.xiaohongshu")
	for _, name := range []string{"plugin.json", "main.js"} {
		data, err := os.ReadFile(filepath.Join("..", name))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	logger := logging.New(false, "")
	catalog := resource.New(root, &config.Config{}, nil, logger, nil)
	defer catalog.Close()
	// Mirror the official source recorded when this plugin is installed.
	if err := os.WriteFile(filepath.Join(root, "plugin-sources.json"), []byte(`{"official.xiaohongshu":"official"}`), 0600); err != nil {
		t.Fatal(err)
	}
	manager := plugin.NewManager(root, func() plugin.NetworkSettings { return plugin.NetworkSettings{} }, nil, catalog, logger)
	if status, ok := manager.Status("official.xiaohongshu"); !ok || !status.Loaded {
		t.Fatalf("fixture plugin not loaded: %#v", status)
	}
	for _, host := range []string{
		"sns-video-qc.xhscdn.com", "picasso-static.xiaohongshu.com", "fe-platform.xhscdn.com",
		"sns-avatar-qc.xhscdn.com", "fe-static.xhscdn.com", "future-edge.xhscdn.com",
		"nested.edge.xhscdn.com", "unknown.xiaohongshu.com", "xhscdn.com", "xiaohongshu.com",
		"SNS-AVATAR-QC.XHSCDN.COM:443", "sns-avatar-qc.xhscdn.com:8443",
	} {
		o := mediaObservation("https://" + host + "/fixture/file.mp4")
		if host != "sns-video-qc.xhscdn.com" {
			o.Response.ContentType = "image/png"
		}
		r := manager.Process(context.Background(), o)
		if !r.Handled || len(r.Resources) != 0 || len(catalog.List()) != 0 {
			t.Fatalf("generic capture leaked for %s: %#v", host, r)
		}
		if limit := manager.BodyLimit(o); limit != 0 {
			t.Fatalf("suppression unexpectedly reads response body for %s: %d", host, limit)
		}
	}
	longURL := mediaObservation("https://sns-avatar-qc.xhscdn.com/fixture/" + strings.Repeat("a", 9000))
	longURL.Response.ContentType = "image/png"
	if r := manager.Process(context.Background(), longURL); !r.Handled || len(r.Resources) != 0 {
		t.Fatal("strict media URL parser leaked generic capture")
	}
	if limit := manager.BodyLimit(fixture(t, "video-qc")[0]); limit != 1048576 {
		t.Fatalf("detail body scope changed: %d", limit)
	}
	r := manager.Process(context.Background(), fixture(t, "video-qc")[0])
	if len(r.Resources) != 2 {
		t.Fatalf("QC video not captured: %#v", r.Diagnostics)
	}
	rows := catalog.List()
	if len(rows) != 1 || rows[0].Kind != "media.collection" || len(rows[0].Children) != 1 || rows[0].Children[0].Kind != "media.video" {
		t.Fatal("QC video not published under collection")
	}
	if !strings.Contains(rows[0].Children[0].Tracks[0].URL, "sns-video-qc.xhscdn.com/") {
		t.Fatal("QC video URL was not preserved")
	}
	for _, host := range []string{"example.com", "evilxhscdn.com", "xhscdn.com.example.com", "xiaohongshu.com.example.com"} {
		o := mediaObservation("https://" + host + "/fixture/other.mp4")
		o.Request.Headers["Referer"] = []string{"https://www.xiaohongshu.com/"}
		r := manager.Process(context.Background(), o)
		if r.Handled || len(r.Resources) != 1 || r.Resources[0].Source.PluginID != "builtin.generic-detector" {
			t.Fatalf("suppression escaped Xiaohongshu domain boundary for %s: %#v", host, r)
		}
	}
}
