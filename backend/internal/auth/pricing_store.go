package auth

import (
	"errors"
	"fmt"
	"strings"

	"infinite-canvas/backend/internal/kernel"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 模型定价的读写层与服务层。
//
// store 只做"查/写 + 事务边界"，service 只做"校验 + 编排"：单价的合法区间、倍率的
// 覆盖顺序各自只实现一份——规则写两遍，后台保存与试算就会各按一套判定走，售价随之
// 出现只有某个入口才看得见的差异。

// EnsurePricingSchema 仅在本地 SQLite 上创建定价表。
//
// 与 EnsureDevSchema / EnsureRbacSchema 相同的驱动白名单：生产库的表结构由 CanvasMind
// 的 Prisma 迁移管理，让 GORM 按自己的类型推断碰到共享库，可能改写真实 DDL。
func EnsurePricingSchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("auth: 数据库连接为空")
	}
	if name := db.Dialector.Name(); name != "sqlite" {
		return fmt.Errorf("auth: 拒绝为 %s 驱动创建定价表；该库的表结构由 Prisma 迁移管理", name)
	}
	if err := db.AutoMigrate(PricingModels()...); err != nil {
		return err
	}
	// 唯一索引必须在这里用幂等 SQL 再声明一次，不能只靠结构体标签：AutoMigrate 对已存在
	// 的表只在它自己确认缺索引时才补，而保存走的是 ON CONFLICT 的 upsert——索引一旦漏建，
	// 读取会直接报 "ON CONFLICT clause does not match any PRIMARY KEY or UNIQUE constraint"。
	// 索引名与结构体标签保持一致，重复建表时这里是 no-op。
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_billing_model_prices_model_capability ON billing_model_prices (model_key, capability)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_billing_markup_rules_scope_target ON billing_markup_rules (scope, target)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("auth: 创建定价索引失败（%s）: %w", statement, err)
		}
	}
	return nil
}

// ---------- 单价配置 ----------

// ModelPrices 返回全部单价配置（含停用），按模型标识与能力排序。
//
// 排序固定：后台列表每次都要一样，否则"哪几条看过"会随查询计划变化。返回非 nil 切片，
// JSON 里是 [] 而不是 null。
func (s *Store) ModelPrices() ([]ModelPrice, error) {
	var prices []ModelPrice
	if err := s.db.Order("model_key ASC, capability ASC").Find(&prices).Error; err != nil {
		return nil, err
	}
	if prices == nil {
		prices = []ModelPrice{}
	}
	return prices, nil
}

// ModelPriceByID 按主键读取单价配置。
func (s *Store) ModelPriceByID(id string) (*ModelPrice, error) {
	var price ModelPrice
	err := s.db.Where("id = ?", strings.TrimSpace(id)).First(&price).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &price, nil
}

// ModelPriceByKey 按 (model_key, capability) 读取单价配置。
//
// 能力值先归一大写：库里只存大写，查询侧不归一就会出现"刚存进去却查不到"。
func (s *Store) ModelPriceByKey(modelKey string, capability string) (*ModelPrice, error) {
	var price ModelPrice
	err := s.db.Where("model_key = ? AND capability = ?", strings.TrimSpace(modelKey), normalizeModelCapability(capability)).
		First(&price).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &price, nil
}

// SaveModelPrice 按主键写入或更新单价配置。
func (s *Store) SaveModelPrice(price *ModelPrice) error {
	if price == nil {
		return errors.New("auth: 单价配置为空")
	}
	now := s.clock()
	if price.ID == "" {
		price.ID = kernel.NewID()
	}
	if price.CreatedAt.IsZero() {
		price.CreatedAt = now
	}
	price.UpdatedAt = now
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"model_key", "capability", "unit", "vendor_code", "upstream_unit_price",
			"sell_unit_price", "multiplier_bp", "currency", "enabled", "note", "updated_at",
		}),
	}).Create(price).Error
}

// DeleteModelPrice 物理删除一条单价配置。
//
// 这里可以真删：单价配置描述的是"当前口径"，没有任何流水以它做外键，删掉不会让历史
// 账目失去解释（订单与核销记录各自在 billing_* 里留了快照）。
func (s *Store) DeleteModelPrice(id string) error {
	return s.db.Where("id = ?", strings.TrimSpace(id)).Delete(&ModelPrice{}).Error
}

// ---------- 倍率规则 ----------

// MarkupRules 返回全部倍率规则，按作用域与目标排序，列表恒为非 nil。
func (s *Store) MarkupRules() ([]MarkupRule, error) {
	var rules []MarkupRule
	if err := s.db.Order("scope ASC, target ASC").Find(&rules).Error; err != nil {
		return nil, err
	}
	if rules == nil {
		rules = []MarkupRule{}
	}
	return rules, nil
}

// SaveMarkupRule 按主键写入或更新一条倍率规则。
func (s *Store) SaveMarkupRule(rule *MarkupRule) error {
	if rule == nil {
		return errors.New("auth: 倍率规则为空")
	}
	now := s.clock()
	if rule.ID == "" {
		rule.ID = kernel.NewID()
	}
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = now
	}
	rule.UpdatedAt = now
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"scope", "target", "multiplier_bp", "note", "updated_at",
		}),
	}).Create(rule).Error
}

// DeleteMarkupRule 删除一条倍率规则。
func (s *Store) DeleteMarkupRule(id string) error {
	return s.db.Where("id = ?", strings.TrimSpace(id)).Delete(&MarkupRule{}).Error
}

// ReplaceMarkupRules 在事务内整体替换倍率规则。
//
// 全量替换而不是逐条增删：规则集是一份"当前生效的覆盖表"，缺一条就意味着回落到更宽的
// 作用域；逐条增删会让"删掉某条规则"要靠调用方算差集，算错的后果是某类模型悄悄按原价
// 出货。整个替换在一个事务里完成，读到的规则集要么是旧的、要么是新的，不会出现中间态。
func (s *Store) ReplaceMarkupRules(rules []MarkupRule) error {
	// 先做 (scope, target) 去重：唯一索引会在插入时才报错，那时事务已经删掉了旧规则，
	// 而调用方只能拿到一句数据库错误。这里提前拒绝，错误才能带上"哪两条重了"的语义。
	seen := make(map[string]bool, len(rules))
	for _, rule := range rules {
		key := markupRuleKey(rule.Scope, rule.Target)
		if seen[key] {
			return invalidArgument("倍率规则的 scope 与 target 不能重复")
		}
		seen[key] = true
	}
	now := s.clock()
	return s.db.Transaction(func(tx *gorm.DB) error {
		// gorm 默认拒绝无条件删除，这里就是要清空整张规则表，因此显式打开全局更新。
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&MarkupRule{}).Error; err != nil {
			return err
		}
		for _, rule := range rules {
			record := rule
			if record.ID == "" {
				record.ID = kernel.NewID()
			}
			if record.CreatedAt.IsZero() {
				record.CreatedAt = now
			}
			record.UpdatedAt = now
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------- 服务层 ----------

// AdminModelPrices 返回后台的单价配置列表（含停用）。
//
// 含停用配置：运营需要看见历史口径的全貌，"还会不会生效"由 Enabled 表达，而不是从
// 列表里直接藏起来。
func (s *Service) AdminModelPrices() ([]ModelPriceView, error) {
	prices, err := s.store.ModelPrices()
	if err != nil {
		return nil, internalFailure(err)
	}
	views := make([]ModelPriceView, 0, len(prices))
	for _, price := range prices {
		views = append(views, ModelPriceViewOf(price))
	}
	return views, nil
}

// SaveModelPrice 新建或更新一条单价配置，ID 为空表示新建。
//
// 金额字段是 *int64：nil = 还没定价、0 = 免费，两者必须在入参、存储与视图三层都保持
// 可区分。这里不接受负数——负售价要么是填错了符号，要么就是资损。
func (s *Service) SaveModelPrice(input ModelPriceInput) (*ModelPriceView, error) {
	modelKey := strings.TrimSpace(input.ModelKey)
	if modelKey == "" {
		return nil, invalidArgument("请填写模型标识")
	}
	if len([]rune(modelKey)) > 120 {
		// 长度与 channel_models.model_key 对齐：对不上的标识在渠道侧匹配不到任何模型。
		return nil, invalidArgument("模型标识最多 120 个字符")
	}
	capability := normalizeModelCapability(input.Capability)
	if !validModelCapability(capability) {
		return nil, invalidArgument("模型能力只能是 TEXT / IMAGE / VIDEO / AUDIO")
	}
	unit := normalizePriceUnit(input.Unit)
	if unit == "" {
		unit = string(DefaultUnitFor(ModelCapability(capability)))
	}
	if !validPriceUnit(unit) {
		return nil, invalidArgument("计费单位只能是 TOKEN_1K / IMAGE / SECOND / REQUEST")
	}
	if input.UpstreamUnitPrice != nil && *input.UpstreamUnitPrice < 0 {
		return nil, invalidArgument("上游单价不能为负数")
	}
	if input.SellUnitPrice != nil && *input.SellUnitPrice < 0 {
		return nil, invalidArgument("售价不能为负数")
	}
	// 倍率留空表示"跟随规则"：此时写 nil 而不是 10000，好让后续改规则时这条配置能跟着变。
	var multiplierBp *int
	if text := strings.TrimSpace(input.Multiplier); text != "" {
		parsed, err := ParseMultiplierBp(text)
		if err != nil {
			return nil, invalidArgument(err.Error())
		}
		multiplierBp = &parsed
	}
	currency := strings.ToUpper(strings.TrimSpace(input.Currency))
	if currency == "" {
		currency = pricingDefaultCurrency
	}
	if len([]rune(currency)) > 8 {
		return nil, invalidArgument("币种最多 8 个字符")
	}

	targetID := strings.TrimSpace(input.ID)
	price := ModelPrice{}
	enabled := true
	if targetID != "" {
		existing, err := s.store.ModelPriceByID(targetID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				// 包成 404 而不是裸 ErrNotFound：HTTP 层只认 *Error，裸错误会落成 500。
				return nil, notFound("单价配置不存在")
			}
			return nil, internalFailure(err)
		}
		price = *existing
		// 更新时沿用原启用状态与创建时间：审计信息不该被这次保存刷新，漏传 enabled
		// 也不该把一条配置静默停用。
		enabled = existing.Enabled
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	// (model_key, capability) 是唯一键，撞车会让"这个模型按哪个价"失去唯一答案。
	// 先查一次并给出中文冲突提示，而不是把唯一索引的报错译成 500。
	conflicting, err := s.store.ModelPriceByKey(modelKey, capability)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, internalFailure(err)
	}
	if err == nil && conflicting.ID != targetID {
		return nil, conflict("该模型与该能力组合已存在单价配置")
	}

	price.ID = targetID
	price.ModelKey = modelKey
	price.Capability = capability
	price.Unit = unit
	price.VendorCode = strings.TrimSpace(input.VendorCode)
	price.UpstreamUnitPrice = input.UpstreamUnitPrice
	price.SellUnitPrice = input.SellUnitPrice
	price.MultiplierBp = multiplierBp
	price.Currency = currency
	price.Enabled = enabled
	price.Note = strings.TrimSpace(input.Note)

	if err := s.store.SaveModelPrice(&price); err != nil {
		return nil, internalFailure(err)
	}
	view := ModelPriceViewOf(price)
	return &view, nil
}

// DeleteModelPrice 删除一条单价配置。
func (s *Service) DeleteModelPrice(id string) error {
	target := strings.TrimSpace(id)
	if target == "" {
		return invalidArgument("请选择要删除的单价配置")
	}
	if _, err := s.store.ModelPriceByID(target); err != nil {
		if errors.Is(err, ErrNotFound) {
			return notFound("单价配置不存在")
		}
		return internalFailure(err)
	}
	if err := s.store.DeleteModelPrice(target); err != nil {
		return internalFailure(err)
	}
	return nil
}

// AdminMarkupRules 返回当前的倍率规则集。
func (s *Service) AdminMarkupRules() (*MarkupView, error) {
	rules, err := s.store.MarkupRules()
	if err != nil {
		return nil, internalFailure(err)
	}
	return MarkupViewOf(rules), nil
}

// ReplaceMarkupRules 全量替换倍率规则。
//
// 校验失败返回中文可读原因：这些文案会直接出现在后台表单下方，让运营知道该改哪一格。
func (s *Service) ReplaceMarkupRules(input MarkupInput) (*MarkupView, error) {
	rules := make([]MarkupRule, 0, len(input.Rules))
	seen := make(map[string]bool, len(input.Rules))
	for _, item := range input.Rules {
		scope := normalizeMarkupScope(item.Scope)
		if !validMarkupScope(scope) {
			return nil, invalidArgument("倍率规则的作用域只能是 GLOBAL / CAPABILITY / VENDOR / MODEL")
		}
		target := strings.TrimSpace(item.Target)
		switch scope {
		case MarkupScopeCapability:
			target = normalizeModelCapability(target)
			if !validModelCapability(target) {
				return nil, invalidArgument("按能力设置的倍率，目标必须是 TEXT / IMAGE / VIDEO / AUDIO 之一")
			}
		case MarkupScopeVendor, MarkupScopeModel:
			// 目标必填：一条"匹配任意厂商"的倍率规则与全局规则没有区别，却会在覆盖顺序里
			// 压过真正的 GLOBAL 配置，属于看起来生效、实际越权的配置。
			if target == "" {
				return nil, invalidArgument("按厂商或模型设置的倍率必须填写目标")
			}
		default:
			// GLOBAL 的目标恒为空：留一个填不进去的目标，只会让人以为全局规则也能带条件。
			target = ""
		}
		if item.MultiplierBp <= 0 {
			return nil, invalidArgument("倍率必须大于 0（10000 表示不加价）")
		}
		if item.MultiplierBp > markupMaxBp {
			return nil, invalidArgument(errMultiplierRange.Error())
		}
		key := markupRuleKey(scope, target)
		if seen[key] {
			return nil, invalidArgument("倍率规则的 scope 与 target 不能重复")
		}
		seen[key] = true
		rules = append(rules, MarkupRule{
			Scope:        scope,
			Target:       target,
			MultiplierBp: item.MultiplierBp,
			Note:         strings.TrimSpace(item.Note),
		})
	}
	if err := s.store.ReplaceMarkupRules(rules); err != nil {
		// store 的冲突校验返回的是结构化 400，不能被包成 500——那会让"两条规则重了"
		// 显示成"系统处理失败"，运营只能反复重试。
		var serviceErr *Error
		if errors.As(err, &serviceErr) {
			return nil, serviceErr
		}
		return nil, internalFailure(err)
	}
	// 回读落库结果而不是回显入参：归一化（能力大写、GLOBAL 清空目标）与生成的 ID
	// 都以库里的为准，前端拿到的就是下次打开表单时会看到的那份。
	return s.AdminMarkupRules()
}

// PreviewModelPrice 试算某个模型的倍率与售价，供后台表单即时反馈。
//
// 找不到单价配置不是错误：那正是"还没定价"的常态，此时用规则集算出的倍率仍然有参考
// 价值（运营据此知道该按几倍去填上游价）。
func (s *Service) PreviewModelPrice(input PricingInput) (*PricingResolution, error) {
	capability := normalizeModelCapability(input.Capability)
	if capability != "" && !validModelCapability(capability) {
		return nil, invalidArgument("模型能力只能是 TEXT / IMAGE / VIDEO / AUDIO")
	}
	if input.UpstreamUnitPrice != nil && *input.UpstreamUnitPrice < 0 {
		return nil, invalidArgument("上游单价不能为负数")
	}

	// 只有模型标识与能力都给了才去查单价：(model_key, capability) 才是唯一键，
	// 少了能力就无法确定"这个模型按哪个价"。
	var price *ModelPrice
	modelKey := strings.TrimSpace(input.ModelKey)
	if modelKey != "" && capability != "" {
		found, err := s.store.ModelPriceByKey(modelKey, capability)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, internalFailure(err)
		}
		if err == nil {
			price = found
		}
	}
	rules, err := s.store.MarkupRules()
	if err != nil {
		return nil, internalFailure(err)
	}
	resolution := ResolvePricing(PricingInput{
		ModelKey:          modelKey,
		VendorCode:        strings.TrimSpace(input.VendorCode),
		Capability:        capability,
		UpstreamUnitPrice: input.UpstreamUnitPrice,
	}, price, rules)
	return &resolution, nil
}
