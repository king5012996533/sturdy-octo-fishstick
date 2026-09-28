package auth

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 计费域的读写层。
//
// 只做「查/写 + 事务边界」，不含定价策略与状态机：折扣怎么算、订单能不能从 PENDING
// 直接跳到 REFUNDED 属于服务层判断，写在这里会让后台批量操作与支付回调各绕开一遍。

// BillingOrderFilter 是后台订单列表的筛选条件。
type BillingOrderFilter struct {
	Status   string
	PlanCode string
	UserID   string
	// Keyword 命中订单号或用户 ID：客服通常只拿到其中一个。
	Keyword  string
	Page     int
	PageSize int
}

// CouponRedemptionFilter 是核销流水的筛选条件。
type CouponRedemptionFilter struct {
	CouponID string
	UserID   string
	Page     int
	PageSize int
}

// ---------- 套餐 ----------

func (s *Store) BillingPlans(onlyEnabled bool) ([]BillingPlan, error) {
	query := s.db.Model(&BillingPlan{})
	if onlyEnabled {
		query = query.Where("enabled = ?", true)
	}
	var plans []BillingPlan
	if err := query.Order("sort_order asc, price_fen asc").Find(&plans).Error; err != nil {
		return nil, err
	}
	return plans, nil
}

func (s *Store) BillingPlanByID(id string) (*BillingPlan, error) {
	var plan BillingPlan
	err := s.db.Where("id = ?", strings.TrimSpace(id)).First(&plan).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

func (s *Store) BillingPlanByCode(code string) (*BillingPlan, error) {
	var plan BillingPlan
	err := s.db.Where("code = ?", strings.TrimSpace(code)).First(&plan).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

// SaveBillingPlan 按主键写入或更新套餐。
func (s *Store) SaveBillingPlan(plan *BillingPlan) error {
	if plan == nil {
		return errors.New("auth: 套餐为空")
	}
	now := s.clock()
	if plan.ID == "" {
		id, err := newBillingID()
		if err != nil {
			return err
		}
		plan.ID = id
	}
	if plan.CreatedAt.IsZero() {
		plan.CreatedAt = now
	}
	plan.UpdatedAt = now
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"code", "name", "description", "sort_order", "enabled", "price_fen", "period_days",
			"quota_calls", "quota_storage_mb", "quota_members", "updated_at",
		}),
	}).Create(plan).Error
}

func (s *Store) DeleteBillingPlan(id string) error {
	return s.db.Where("id = ?", strings.TrimSpace(id)).Delete(&BillingPlan{}).Error
}

// ---------- 订阅 ----------

// ActiveSubscription 返回当前未过期的订阅，过期的那些只作为历史存在。
func (s *Store) ActiveSubscription(userID string) (*BillingSubscription, error) {
	var record BillingSubscription
	err := s.db.Where("user_id = ? AND status = ? AND expires_at > ?", strings.TrimSpace(userID), SubscriptionStatusActive, s.clock()).
		Order("expires_at desc").First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Store) SubscriptionHistory(userID string, limit int) ([]BillingSubscription, error) {
	if limit <= 0 {
		limit = 20
	}
	var records []BillingSubscription
	if err := s.db.Where("user_id = ?", strings.TrimSpace(userID)).
		Order("created_at desc").Limit(limit).Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// ActivateSubscription 在当前订阅基础上续期，或在没有订阅时新开一条。
//
// 顺延而不是重置：用户还剩 20 天时续一年，应该是 385 天。这一点必须在这里统一实现，
// 否则"手动补单"和"支付回调"会算出两个不同的到期时间。
func (s *Store) ActivateSubscription(userID string, plan BillingPlan, orderID string) (*BillingSubscription, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, invalidArgument("缺少账号标识")
	}
	periodDays := plan.PeriodDays
	if periodDays <= 0 {
		periodDays = 30
	}
	now := s.clock()
	var result *BillingSubscription
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// 同一笔订单只允许续期一次。渠道重试、客服连点会让同一个订单并发走到这里，
		// 而"读订阅 → 加一期"本身没有排他性，两次都读到同一份旧到期时间就会多送
		// 一期时长（真金白银）。source_order_id 永远指向最近一次续期所依据的订单，
		// 命中即代表这单已经生效过，直接返回原记录。
		if key := strings.TrimSpace(orderID); key != "" {
			var applied BillingSubscription
			appliedErr := tx.Where("user_id = ? AND source_order_id = ?", userID, key).
				Order("updated_at desc").First(&applied).Error
			switch {
			case appliedErr == nil:
				result = &applied
				return nil
			case !errors.Is(appliedErr, gorm.ErrRecordNotFound):
				return appliedErr
			}
		}
		var current BillingSubscription
		findErr := tx.Where("user_id = ? AND status = ? AND expires_at > ?", userID, SubscriptionStatusActive, now).
			Order("expires_at desc").First(&current).Error
		switch {
		case findErr == nil:
			expiresAt := current.ExpiresAt.AddDate(0, 0, periodDays)
			// 同一套餐续期只延长到期时间，不做任何记录改写：历史订单已经记了这次购买。
			if err := tx.Model(&BillingSubscription{}).Where("id = ?", current.ID).
				Updates(map[string]any{
					"expires_at":      expiresAt,
					"plan_id":         plan.ID,
					"plan_code":       plan.Code,
					"source_order_id": orderID,
					"updated_at":      now,
				}).Error; err != nil {
				return err
			}
			current.ExpiresAt = expiresAt
			current.PlanID = plan.ID
			current.PlanCode = plan.Code
			current.SourceOrderID = orderID
			current.UpdatedAt = now
			result = &current
			return nil
		case !errors.Is(findErr, gorm.ErrRecordNotFound):
			return findErr
		}

		// 换套餐或首次购买：旧记录降级成历史，保留它的到期时间用于客诉核对。
		startedAt := now
		if err := tx.Model(&BillingSubscription{}).Where("user_id = ? AND status = ?", userID, SubscriptionStatusActive).
			Updates(map[string]any{"status": SubscriptionStatusExpired, "updated_at": now}).Error; err != nil {
			return err
		}
		id, err := newBillingID()
		if err != nil {
			return err
		}
		record := BillingSubscription{
			ID:            id,
			UserID:        userID,
			PlanID:        plan.ID,
			PlanCode:      plan.Code,
			Status:        SubscriptionStatusActive,
			StartedAt:     startedAt,
			ExpiresAt:     startedAt.AddDate(0, 0, periodDays),
			SourceOrderID: orderID,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		result = &record
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// CountActiveSubscriptions 统计当前有效订阅数，用于后台仪表盘。
func (s *Store) CountActiveSubscriptions() (int64, error) {
	var count int64
	err := s.db.Model(&BillingSubscription{}).
		Where("status = ? AND expires_at > ?", SubscriptionStatusActive, s.clock()).Count(&count).Error
	return count, err
}

// ---------- 订单 ----------

func (s *Store) CreateBillingOrder(order *BillingOrder) error {
	if order == nil {
		return errors.New("auth: 订单为空")
	}
	now := s.clock()
	if order.ID == "" {
		id, err := newBillingID()
		if err != nil {
			return err
		}
		order.ID = id
	}
	if order.OrderNo == "" {
		orderNo, err := NewBillingOrderNo(now)
		if err != nil {
			return err
		}
		order.OrderNo = orderNo
	}
	if order.CreatedAt.IsZero() {
		order.CreatedAt = now
	}
	order.UpdatedAt = now
	return s.db.Create(order).Error
}

func (s *Store) BillingOrderByID(id string) (*BillingOrder, error) {
	var order BillingOrder
	err := s.db.Where("id = ?", strings.TrimSpace(id)).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func (s *Store) BillingOrderByNo(orderNo string) (*BillingOrder, error) {
	var order BillingOrder
	err := s.db.Where("order_no = ?", strings.TrimSpace(orderNo)).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func (s *Store) SaveBillingOrder(order *BillingOrder) error {
	if order == nil {
		return errors.New("auth: 订单为空")
	}
	order.UpdatedAt = s.clock()
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"status", "provider", "provider_order_no", "paid_at", "remark", "updated_at",
		}),
	}).Create(order).Error
}

func (s *Store) BillingOrders(filter BillingOrderFilter) ([]BillingOrder, int64, error) {
	query := s.billingOrderQuery(filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	pageSize := filter.PageSize
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	page := filter.Page
	if page <= 0 {
		page = 1
	}
	var orders []BillingOrder
	if err := query.Order("created_at desc").Limit(pageSize).Offset((page - 1) * pageSize).Find(&orders).Error; err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

func (s *Store) billingOrderQuery(filter BillingOrderFilter) *gorm.DB {
	query := s.db.Model(&BillingOrder{})
	if status := strings.TrimSpace(filter.Status); status != "" {
		query = query.Where("status = ?", status)
	}
	if planCode := strings.TrimSpace(filter.PlanCode); planCode != "" {
		query = query.Where("plan_code = ?", planCode)
	}
	if userID := strings.TrimSpace(filter.UserID); userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("order_no LIKE ? OR user_id LIKE ?", like, like)
	}
	return query
}

// BillingRevenue 汇总一段时间内的收款情况。
//
// 只统计已支付订单：把待支付也加进来会让"今天的营收"随用户点开支付页而波动。
func (s *Store) BillingRevenue(since time.Time) (BillingRevenue, error) {
	var revenue BillingRevenue
	if err := s.db.Model(&BillingOrder{}).
		Where("status = ? AND paid_at >= ?", OrderStatusPaid, since).
		Select("COUNT(*) as paid_orders, COALESCE(SUM(payable_fen), 0) as paid_amount_fen").
		Scan(&revenue).Error; err != nil {
		return BillingRevenue{}, err
	}
	if err := s.db.Model(&BillingOrder{}).Where("status = ?", OrderStatusPending).
		Count(&revenue.PendingOrders).Error; err != nil {
		return BillingRevenue{}, err
	}
	// 独立承接退款汇总：GORM 的 Scan 会整体重置目标结构体，复用 revenue 会把上面
	// 查出来的已付订单数与金额一起清零（后台因此会长期显示营收为零）。
	var refunded struct {
		RefundedFen int64 `gorm:"column:refunded_fen"`
	}
	if err := s.db.Model(&BillingOrder{}).Where("status = ? AND paid_at >= ?", OrderStatusRefunded, since).
		Select("COALESCE(SUM(payable_fen), 0) as refunded_fen").Scan(&refunded).Error; err != nil {
		return BillingRevenue{}, err
	}
	revenue.RefundedFen = refunded.RefundedFen
	return revenue, nil
}

func (s *Store) CountBillingOrders(since time.Time) (int64, error) {
	var count int64
	err := s.db.Model(&BillingOrder{}).Where("created_at >= ?", since).Count(&count).Error
	return count, err
}

// QuoteBillingCoupon 在库层完成一次优惠券试算：读券、校验可用性、算抵扣。
//
// 放在库层而不是单纯的数学函数里，是因为"这个账号还能不能用这张券"要查核销记录；
// 试算与真正下单必须走同一段逻辑，否则会出现"试算 8 折、下单原价"的客诉。
func (s *Store) QuoteBillingCoupon(userID string, couponCode string, amountFen int64) (*BillingCoupon, int64, error) {
	code := NormalizeBillingCouponCode(couponCode)
	if code == "" {
		return nil, 0, nil
	}
	coupon, err := s.BillingCouponByCode(code)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, 0, invalidArgument("优惠券不存在")
		}
		return nil, 0, err
	}
	now := s.clock()
	if err := BillingCouponUsable(*coupon, amountFen, now); err != nil {
		return coupon, 0, err
	}
	if coupon.PerUserLimit > 0 {
		used, err := s.CouponUsedByUser(coupon.ID, userID)
		if err != nil {
			return coupon, 0, err
		}
		if used >= int64(coupon.PerUserLimit) {
			return coupon, 0, conflict("该优惠券你已使用过")
		}
	}
	return coupon, BillingCouponDiscount(*coupon, amountFen), nil
}

// ExpireStaleOrders 把超过支付时限的待支付订单标记为已取消。
//
// 不做这件事的话，用户放弃支付后订单会永远停在 PENDING，后台的"待处理"列表很快就
// 失去意义（也容易和真正的掉单混在一起）。
func (s *Store) ExpireStaleOrders(now time.Time, limit int) (int, error) {
	closed, err := s.CloseStaleOrders(now, limit)
	if err != nil {
		return len(closed), err
	}
	return len(closed), nil
}

// CloseStaleOrders 与 ExpireStaleOrders 同义，但把被关闭的订单回给调用方。
//
// 调用方必须拿到这些订单：关闭超时订单的同时要按订单归还它占用的优惠券，只回一个计数
// 会让券永久留在"已核销"上——这也是为什么这里不能只是把计数换成订单数。
func (s *Store) CloseStaleOrders(now time.Time, limit int) ([]BillingOrder, error) {
	if limit <= 0 {
		limit = 200
	}
	var stale []BillingOrder
	if err := s.db.Where("status = ? AND expires_at < ?", OrderStatusPending, now).
		Order("created_at asc").Limit(limit).Find(&stale).Error; err != nil {
		return nil, err
	}
	closed := make([]BillingOrder, 0, len(stale))
	for index := range stale {
		stale[index].Status = OrderStatusCanceled
		stale[index].Remark = "支付超时自动关闭"
		if err := s.SaveBillingOrder(&stale[index]); err != nil {
			return closed, err
		}
		closed = append(closed, stale[index])
	}
	return closed, nil
}

// ---------- 优惠券 ----------

func (s *Store) BillingCoupons(onlyEnabled bool) ([]BillingCoupon, error) {
	query := s.db.Model(&BillingCoupon{})
	if onlyEnabled {
		query = query.Where("enabled = ?", true)
	}
	var coupons []BillingCoupon
	if err := query.Order("created_at desc").Find(&coupons).Error; err != nil {
		return nil, err
	}
	return coupons, nil
}

func (s *Store) BillingCouponByID(id string) (*BillingCoupon, error) {
	var coupon BillingCoupon
	err := s.db.Where("id = ?", strings.TrimSpace(id)).First(&coupon).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &coupon, nil
}

// BillingCouponByCode 按券码查找。券码大小写不敏感：用户手输时不该因为大小写失败。
func (s *Store) BillingCouponByCode(code string) (*BillingCoupon, error) {
	var coupon BillingCoupon
	err := s.db.Where("UPPER(code) = ?", strings.ToUpper(strings.TrimSpace(code))).First(&coupon).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &coupon, nil
}

func (s *Store) SaveBillingCoupon(coupon *BillingCoupon) error {
	if coupon == nil {
		return errors.New("auth: 优惠券为空")
	}
	now := s.clock()
	if coupon.ID == "" {
		id, err := newBillingID()
		if err != nil {
			return err
		}
		coupon.ID = id
	}
	if coupon.CreatedAt.IsZero() {
		coupon.CreatedAt = now
	}
	coupon.UpdatedAt = now
	// used_count 不进更新列表：核销计数只能由 RedeemCoupon 在事务里改，后台覆盖写入会
	// 让"发出去多少张"和"实际用了多少张"失去对账依据。
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"code", "name", "kind", "value", "min_amount_fen", "total_quota",
			"per_user_limit", "starts_at", "expires_at", "enabled", "updated_at",
		}),
	}).Create(coupon).Error
}

func (s *Store) DeleteBillingCoupon(id string) error {
	return s.db.Where("id = ?", strings.TrimSpace(id)).Delete(&BillingCoupon{}).Error
}

// CouponUsedByUser 返回某账号对某张券已核销的次数，用于校验每人限领。
func (s *Store) CouponUsedByUser(couponID string, userID string) (int64, error) {
	var count int64
	err := s.db.Model(&BillingCouponRedemption{}).
		Where("coupon_id = ? AND user_id = ?", strings.TrimSpace(couponID), strings.TrimSpace(userID)).Count(&count).Error
	return count, err
}

func (s *Store) CouponRedemptions(filter CouponRedemptionFilter) ([]BillingCouponRedemption, int64, error) {
	query := s.db.Model(&BillingCouponRedemption{})
	if couponID := strings.TrimSpace(filter.CouponID); couponID != "" {
		query = query.Where("coupon_id = ?", couponID)
	}
	if userID := strings.TrimSpace(filter.UserID); userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	pageSize := filter.PageSize
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	page := filter.Page
	if page <= 0 {
		page = 1
	}
	var records []BillingCouponRedemption
	if err := query.Order("redeemed_at desc").Limit(pageSize).Offset((page - 1) * pageSize).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

// RedeemCoupon 在一个事务里核销优惠券：占用额度 + 写留痕。
//
// 额度判断必须放在写语句里（而不是先读后判断），否则并发下单会出现"最后一张券被两个
// 订单同时用掉"。这里用带条件的 UPDATE 让数据库来做这件事。
func (s *Store) RedeemCoupon(coupon BillingCoupon, userID string, orderID string, discountFen int64) error {
	now := s.clock()
	id, err := newBillingID()
	if err != nil {
		return err
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		// 每人限领在试算里查过一遍，但试算与核销之间没有排他性：并发提交同一张限领券
		// 时两笔都能通过试算。这里在事务内再数一次自己的核销记录，把额度判断放到
		// 与自增同一个事务里（SQLite 的写事务串行，数据库层面不会再出现两次都读到 0）。
		uid := strings.TrimSpace(userID)
		if coupon.PerUserLimit > 0 {
			var used int64
			if err := tx.Model(&BillingCouponRedemption{}).
				Where("coupon_id = ? AND user_id = ?", coupon.ID, uid).Count(&used).Error; err != nil {
				return err
			}
			if used >= int64(coupon.PerUserLimit) {
				return conflict("该优惠券每人限领 " + strconv.Itoa(coupon.PerUserLimit) + " 张，已用完")
			}
		}
		update := tx.Model(&BillingCoupon{}).Where("id = ? AND enabled = ?", coupon.ID, true)
		if coupon.TotalQuota > 0 {
			update = update.Where("used_count < ?", coupon.TotalQuota)
		}
		result := update.UpdateColumns(map[string]any{
			"used_count": gorm.Expr("used_count + 1"),
			"updated_at": now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return conflict("优惠券已领完，请换一张")
		}
		return tx.Create(&BillingCouponRedemption{
			ID:          id,
			CouponID:    coupon.ID,
			CouponCode:  coupon.Code,
			UserID:      uid,
			OrderID:     strings.TrimSpace(orderID),
			DiscountFen: discountFen,
			RedeemedAt:  now,
		}).Error
	})
}

// ReleaseCouponByOrder 在订单取消或退款时按订单精确回滚核销记录。
//
// 按订单回滚而不是"减一"：一个账号可能同时有多张券在用，减一很可能把别人的额度还错。
func (s *Store) ReleaseCouponByOrder(orderID string) error {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return nil
	}
	now := s.clock()
	return s.db.Transaction(func(tx *gorm.DB) error {
		var records []BillingCouponRedemption
		if err := tx.Where("order_id = ?", orderID).Find(&records).Error; err != nil {
			return err
		}
		if len(records) == 0 {
			return nil
		}
		for _, record := range records {
			if err := tx.Model(&BillingCoupon{}).Where("id = ? AND used_count > 0", record.CouponID).
				UpdateColumns(map[string]any{
					"used_count": gorm.Expr("used_count - 1"),
					"updated_at": now,
				}).Error; err != nil {
				return err
			}
		}
		return tx.Where("order_id = ?", orderID).Delete(&BillingCouponRedemption{}).Error
	})
}

// ---------- 支付渠道配置 ----------

func (s *Store) PaymentConfig(channel string) (*BillingPaymentConfig, error) {
	var record BillingPaymentConfig
	err := s.db.Where("channel = ?", strings.TrimSpace(channel)).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Store) SavePaymentConfig(record *BillingPaymentConfig) error {
	if record == nil {
		return errors.New("auth: 支付渠道配置为空")
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = s.clock()
	}
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "channel"}},
		DoUpdates: clause.AssignmentColumns([]string{"config_json", "enabled", "updated_at", "updated_by"}),
	}).Create(record).Error
}
