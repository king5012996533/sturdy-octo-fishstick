package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// snapshotWith 拼一份画布快照。nodes 用原始 JSON 传，方便逐个用例捏出不规则的形状。
func snapshotWith(nodes ...string) string {
	return `{"nodes":[` + strings.Join(nodes, ",") + `],"edges":[],"savedAt":1}`
}

func videoNodeJSON(id string, output string, params string) string {
	return fmt.Sprintf(`{"id":%q,"type":"video","data":{"action":"video_generate","url":[%q],"params":%s}}`, id, output, params)
}

// 快照里往往有好几个视频节点（一条工作流里每个镜头都是一个节点）。
// 配方必须锚到最终成片那一个，否则用户复刻到的是别的镜头的参数。
func TestInspirationRecipeAnchorsFinalOutput(t *testing.T) {
	snapshot := snapshotWith(
		videoNodeJSON("v-1", "https://cdn.example.com/a/shot-1.mp4", `{"model":"star-video2-fast","modeType":"text2video","settings":{"duration":5}}`),
		videoNodeJSON("v-2", "https://cdn.example.com/a/final.mp4", `{"model":"star-video2","modeType":"mixed2video","imageList":[{"url":"https://cdn.example.com/ref/1.png"}],"settings":{"ratio":"16:9","resolution":"720p","duration":15}}`),
	)
	recipe := inspirationRecipeFromSnapshot("https://cdn.example.com/a/final.mp4", snapshot)
	if recipe.VideoModel != "star-video2" || recipe.VideoMode != "mixed2video" {
		t.Fatalf("应取成片对应的节点，实际 %+v", recipe)
	}
	if recipe.Ratio != "16:9" || recipe.Resolution != "720p" || recipe.DurationSeconds != 15 {
		t.Fatalf("应带上时长比例，实际 %+v", recipe)
	}
	if len(recipe.ImageURLs) != 1 || recipe.ImageURLs[0] != "https://cdn.example.com/ref/1.png" {
		t.Fatalf("应带上参考图，实际 %v", recipe.ImageURLs)
	}
}

// 锚不到成片时退回最后一个视频节点：它是这一稿的收尾产物，比第一个更接近成片。
func TestInspirationRecipeFallsBackToLastVideoNode(t *testing.T) {
	snapshot := snapshotWith(
		videoNodeJSON("v-1", "https://cdn.example.com/a/shot-1.mp4", `{"model":"star-video2-fast","modeType":"text2video","settings":{"duration":5}}`),
		videoNodeJSON("v-2", "https://cdn.example.com/a/shot-2.mp4", `{"model":"kling-v3-omni","modeType":"frames2video","settings":{"duration":10}}`),
	)
	recipe := inspirationRecipeFromSnapshot("https://cdn.example.com/a/unknown.mp4", snapshot)
	if recipe.VideoModel != "kling-v3-omni" || recipe.DurationSeconds != 10 {
		t.Fatalf("应退回最后一个视频节点，实际 %+v", recipe)
	}
}

// 上游的地址字段有字符串与数组两种形态，同一条作品里还混着出现。用真实形状钉住：
// 参考图的 url 给数组、视频节点的 url 给数组，配方仍要解出来——线上正是因为一个
// 字段声明成 string，让整份快照反序列化失败、整条作品的配方丢成空。
func TestInspirationRecipeReadsArrayShapedURLs(t *testing.T) {
	node := `{"id":"v-1","type":"video","data":{"action":"video_generate","url":["https://cdn.example.com/a/final.mp4"],` +
		`"params":{"model":"kling-v3-omni","modeType":"mixed2video",` +
		`"imageList":[{"url":["https://cdn.example.com/ref/1.png"]},{"url":"https://cdn.example.com/ref/2.png"}],` +
		`"settings":{"ratio":"16:9","duration":"8"}}}}`
	recipe := inspirationRecipeFromSnapshot("https://cdn.example.com/a/final.mp4", snapshotWith(node))
	if recipe.VideoModel != "kling-v3-omni" || recipe.DurationSeconds != 8 || recipe.Ratio != "16:9" {
		t.Fatalf("数组形态的地址应照样解出配方，实际 %+v", recipe)
	}
	if len(recipe.ImageURLs) != 2 || recipe.ImageURLs[0] != "https://cdn.example.com/ref/1.png" {
		t.Fatalf("字符串与数组两种参考图都应读到，实际 %v", recipe.ImageURLs)
	}
}

// 画布是用户自由编织的，节点形状不可控。一个节点解不出来只该少一个候选，
// 不能让整条作品的配方归零。
func TestInspirationRecipeSkipsMalformedNode(t *testing.T) {
	snapshot := snapshotWith(
		`{"id":"v-bad","type":"video","data":{"action":"video_generate","url":1,"params":"oops"}}`,
		videoNodeJSON("v-1", "https://cdn.example.com/a/shot-1.mp4", `{"model":"star-video2","modeType":"mixed2video","imageList":[{"url":"https://cdn.example.com/ref/1.png"}]}`),
	)
	recipe := inspirationRecipeFromSnapshot("https://cdn.example.com/a/unknown.mp4", snapshot)
	if recipe.VideoModel != "star-video2" || len(recipe.ImageURLs) != 1 {
		t.Fatalf("坏节点应被跳过、好节点仍要选中，实际 %+v", recipe)
	}
}

// 锚不到成片时，"最后一个节点"经常是作者最后补的一小段纯文生视频，或者一个只有
// model 没有 modeType 的后处理节点（放大、补帧）。参考图正是提示词补不回来的那半，
// 所以带参考图的生成节点优先，同一档再取最后的。
func TestInspirationRecipePrefersImageBearingNode(t *testing.T) {
	snapshot := snapshotWith(
		videoNodeJSON("v-1", "https://cdn.example.com/a/shot-1.mp4", `{"model":"kling-v3-omni","modeType":"mixed2video","imageList":[{"url":"https://cdn.example.com/ref/1.png"},{"url":"https://cdn.example.com/ref/2.png"}],"settings":{"ratio":"9:16","duration":8}}`),
		videoNodeJSON("v-2", "https://cdn.example.com/a/shot-2.mp4", `{"model":"star-video2-fast","modeType":"text2video","settings":{"duration":5}}`),
		videoNodeJSON("v-3", "https://cdn.example.com/a/shot-3.mp4", `{"model":"topaz-video-upscaler","imageList":[{"url":"https://cdn.example.com/ref/9.png"}]}`),
	)
	recipe := inspirationRecipeFromSnapshot("https://cdn.example.com/a/unknown.mp4", snapshot)
	if recipe.VideoModel != "kling-v3-omni" || recipe.Ratio != "9:16" || recipe.DurationSeconds != 8 {
		t.Fatalf("带参考图的生成节点应优先于末尾的纯文生视频与后处理节点，实际 %+v", recipe)
	}
	if len(recipe.ImageURLs) != 2 {
		t.Fatalf("应只带选中节点的参考图，实际 %v", recipe.ImageURLs)
	}
}

// 时长在快照里既出现过数字也出现过字符串；两种都要读得出来，读不出来按 0 处理
// （0 在条目上就是"没记录"），而不是丢掉整份配方。
func TestInspirationRecipeDurationForms(t *testing.T) {
	cases := []struct {
		name     string
		settings string
		want     int
	}{
		{"数字", `{"duration":15}`, 15},
		{"字符串", `{"duration":"8"}`, 8},
		{"null", `{"duration":null}`, 0},
		{"字段缺失", `{"ratio":"16:9"}`, 0},
		{"非法值", `{"duration":"soon"}`, 0},
	}
	for _, item := range cases {
		snapshot := snapshotWith(videoNodeJSON("v-1", "https://cdn.example.com/a/final.mp4", `{"model":"star-video2","settings":`+item.settings+`}`))
		recipe := inspirationRecipeFromSnapshot("https://cdn.example.com/a/final.mp4", snapshot)
		if recipe.DurationSeconds != item.want {
			t.Fatalf("%s：预期 %d，实际 %d", item.name, item.want, recipe.DurationSeconds)
		}
	}
}

// 参考图最多带 4 张，且只认 http(s)：全带走会让"使用这个创意"变成一次几十张图的上传，
// 而本地路径或空地址交给前端就是一个必然裂的图。
func TestInspirationRecipeCapsAndFiltersImages(t *testing.T) {
	images := make([]string, 0, 7)
	for index := 0; index < 5; index++ {
		images = append(images, fmt.Sprintf(`{"url":"https://cdn.example.com/ref/%d.png"}`, index))
	}
	images = append(images, `{"url":"/local/only.png"}`, `{"url":""}`)
	snapshot := snapshotWith(videoNodeJSON("v-1", "https://cdn.example.com/a/final.mp4",
		`{"model":"star-video2","imageList":[`+strings.Join(images, ",")+`]}`))
	recipe := inspirationRecipeFromSnapshot("https://cdn.example.com/a/final.mp4", snapshot)
	if len(recipe.ImageURLs) != inspirationRecipeMaxImages {
		t.Fatalf("参考图应截到 %d 张，实际 %d", inspirationRecipeMaxImages, len(recipe.ImageURLs))
	}
	for _, url := range recipe.ImageURLs {
		if !strings.HasPrefix(url, "https://") {
			t.Fatalf("非 http(s) 的地址应被过滤，实际 %q", url)
		}
	}
}

// 只有图片节点、纯文生视频、快照损坏这些情况都不该报错，得到一个空配方即可——
// "这条没有配方"是正常状态，抓取命令不该因此变红。
func TestInspirationRecipeEmptyCases(t *testing.T) {
	cases := []struct{ name, finalOutput, snapshot string }{
		{"纯文生视频无参考图", "https://cdn.example.com/a/final.mp4", snapshotWith(videoNodeJSON("v-1", "https://cdn.example.com/a/final.mp4", `{"model":"star-video2","modeType":"text2video"}`))},
		{"没有视频节点", "https://cdn.example.com/a/final.mp4", snapshotWith(`{"id":"i-1","type":"image","data":{"action":"image_resource"}}`)},
		{"快照不是 JSON", "https://cdn.example.com/a/final.mp4", "{oops"},
		{"快照为空", "https://cdn.example.com/a/final.mp4", ""},
		{"没有节点", "https://cdn.example.com/a/final.mp4", `{"nodes":[]}`},
	}
	for _, item := range cases {
		recipe := inspirationRecipeFromSnapshot(item.finalOutput, item.snapshot)
		if len(recipe.ImageURLs) != 0 {
			t.Fatalf("%s：不该有参考图，实际 %v", item.name, recipe.ImageURLs)
		}
	}
	// 纯文生视频仍然应当留下模型与模式：那也是复刻要用的信息。
	recipe := inspirationRecipeFromSnapshot("https://cdn.example.com/a/final.mp4",
		snapshotWith(videoNodeJSON("v-1", "https://cdn.example.com/a/final.mp4", `{"model":"star-video2","modeType":"text2video"}`)))
	if recipe.VideoModel != "star-video2" || recipe.VideoMode != "text2video" {
		t.Fatalf("纯文生视频也应留下模型与模式，实际 %+v", recipe)
	}
}

// 参考图 ID 在列上是逗号分隔的，空值不参与，视图拿到的是干净数组。
func TestInspirationRecipeImageIDRoundTrip(t *testing.T) {
	if got := joinInspirationRecipeImageIDs([]string{"a", " b ", "", "c"}); got != "a,b,c" {
		t.Fatalf("拼接应去掉空值，实际 %q", got)
	}
	if got := splitInspirationRecipeImageIDs(""); got != nil {
		t.Fatalf("空串应还原成 nil，实际 %v", got)
	}
	if got := splitInspirationRecipeImageIDs(" , "); len(got) != 0 {
		t.Fatalf("只有分隔符时应还原成空列表，实际 %v", got)
	}
}

// 上游用业务码而不是 HTTP 状态表达"没有这条作品"。业务码非 0 时必须当失败处理，
// 否则详情里那份空数据会被当成"这条作品没有成片"，把已有地址覆盖成空串。
func TestFetchInspirationTemplateDetailRejectsBusinessError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code":1001,"msg":"not found","data":{"detail":{"finalOutput":"","snapshotData":""}}}`)
	}))
	defer server.Close()
	// 必须换掉基地址：否则这个用例会真的去打生产接口，拿到一个"成功"再断言失败。
	withTemplateDetailBaseURL(t, server.URL)

	if _, err := fetchInspirationTemplateDetail(context.Background(), server.Client(), "10b86d68aa3b4d9db915f0f8b53fdd3c"); err == nil {
		t.Fatal("业务码非 0 时应返回错误")
	}
	if _, err := fetchInspirationTemplateDetail(context.Background(), server.Client(), "10b86d68aa3b4d9db915f0f8b53fdd3c"); err != nil && !strings.Contains(err.Error(), "1001") {
		t.Fatalf("错误信息应带上业务码便于排查，实际 %v", err)
	}
}

// 复刻配方由抓取命令写、后台表单不提供编辑入口。运营在后台改一次文案，不能顺手把
// 配方清空——保存走的是"读出已有行再逐字段覆盖"，配方不在覆盖列表里就应当原地保留。
func TestSaveCreationInspirationKeepsRecipe(t *testing.T) {
	svc := newInspirationTestService(t)
	record := seedVideoInspiration(t, svc, "INSP_RECIPE_2", "https://www.liblib.tv/detail/385c4036c86441a29994f53d57011e64")
	record.RecipeVideoModel = "star-video2"
	record.RecipeVideoMode = "mixed2video"
	record.RecipeRatio = "16:9"
	record.RecipeResolution = "720p"
	record.RecipeDurationSeconds = 15
	record.RecipeImageIDs = "aaa,bbb"
	if err := svc.repo.SaveCreationInspiration(record); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.SaveCreationInspiration(CreationInspirationInput{
		ID:       record.ID,
		Title:    "改过的标题",
		Mode:     "video",
		Status:   "ONLINE",
		Prompt:   "改过的提示词",
		CoverURL: record.CoverURL,
	}); err != nil {
		t.Fatal(err)
	}

	saved, err := svc.repo.CreationInspirationByID(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.RecipeVideoModel != "star-video2" || saved.RecipeImageIDs != "aaa,bbb" || saved.RecipeDurationSeconds != 15 {
		t.Fatalf("后台保存不该清空复刻配方，实际 %+v", saved)
	}
	if saved.Title != "改过的标题" {
		t.Fatalf("后台保存应当生效，实际 %q", saved.Title)
	}
}
