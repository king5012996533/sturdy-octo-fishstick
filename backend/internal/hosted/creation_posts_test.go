package hosted

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/bootstrap"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// newCreationPostRouter 在标准测试路由上补建灵感表并回传服务，供用例造出真实的生成产物。
func newCreationPostRouter(t *testing.T) (bootstrap.HostedExtension, *gin.Engine, *gorm.DB, *app.Service) {
	t.Helper()
	extension, authDB, canvasDB, service := newTestExtension(t)
	if err := canvasDB.AutoMigrate(&model.CreationInspiration{}); err != nil {
		t.Fatalf("建灵感表失败：%v", err)
	}
	return extension, newTestRouter(extension, service), authDB, service
}

// readyImageResource 造一张就绪图片产物：投稿只认 ready 且本地的资源。
func readyImageResource(t *testing.T, service *app.Service, userID string, name string) string {
	t.Helper()
	payload := []byte("published-image-bytes")
	resource, err := service.UploadLocalResourceFile(userID, name, int64(len(payload)), "image", 64, 64, 0, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("准备生成产物失败：%v", err)
	}
	return resource.ID
}

type creationPostPayload struct {
	ID           string `json:"id"`
	Origin       string `json:"origin"`
	Title        string `json:"title"`
	Mode         string `json:"mode"`
	Author       string `json:"author"`
	Status       string `json:"status"`
	ReviewStatus string `json:"reviewStatus"`
	ReviewNote   string `json:"reviewNote"`
	CoverURL     string `json:"coverUrl"`
	ResourceID   string `json:"resourceId"`
}

// publishCreationPost 提交一条投稿并解出响应里的条目；失败响应时返回 nil。
func publishCreationPost(t *testing.T, router *gin.Engine, cookie *http.Cookie, body string) (int, *creationPostPayload) {
	t.Helper()
	recorder := perform(router, http.MethodPost, "/api/posts", body, cookie)
	var payload struct {
		Data struct {
			Post creationPostPayload `json:"post"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析投稿响应失败：%v %s", err, recorder.Body.String())
	}
	if recorder.Code != http.StatusOK {
		return recorder.Code, nil
	}
	return recorder.Code, &payload.Data.Post
}

func creationPostTitles(t *testing.T, router *gin.Engine, path string, cookie *http.Cookie) []string {
	t.Helper()
	recorder := perform(router, http.MethodGet, path, "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取 %s 失败：%d %s", path, recorder.Code, recorder.Body.String())
	}
	// 投稿列表与广场目录用两个不同的字段名（posts / inspirations），这里一并接住：
	// 断言的是"哪些标题可见"，不该因为接口信封的名字不同而各写一份辅助函数。
	var payload struct {
		Data struct {
			Posts        []creationPostPayload `json:"posts"`
			Inspirations []creationPostPayload `json:"inspirations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析 %s 响应失败：%v %s", path, err, recorder.Body.String())
	}
	entries := payload.Data.Posts
	if entries == nil {
		entries = payload.Data.Inspirations
	}
	titles := make([]string, 0, len(entries))
	for _, post := range entries {
		titles = append(titles, post.Title)
	}
	return titles
}

// TestHostedCreationPostPublishRequiresReview 覆盖投稿的完整生命周期：
// 提交后不进广场 → 管理员通过才上线，且封面由平台签发而不是存外链。
func TestHostedCreationPostPublishRequiresReview(t *testing.T) {
	t.Setenv("CANVAS_PUBLIC_BASE_URL", "https://example.com")
	extension, router, authDB, service := newCreationPostRouter(t)
	defer extension.Close()

	authorCookie, authorID := registerAccount(t, router, authDB, "post-author@example.com")
	resourceID := readyImageResource(t, service, authorID, "post.png")

	code, post := publishCreationPost(t, router, authorCookie,
		`{"resourceId":"`+resourceID+`","title":"霓虹夜巷","prompt":"夜晚的霓虹小巷，赛博朋克风格","mode":"image","category":"赛博朋克"}`)
	if code != http.StatusOK || post == nil {
		t.Fatalf("投稿应成功，实际 %d", code)
	}
	if post.Origin != string(model.CreationInspirationOriginUser) || post.ReviewStatus != string(model.CreationInspirationReviewPending) {
		t.Fatalf("投稿应落在待审状态：%#v", post)
	}
	if post.Status != string(model.CreationInspirationOffline) {
		t.Fatalf("未过审的投稿不能上架，实际 %q", post.Status)
	}
	if post.ResourceID != resourceID {
		t.Fatalf("投稿应记录产物引用，实际 %q", post.ResourceID)
	}
	// 封面必须是平台签发的地址：直接存下来会在过期后变成一片死图。
	parsedCover, err := url.Parse(post.CoverURL)
	if err != nil || !strings.HasPrefix(parsedCover.Path, "/api/public/resources/") || parsedCover.Query().Get("signature") == "" {
		t.Fatalf("投稿封面应是平台签发的地址，实际 %q", post.CoverURL)
	}
	if strings.TrimSpace(post.Author) == "" {
		t.Fatal("投稿应带上署名")
	}

	// 待审的投稿对任何人都不可见：作者自己也不能在广场上看到它。
	if titles := creationPostTitles(t, router, "/api/inspirations", authorCookie); len(titles) != 0 {
		t.Fatalf("待审投稿不应出现在广场：%#v", titles)
	}
	readerCookie, _ := registerAccount(t, router, authDB, "post-reader@example.com")
	if titles := creationPostTitles(t, router, "/api/inspirations", readerCookie); len(titles) != 0 {
		t.Fatalf("待审投稿不应出现在广场：%#v", titles)
	}

	// 审核队列里能看到它，且管理员通过后立刻上架。
	adminCookie, adminID := registerAccount(t, router, authDB, "post-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	if titles := creationPostTitles(t, router, "/api/admin/posts", adminCookie); strings.Join(titles, ",") != "霓虹夜巷" {
		t.Fatalf("审核队列应有这条投稿：%#v", titles)
	}
	recorder := perform(router, http.MethodPost, "/api/admin/posts/"+post.ID+"/approve", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("通过投稿应成功，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if titles := creationPostTitles(t, router, "/api/admin/posts", adminCookie); len(titles) != 0 {
		t.Fatalf("已审的投稿应离开队列：%#v", titles)
	}
	if titles := creationPostTitles(t, router, "/api/inspirations", readerCookie); strings.Join(titles, ",") != "霓虹夜巷" {
		t.Fatalf("过审投稿应对所有用户可见：%#v", titles)
	}
}

// TestHostedCreationPostBlocksBannedContent 覆盖关键词预筛：命中的投稿直接驳回、不占队列，
// 但仍然落库，让投稿人能看到结论与理由。
func TestHostedCreationPostBlocksBannedContent(t *testing.T) {
	extension, router, authDB, service := newCreationPostRouter(t)
	defer extension.Close()

	cookie, userID := registerAccount(t, router, authDB, "post-banned@example.com")
	resourceID := readyImageResource(t, service, userID, "banned.png")

	code, post := publishCreationPost(t, router, cookie,
		`{"resourceId":"`+resourceID+`","title":"裸聊直播","prompt":"proompt","mode":"image"}`)
	if code != http.StatusOK || post == nil {
		t.Fatalf("被预筛拦下的投稿也应受理并留下记录，实际 %d", code)
	}
	if post.ReviewStatus != string(model.CreationInspirationReviewRejected) {
		t.Fatalf("命中硬拦词应直接驳回，实际 %q", post.ReviewStatus)
	}
	if strings.TrimSpace(post.ReviewNote) == "" {
		t.Fatal("驳回必须回显理由，否则投稿人不知道该改什么")
	}
	if strings.Contains(post.ReviewNote, "裸聊") {
		t.Fatalf("驳回理由不能回显命中的词，否则等于把词表送出去：%q", post.ReviewNote)
	}
	if titles := creationPostTitles(t, router, "/api/me/posts", cookie); strings.Join(titles, ",") != "裸聊直播" {
		t.Fatalf("被驳回的投稿应出现在自己的列表里：%#v", titles)
	}

	adminCookie, adminID := registerAccount(t, router, authDB, "post-banned-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	if titles := creationPostTitles(t, router, "/api/admin/posts", adminCookie); len(titles) != 0 {
		t.Fatalf("被预筛驳回的投稿不该占用人工队列：%#v", titles)
	}
	if titles := creationPostTitles(t, router, "/api/inspirations", cookie); len(titles) != 0 {
		t.Fatalf("被驳回的投稿不能出现在广场：%#v", titles)
	}
}

// TestHostedCreationPostValidatesInput 覆盖投稿的准入：只能发自己的、就绪的产物。
func TestHostedCreationPostValidatesInput(t *testing.T) {
	extension, router, authDB, service := newCreationPostRouter(t)
	defer extension.Close()

	ownerCookie, ownerID := registerAccount(t, router, authDB, "post-owner@example.com")
	resourceID := readyImageResource(t, service, ownerID, "owned.png")

	otherCookie, _ := registerAccount(t, router, authDB, "post-other@example.com")
	code, _ := publishCreationPost(t, router, otherCookie,
		`{"resourceId":"`+resourceID+`","title":"别人的图","prompt":"x","mode":"image"}`)
	if code != http.StatusNotFound {
		t.Fatalf("发布他人产物应 404，实际 %d", code)
	}

	cases := []struct {
		name string
		body string
	}{
		{"缺少标题", `{"resourceId":"` + resourceID + `","prompt":"x","mode":"image"}`},
		{"缺少提示词", `{"resourceId":"` + resourceID + `","title":"标题","mode":"image"}`},
		{"模式与产物不符", `{"resourceId":"` + resourceID + `","title":"标题","prompt":"x","mode":"video"}`},
		{"产物不存在", `{"resourceId":"missing-resource","title":"标题","prompt":"x","mode":"image"}`},
	}
	for _, item := range cases {
		if code, _ := publishCreationPost(t, router, ownerCookie, item.body); code != http.StatusBadRequest && code != http.StatusNotFound {
			t.Fatalf("%s 应被拒绝，实际 %d", item.name, code)
		}
	}
}

// TestHostedCreationPostWithdrawAndReject 覆盖两个终态：运营驳回留理由、作者可撤回自己的投稿。
func TestHostedCreationPostWithdrawAndReject(t *testing.T) {
	extension, router, authDB, service := newCreationPostRouter(t)
	defer extension.Close()

	cookie, userID := registerAccount(t, router, authDB, "post-withdraw@example.com")
	resourceID := readyImageResource(t, service, userID, "withdraw.png")
	_, post := publishCreationPost(t, router, cookie,
		`{"resourceId":"`+resourceID+`","title":"待撤回","prompt":"x","mode":"image"}`)
	if post == nil {
		t.Fatal("投稿未成功")
	}

	adminCookie, adminID := registerAccount(t, router, authDB, "post-withdraw-admin@example.com")
	promoteToAdmin(t, authDB, adminID)
	recorder := perform(router, http.MethodPost, "/api/admin/posts/"+post.ID+"/reject", `{"note":"画面与描述不符"}`, adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("驳回投稿应成功，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	var rejected struct {
		Data struct {
			Post creationPostPayload `json:"post"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &rejected); err != nil {
		t.Fatalf("解析驳回响应失败：%v", err)
	}
	if rejected.Data.Post.ReviewStatus != string(model.CreationInspirationReviewRejected) ||
		rejected.Data.Post.ReviewNote != "画面与描述不符" {
		t.Fatalf("驳回结论与理由应回传：%#v", rejected.Data.Post)
	}

	// 撤回只能动自己的：换个账号删同一条必须是 not found。
	strangerCookie, _ := registerAccount(t, router, authDB, "post-stranger@example.com")
	if recorder := perform(router, http.MethodDelete, "/api/me/posts/"+post.ID, "", strangerCookie); recorder.Code != http.StatusNotFound {
		t.Fatalf("删除他人投稿应 404，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodDelete, "/api/me/posts/"+post.ID, "", cookie); recorder.Code != http.StatusOK {
		t.Fatalf("撤回自己的投稿应成功，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if titles := creationPostTitles(t, router, "/api/me/posts", cookie); len(titles) != 0 {
		t.Fatalf("撤回后自己的列表应为空：%#v", titles)
	}
}

// TestHostedCreationPostDailyQuota 覆盖投稿配额：没有它，一个人就能把广场刷成自己的作品集。
func TestHostedCreationPostDailyQuota(t *testing.T) {
	extension, router, authDB, service := newCreationPostRouter(t)
	defer extension.Close()

	cookie, userID := registerAccount(t, router, authDB, "post-quota@example.com")
	resourceID := readyImageResource(t, service, userID, "quota.png")

	for index := 0; index < 5; index++ {
		code, _ := publishCreationPost(t, router, cookie,
			`{"resourceId":"`+resourceID+`","title":"第 `+strconv.Itoa(index+1)+` 条","prompt":"x","mode":"image"}`)
		if code != http.StatusOK {
			t.Fatalf("第 %d 条投稿应成功，实际 %d", index+1, code)
		}
	}
	if code, _ := publishCreationPost(t, router, cookie,
		`{"resourceId":"`+resourceID+`","title":"第六条","prompt":"x","mode":"image"}`); code != http.StatusBadRequest {
		t.Fatalf("超出配额应被拒绝，实际 %d", code)
	}
}

// TestHostedCreationPostRequiresLogin 确认投稿不是匿名可用的写入口。
func TestHostedCreationPostRequiresLogin(t *testing.T) {
	extension, router, _, _ := newCreationPostRouter(t)
	defer extension.Close()

	if recorder := perform(router, http.MethodPost, "/api/posts", `{"title":"x"}`, nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录投稿应 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodGet, "/api/me/posts", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录读取自己的投稿应 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	if recorder := perform(router, http.MethodGet, "/api/admin/posts", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未登录读取审核队列应 401，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
}

// TestHostedCreationPostLegacyRowStaysVisible 覆盖迁移前的历史灵感：
// 这些行的 origin 是空串而不是 'PLATFORM'，只判 NULL 会让它们在前台凭空消失。
func TestHostedCreationPostLegacyRowStaysVisible(t *testing.T) {
	extension, authDB, canvasDB, service := newTestExtension(t)
	defer extension.Close()
	if err := canvasDB.AutoMigrate(&model.CreationInspiration{}); err != nil {
		t.Fatalf("建灵感表失败：%v", err)
	}
	legacy := model.CreationInspiration{
		ID:     "INSP_LEGACY",
		Title:  "老灵感",
		Mode:   "image",
		Status: model.CreationInspirationOnline,
	}
	if err := canvasDB.Create(&legacy).Error; err != nil {
		t.Fatalf("写入历史灵感失败：%v", err)
	}

	router := newTestRouter(extension, service)
	cookie, _ := registerAccount(t, router, authDB, "post-legacy@example.com")
	if titles := creationPostTitles(t, router, "/api/inspirations", cookie); strings.Join(titles, ",") != "老灵感" {
		t.Fatalf("历史灵感应仍然可见：%#v", titles)
	}
}
