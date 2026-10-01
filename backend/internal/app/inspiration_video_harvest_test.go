package app

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
)

// fakeLibtvTemplateDetail 起一个假模板详情接口：成片与参考图地址都指回这个假服务器
// 自己，这样 HLS 播放列表探测与参考图下载都能被真实走一遍。
type fakeLibtvTemplateDetail struct {
	server      *httptest.Server
	finalOutput string
	images      int
}

func newFakeLibtvTemplateDetail(t *testing.T, uuid string, withPlaylist bool, images int) *fakeLibtvTemplateDetail {
	t.Helper()
	fake := &fakeLibtvTemplateDetail{images: images}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/community/project/template/detail":
			fake.finalOutput = fake.server.URL + "/upload/" + uuid + ".mp4"
			payload := map[string]any{
				"code": 0,
				"data": map[string]any{
					"detail": map[string]any{
						"finalOutput":  fake.finalOutput,
						"snapshotData": fake.snapshot(uuid),
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(payload)
		case r.URL.Path == "/upload/"+uuid+"/master.m3u8" && withPlaylist:
			w.Header().Set("Content-Type", "application/x-mpegURL")
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=2800000\n720p/index.m3u8\n")
		case strings.HasPrefix(r.URL.Path, "/ref/"):
			w.Header().Set("Content-Type", "image/png")
			_ = png.Encode(w, testGradientImage(1600, 900))
		default:
			// 上游对不存在的转码目录常回 200 的 XML 错误体，这里也照那个形状来。
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `<?xml version="1.0"?><Error><Code>NoSuchKey</Code></Error>`)
		}
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

// snapshot 造一份形状照抄上游的画布快照：一个文本节点 + 一个带参考图的视频节点。
func (f *fakeLibtvTemplateDetail) snapshot(uuid string) string {
	images := make([]map[string]any, 0, f.images)
	for index := 0; index < f.images; index++ {
		images = append(images, map[string]any{"nodeId": fmt.Sprintf("i-%d", index), "url": fmt.Sprintf("%s/ref/%s-%d.png", f.server.URL, uuid, index)})
	}
	snapshot := map[string]any{
		"nodes": []map[string]any{
			{"type": "text", "data": map[string]any{"action": "text_generate", "params": map[string]any{"model": "aurora-3-prime"}}},
			{
				"type": "video",
				"data": map[string]any{
					"action": "video_generate",
					"url":    []string{f.finalOutput},
					"params": map[string]any{
						"model":     "star-video2",
						"modeType":  "mixed2video",
						"imageList": images,
						"settings":  map[string]any{"ratio": "16:9", "resolution": "720p", "duration": 15},
					},
				},
			},
		},
		"edges": []any{},
	}
	encoded, _ := json.Marshal(snapshot)
	return string(encoded)
}

// testGradientImage 造一张有渐变的图，便于断言缩放后的尺寸而不是像素全等。
func testGradientImage(width, height int) image.Image {
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	return canvas
}

func seedVideoInspiration(t *testing.T, svc *Service, id string, detail string) *model.CreationInspiration {
	t.Helper()
	record := &model.CreationInspiration{
		ID:           id,
		Title:        "示例成片",
		CoverURL:     "https://cdn.example.com/cover.webp",
		Prompt:       "一段提示词",
		Mode:         "video",
		Status:       model.CreationInspirationOnline,
		Origin:       model.CreationInspirationOriginPlatform,
		ReviewStatus: model.CreationInspirationReviewApproved,
		SourceURL:    detail,
	}
	if err := svc.repo.SaveCreationInspiration(record); err != nil {
		t.Fatal(err)
	}
	return record
}

func withTemplateDetailBaseURL(t *testing.T, baseURL string) {
	t.Helper()
	previous := liblibTemplateDetailBaseURL
	liblibTemplateDetailBaseURL = baseURL
	t.Cleanup(func() { liblibTemplateDetailBaseURL = previous })
}

// 同一个作品有 1080p/720p/480p 三档 HLS，也有 1080p 的 mp4 原件。
// 直接存 mp4 等于让每个看灵感的人都下一部三五百兆的片子，所以必须优先取播放列表。
func TestHarvestInspirationVideosPrefersHlsPlaylist(t *testing.T) {
	const uuid = "10b86d68aa3b4d9db915f0f8b53fdd3c"
	fake := newFakeLibtvTemplateDetail(t, uuid, true, 2)
	withTemplateDetailBaseURL(t, fake.server.URL)

	svc := newInspirationTestService(t)
	record := seedVideoInspiration(t, svc, "INSP_VIDEO_1", "https://www.liblib.tv/detail/"+uuid)

	result, err := svc.HarvestInspirationVideos(context.Background(), InspirationVideoHarvestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Harvested != 1 || result.Failed != 0 {
		t.Fatalf("预期 1 条抓取成功，实际 %+v", result)
	}
	saved, err := svc.repo.CreationInspirationByID(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := fake.server.URL + "/upload/" + uuid + "/master.m3u8"; saved.VideoURL != want {
		t.Fatalf("应优先存 HLS 播放列表：预期 %q，实际 %q", want, saved.VideoURL)
	}
}

// 上游不是每个作品都转过码（或转码目录已清理）。播放列表探测失败时必须回落 mp4 原件，
// 而不是把这条判成失败、让卡片彻底没有播放入口。
func TestHarvestInspirationVideosFallsBackToMP4(t *testing.T) {
	const uuid = "8a8fe0fb83aa4bf186a764d59f9cd66f"
	fake := newFakeLibtvTemplateDetail(t, uuid, false, 0)
	withTemplateDetailBaseURL(t, fake.server.URL)

	svc := newInspirationTestService(t)
	record := seedVideoInspiration(t, svc, "INSP_VIDEO_2", "https://www.liblib.tv/detail/"+uuid)

	result, err := svc.HarvestInspirationVideos(context.Background(), InspirationVideoHarvestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Harvested != 1 {
		t.Fatalf("预期回落 mp4 并成功，实际 %+v", result)
	}
	saved, err := svc.repo.CreationInspirationByID(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := fake.server.URL + "/upload/" + uuid + ".mp4"; saved.VideoURL != want {
		t.Fatalf("应回落 mp4 原件：预期 %q，实际 %q", want, saved.VideoURL)
	}
}

// 非视频条目、非 LibTV 来源没有可推导的成片地址，应当跳过而不是记失败：
// 运营手录的图文条目本来就走不到这一步，把它们算进失败会让命令每次都以非零退出。
func TestHarvestInspirationVideosSkipsUnknownRows(t *testing.T) {
	svc := newInspirationTestService(t)
	image := &model.CreationInspiration{
		ID: "INSP_VIDEO_3", Title: "图片条目", Mode: "image", Prompt: "x",
		CoverURL: "https://cdn.example.com/a.webp", SourceURL: "https://www.liblib.tv/detail/10b86d68aa3b4d9db915f0f8b53fdd3c",
		Status: model.CreationInspirationOnline, Origin: model.CreationInspirationOriginPlatform,
	}
	manual := &model.CreationInspiration{
		ID: "INSP_VIDEO_4", Title: "运营手录", Mode: "video", Prompt: "x",
		CoverURL: "https://cdn.example.com/b.webp", SourceURL: "https://example.com/whatever",
		Status: model.CreationInspirationOnline, Origin: model.CreationInspirationOriginPlatform,
	}
	for _, record := range []*model.CreationInspiration{image, manual} {
		if err := svc.repo.SaveCreationInspiration(record); err != nil {
			t.Fatal(err)
		}
	}
	result, err := svc.HarvestInspirationVideos(context.Background(), InspirationVideoHarvestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 0 || result.Skipped != 2 || result.Failed != 0 {
		t.Fatalf("不可推导的条目应计入跳过，实际 %+v", result)
	}
}

// 成片地址与配方都已经在库里时不再打上游：重跑一次就再拉一次详情纯属浪费，
// 而这条命令是要在线上跑的，上游流量要能省则省。
func TestHarvestInspirationVideosKeepsCompleteRows(t *testing.T) {
	const uuid = "385c4036c86441a29994f53d57011e64"
	svc := newInspirationTestService(t)
	record := seedVideoInspiration(t, svc, "INSP_VIDEO_5", "https://www.liblib.tv/detail/"+uuid)
	record.VideoURL = "https://cdn.example.com/existing/master.m3u8"
	record.RecipeVideoModel = "star-video2"
	record.RecipeImageIDs = "aaa,bbb"
	if err := svc.repo.SaveCreationInspiration(record); err != nil {
		t.Fatal(err)
	}
	result, err := svc.HarvestInspirationVideos(context.Background(), InspirationVideoHarvestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kept != 1 || result.Harvested != 0 {
		t.Fatalf("成片与配方都齐的条目应原样保留，实际 %+v", result)
	}
	if result.Items[0].RecipeImages != 2 {
		t.Fatalf("保留的条目应报出已有参考图数量，实际 %d", result.Items[0].RecipeImages)
	}
}

// 库里现存的条目只有成片地址、没有配方（这一版之前抓的）。它们必须被补上配方，
// 而不是因为"已经有成片地址"整条跳过——否则这些条目永远复刻不出来。
func TestHarvestInspirationVideosBackfillsRecipe(t *testing.T) {
	const uuid = "9499e87df0e94098a420ec36061977ec"
	fake := newFakeLibtvTemplateDetail(t, uuid, true, 3)
	withTemplateDetailBaseURL(t, fake.server.URL)

	svc := newInspirationTestService(t)
	record := seedVideoInspiration(t, svc, "INSP_VIDEO_6", "https://www.liblib.tv/detail/"+uuid)
	existing := "https://cdn.example.com/existing/master.m3u8"
	record.VideoURL = existing
	if err := svc.repo.SaveCreationInspiration(record); err != nil {
		t.Fatal(err)
	}

	result, err := svc.HarvestInspirationVideos(context.Background(), InspirationVideoHarvestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Harvested != 1 || result.Images != 3 || result.ImageFailures != 0 {
		t.Fatalf("应补齐配方并收下 3 张参考图，实际 %+v", result)
	}
	saved, err := svc.repo.CreationInspirationByID(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.VideoURL != existing {
		t.Fatalf("已有成片地址不该被改写：预期 %q，实际 %q", existing, saved.VideoURL)
	}
	if saved.RecipeVideoModel != "star-video2" || saved.RecipeDurationSeconds != 15 || saved.RecipeRatio != "16:9" {
		t.Fatalf("配方应落库，实际 model=%q duration=%d ratio=%q", saved.RecipeVideoModel, saved.RecipeDurationSeconds, saved.RecipeRatio)
	}
	if ids := splitInspirationRecipeImageIDs(saved.RecipeImageIDs); len(ids) != 3 {
		t.Fatalf("参考图应记下 3 个资源 ID，实际 %v", ids)
	}
	// 参考图必须真的落进本地资源库，否则前台拿到的签名地址会指向不存在的文件。
	for _, id := range splitInspirationRecipeImageIDs(saved.RecipeImageIDs) {
		resource, err := svc.repo.Resource(id)
		if err != nil || resource == nil {
			t.Fatalf("参考图资源 %q 没有落库：%v", id, err)
		}
		if resource.UserID != InspirationReferenceOwnerID || resource.MimeType != "image/jpeg" {
			t.Fatalf("参考图资源形态不对：%+v", resource)
		}
	}
}

func TestLiblibDetailUUID(t *testing.T) {
	const uuid = "10b86d68aa3b4d9db915f0f8b53fdd3c"
	cases := []struct{ name, raw, want string }{
		{"标准作品页", "https://www.liblib.tv/detail/" + uuid, uuid},
		{"带查询串", "https://www.liblib.tv/detail/" + uuid + "?from=feed", uuid},
		{"大小写归一", "https://www.liblib.tv/detail/" + strings.ToUpper(uuid), uuid},
		{"非作品页", "https://example.com/detail/" + uuid, ""},
		{"标识长度不对", "https://www.liblib.tv/detail/abc", ""},
		{"空地址", "", ""},
	}
	for _, item := range cases {
		if got := liblibDetailUUID(item.raw); got != item.want {
			t.Fatalf("%s：预期 %q，实际 %q", item.name, item.want, got)
		}
	}
}

// 成片后缀不固定：同一个作品池里 mp4 与 m4v 都有。只认 .mp4 时那条作品会被当成
// "没有成片"，播放按钮消失，而实际上它的 HLS 就在同名目录下。
func TestHlsPlaylistFromOutput(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{"mp4", "https://cdn.example.com/upload/a/abc.mp4", "https://cdn.example.com/upload/a/abc/master.m3u8"},
		{"m4v", "https://cdn.example.com/upload/a/abc.m4v", "https://cdn.example.com/upload/a/abc/master.m3u8"},
		{"带查询串保留查询串", "https://cdn.example.com/upload/a/abc.mp4?v=2", "https://cdn.example.com/upload/a/abc/master.m3u8?v=2"},
		{"没有后缀", "https://cdn.example.com/upload/a/abc", ""},
		{"空地址", "", ""},
	}
	for _, item := range cases {
		if got := hlsPlaylistFromOutput(item.raw); got != item.want {
			t.Fatalf("%s：预期 %q，实际 %q", item.name, item.want, got)
		}
	}
}

// 详情接口的成片地址为空（作品已下架、或这条只有图）时记失败并说明原因，
// 不要写一个空地址进去让卡片以为有播放入口。
func TestHarvestInspirationVideosFailsWithoutOutput(t *testing.T) {
	const uuid = "5fbec0d3d2e9430a98466231431b90e6"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code":0,"data":{"detail":{"finalOutput":"","snapshotData":"{\"nodes\":[]}"}}}`)
	}))
	defer server.Close()
	withTemplateDetailBaseURL(t, server.URL)

	svc := newInspirationTestService(t)
	seedVideoInspiration(t, svc, "INSP_VIDEO_7", "https://www.liblib.tv/detail/"+uuid)

	result, err := svc.HarvestInspirationVideos(context.Background(), InspirationVideoHarvestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 || result.Items[0].Error == "" {
		t.Fatalf("没有成片地址时应记失败并带上原因，实际 %+v", result)
	}
}
