package auth

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// 套餐与订阅的服务层。
//
// 这里只做「校验 + 编排」：货架的读写、订阅的顺延都在 Store 里，折扣计算在
// billing_pricing.go 里。把校验集中在这一层是为了让后台管理接口、用户端商品目录
// 与手工开通走同一条路径——否则"后台能存进去、用户端却建不了单"这类不一致会反复出现。

// billingPlanCodePattern 是套餐标识的白名单。
//
// 限制成小写字母、数字与连字符，是因为 code 会出现在支付回调、对账文件与前端路由里：
// 一旦允许大写、下划线或空格，同一个套餐在不同系统里就会被写成多个"不一样"的标识，
// 而唯一索引只挡得住完全相同的字符串。
var billingPlanCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// AdminBillingPlans 返回后台的完整货架，含已停用套餐。
//
// 停用只是"不再对新用户展示"，后台必须还能看到并重新启用它：否则下架即等于丢失配置，
// 想恢复促销价就只剩手动改库一条路。
func (s *Service) AdminBillingPlans() ([]BillingPlanView, error) {
	plans, err := s.store.BillingPlans(false)
	if err != nil {
		return nil, internalFailure(err)
	}
	return billingPlanViews(plans), nil
}

// BillingCatalog 返回用户端可见的商品目录，只含启用中的套餐。
func (s *Service) BillingCatalog() ([]BillingPlanView, error) {
	plans, err := s.store.BillingPlans(true)
	if err != nil {
		return nil, internalFailure(err)
	}
	return billingPlanViews(plans), nil
}

// billingPlanViews 把存储模型批量投影成对外视图。
//
// 返回非 nil 的空切片：JSON 序列化后是 []，前端列表页不必再为 null 写分支。
func billingPlanViews(plans []BillingPlan) []BillingPlanView {
	views := make([]BillingPlanView, 0, len(plans))
	for _, plan := range plans {
		views = append(views, BillingPlanViewOf(plan))
	}
	return views
}

// SaveBillingPlan 新建或更新一个套餐。
//
// 入参 ID 为空表示新建：后台只要传了 ID 就一律按"更新"处理，不会因为 ID 写错而
// 悄悄多出一个重复套餐。
func (s *Service) SaveBillingPlan(input BillingPlanInput) (*BillingPlanView, error) {
	code := strings.TrimSpace(input.Code)
	if !billingPlanCodePattern.MatchString(code) {
		return nil, invalidArgument("套餐标识只能由小写字母、数字和连字符组成，需以字母或数字开头且不超过 32 位")
	}
	name := strings.TrimSpace(input.Name)
	if count := utf8.RuneCountInString(name); count < 1 || count > 40 {
		return nil, invalidArgument("套餐名称需为 1 到 40 个字符")
	}
	if input.PriceFen < 0 {
		return nil, invalidArgument("套餐价格不能为负数")
	}
	if input.Credits < 0 || input.GiftCredits < 0 {
		return nil, invalidArgument("套餐积分与赠送积分不能为负数")
	}
	// 周期为 0 只在"这是个纯积分包"时成立：既不带时长、也不带积分的东西卖了也没有
	// 任何东西能交付，那多半是运营漏填了字段，而不是真想做一件空商品。
	if input.PeriodDays < 0 || input.PeriodDays > 3650 {
		return nil, invalidArgument("套餐周期需在 0 到 3650 天之间")
	}
	if input.PeriodDays == 0 && input.Credits+input.GiftCredits == 0 {
		return nil, invalidArgument("套餐周期为 0 时必须是积分包，请填写到账积分或赠送积分")
	}
	if input.QuotaCalls < 0 || input.QuotaStorageMB < 0 || input.QuotaMembers < 0 {
		return nil, invalidArgument("套餐配额不能为负数")
	}

	targetID := strings.TrimSpace(input.ID)
	plan := BillingPlan{}
	if targetID != "" {
		existing, err := s.store.BillingPlanByID(targetID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				// 包成 404 而不是裸 ErrNotFound：HTTP 层只认 *Error，裸错误会落到 500，
				// "套餐被删掉了"在后台会显示成"系统处理失败"。
				return nil, notFound("套餐不存在")
			}
			return nil, internalFailure(err)
		}
		// 沿用原记录的创建时间：更新语义下 CreatedAt 属于审计信息，不该被这次保存刷新。
		plan = *existing
	}

	// code 是回调与订单快照的关联键，撞车会让"这笔钱买的是哪个套餐"失去唯一答案，
	// 所以这里先查一次并给出明确的中文冲突提示，而不是把唯一索引的报错译成 500。
	conflicting, err := s.store.BillingPlanByCode(code)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, internalFailure(err)
	}
	if err == nil && conflicting.ID != targetID {
		return nil, conflict("套餐标识已被占用")
	}

	plan.ID = targetID
	plan.Code = code
	plan.Name = name
	plan.Description = strings.TrimSpace(input.Description)
	plan.SortOrder = input.SortOrder
	plan.Enabled = input.Enabled
	plan.PriceFen = input.PriceFen
	plan.PeriodDays = input.PeriodDays
	plan.Credits = input.Credits
	plan.GiftCredits = input.GiftCredits
	plan.QuotaCalls = input.QuotaCalls
	plan.QuotaStorageMB = input.QuotaStorageMB
	plan.QuotaMembers = input.QuotaMembers

	if err := s.store.SaveBillingPlan(&plan); err != nil {
		return nil, internalFailure(err)
	}
	view := BillingPlanViewOf(plan)
	return &view, nil
}

// DeleteBillingPlan 删除一个套餐，但有订单在身的套餐只允许停用。
//
// 订单里的 PlanCode/PlanName 是下单时的快照，删掉套餐本身不会破坏历史订单；真正
// 不能丢的是"这个 code 曾经卖过什么"这层关联——删了之后再看订单，就再也无法回溯
// 它对应货架上的哪一版配置（价格、周期、配额）。因此有订单时引导运营去停用：
// 既满足"不再售卖"，又保留对账所需的沿革。
func (s *Service) DeleteBillingPlan(id string) error {
	plan, err := s.store.BillingPlanByID(id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return notFound("套餐不存在")
		}
		return internalFailure(err)
	}
	_, total, err := s.store.BillingOrders(BillingOrderFilter{PlanCode: plan.Code, PageSize: 1})
	if err != nil {
		return internalFailure(err)
	}
	if total > 0 {
		return conflict("该套餐已有订单，请改为停用")
	}
	if err := s.store.DeleteBillingPlan(plan.ID); err != nil {
		return internalFailure(err)
	}
	return nil
}

// BillingEntitlements 汇总某个账号当前可用的权益。
//
// 没有有效订阅不是错误，而是"这个账号还没付费"的正常状态：前台据此展示充值引导，
// 若返回 ErrNotFound，调用方会把"未订阅"和"服务故障"混为一谈。
func (s *Service) BillingEntitlements(userID string) (*BillingEntitlementsView, error) {
	target := strings.TrimSpace(userID)
	if target == "" {
		return nil, invalidArgument("缺少账号标识")
	}
	subscription, err := s.store.ActiveSubscription(target)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &BillingEntitlementsView{Active: false}, nil
		}
		return nil, internalFailure(err)
	}

	expiresAt := subscription.ExpiresAt
	view := &BillingEntitlementsView{
		Active:    true,
		PlanCode:  subscription.PlanCode,
		ExpiresAt: &expiresAt,
	}

	plan, planErr := s.store.BillingPlanByID(subscription.PlanID)
	if errors.Is(planErr, ErrNotFound) {
		// 套餐被删除或订阅上的 PlanID 为空时，退到 code 再找一次：订阅里同时留了
		// 两个标识，正是为了在这种残缺场景下还能把名称与配额补齐。
		plan, planErr = s.store.BillingPlanByCode(subscription.PlanCode)
	}
	if planErr != nil {
		if errors.Is(planErr, ErrNotFound) {
			// 套餐确实找不回来了：用户仍在有效期内，只是配额无从得知。这里按 0
			// （即不限量）返回而不是报错，否则一次后台误删就等于提前终止了已付费的服务。
			return view, nil
		}
		return nil, internalFailure(planErr)
	}

	view.PlanCode = plan.Code
	view.PlanName = plan.Name
	view.QuotaCalls = plan.QuotaCalls
	view.QuotaStorageMB = plan.QuotaStorageMB
	view.QuotaMembers = plan.QuotaMembers
	return view, nil
}

// GrantBillingSubscription 手工给账号开通或续期一个套餐。
//
// orderID 传空：手工赠送没有对应的收款单，而订阅的外键若指向不存在的订单，日后的
// "续期来自哪笔钱"反而更难查。
//
// actorID 只用于 HTTP 层的审计留痕，本层不落库——桌面/本地形态没有审计表，服务层
// 一旦依赖它，同一条开通逻辑就无法在两种形态下共用。
func (s *Service) GrantBillingSubscription(userID string, planCode string, actorID string) (*BillingEntitlementsView, error) {
	target := strings.TrimSpace(userID)
	if target == "" {
		return nil, invalidArgument("请选择要开通的账号")
	}
	plan, err := s.store.BillingPlanByCode(planCode)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, invalidArgument("套餐不存在或已停用")
		}
		return nil, internalFailure(err)
	}
	// 停用套餐与不存在的套餐对调用方是同一件事：都不能卖，也都不该被手工开通。
	if !plan.Enabled {
		return nil, invalidArgument("套餐不存在或已停用")
	}
	if _, err := s.store.ActivateSubscription(target, *plan, ""); err != nil {
		return nil, internalFailure(err)
	}
	return s.BillingEntitlements(target)
}
