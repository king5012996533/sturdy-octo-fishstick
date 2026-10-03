package hosted

import (
	"strings"
	"time"

	"infinite-canvas/backend/internal/app"
)

// 产物对账：把画布库里的产物、任务与账号库里的扣费拼成一份能直接看的账。
//
// 为什么放在托管层：扣费流水在账号库，产物与任务在画布库，只有同时持有两套库的这一层
// 才拼得出来。纯本地形态没有账号库，也就没有这份视图——它不会假装自己对账对过。

// 对账列表各自的返回上限。数字取小：它们的用途是"点开看一眼确认存在"，真正的全量
// 排查应该按条件重新查，而不是让一次页面加载拖着几百条记录。
const (
	hostedReconciliationChargeScanLimit = 300
	hostedReconciliationResourceLimit   = 200
	hostedReconciliationListLimit       = 50
)

// resourceKindProducesMedia 判断一个任务类型是否应该产出媒体产物。
//
// 文本任务也扣费，但它的结果就是一段文字，永远不会有 resources 记录。不按类型过滤，
// 反向对账列表会被文本任务刷屏，真正的漏单反而看不见。
func resourceKindProducesMedia(taskType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(taskType))
	switch normalized {
	case "canvas_image", "canvas_video", "canvas_audio", "image", "video", "audio":
		return true
	default:
		return false
	}
}

// enrichResourceReconciliation 补齐页面的扣费列与顶部异常读数。
//
// 任何一步取不到数据（账号库抖动、没有计费记录）都只降低这一块的完整度，不让整页
// 列表返 500：产物本身在画布库里，那才是管理员此刻来看的东西。
func (e *Extension) enrichResourceReconciliation(view *app.AdminResourcePageView) {
	if e == nil || e.service == nil || view == nil {
		return
	}
	billingStart, hasBilling, err := e.service.AdminTaskChargeBillingStart()
	if err != nil {
		return
	}
	reconciliation := &app.AdminResourceReconciliationView{
		Untracked:          view.Totals.Untracked,
		UnchargedResources: []app.AdminResourceView{},
		ChargedTasks:       []app.AdminTaskChargeView{},
	}
	if hasBilling {
		reconciliation.BillingStart = billingStart.UTC().Format(time.RFC3339)
	}

	e.fillResourceChargeStates(view.Resources, billingStart, hasBilling)
	e.fillUnchargedResources(reconciliation, billingStart, hasBilling)
	e.fillChargedWithoutResource(reconciliation)
	view.Reconciliation = reconciliation
}

// fillResourceChargeStates 给当前页每一行标出扣费状态。
func (e *Extension) fillResourceChargeStates(resources []app.AdminResourceView, billingStart time.Time, hasBilling bool) {
	taskIDs := make([]string, 0, len(resources))
	for _, resource := range resources {
		if strings.TrimSpace(resource.TaskID) != "" {
			taskIDs = append(taskIDs, resource.TaskID)
		}
	}
	if len(taskIDs) == 0 {
		return
	}
	tasks, err := e.canvas.AdminTasksByIDs(taskIDs)
	if err != nil {
		tasks = nil
	}
	totals, err := e.service.AdminTaskChargeTotals(taskIDs)
	if err != nil {
		totals = nil
	}
	for index := range resources {
		taskID := strings.TrimSpace(resources[index].TaskID)
		if taskID == "" {
			resources[index].ChargeState = app.ResourceChargeStateUntracked
			continue
		}
		if task, ok := tasks[taskID]; ok {
			resources[index].TaskType = task.Type
			resources[index].TaskStatus = string(task.Status)
			resources[index].TaskModel = task.Model
			resources[index].ProviderRequestID = task.ProviderRequestID
		}
		resources[index].ChargedCredits = totals[taskID]
		resources[index].ChargeState = resourceChargeState(resources[index].ChargedCredits, resources[index].CreatedAt, billingStart, hasBilling)
	}
}

// fillUnchargedResources 列出计费上线后创建、却没有任何扣费的产物。
func (e *Extension) fillUnchargedResources(reconciliation *app.AdminResourceReconciliationView, billingStart time.Time, hasBilling bool) {
	if !hasBilling {
		// 没有计费起始时间说明账号库里一条扣费都没有，此时"没扣费"不是异常，
		// 而是整个实例还没开始计费。
		return
	}
	candidates, err := e.canvas.AdminResourceChargeCandidates(billingStart, hostedReconciliationResourceLimit)
	if err != nil {
		return
	}
	taskIDs := make([]string, 0, len(candidates))
	for _, resource := range candidates {
		taskIDs = append(taskIDs, resource.TaskID)
	}
	totals, err := e.service.AdminTaskChargeTotals(taskIDs)
	if err != nil {
		return
	}
	uncharged := make([]app.AdminResourceView, 0, len(candidates))
	for _, resource := range candidates {
		if resource.ChargedCredits != 0 || totals[resource.TaskID] != 0 {
			continue
		}
		resource.ChargeState = app.ResourceChargeStateUncharged
		uncharged = append(uncharged, resource)
	}
	reconciliation.Uncharged = int64(len(uncharged))
	if len(uncharged) > hostedReconciliationListLimit {
		uncharged = uncharged[:hostedReconciliationListLimit]
	}
	reconciliation.UnchargedResources = uncharged
}

// fillChargedWithoutResource 列出扣了费、任务也跑了，却一条产物都没有的媒体任务。
func (e *Extension) fillChargedWithoutResource(reconciliation *app.AdminResourceReconciliationView) {
	charges, err := e.service.AdminRecentTaskCharges(hostedReconciliationChargeScanLimit)
	if err != nil {
		return
	}
	merged := make(map[string]int64, len(charges))
	taskIDs := make([]string, 0, len(charges))
	for _, charge := range charges {
		if charge.Net <= 0 {
			continue
		}
		if _, seen := merged[charge.TaskID]; !seen {
			merged[charge.TaskID] = charge.Net
			taskIDs = append(taskIDs, charge.TaskID)
		}
	}
	if len(taskIDs) == 0 {
		return
	}
	tasks, err := e.canvas.AdminTasksByIDs(taskIDs)
	if err != nil {
		return
	}
	counts, err := e.canvas.ResourceCountsByTaskIDs(taskIDs)
	if err != nil {
		return
	}
	rows := make([]app.AdminTaskChargeView, 0, len(taskIDs))
	for _, charge := range charges {
		if charge.Net <= 0 {
			continue
		}
		task, ok := tasks[charge.TaskID]
		if !ok || !resourceKindProducesMedia(task.Type) || counts[charge.TaskID] > 0 {
			continue
		}
		rows = append(rows, app.AdminTaskChargeView{
			TaskID: charge.TaskID, UserID: charge.UserID, Net: charge.Net, ChargedAt: charge.ChargedAt,
			TaskType: task.Type, TaskStatus: string(task.Status), TaskError: task.Error,
		})
	}
	reconciliation.ChargedWithoutResource = int64(len(rows))
	if len(rows) > hostedReconciliationListLimit {
		rows = rows[:hostedReconciliationListLimit]
	}
	reconciliation.ChargedTasks = rows
	e.mergeTaskChargeOwnerNames(reconciliation.ChargedTasks)
}

// mergeTaskChargeOwnerNames 把账号昵称补到反向对账列表上：只有账号 ID 时，运营还得
// 自己去账号管理里搜一遍才知道该找谁。
func (e *Extension) mergeTaskChargeOwnerNames(rows []app.AdminTaskChargeView) {
	if len(rows) == 0 || e.service == nil {
		return
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.UserID)
	}
	owners, err := e.service.AdminUsersByIDs(ids)
	if err != nil {
		return
	}
	for index := range rows {
		if name := owners[rows[index].UserID].Name; name != "" {
			rows[index].UserName = name
		}
	}
}

// resourceChargeState 判定一条产物该标成哪种扣费状态。
func resourceChargeState(charged int64, createdAt time.Time, billingStart time.Time, hasBilling bool) string {
	if !hasBilling {
		// 账号库里没有任何扣费记录：这时说"漏扣"没有依据，只能说计费尚未开始。
		return app.ResourceChargeStatePrebilling
	}
	if charged != 0 {
		return app.ResourceChargeStateCharged
	}
	if createdAt.Before(billingStart) {
		return app.ResourceChargeStatePrebilling
	}
	return app.ResourceChargeStateUncharged
}
