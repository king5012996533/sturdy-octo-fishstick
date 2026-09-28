package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// 计费域（套餐 / 订阅 / 订单 / 优惠券）。
//
// 这里回答的是"这个账号买了什么、还能用多久、这笔钱到底收没收到"。它刻意与画布侧的
// 用量统计分开：用量在画布库里按工作区累计，订单与订阅必须跟账号库放一起——支付回调
// 要在一个事务里同时改订单状态并续期订阅，跨库就没法保证这一点。
//
// 金额一律用"分"的整数存储。浮点金额在打折、分摊与对账时都会悄悄丢精度，而这里每
// 一分都要能和支付渠道的流水对上。

const (
	SubscriptionStatusActive   = "ACTIVE"
	SubscriptionStatusExpired  = "EXPIRED"
	SubscriptionStatusCanceled = "CANCELED"
)

const (
	OrderStatusPending  = "PENDING"
	OrderStatusPaid     = "PAID"
	OrderStatusCanceled = "CANCELED"
	OrderStatusRefunded = "REFUNDED"
	OrderStatusFailed   = "FAILED"
)

const (
	// CouponKindAmount 是满减券：Value 即抵扣金额（分）。
	CouponKindAmount = "AMOUNT"
	// CouponKindPercent 是折扣券：Value 是折扣率（万分比，8000 表示 8 折），
	// 抵扣额 = 金额 ×(10000-Value)/10000 向下取整。用万分比而不是 0.8 这种小数，
	// 同样是为了不把金额计算搬进浮点。
	CouponKindPercent = "PERCENT"
)

// 支付渠道：配置在后台，回调按渠道分别验签。
const (
	PaymentChannelAggregate = "AGGREGATE"
	PaymentChannelManual    = "MANUAL"
	PaymentChannelWechat    = "WECHAT"
	PaymentChannelAlipay    = "ALIPAY"
)

// BillingPlan 是货架上的一个套餐。
//
// Code 是给人和回调用的稳定标识（"pro-month"），ID 是主键：运营改名不该让已售出的
// 订单换名字，所以订单里同时留 PlanID 与下单时的 PlanCode/PlanName 快照。
type BillingPlan struct {
	ID          string `gorm:"column:id;primaryKey;size:36"`
	Code        string `gorm:"column:code;size:32"`
	Name        string `gorm:"column:name;size:64"`
	Description string `gorm:"column:description;size:255"`
	SortOrder   int    `gorm:"column:sort_order"`
	Enabled     bool   `gorm:"column:enabled"`
	PriceFen    int64  `gorm:"column:price_fen"`
	PeriodDays  int    `gorm:"column:period_days"`
	// 配额：0 表示不限量。后台开通的套餐默认不限制调用次数，按量计费由渠道侧承担。
	QuotaCalls     int64 `gorm:"column:quota_calls"`
	QuotaStorageMB int64 `gorm:"column:quota_storage_mb"`
	QuotaMembers   int   `gorm:"column:quota_members"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (BillingPlan) TableName() string { return "billing_plans" }

// BillingSubscription 是一个账号当前（或历史）的订阅。
//
// 续费不新开一条记录，而是沿用当前这条并把 ExpiresAt 往后顺延：否则"我什么时候到期"
// 在后台会变成一行行需要心算的账单。历史只保留被替换掉的那些记录。
type BillingSubscription struct {
	ID            string    `gorm:"column:id;primaryKey;size:36"`
	UserID        string    `gorm:"column:user_id;size:36"`
	PlanID        string    `gorm:"column:plan_id;size:36"`
	PlanCode      string    `gorm:"column:plan_code;size:32"`
	Status        string    `gorm:"column:status;size:16"`
	StartedAt     time.Time `gorm:"column:started_at"`
	ExpiresAt     time.Time `gorm:"column:expires_at"`
	SourceOrderID string    `gorm:"column:source_order_id;size:36"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (BillingSubscription) TableName() string { return "billing_subscriptions" }

// BillingOrder 是一笔收款单。
//
// PlanName/CouponCode 都是下单时的快照：套餐改名、优惠券下架之后，历史订单必须还能
// 原样展示，否则财务对不上账。
type BillingOrder struct {
	ID              string     `gorm:"column:id;primaryKey;size:36"`
	OrderNo         string     `gorm:"column:order_no;size:40"`
	UserID          string     `gorm:"column:user_id;size:36"`
	PlanID          string     `gorm:"column:plan_id;size:36"`
	PlanCode        string     `gorm:"column:plan_code;size:32"`
	PlanName        string     `gorm:"column:plan_name;size:64"`
	AmountFen       int64      `gorm:"column:amount_fen"`
	DiscountFen     int64      `gorm:"column:discount_fen"`
	PayableFen      int64      `gorm:"column:payable_fen"`
	CouponID        string     `gorm:"column:coupon_id;size:36"`
	CouponCode      string     `gorm:"column:coupon_code;size:32"`
	Status          string     `gorm:"column:status;size:16"`
	Provider        string     `gorm:"column:provider;size:16"`
	ProviderOrderNo string     `gorm:"column:provider_order_no;size:64"`
	PaidAt          *time.Time `gorm:"column:paid_at"`
	// ExpiresAt 是支付超时时间：长期挂着的待支付订单会让"未支付"和"掉单"混在一起。
	ExpiresAt time.Time `gorm:"column:expires_at"`
	Remark    string    `gorm:"column:remark;size:255"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (BillingOrder) TableName() string { return "billing_orders" }

// BillingCoupon 是一张优惠券。
//
// TotalQuota/PerUserLimit 为 0 表示不限。UsedCount 只在核销事务里自增，后台不允许
// 直接改它——手工改计数会让"发出去多少张"和"实际用了多少张"失去对账依据。
type BillingCoupon struct {
	ID           string    `gorm:"column:id;primaryKey;size:36"`
	Code         string    `gorm:"column:code;size:32"`
	Name         string    `gorm:"column:name;size:64"`
	Kind         string    `gorm:"column:kind;size:16"`
	Value        int64     `gorm:"column:value"`
	MinAmountFen int64     `gorm:"column:min_amount_fen"`
	TotalQuota   int       `gorm:"column:total_quota"`
	UsedCount    int       `gorm:"column:used_count"`
	PerUserLimit int       `gorm:"column:per_user_limit"`
	StartsAt     time.Time `gorm:"column:starts_at"`
	ExpiresAt    time.Time `gorm:"column:expires_at"`
	Enabled      bool      `gorm:"column:enabled"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (BillingCoupon) TableName() string { return "billing_coupons" }

// BillingCouponRedemption 是一次核销留痕。
//
// 单独成表而不是只加计数：客服排查"为什么这张券用不了"必须能看到是谁、在哪张订单上
// 用掉的；退款时也要按订单精确回滚，不能靠减一。
//
// 这个结构体会被后台核销记录接口直接序列化，因此每个字段都必须带 json tag：少了 tag
// 会退化成 Go 字段名（UserID/OrderID/DiscountFen），而前端读的是 camelCase，结果就是
// 客服页面上账号与订单为空、抵扣额恒显示 ¥0.00 —— 静默错数据比报错更难发现。
type BillingCouponRedemption struct {
	ID          string    `gorm:"column:id;primaryKey;size:36" json:"id"`
	CouponID    string    `gorm:"column:coupon_id;size:36" json:"couponId"`
	CouponCode  string    `gorm:"column:coupon_code;size:32" json:"couponCode"`
	UserID      string    `gorm:"column:user_id;size:36" json:"userId"`
	OrderID     string    `gorm:"column:order_id;size:36" json:"orderId"`
	DiscountFen int64     `gorm:"column:discount_fen" json:"discountFen"`
	RedeemedAt  time.Time `gorm:"column:redeemed_at" json:"redeemedAt"`
}

func (BillingCouponRedemption) TableName() string { return "billing_coupon_redemptions" }

// BillingPaymentConfig 是一个支付渠道的后台配置（密钥密文）。
//
// 与验证码网关同样的处理：AccessKey/商户密钥只写不读，落库前用 StateSecret 派生的
// 密钥加密。支付密钥泄露等于别人能替你发起收款与退款，比 SMTP 密码严重得多。
type BillingPaymentConfig struct {
	Channel    string    `gorm:"column:channel;primaryKey;size:16"`
	ConfigJSON []byte    `gorm:"column:config_json"`
	Enabled    bool      `gorm:"column:enabled"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
	UpdatedBy  string    `gorm:"column:updated_by;size:36"`
}

func (BillingPaymentConfig) TableName() string { return "billing_payment_configs" }

// BillingModels 供 EnsureDevSchema 建表使用（生产由 Prisma 迁移管理）。
func BillingModels() []any {
	return []any{
		&BillingPlan{},
		&BillingSubscription{},
		&BillingOrder{},
		&BillingCoupon{},
		&BillingCouponRedemption{},
		&BillingPaymentConfig{},
	}
}

// newBillingID 生成主键。
//
// 与账号库其余表的 ID 形态一致：随机而不是自增——订单 ID 若可枚举，攻击者就能拿别人
// 的订单号去轮询支付状态或伪造回调。
func newBillingID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("auth: 生成计费主键失败: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

// NewBillingOrderNo 生成对外的订单号。
//
// 形态是"时间前缀 + 随机尾号"：客服与渠道对账时靠前缀就能定位下单时间，而随机尾号
// 让订单号无法被顺序猜测。
func NewBillingOrderNo(now time.Time) (string, error) {
	buffer := make([]byte, 5)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("auth: 生成订单号失败: %w", err)
	}
	return fmt.Sprintf("K%02d%02d%02d%s", now.Year()%100, int(now.Month()), now.Day(), strings.ToUpper(hex.EncodeToString(buffer))), nil
}

// ---------- 对外视图 ----------

// BillingPlanView 是后台与前台共用的套餐视图。
type BillingPlanView struct {
	ID             string    `json:"id"`
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	SortOrder      int       `json:"sortOrder"`
	Enabled        bool      `json:"enabled"`
	PriceFen       int64     `json:"priceFen"`
	PeriodDays     int       `json:"periodDays"`
	QuotaCalls     int64     `json:"quotaCalls"`
	QuotaStorageMB int64     `json:"quotaStorageMb"`
	QuotaMembers   int       `json:"quotaMembers"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

func BillingPlanViewOf(plan BillingPlan) BillingPlanView {
	return BillingPlanView{
		ID:             plan.ID,
		Code:           plan.Code,
		Name:           plan.Name,
		Description:    plan.Description,
		SortOrder:      plan.SortOrder,
		Enabled:        plan.Enabled,
		PriceFen:       plan.PriceFen,
		PeriodDays:     plan.PeriodDays,
		QuotaCalls:     plan.QuotaCalls,
		QuotaStorageMB: plan.QuotaStorageMB,
		QuotaMembers:   plan.QuotaMembers,
		CreatedAt:      plan.CreatedAt,
		UpdatedAt:      plan.UpdatedAt,
	}
}

// BillingEntitlementsView 是"这个账号现在能用什么"。
//
// 前台用它决定要不要弹充值引导；后台用它回答客服"这个用户到底还有没有额度"。
type BillingEntitlementsView struct {
	Active         bool       `json:"active"`
	PlanCode       string     `json:"planCode"`
	PlanName       string     `json:"planName"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	QuotaCalls     int64      `json:"quotaCalls"`
	QuotaStorageMB int64      `json:"quotaStorageMb"`
	QuotaMembers   int        `json:"quotaMembers"`
}

// BillingOrderView 是订单视图。金额保持"分"，由前端统一格式化。
type BillingOrderView struct {
	ID              string     `json:"id"`
	OrderNo         string     `json:"orderNo"`
	UserID          string     `json:"userId"`
	UserName        string     `json:"userName"`
	UserEmail       string     `json:"userEmail"`
	UserPhone       string     `json:"userPhone"`
	PlanID          string     `json:"planId"`
	PlanCode        string     `json:"planCode"`
	PlanName        string     `json:"planName"`
	AmountFen       int64      `json:"amountFen"`
	DiscountFen     int64      `json:"discountFen"`
	PayableFen      int64      `json:"payableFen"`
	CouponCode      string     `json:"couponCode"`
	Status          string     `json:"status"`
	Provider        string     `json:"provider"`
	ProviderOrderNo string     `json:"providerOrderNo"`
	PaidAt          *time.Time `json:"paidAt,omitempty"`
	ExpiresAt       time.Time  `json:"expiresAt"`
	Remark          string     `json:"remark"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// BillingOrderViewOfUser 把账号字段拼进订单视图，账号缺失时留空而不是报错：
// 账号被删除后历史订单仍要为财务保留。
func BillingOrderViewOfUser(order BillingOrder, user *User) BillingOrderView {
	view := BillingOrderView{
		ID:              order.ID,
		OrderNo:         order.OrderNo,
		UserID:          order.UserID,
		PlanID:          order.PlanID,
		PlanCode:        order.PlanCode,
		PlanName:        order.PlanName,
		AmountFen:       order.AmountFen,
		DiscountFen:     order.DiscountFen,
		PayableFen:      order.PayableFen,
		CouponCode:      order.CouponCode,
		Status:          order.Status,
		Provider:        order.Provider,
		ProviderOrderNo: order.ProviderOrderNo,
		PaidAt:          order.PaidAt,
		ExpiresAt:       order.ExpiresAt,
		Remark:          order.Remark,
		CreatedAt:       order.CreatedAt,
	}
	if user != nil {
		// DisplayName 已经处理了"昵称为空回落邮箱/账号"的展示规则，这里不另立一套。
		view.UserName = user.DisplayName()
		if user.Email != nil {
			view.UserEmail = *user.Email
		}
		if user.Phone != nil {
			view.UserPhone = *user.Phone
		}
	}
	return view
}

// BillingCouponView 是优惠券视图，附带"还能用多少张"的余量。
type BillingCouponView struct {
	ID             string    `json:"id"`
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	Kind           string    `json:"kind"`
	Value          int64     `json:"value"`
	MinAmountFen   int64     `json:"minAmountFen"`
	TotalQuota     int       `json:"totalQuota"`
	UsedCount      int       `json:"usedCount"`
	PerUserLimit   int       `json:"perUserLimit"`
	StartsAt       time.Time `json:"startsAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	RemainingQuota *int      `json:"remainingQuota,omitempty"`
}

func BillingCouponViewOf(coupon BillingCoupon) BillingCouponView {
	view := BillingCouponView{
		ID:           coupon.ID,
		Code:         coupon.Code,
		Name:         coupon.Name,
		Kind:         coupon.Kind,
		Value:        coupon.Value,
		MinAmountFen: coupon.MinAmountFen,
		TotalQuota:   coupon.TotalQuota,
		UsedCount:    coupon.UsedCount,
		PerUserLimit: coupon.PerUserLimit,
		StartsAt:     coupon.StartsAt,
		ExpiresAt:    coupon.ExpiresAt,
		Enabled:      coupon.Enabled,
		CreatedAt:    coupon.CreatedAt,
		UpdatedAt:    coupon.UpdatedAt,
	}
	if coupon.TotalQuota > 0 {
		remaining := coupon.TotalQuota - coupon.UsedCount
		if remaining < 0 {
			remaining = 0
		}
		view.RemainingQuota = &remaining
	}
	return view
}

// BillingRevenue 是后台的经营读数。
type BillingRevenue struct {
	PaidOrders    int64 `json:"paidOrders"`
	PaidAmountFen int64 `json:"paidAmountFen"`
	PendingOrders int64 `json:"pendingOrders"`
	RefundedFen   int64 `json:"refundedFen"`
}

// ---------- 服务层契约（输入与分页视图） ----------
//
// 这些结构体的 JSON 标签就是前后端契约，和 web/src/services/api/billing.ts、
// web/src/features/admin-console/api.ts 一一对应。服务层的实现文件只填行为，
// 不要再另造一套同名类型：字段名漂移会让前端静默拿到 undefined。

// BillingPlanInput 是后台保存套餐的入参。ID 为空表示新建。
type BillingPlanInput struct {
	ID             string `json:"id"`
	Code           string `json:"code"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	SortOrder      int    `json:"sortOrder"`
	Enabled        bool   `json:"enabled"`
	PriceFen       int64  `json:"priceFen"`
	PeriodDays     int    `json:"periodDays"`
	QuotaCalls     int64  `json:"quotaCalls"`
	QuotaStorageMB int64  `json:"quotaStorageMb"`
	QuotaMembers   int    `json:"quotaMembers"`
}

// BillingCouponInput 是后台保存优惠券的入参。ID 为空表示新建。
type BillingCouponInput struct {
	ID           string    `json:"id"`
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	Value        int64     `json:"value"`
	MinAmountFen int64     `json:"minAmountFen"`
	TotalQuota   int       `json:"totalQuota"`
	PerUserLimit int       `json:"perUserLimit"`
	StartsAt     time.Time `json:"startsAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
	Enabled      bool      `json:"enabled"`
}

// BillingOrderInput 是下单入参：选哪个套餐、可选一张券。
type BillingOrderInput struct {
	PlanCode   string `json:"planCode"`
	CouponCode string `json:"couponCode"`
}

// BillingCouponQuoteView 是结算试算结果。
//
// 试算与下单共用同一段定价逻辑，前端因此能在用户点"立即支付"之前就把实付金额、
// 抵扣金额和券不可用的原因显示出来，而不是等下单失败再报错。
type BillingCouponQuoteView struct {
	AmountFen   int64  `json:"amountFen"`
	DiscountFen int64  `json:"discountFen"`
	PayableFen  int64  `json:"payableFen"`
	CouponCode  string `json:"couponCode"`
	CouponError string `json:"couponError,omitempty"`
}

// BillingOrderPageView 同时服务用户端（我的订单）与后台（订单管理）。
type BillingOrderPageView struct {
	Orders   []BillingOrderView `json:"orders"`
	Total    int64              `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"pageSize"`
	Revenue  *BillingRevenue    `json:"revenue,omitempty"`
}

// BillingPaymentLaunch 是"发起支付"的结果：订单 + 渠道收银台地址（或自定义参数）。
type BillingPaymentLaunch struct {
	Order    BillingOrderView  `json:"order"`
	Provider string            `json:"provider"`
	PayURL   string            `json:"payUrl,omitempty"`
	Params   map[string]string `json:"payParams,omitempty"`
}

// PaymentChannelView 是后台的支付渠道视图。
//
// Config 里只放非密钥字段（商户号、回调地址这类）；密钥一概不回传，只给 HasSecret。
type PaymentChannelView struct {
	Channel   string            `json:"channel"`
	Enabled   bool              `json:"enabled"`
	Source    string            `json:"source"`
	Ready     bool              `json:"ready"`
	Detail    string            `json:"detail"`
	UpdatedAt *time.Time        `json:"updatedAt,omitempty"`
	UpdatedBy string            `json:"updatedBy,omitempty"`
	Config    map[string]string `json:"config"`
	HasSecret bool              `json:"hasSecret"`
}

// PaymentChannelsView 是支付渠道的合并视图。
type PaymentChannelsView struct {
	Channels []PaymentChannelView `json:"channels"`
}

// PaymentChannelInput 是保存支付渠道的入参。
//
// Config 中未出现的密钥字段保持原值：后台读不到密钥，若把空值当清空，改一次回调地址
// 就会把商户密钥弄丢，而界面上看不出异常。
type PaymentChannelInput struct {
	Enabled bool              `json:"enabled"`
	Config  map[string]string `json:"config"`
}
