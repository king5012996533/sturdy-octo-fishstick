package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
)

// fakeLibtvDetail 起一个假作品页：页面里的成片地址指回这个假服务器自己，
// 这样 HLS 播放列表也落在同一处，探测路径能被真实走一遍。
func fakeLibtvDetail(t *testing.T, uuid string, withPlaylist bool) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/detail/"+uuid:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			// 形状照抄 Next.js 流式载荷：键被转义，值是绝对地址。
			fmt.Fprintf(w, `<html><script>self.__next_f.push([1,"{\"initialDetail\":{\"finalOutput\":\"%s\"}}"])</script></html>`, server.URL+"/upload/"+uuid+".mp4")
		case strings.HasSuffix(r.URL.Path, "/master.m3u8") && withPlaylist:
			w.Header().Set("Content-Type", "application/x-mpegURL")
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=2800000\n720p/index.m3u8\n")
		default:
			// 上游对不存在的转码目录常回 200 的 XML 错误体，这里也照那个形状来。
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `<?xml version="1.0"?><Error><Code>NoSuchKey</Code></Error>`)
		}
	}))
	t.Cleanup(server.Close)
	return server
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

// 同一个作品有 1080p/720p/480p 三档 HLS，也有 1080p 的 mp4 原件。
// 直接存 mp4 等于让每个看灵感的人都下一部三五百兆的片子，所以必须优先取播放列表。
func TestHarvestInspirationVideosPrefersHlsPlaylist(t *testing.T) {
	const uuid = "10b86d68aa3b4d9db915f0f8b53fdd3c"
	server := fakeLibtvDetail(t, uuid, true)
	previous := liblibDetailBaseURL
	liblibDetailBaseURL = server.URL
	defer func() { liblibDetailBaseURL = previous }()

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
	if want := server.URL + "/upload/" + uuid + "/master.m3u8"; saved.VideoURL != want {
		t.Fatalf("应优先存 HLS 播放列表：预期 %q，实际 %q", want, saved.VideoURL)
	}
}

// 上游不是每个作品都转过码（或转码目录已清理）。播放列表探测失败时必须回落 mp4 原件，
// 而不是把这条判成失败、让卡片彻底没有播放入口。
func TestHarvestInspirationVideosFallsBackToMP4(t *testing.T) {
	const uuid = "8a8fe0fb83aa4bf186a764d59f9cd66f"
	server := fakeLibtvDetail(t, uuid, false)
	previous := liblibDetailBaseURL
	liblibDetailBaseURL = server.URL
	defer func() { liblibDetailBaseURL = previous }()

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
	if want := server.URL + "/upload/" + uuid + ".mp4"; saved.VideoURL != want {
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

// 已有地址默认不动：成片地址是上游资源，重跑一次就再打一次作品页纯属浪费。
func TestHarvestInspirationVideosKeepsExistingAddress(t *testing.T) {
	const uuid = "385c4036c86441a29994f53d57011e64"
	svc := newInspirationTestService(t)
	record := seedVideoInspiration(t, svc, "INSP_VIDEO_5", "https://www.liblib.tv/detail/"+uuid)
	record.VideoURL = "https://cdn.example.com/existing/master.m3u8"
	if err := svc.repo.SaveCreationInspiration(record); err != nil {
		t.Fatal(err)
	}
	result, err := svc.HarvestInspirationVideos(context.Background(), InspirationVideoHarvestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kept != 1 || result.Harvested != 0 {
		t.Fatalf("已有地址应原样保留，实际 %+v", result)
	}
	if result.Items[0].VideoURL != record.VideoURL {
		t.Fatalf("保留的地址应与库中一致，实际 %q", result.Items[0].VideoURL)
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

// 作品页里除了本条作品的成片，还挂着相关推荐的成片；播放列表必须由本条成片推导，
// 而不是取页面里第一个 m3u8。
func TestHarvestInspirationVideosUsesOwnOutput(t *testing.T) {
	const uuid = "9499e87df0e94098a420ec36061977ec"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/detail/"+uuid:
			// 相关推荐排在本条前面，模拟真实页面顺序。
			fmt.Fprintf(w, `<script>self.__next_f.push([1,"{\"other\":{\"finalOutput\":\"%s\"},\"initialDetail\":{\"finalOutput\":\"%s\"}}"])</script>`,
				server.URL+"/upload/other.mp4", server.URL+"/upload/"+uuid+".m4v")
		case strings.HasSuffix(r.URL.Path, "/master.m3u8"):
			w.Header().Set("Content-Type", "application/x-mpegURL")
			fmt.Fprint(w, "#EXTM3U\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	previous := liblibDetailBaseURL
	liblibDetailBaseURL = server.URL
	defer func() { liblibDetailBaseURL = previous }()

	svc := newInspirationTestService(t)
	record := seedVideoInspiration(t, svc, "INSP_VIDEO_6", "https://www.liblib.tv/detail/"+uuid)
	result, err := svc.HarvestInspirationVideos(context.Background(), InspirationVideoHarvestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Harvested != 1 {
		t.Fatalf("预期抓取成功，实际 %+v", result)
	}
	saved, err := svc.repo.CreationInspirationByID(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := server.URL + "/upload/" + uuid + "/master.m3u8"; saved.VideoURL != want {
		t.Fatalf("应取本条作品的播放列表：预期 %q，实际 %q", want, saved.VideoURL)
	}
}
