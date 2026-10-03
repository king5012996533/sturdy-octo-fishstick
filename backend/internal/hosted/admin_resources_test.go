package hosted

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"infinite-canvas/backend/internal/bootstrap"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// newResourceAdminRouter 复用标准托管路由（含管理员守卫）。
//
// 产物路由由 registerAdminRoutes 在生产装配里挂载，这里不再重复注册：同一方法+路径
// 注册两次 gin 会直接 panic。
func newResourceAdminRouter(t *testing.T) (bootstrap.HostedExtension, *gin.Engine, *gorm.DB, *gorm.DB) {
	t.Helper()
	extension, authDB, canvasDB, service := newTestExtension(t)
	if err := canvasDB.AutoMigrate(&model.Resource{}); err != nil {
		t.Fatalf("初始化产物表失败: %v", err)
	}
	return extension, newTestRouter(extension, service), authDB, canvasDB
}

type adminResourceListBody struct {
	Data struct {
		Resources []struct {
			ID                string `json:"id"`
			Kind              string `json:"kind"`
			UserName          string `json:"userName"`
			Referenced        bool   `json:"referenced"`
			PreviewURL        string `json:"previewUrl"`
			TaskID            string `json:"taskId"`
			TaskType          string `json:"taskType"`
			TaskStatus        string `json:"taskStatus"`
			TaskModel         string `json:"taskModel"`
			ProviderRequestID string `json:"providerRequestId"`
			ChargedCredits    int64  `json:"chargedCredits"`
			ChargeState       string `json:"chargeState"`
		} `json:"resources"`
		Total    int64 `json:"total"`
		Page     int   `json:"page"`
		PageSize int   `json:"pageSize"`
		Totals   struct {
			Total        int64 `json:"total"`
			Unreferenced int64 `json:"unreferenced"`
			Untracked    int64 `json:"untracked"`
			TotalBytes   int64 `json:"totalBytes"`
			Users        int64 `json:"users"`
		} `json:"totals"`
		Reconciliation struct {
			BillingStart           string `json:"billingStart"`
			Untracked              int64  `json:"untracked"`
			Uncharged              int64  `json:"uncharged"`
			ChargedWithoutResource int64  `json:"chargedWithoutResource"`
			UnchargedResources     []struct {
				ID          string `json:"id"`
				ChargeState string `json:"chargeState"`
			} `json:"unchargedResources"`
			ChargedTasks []struct {
				TaskID     string `json:"taskId"`
				UserName   string `json:"userName"`
				Net        int64  `json:"net"`
				TaskStatus string `json:"taskStatus"`
			} `json:"chargedTasks"`
		} `json:"reconciliation"`
	} `json:"data"`
}

func TestHostedAdminResourceListRequiresAdminAndReportsUnreferenced(t *testing.T) {
	// 预览地址要现场签名，没有公网基址就签不出来；这一条用例要同时验证预览这一层。
	t.Setenv("CANVAS_PUBLIC_BASE_URL", "https://example.com")
	extension, router, authDB, canvasDB := newResourceAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "admin-resources@example.com")
	promoteToAdmin(t, authDB, adminID)
	_, ownerID := registerAccount(t, router, authDB, "resource-owner@example.com")

	now := time.Now().UTC()
	if err := canvasDB.Create(&model.Workspace{ID: ownerID, Name: "产物作者", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("写入工作区失败: %v", err)
	}
	referencedID := "resaaa0000000000000000000000001"
	strandedID := "resbbb0000000000000000000000002"
	if err := canvasDB.Create(&[]model.Resource{
		{ID: referencedID, UserID: ownerID, Kind: "image", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "users/x/image/one.png", MimeType: "image/png", Size: 2048, CreatedAt: now, UpdatedAt: now},
		// 没人引用：上游可能已产出，但用户端没拿到，正是对账要捞的那条。
		{ID: strandedID, UserID: ownerID, Kind: "video", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "users/x/video/two.mp4", MimeType: "video/mp4", Size: 4096, CreatedAt: now.Add(time.Second), UpdatedAt: now},
	}).Error; err != nil {
		t.Fatalf("写入产物失败: %v", err)
	}
	if err := canvasDB.Create(&model.Asset{
		ID: "asset-ref", UserID: ownerID, Kind: "image", Category: model.AssetCategoryMaterial,
		Status: model.AssetVersionStatusConfirmed, Title: "引用产物",
		PayloadJSON: `{"resourceId":"` + referencedID + `"}`, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("写入素材失败: %v", err)
	}

	if recorder := perform(router, http.MethodGet, "/api/admin/resources", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("匿名访问产物列表 = %d; want 401", recorder.Code)
	}

	recorder := perform(router, http.MethodGet, "/api/admin/resources", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("管理员访问产物列表 = %d; want 200，响应 %s", recorder.Code, recorder.Body.String())
	}
	var body adminResourceListBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.Data.Total != 2 || len(body.Data.Resources) != 2 {
		t.Fatalf("产物列表 = total %d rows %d; want 2 / 2", body.Data.Total, len(body.Data.Resources))
	}
	if body.Data.Totals.Total != 2 || body.Data.Totals.Unreferenced != 1 || body.Data.Totals.TotalBytes != 6144 || body.Data.Totals.Users != 1 {
		t.Fatalf("顶部读数异常：%#v", body.Data.Totals)
	}
	byID := map[string]bool{}
	previews := map[string]string{}
	for _, row := range body.Data.Resources {
		byID[row.ID] = row.Referenced
		previews[row.ID] = row.PreviewURL
	}
	if !byID[referencedID] || byID[strandedID] {
		t.Fatalf("引用判定异常：%#v", byID)
	}
	if previews[referencedID] == "" || previews[strandedID] == "" {
		t.Fatalf("ready 的本地产物都应带签名预览地址：%#v", previews)
	}

	// 「只看用户没拿到」必须能筛出那一条。
	recorder = perform(router, http.MethodGet, "/api/admin/resources?unreferenced=true", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("未引用筛选 = %d; want 200", recorder.Code)
	}
	var filtered adminResourceListBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("解析筛选响应失败: %v", err)
	}
	if filtered.Data.Total != 1 || len(filtered.Data.Resources) != 1 || filtered.Data.Resources[0].ID != strandedID {
		t.Fatalf("未引用筛选结果异常：%#v", filtered.Data.Resources)
	}
	// 顶部读数不随筛选变化，否则看不出整体规模。
	if filtered.Data.Totals.Total != 2 || filtered.Data.Totals.Unreferenced != 1 {
		t.Fatalf("筛选后顶部读数不该跟着变：%#v", filtered.Data.Totals)
	}

	// 非法时间格式要当场拒绝，而不是悄悄按无筛选返回一堆数据。
	if recorder := perform(router, http.MethodGet, "/api/admin/resources?since=昨天", "", adminCookie); recorder.Code != http.StatusBadRequest {
		t.Fatalf("非法 since = %d; want 400", recorder.Code)
	}
}

func TestHostedAdminResourceReconciliationJoinsTaskAndCharge(t *testing.T) {
	t.Setenv("CANVAS_PUBLIC_BASE_URL", "https://example.com")
	extension, router, authDB, canvasDB := newResourceAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "admin-recon@example.com")
	promoteToAdmin(t, authDB, adminID)
	_, ownerID := registerAccount(t, router, authDB, "recon-owner@example.com")

	// 先落一笔扣费，它的时间就是"计费上线"的分界：再往前走的历史产物不该被算成漏单。
	credits := extension.(*Extension).service
	if _, err := credits.AdjustCredits(ownerID, 1000, "对账用例充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}
	if _, _, err := credits.ChargeTaskCredits(ownerID, "task-charged", 45, "gpt-image-2 45分/张 × 1"); err != nil {
		t.Fatalf("写入扣费失败: %v", err)
	}
	if _, _, err := credits.ChargeTaskCredits(ownerID, "task-orphan", 45, "gpt-image-2 45分/张 × 1"); err != nil {
		t.Fatalf("写入扣费失败: %v", err)
	}
	// 文本任务也扣费，但它的结果是一段文字，不该出现在"扣费无产物"里。
	if _, _, err := credits.ChargeTaskCredits(ownerID, "task-text", 1, "文本任务"); err != nil {
		t.Fatalf("写入扣费失败: %v", err)
	}
	billingStart := time.Now()
	created := billingStart.Add(2 * time.Second)

	if err := canvasDB.Create(&model.Workspace{ID: ownerID, Name: "对账作者", CreatedAt: created, UpdatedAt: created}).Error; err != nil {
		t.Fatalf("写入工作区失败: %v", err)
	}
	if err := canvasDB.Create(&[]model.Task{
		{ID: "task-charged", UserID: ownerID, Type: "canvas_image", Status: model.TaskStatusSucceeded, Model: "gpt-image-2", ProviderRequestID: "req-1", CreatedAt: created, CompletedAt: &created},
		{ID: "task-uncharged", UserID: ownerID, Type: "canvas_image", Status: model.TaskStatusSucceeded, Model: "gpt-image-2", CreatedAt: created, CompletedAt: &created},
		{ID: "task-old", UserID: ownerID, Type: "canvas_image", Status: model.TaskStatusSucceeded, CreatedAt: billingStart.Add(-2 * time.Hour)},
		{ID: "task-orphan", UserID: ownerID, Type: "canvas_image", Status: model.TaskStatusFailed, Error: "生成失败", CreatedAt: created},
		{ID: "task-text", UserID: ownerID, Type: "canvas_text", Status: model.TaskStatusSucceeded, CreatedAt: created},
	}).Error; err != nil {
		t.Fatalf("写入任务失败: %v", err)
	}
	if err := canvasDB.Create(&[]model.Resource{
		{ID: "res-charged", UserID: ownerID, Kind: "image", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "users/x/image/a.png", MimeType: "image/png", Size: 10, TaskID: "task-charged", Source: "generation", CreatedAt: created},
		// 计费上线后创建却没有任何流水：这就是平台的漏单。
		{ID: "res-uncharged", UserID: ownerID, Kind: "image", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "users/x/image/b.png", MimeType: "image/png", Size: 10, TaskID: "task-uncharged", Source: "generation", CreatedAt: created},
		// 计费上线前跑出来的：没扣费是当时的正常状态，不该报警。
		{ID: "res-prebilling", UserID: ownerID, Kind: "image", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "users/x/image/c.png", MimeType: "image/png", Size: 10, TaskID: "task-old", Source: "generation", CreatedAt: billingStart.Add(-2 * time.Hour)},
		// 用户上传：本来就不该有任务。
		{ID: "res-upload", UserID: ownerID, Kind: "image", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "users/x/image/d.png", MimeType: "image/png", Size: 10, Source: "upload", CreatedAt: created},
	}).Error; err != nil {
		t.Fatalf("写入产物失败: %v", err)
	}

	recorder := perform(router, http.MethodGet, "/api/admin/resources?pageSize=50", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取对账列表 = %d; want 200，响应 %s", recorder.Code, recorder.Body.String())
	}
	var body adminResourceListBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	rows := map[string]struct {
		state   string
		credits int64
		model   string
		reqID   string
	}{}
	ownerNames := map[string]string{}
	for _, row := range body.Data.Resources {
		rows[row.ID] = struct {
			state   string
			credits int64
			model   string
			reqID   string
		}{row.ChargeState, row.ChargedCredits, row.TaskModel, row.ProviderRequestID}
		ownerNames[row.ID] = row.UserName
	}
	if ownerNames["res-charged"] == "" {
		t.Fatalf("产物行缺少账号昵称：%#v", ownerNames)
	}
	if got := rows["res-charged"]; got.state != "charged" || got.credits != 45 || got.model != "gpt-image-2" || got.reqID != "req-1" {
		t.Fatalf("已扣费产物 = %#v; want charged/45/gpt-image-2/req-1", got)
	}
	if got := rows["res-uncharged"]; got.state != "uncharged" || got.credits != 0 {
		t.Fatalf("漏单产物 = %#v; want uncharged/0", got)
	}
	if got := rows["res-prebilling"]; got.state != "prebilling" {
		t.Fatalf("计费前产物 = %#v; want prebilling", got)
	}
	if got := rows["res-upload"]; got.state != "untracked" {
		t.Fatalf("上传产物 = %#v; want untracked", got)
	}
	if body.Data.Totals.Untracked != 1 {
		t.Fatalf("未关联任务读数 = %d; want 1", body.Data.Totals.Untracked)
	}

	reconciliation := body.Data.Reconciliation
	if reconciliation.BillingStart == "" {
		t.Fatal("有扣费记录时应当给出计费起始时间")
	}
	if reconciliation.Untracked != 1 || reconciliation.Uncharged != 1 {
		t.Fatalf("对账读数 = untracked %d / uncharged %d; want 1 / 1", reconciliation.Untracked, reconciliation.Uncharged)
	}
	if len(reconciliation.UnchargedResources) != 1 || reconciliation.UnchargedResources[0].ID != "res-uncharged" {
		t.Fatalf("漏单列表 = %#v; want 只含 res-uncharged", reconciliation.UnchargedResources)
	}
	if reconciliation.ChargedWithoutResource != 1 {
		t.Fatalf("扣费无产物读数 = %d; want 1（文本任务不算）", reconciliation.ChargedWithoutResource)
	}
	if len(reconciliation.ChargedTasks) != 1 || reconciliation.ChargedTasks[0].TaskID != "task-orphan" {
		t.Fatalf("扣费无产物列表 = %#v; want 只含 task-orphan", reconciliation.ChargedTasks)
	}
	// 昵称以账号库为准（画布库的 workspaces.name 只是延迟副本），所以跟产物行对齐即可。
	if reconciliation.ChargedTasks[0].UserName != ownerNames["res-charged"] {
		t.Fatalf("扣费无产物列表昵称 = %q; want %q", reconciliation.ChargedTasks[0].UserName, ownerNames["res-charged"])
	}

	// 只看没关联任务的产物：上传的那条。
	recorder = perform(router, http.MethodGet, "/api/admin/resources?untracked=true", "", adminCookie)
	var filtered adminResourceListBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("解析筛选响应失败: %v", err)
	}
	if filtered.Data.Total != 1 || filtered.Data.Resources[0].ID != "res-upload" {
		t.Fatalf("未关联任务筛选结果异常：%#v", filtered.Data.Resources)
	}
}

func TestHostedAdminResourceBackfillIsAdminOnly(t *testing.T) {
	extension, router, authDB, canvasDB := newResourceAdminRouter(t)
	defer extension.Close()

	adminCookie, adminID := registerAccount(t, router, authDB, "admin-backfill@example.com")
	promoteToAdmin(t, authDB, adminID)
	userCookie, ownerID := registerAccount(t, router, authDB, "backfill-owner@example.com")

	now := time.Now().UTC()
	if err := canvasDB.Create(&model.Resource{
		ID: "res-backfill", UserID: ownerID, Kind: "image", Status: model.ResourceStatusReady,
		Provider: "local", ObjectKey: "users/x/image/backfill.png", MimeType: "image/png", Size: 10, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("写入产物失败: %v", err)
	}
	if err := canvasDB.Create(&model.Task{
		ID: "task-backfill", UserID: ownerID, Type: "canvas_image", Status: model.TaskStatusSucceeded,
		CreatedAt: now, ResultJSON: `{"images":[{"resourceId":"res-backfill"}]}`,
	}).Error; err != nil {
		t.Fatalf("写入任务失败: %v", err)
	}

	if recorder := perform(router, http.MethodPost, "/api/admin/resources/backfill", "", userCookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("普通账号回填 = %d; want 403", recorder.Code)
	}

	recorder := perform(router, http.MethodPost, "/api/admin/resources/backfill?dryRun=true", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("演练回填 = %d; want 200，响应 %s", recorder.Code, recorder.Body.String())
	}
	var dryRunBody struct {
		Data struct {
			Result struct {
				Linked  int  `json:"linked"`
				Applied bool `json:"applied"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &dryRunBody); err != nil {
		t.Fatalf("解析演练结果失败: %v", err)
	}
	if dryRunBody.Data.Result.Linked != 1 || dryRunBody.Data.Result.Applied {
		t.Fatalf("演练结果 = %#v; want linked 1 / 不写库", dryRunBody.Data.Result)
	}
	var untouched model.Resource
	if err := canvasDB.Where("id = ?", "res-backfill").First(&untouched).Error; err != nil {
		t.Fatal(err)
	}
	if untouched.TaskID != "" {
		t.Fatalf("演练不该写库：%#v", untouched)
	}

	recorder = perform(router, http.MethodPost, "/api/admin/resources/backfill", "", adminCookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("正式回填 = %d; want 200，响应 %s", recorder.Code, recorder.Body.String())
	}
	var linked model.Resource
	if err := canvasDB.Where("id = ?", "res-backfill").First(&linked).Error; err != nil {
		t.Fatal(err)
	}
	if linked.TaskID != "task-backfill" || linked.Source != "generation" {
		t.Fatalf("回填后溯源 = %q/%q; want task-backfill/generation", linked.TaskID, linked.Source)
	}
}
