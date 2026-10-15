package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

// 渠道级兜底覆盖三条互斥的结论：
//  1. 上游确定没建任务 → 换成同名的另一条平台渠道重发；
//  2. 结果不明（5xx、超时）→ 一条都不换，重复计费的风险比失败本身更大；
//  3. 换不了（没有同名渠道、备用渠道能力或价格对不上）→ 保留原始失败。
//
// 用例都从"任务快照 + 失败尝试"这两个真实输入出发，而不是直接调内部小函数：兜底的判据全是
// 从这两份数据推出来的，绕过它们就会把测试写成对实现的重述。

type channelFallbackLedger struct {
	prices map[string]int64
	quotes []string
}

func (l *channelFallbackLedger) quote(modelKey string) (TaskChargeOutcome, error) {
	l.quotes = append(l.quotes, modelKey)
	credits, ok := l.prices[modelKey]
	if !ok {
		return TaskChargeOutcome{}, errors.New("该渠道模型尚未定价")
	}
	return TaskChargeOutcome{Credits: credits, Priced: true, Quantity: 1}, nil
}

func (l *channelFallbackLedger) ChargeTask(request TaskChargeRequest) (TaskChargeOutcome, error) {
	return l.quote(request.ModelKey)
}

func (l *channelFallbackLedger) QuoteTask(request TaskChargeRequest) (TaskChargeOutcome, error) {
	return l.quote(request.ModelKey)
}

func (l *channelFallbackLedger) RefundTask(string, string, string) (int64, bool, error) {
	return 0, false, nil
}

func (l *channelFallbackLedger) SettleTextTask(TaskTextSettleRequest) (TaskTextSettleOutcome, error) {
	return TaskTextSettleOutcome{}, nil
}

func (l *channelFallbackLedger) EnsureTextTaskBalance(string, string) error { return nil }

type channelFallbackFixture struct {
	service        *Service
	db             *gorm.DB
	ledger         *channelFallbackLedger
	task           *model.Task
	primaryModel   model.ChannelModel
	siblingModel   model.ChannelModel
	primaryVariant model.ChannelModelVariant
	siblingVariant model.ChannelModelVariant
	attempt        *model.RouteAttempt
}

const (
	falChannelID = "CHANNEL_FAL"
	repChannelID = "CHANNEL_REPLICATE"
)

// newChannelFallbackFixture 造一对真实的同名渠道模型：fal 排在前面（渠道顺序 0），
// Replicate 是它的备用。两边同显示名、同能力、同价，正是前台目录会并成一行的样子。
func newChannelFallbackFixture(t *testing.T) *channelFallbackFixture {
	t.Helper()
	service, db := newFeatureAvailabilityTestService(t)
	if err := db.AutoMigrate(&model.Task{}, &model.RouteAttempt{}, &model.TaskLog{}, &model.ModelChannel{}, &model.ChannelModel{}, &model.ChannelModelVariant{}, &model.IDSequence{}); err != nil {
		t.Fatal(err)
	}
	primary := "openai/gpt-image-2.5/sunburst"
	sibling := "openai/gpt-image-2.5-sunburst"
	capability := imageCapabilityForFallbackTest(t)
	for _, row := range []any{
		&model.ModelChannel{ID: falChannelID, Name: "fal.ai · 主账号", Scope: model.ChannelScopeSystem, Enabled: true, SortOrder: 0, APIFormat: "openai"},
		&model.ModelChannel{ID: repChannelID, Name: "Replicate · 主账号", Scope: model.ChannelScopeSystem, Enabled: true, SortOrder: 1, APIFormat: "openai"},
		&model.ChannelModel{ID: "MODEL_FAL", ChannelID: falChannelID, ModelKey: primary, ProviderModelKey: primary, DisplayName: "GPT Image 2.5 Sunburst", Capability: "image", Protocol: model.ChannelInterfaceType("fal-image"), CapabilityConfigJSON: capability, Enabled: true},
		&model.ChannelModel{ID: "MODEL_REP", ChannelID: repChannelID, ModelKey: sibling, ProviderModelKey: sibling, DisplayName: "GPT Image 2.5 Sunburst", Capability: "image", Protocol: model.ChannelInterfaceReplicatePredictionImage, CapabilityConfigJSON: capability, Enabled: true},
		&model.ChannelModelVariant{ID: "VARIANT_FAL", ChannelModelID: "MODEL_FAL", SelectorKey: "{}", SelectorJSON: "{}", Resolution: "*", ProviderModelKey: primary, Enabled: true},
		&model.ChannelModelVariant{ID: "VARIANT_REP", ChannelModelID: "MODEL_REP", SelectorKey: "{}", SelectorJSON: "{}", Resolution: "*", ProviderModelKey: sibling, Enabled: true},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	input := map[string]any{
		"mode":              "image",
		"prompt":            "把背景换成浅蓝色",
		"capabilityOptions": map[string]any{"count": "1", "quality": "low", "size": "1:1"},
		"config": map[string]any{
			"channelId": falChannelID, "channelModelKey": primary, "model": primary,
			"providerModelKey": primary, "variantId": "VARIANT_FAL",
			"interfaceType": "fal-image", "apiFormat": "openai", "apiKey": "system",
			"size": "1:1", "quality": "low", "count": "1",
		},
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	task := &model.Task{ID: "task-fallback", UserID: "user-1", Type: "canvas_image", Operation: "text_to_image", Status: model.TaskStatusRunning, Stage: "调用生成模型", InputJSON: string(encoded), ChannelModelID: ""}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	attempt := &model.RouteAttempt{ID: "ATTEMPT_1", TaskID: task.ID, RouteRun: 0, AttemptNumber: 1, ChannelModelID: "MODEL_FAL", ChannelID: falChannelID, Status: "failed", DispatchState: "rejected_no_job", FailureCode: "upstream_402"}
	if err := db.Create(attempt).Error; err != nil {
		t.Fatal(err)
	}
	ledger := &channelFallbackLedger{prices: map[string]int64{falChannelID + "::" + primary: 45, repChannelID + "::" + sibling: 45}}
	service.UseTaskCreditLedger(ledger)
	return &channelFallbackFixture{service: service, db: db, ledger: ledger, task: task, attempt: attempt}
}

// imageCapabilityForFallbackTest 用与线上 gpt-image-2 两条渠道一致的图片能力合同：
// 同样的比例取值、同样的三档质量。能力合同不等的两条渠道不该互相兜底。
func imageCapabilityForFallbackTest(t *testing.T) string {
	t.Helper()
	config := DefaultImageCapabilityConfig("fal-image", "openai/gpt-image-2.5/sunburst")
	config.Size = ImageSizeConfig{Parameter: "aspect_ratio", Values: []string{"1:1", "16:9", "9:16", "3:2", "2:3", "4:3", "3:4"}, Default: "1:1"}
	config.Quality = ImageQualityConfig{Supported: true, Values: []string{"low", "medium", "high"}, Default: "low"}
	config.References.MaxImages = 4
	config.TransparentBackground = VideoBooleanConfig{Supported: false, Default: false}
	config.MaxOutputs = 10
	return mustEncodeModelCapabilityConfig(t, &ModelCapabilityConfig{Version: 1, Image: config})
}

func (f *channelFallbackFixture) next(t *testing.T) *model.RouteAttempt {
	t.Helper()
	next, err := f.service.nextChannelAttemptAfterFailure(f.task, f.attempt)
	if err != nil {
		t.Fatalf("换渠道兜底报错：%v", err)
	}
	return next
}

func (f *channelFallbackFixture) reloadTask(t *testing.T) model.Task {
	t.Helper()
	var latest model.Task
	if err := f.db.First(&latest, "id = ?", f.task.ID).Error; err != nil {
		t.Fatal(err)
	}
	return latest
}

func (f *channelFallbackFixture) configOf(t *testing.T, raw string) map[string]any {
	t.Helper()
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	config, _ := input["config"].(map[string]any)
	return config
}

// 上游明确拒单（这里是最常见的余额/额度类 402）时，同名渠道里换一条重发，
// 并且把任务的落点与输入快照一起改写：新的尝试必须打到备用渠道，而不是又发回原渠道。
func TestChannelFallbackSwitchesToDisplayNameSibling(t *testing.T) {
	f := newChannelFallbackFixture(t)
	next := f.next(t)
	if next == nil {
		t.Fatal("同名渠道可用时必须换渠道重发")
	}
	if next.ChannelModelID != "MODEL_REP" || next.ChannelID != repChannelID {
		t.Fatalf("新尝试落点 = %s/%s，期望 MODEL_REP/%s", next.ChannelModelID, next.ChannelID, repChannelID)
	}
	if next.DispatchState != "not_sent" || next.AttemptNumber != 2 {
		t.Fatalf("新尝试状态 = %s #%d，期望 not_sent #2", next.DispatchState, next.AttemptNumber)
	}
	latest := f.reloadTask(t)
	if latest.ChannelModelID != "MODEL_REP" {
		t.Fatalf("任务落点 = %q，期望 MODEL_REP", latest.ChannelModelID)
	}
	config := f.configOf(t, latest.InputJSON)
	if config["channelId"] != repChannelID || config["channelModelKey"] != "openai/gpt-image-2.5-sunburst" || config["model"] != "openai/gpt-image-2.5-sunburst" {
		t.Fatalf("改写后的渠道选择 = %v/%v/%v", config["channelId"], config["channelModelKey"], config["model"])
	}
	// 规格变体属于具体渠道：不换成备用渠道自己的规格，请求会带着 fal 的变体 ID 发到 Replicate。
	if config["variantId"] != "VARIANT_REP" || config["providerModelKey"] != "openai/gpt-image-2.5-sunburst" {
		t.Fatalf("改写后的规格 = %v/%v", config["variantId"], config["providerModelKey"])
	}
}

// 结果不明的失败一律不换：请求可能已经在上游建了任务，换渠道就是重复计费。
func TestChannelFallbackIgnoresAmbiguousFailures(t *testing.T) {
	f := newChannelFallbackFixture(t)
	f.attempt.DispatchState = "submission_unknown"
	next, err := f.service.nextChannelAttemptAfterFailure(f.task, f.attempt)
	if err != nil || next != nil {
		t.Fatalf("提交结果不明时不得换渠道：attempt=%v err=%v", next, err)
	}
	f.attempt.DispatchState = "failed_before_send"
	if next, _ := f.service.nextChannelAttemptAfterFailure(f.task, f.attempt); next != nil {
		t.Fatal("请求没发出时不该换渠道：那种失败用户重试即可")
	}
	latest := f.reloadTask(t)
	if latest.ChannelModelID != "" {
		t.Fatalf("任务落点被改写：%q", latest.ChannelModelID)
	}
}

// 一轮任务里每条渠道只发一次：原渠道与备用渠道都试过之后就停手，
// 否则两条渠道会互相兜底成死循环。
func TestChannelFallbackStopsAfterEveryChannelTried(t *testing.T) {
	f := newChannelFallbackFixture(t)
	tried := &model.RouteAttempt{ID: "ATTEMPT_2", TaskID: f.task.ID, RouteRun: 0, AttemptNumber: 2, ChannelModelID: "MODEL_REP", ChannelID: repChannelID, Status: "failed", DispatchState: "rejected_no_job"}
	if err := f.db.Create(tried).Error; err != nil {
		t.Fatal(err)
	}
	if next := f.next(t); next != nil {
		t.Fatalf("两条渠道都试过之后仍在换渠道：%+v", next)
	}
}

// 备用渠道更贵或更便宜都不自动换：预扣是按原渠道的价目收的，差价不该由任一方默默承担。
func TestChannelFallbackRequiresSamePrice(t *testing.T) {
	f := newChannelFallbackFixture(t)
	f.ledger.prices[f.primaryModelKey()] = 45
	f.ledger.prices[repChannelID+"::openai/gpt-image-2.5-sunburst"] = 90
	if next := f.next(t); next != nil {
		t.Fatalf("备用渠道不同价时不得自动换：%+v", next)
	}
}

// 备用渠道没有定价同样不换：这种"相等"无从判断，换过去可能既没扣对钱也说不清该退多少。
func TestChannelFallbackRequiresBothSidesPriced(t *testing.T) {
	f := newChannelFallbackFixture(t)
	delete(f.ledger.prices, repChannelID+"::openai/gpt-image-2.5-sunburst")
	if next := f.next(t); next != nil {
		t.Fatalf("备用渠道未定价时不得自动换：%+v", next)
	}
}

// 备用渠道的能力合同接不住这次参数时不能换：换过去只是把一个拒绝换成另一个更晚的拒绝。
func TestChannelFallbackSkipsSiblingWithoutRequestedSize(t *testing.T) {
	f := newChannelFallbackFixture(t)
	// 先确认这次参数本来能换（否则下面的 nil 可能来自别的原因）。
	if next := f.next(t); next == nil {
		t.Fatal("对照组应当可以换渠道")
	}
	narrow := DefaultImageCapabilityConfig(string(model.ChannelInterfaceReplicatePredictionImage), "openai/gpt-image-2.5-sunburst")
	narrow.Size = ImageSizeConfig{Parameter: "aspect_ratio", Values: []string{"1:1"}, Default: "1:1"}
	narrow.Quality = ImageQualityConfig{Supported: true, Values: []string{"low", "medium", "high"}, Default: "low"}
	if err := f.db.Model(&model.ChannelModel{}).Where("id = ?", "MODEL_REP").Update("capability_config_json", mustEncodeModelCapabilityConfig(t, &ModelCapabilityConfig{Version: 1, Image: narrow})).Error; err != nil {
		t.Fatal(err)
	}
	input := map[string]any{
		"mode":              "image",
		"capabilityOptions": map[string]any{"count": "1", "quality": "low", "size": "16:9"},
		"config":            map[string]any{"channelId": falChannelID, "channelModelKey": "openai/gpt-image-2.5/sunburst", "model": "openai/gpt-image-2.5/sunburst", "variantId": "VARIANT_FAL", "size": "16:9", "quality": "low", "count": "1"},
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	f.task.InputJSON = string(encoded)
	if next := f.next(t); next != nil {
		t.Fatalf("备用渠道不支持所选比例时不得自动换：%+v", next)
	}
}

// 平台只有一条渠道时，兜底不做任何事：找不到备用渠道不是错误，原始失败照旧。
func TestChannelFallbackWithoutSiblingKeepsOriginalFailure(t *testing.T) {
	f := newChannelFallbackFixture(t)
	if err := f.db.Model(&model.ChannelModel{}).Where("id = ?", "MODEL_REP").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if next := f.next(t); next != nil {
		t.Fatalf("没有可用备用渠道时不得换渠道：%+v", next)
	}
}

// 显示名不同的模型不是同一行，绝不能互相兜底：用户选的是这个型号，不能被换成另一个。
func TestChannelFallbackIgnoresDifferentDisplayName(t *testing.T) {
	f := newChannelFallbackFixture(t)
	if err := f.db.Model(&model.ChannelModel{}).Where("id = ?", "MODEL_REP").Update("display_name", "GPT Image 2.5 Flare").Error; err != nil {
		t.Fatal(err)
	}
	if next := f.next(t); next != nil {
		t.Fatalf("不同显示名的渠道模型不得兜底：%+v", next)
	}
}

func (f *channelFallbackFixture) primaryModelKey() string {
	return falChannelID + "::openai/gpt-image-2.5/sunburst"
}

// 只有"每次都停在边缘网关"才算确定没建任务。供应商自己回的 JSON 5xx 说明请求已经进了
// 源站，可能已经建任务并计费，只能记成提交结果不明。
func TestEdgeGatewayFailureBecomesNoJobEvidence(t *testing.T) {
	gateway := providerAttemptNotCreatedError{Cause: providerHTTPError{StatusCode: 502, Status: "502 Bad Gateway", Body: "error code: 502"}}
	if !providerAttemptNotCreated(gateway) {
		t.Fatal("两次都停在边缘网关必须被识别为确定没建任务")
	}
	if wrapped := uncertainProviderSubmission(context.Background(), gateway); wrapped != error(gateway) {
		t.Fatalf("确定没建任务的失败不该被标成提交结果不明：%v", wrapped)
	}
	jsonFailure := providerHTTPError{StatusCode: 502, Status: "502 Bad Gateway", Body: `{"detail":"upstream exploded"}`}
	wrapped := uncertainProviderSubmission(context.Background(), jsonFailure)
	var unknown providerSubmissionUnknownError
	if !errors.As(wrapped, &unknown) {
		t.Fatalf("供应商 JSON 5xx 仍须按提交结果不明处理：%v", wrapped)
	}
}

// 失败尝试的落点状态决定能不能换渠道，写错一个状态就会让"结果不明"的任务被重发两次。
func TestFinishTaskRouteAttemptRecordsNoJobEvidence(t *testing.T) {
	f := newChannelFallbackFixture(t)
	attempt := &model.RouteAttempt{ID: "ATTEMPT_STATE", TaskID: f.task.ID, RouteRun: 0, AttemptNumber: 3, Status: "dispatching", DispatchState: "submission_unknown"}
	if err := f.db.Create(attempt).Error; err != nil {
		t.Fatal(err)
	}
	f.service.finishTaskRouteAttempt(attempt, f.task, providerAttemptNotCreatedError{Cause: providerHTTPError{StatusCode: 502, Body: "error code: 502"}}, true)
	if attempt.DispatchState != "rejected_no_job" {
		t.Fatalf("边缘网关失败落点 = %q，期望 rejected_no_job", attempt.DispatchState)
	}
	var stored model.RouteAttempt
	if err := f.db.First(&stored, "id = ?", attempt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.DispatchState != "rejected_no_job" || stored.FailureCode != "upstream_502" {
		t.Fatalf("落库后的尝试 = %s/%s", stored.DispatchState, stored.FailureCode)
	}
}

// 直连任务的尝试行必须带上渠道模型身份：渠道级兜底靠它判断"这条渠道已经发过"，
// 缺了它备用渠道会在两条渠道之间来回重发。
func TestCreateDirectTaskAttemptRecordsChannelModel(t *testing.T) {
	f := newChannelFallbackFixture(t)
	fresh := *f.task
	fresh.ID = "task-fresh"
	if err := f.db.Create(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	attempt, err := f.service.createDirectTaskAttempt(&fresh)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.ChannelModelID != "MODEL_FAL" || attempt.ChannelID != falChannelID {
		t.Fatalf("直连尝试的渠道身份 = %s/%s，期望 MODEL_FAL/%s", attempt.ChannelModelID, attempt.ChannelID, falChannelID)
	}
}

// channelFallbackExecutorPort 把真实的失败切换逻辑接进执行器，只把"调上游"这一步换成
// 受控的假实现：要验的是整轮循环——第一次失败后，第二次请求真的带着备用渠道的配置出去。
type channelFallbackExecutorPort struct {
	service   *Service
	channelID []string
	attempts  []string
	logs      []string
}

func (p *channelFallbackExecutorPort) markRouteAttemptDispatching(attempt *model.RouteAttempt) error {
	return p.service.markRouteAttemptDispatching(attempt)
}

func (p *channelFallbackExecutorPort) processTask(_ context.Context, task model.Task) (map[string]interface{}, []map[string]interface{}, error) {
	var input map[string]any
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		return nil, nil, err
	}
	config, _ := input["config"].(map[string]any)
	channelID, _ := config["channelId"].(string)
	p.channelID = append(p.channelID, channelID)
	p.attempts = append(p.attempts, task.ChannelModelID)
	if len(p.channelID) == 1 {
		return nil, nil, providerHTTPError{StatusCode: 402, Status: "402 Payment Required", Body: "insufficient credit"}
	}
	return map[string]interface{}{"ok": true}, nil, nil
}

func (p *channelFallbackExecutorPort) refreshTaskProviderState(*model.Task) error { return nil }

func (p *channelFallbackExecutorPort) finishTaskRouteAttempt(attempt *model.RouteAttempt, task *model.Task, taskErr error, issued bool) {
	p.service.finishTaskRouteAttempt(attempt, task, taskErr, issued)
}

func (p *channelFallbackExecutorPort) nextRouteAttemptAfterFailure(task *model.Task, attempt *model.RouteAttempt, taskErr error) (*model.RouteAttempt, error) {
	return p.service.nextRouteAttemptAfterFailure(task, attempt, taskErr)
}

func (p *channelFallbackExecutorPort) log(_ string, _ string, _ string, message string, payload string) error {
	p.logs = append(p.logs, message+":"+payload)
	return nil
}

// 一次上游拒单之后，这一轮执行必须换成备用渠道的配置再发一次，而不是把失败直接交给用户。
func TestRouteExecutorFallsBackToSiblingChannel(t *testing.T) {
	f := newChannelFallbackFixture(t)
	port := &channelFallbackExecutorPort{service: f.service}
	result, err := (&taskRouteExecutor{port: port}).execute(context.Background(), f.task, f.attempt)
	if err != nil {
		t.Fatalf("执行报错：%v", err)
	}
	if !result.providerSucceeded || result.err != nil {
		t.Fatalf("换渠道后应当成功：%+v", result)
	}
	if len(port.channelID) != 2 || port.channelID[0] != falChannelID || port.channelID[1] != repChannelID {
		t.Fatalf("两次请求的渠道 = %v，期望先 %s 再 %s", port.channelID, falChannelID, repChannelID)
	}
	if port.attempts[1] != "MODEL_REP" {
		t.Fatalf("第二次请求的落点 = %q，期望 MODEL_REP", port.attempts[1])
	}
	if len(port.logs) != 1 || port.logs[0] != "上游未创建任务，切换备用能力路由:MODEL_REP" {
		t.Fatalf("切换日志 = %v", port.logs)
	}
	var count int64
	if err := f.db.Model(&model.RouteAttempt{}).Where("task_id = ?", f.task.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("尝试记录 = %d 条，期望 2 条", count)
	}
	if latest := f.reloadTask(t); latest.ChannelModelID != "MODEL_REP" {
		t.Fatalf("任务落点 = %q，期望 MODEL_REP", latest.ChannelModelID)
	}
}
