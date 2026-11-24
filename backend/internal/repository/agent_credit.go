package repository

import (
	"errors"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"infinite-canvas/backend/internal/model"

	"github.com/google/uuid"
)

var (
	// ErrAgentBudgetExceeded 表示本次预留会突破 Run 或当日的授权额度。
	ErrAgentBudgetExceeded = errors.New("agent credit budget exceeded")
	// ErrAgentGrantRevoked 表示授权被撤销。
	ErrAgentGrantRevoked = errors.New("agent grant revoked")
	// ErrAgentGrantStale 表示授权在 Run 开始后被修改，旧 Run 不能继续使用过期权限。
	ErrAgentGrantStale = errors.New("agent grant version changed")
)

// agentCreditReserveMu 只串行化本进程内的预留。
//
// 真正的并发闸门是下面那条带条件的 UPDATE；这把锁是为了让 SQLite 在测试与
// 单机部署里不必处理"读快照后升级写锁"的 BUSY，避免把一个额度问题表现成
// 偶发的 database is locked。多进程部署仍由数据库条件更新兜底。
var agentCreditReserveMu sync.Mutex

// AgentCreditReserve 是一次任务提交前的额度预留请求。
type AgentCreditReserve struct {
	UserID  string
	GrantID string
	// GrantVersion 为 0 表示这次 Run 不挂在 Grant 上（例如网页端只带单轮预算）。
	GrantVersion     int64
	RunID            string
	TaskID           string
	Credits          int64
	MaxCreditsPerRun int64
	MaxCreditsPerDay int64
	Now              time.Time
}

// AgentGrantForToken 读取令牌绑定的授权；老令牌第一次使用时补建默认授权。
//
// 这样已经发出去的 MCP 令牌不会因为升级后"没有 Grant"而全部失效，同时新令牌
// 从第一天起就受同一套额度约束。
func (r *Repository) AgentGrantForToken(userID, tokenID string) (*model.AgentGrant, error) {
	tokenID = strings.TrimSpace(tokenID)
	if tokenID == "" {
		return nil, nil
	}
	var token model.MCPToken
	if err := r.db.First(&token, "id = ? AND user_id = ?", tokenID, userID).Error; err != nil {
		return nil, err
	}
	if token.GrantID != "" {
		return r.agentGrant(userID, token.GrantID)
	}
	now := time.Now().UTC()
	grant := model.AgentGrant{
		ID:               uuid.NewString(),
		UserID:           userID,
		Client:           model.AgentGrantClientMCP,
		ScopesJSON:       token.ScopesJSON,
		PermissionMode:   "auto",
		MaxCreditsPerRun: model.AgentGrantDefaultMaxCreditsPerRun,
		MaxCreditsPerDay: model.AgentGrantDefaultMaxCreditsPerDay,
		AutoRetryEnabled: false,
		Status:           model.AgentGrantStatusActive,
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&grant).Error; err != nil {
			return err
		}
		// 只有抢到"仍未绑定"的那一次更新才算自己的授权；并发的另一个请求会
		// 读到已绑定的值并沿用它，避免一个令牌底下出现两份额度。
		result := tx.Model(&model.MCPToken{}).
			Where("id = ? AND user_id = ? AND (grant_id IS NULL OR grant_id = '')", tokenID, userID).
			Update("grant_id", grant.ID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errAgentGrantBindRace
		}
		return nil
	})
	if errors.Is(err, errAgentGrantBindRace) {
		if err := r.db.First(&token, "id = ? AND user_id = ?", tokenID, userID).Error; err != nil {
			return nil, err
		}
		if token.GrantID != "" {
			return r.agentGrant(userID, token.GrantID)
		}
	}
	if err != nil {
		return nil, err
	}
	return &grant, nil
}

var errAgentGrantBindRace = errors.New("agent grant binding raced")

func (r *Repository) agentGrant(userID, grantID string) (*model.AgentGrant, error) {
	var grant model.AgentGrant
	if err := r.db.First(&grant, "id = ? AND user_id = ?", grantID, userID).Error; err != nil {
		return nil, err
	}
	return &grant, nil
}

// AgentGrant 读取一个属于该用户的授权。
func (r *Repository) AgentGrant(userID, grantID string) (*model.AgentGrant, error) {
	return r.agentGrant(userID, grantID)
}

// RevokeAgentGrant 撤销授权并递增版本号。
func (r *Repository) RevokeAgentGrant(userID, grantID string) error {
	now := time.Now().UTC()
	return r.db.Model(&model.AgentGrant{}).
		Where("id = ? AND user_id = ? AND status = ?", grantID, userID, model.AgentGrantStatusActive).
		Updates(map[string]any{
			"status":     model.AgentGrantStatusRevoked,
			"revoked_at": now,
			"updated_at": now,
			"version":    gorm.Expr("version + 1"),
		}).Error
}

// ReserveAgentCredits 在一个事务里原子占用 Run 与当日两个额度桶。
//
// 顺序刻意是"先插预留行，再动桶"：TaskID 唯一索引同时充当幂等键——重放同一个
// 任务时直接返回成功，不会把额度算两遍。桶的检查与扣减在同一条 UPDATE 的 WHERE
// 里完成，不存在"先读后写"的竞态窗口。
func (r *Repository) ReserveAgentCredits(request AgentCreditReserve) error {
	if request.Credits <= 0 {
		return nil
	}
	if strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.TaskID) == "" {
		return errors.New("repository: agent credit reservation needs run and task")
	}
	now := request.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	agentCreditReserveMu.Lock()
	defer agentCreditReserveMu.Unlock()
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := r.releaseExpiredReservations(tx, request.UserID, now); err != nil {
			return err
		}
		runLimit := request.MaxCreditsPerRun
		dayLimit := request.MaxCreditsPerDay
		if request.GrantID != "" {
			var grant model.AgentGrant
			if err := tx.First(&grant, "id = ? AND user_id = ?", request.GrantID, request.UserID).Error; err != nil {
				return err
			}
			if grant.Status != model.AgentGrantStatusActive {
				return ErrAgentGrantRevoked
			}
			if request.GrantVersion > 0 && grant.Version != request.GrantVersion {
				return ErrAgentGrantStale
			}
			runLimit = minCreditsLimit(runLimit, grant.MaxCreditsPerRun)
			dayLimit = minCreditsLimit(dayLimit, grant.MaxCreditsPerDay)
		}
		if runLimit <= 0 {
			return ErrAgentBudgetExceeded
		}
		reservation := model.AgentCreditReservation{
			ID:              uuid.NewString(),
			GrantID:         request.GrantID,
			UserID:          request.UserID,
			RunID:           request.RunID,
			TaskID:          request.TaskID,
			Day:             model.AgentCreditDay(now),
			ReservedCredits: request.Credits,
			Status:          model.AgentReservationReserved,
			ExpiresAt:       now.Add(15 * time.Minute),
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		inserted := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&reservation)
		if inserted.Error != nil {
			return inserted.Error
		}
		if inserted.RowsAffected == 0 {
			// 同一个任务已经预留过：幂等命中，不再动桶。
			return nil
		}
		if err := reserveAgentBucket(tx, model.AgentCreditScopeRun, request.RunID, "all", request.Credits, runLimit); err != nil {
			return err
		}
		if dayLimit > 0 {
			if err := reserveAgentBucket(tx, model.AgentCreditScopeDaily, request.UserID, reservation.Day, request.Credits, dayLimit); err != nil {
				return err
			}
		}
		return nil
	})
}

// SettleAgentReservation 把预留标记为已收费；文本结算补的差额也在这里计入桶。
//
// 差额发生在钱已经花掉之后，不能再拒绝，只能让后续任务看到更高的已用额度。
func (r *Repository) SettleAgentReservation(userID, taskID string, charged int64) error {
	if strings.TrimSpace(taskID) == "" || charged < 0 {
		return nil
	}
	agentCreditReserveMu.Lock()
	defer agentCreditReserveMu.Unlock()
	return r.db.Transaction(func(tx *gorm.DB) error {
		var reservation model.AgentCreditReservation
		if err := tx.First(&reservation, "task_id = ? AND user_id = ?", taskID, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if reservation.Status == model.AgentReservationReleased {
			return nil
		}
		// 桶里当前占的是 ReservedCredits；首次结算时 SettledCredits 还是 0。
		// 用 SettledCredits 作基准会把已预留的金额再加一遍，等于每张图占双倍额度。
		// 后续文本补扣时 ReservedCredits 已被抬到上次的成交额，因此同一个公式
		// 也能正确处理"起步价 30、实际 45"这类差额结算。
		delta := charged - reservation.ReservedCredits
		update := map[string]any{
			"settled_credits": charged,
			"status":          model.AgentReservationCharged,
			"updated_at":      time.Now().UTC(),
		}
		if delta < 0 {
			// 实际成交价低于预留：把差额还给桶，避免额度被多占。
			release := -delta
			update["reserved_credits"] = gorm.Expr("CASE WHEN reserved_credits >= ? THEN reserved_credits - ? ELSE 0 END", release, release)
			if err := adjustAgentBuckets(tx, &reservation, -release); err != nil {
				return err
			}
		}
		if delta > 0 {
			// 补扣的差额没有再次做上限校验：钱已经花掉，拒绝只会让账实更不一致。
			if err := adjustAgentBuckets(tx, &reservation, delta); err != nil {
				return err
			}
			update["reserved_credits"] = gorm.Expr("reserved_credits + ?", delta)
		}
		result := tx.Model(&model.AgentCreditReservation{}).
			Where("id = ? AND status <> ?", reservation.ID, model.AgentReservationReleased).
			Updates(update)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			// 并发释放已经先一步把额度还回去；不能再把桶加回来。
			return nil
		}
		return nil
	})
}

// ReleaseAgentReservation 在退款/取消后释放一次预留，幂等。
func (r *Repository) ReleaseAgentReservation(userID, taskID string) error {
	if strings.TrimSpace(taskID) == "" {
		return nil
	}
	agentCreditReserveMu.Lock()
	defer agentCreditReserveMu.Unlock()
	return r.db.Transaction(func(tx *gorm.DB) error {
		var reservation model.AgentCreditReservation
		if err := tx.First(&reservation, "task_id = ? AND user_id = ?", taskID, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if reservation.Status == model.AgentReservationReleased {
			return nil
		}
		if err := adjustAgentBuckets(tx, &reservation, -reservation.ReservedCredits); err != nil {
			return err
		}
		return tx.Model(&model.AgentCreditReservation{}).
			Where("id = ? AND status <> ?", reservation.ID, model.AgentReservationReleased).
			Updates(map[string]any{"status": model.AgentReservationReleased, "updated_at": time.Now().UTC()}).Error
	})
}

func reserveAgentBucket(tx *gorm.DB, scope, key, period string, credits, limit int64) error {
	bucket := model.AgentCreditBucket{
		ID:        scope + ":" + key + ":" + period,
		Scope:     scope,
		ScopeKey:  key,
		Period:    period,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&bucket).Error; err != nil {
		return err
	}
	result := tx.Model(&model.AgentCreditBucket{}).
		Where("scope = ? AND scope_key = ? AND period = ? AND reserved_credits + ? <= ?", scope, key, period, credits, limit).
		Updates(map[string]any{
			"reserved_credits": gorm.Expr("reserved_credits + ?", credits),
			"updated_at":       time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAgentBudgetExceeded
	}
	return nil
}

func adjustAgentBuckets(tx *gorm.DB, reservation *model.AgentCreditReservation, delta int64) error {
	if reservation == nil || delta == 0 {
		return nil
	}
	if err := adjustAgentBucket(tx, model.AgentCreditScopeRun, reservation.RunID, "all", delta); err != nil {
		return err
	}
	return adjustAgentBucket(tx, model.AgentCreditScopeDaily, reservation.UserID, reservation.Day, delta)
}

func adjustAgentBucket(tx *gorm.DB, scope, key, period string, delta int64) error {
	result := tx.Model(&model.AgentCreditBucket{}).Where("scope = ? AND scope_key = ? AND period = ?", scope, key, period)
	if delta < 0 {
		release := -delta
		return result.Update("reserved_credits", gorm.Expr("CASE WHEN reserved_credits >= ? THEN reserved_credits - ? ELSE 0 END", release, release)).Error
	}
	return result.Update("reserved_credits", gorm.Expr("reserved_credits + ?", delta)).Error
}

// releaseExpiredReservations 回收"预留了但从未收费"的孤儿。
//
// 只回收 still reserved 的行：已收费的任务不能因为时间过去就被认为没花钱，
// 否则长视频跑完后额度会凭空恢复，下一次提交就能超额。收费在预留后立刻发生，
// 因此 15 分钟足够覆盖进程在两步之间被杀死的情况。
func (r *Repository) releaseExpiredReservations(tx *gorm.DB, userID string, now time.Time) error {
	var stale []model.AgentCreditReservation
	if err := tx.Where("user_id = ? AND status = ? AND expires_at < ?", userID, model.AgentReservationReserved, now).
		Limit(100).Find(&stale).Error; err != nil {
		return err
	}
	for index := range stale {
		reservation := stale[index]
		if err := adjustAgentBuckets(tx, &reservation, -reservation.ReservedCredits); err != nil {
			return err
		}
		if err := tx.Model(&model.AgentCreditReservation{}).
			Where("id = ? AND status = ?", reservation.ID, model.AgentReservationReserved).
			Updates(map[string]any{"status": model.AgentReservationReleased, "updated_at": now}).Error; err != nil {
			return err
		}
	}
	return nil
}

func minCreditsLimit(a, b int64) int64 {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

// CreateAgentBudgetRequest 记录一次 Agent 主动提出的加额申请。
//
// 它不改 Grant，也不产生任何"已批准"状态；批准只能走用户会话接口。
func (r *Repository) CreateAgentBudgetRequest(request model.AgentBudgetRequest) (*model.AgentBudgetRequest, error) {
	if request.ID == "" {
		request.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	request.Status = model.AgentBudgetRequestPending
	request.CreatedAt, request.UpdatedAt = now, now
	if err := r.db.Create(&request).Error; err != nil {
		return nil, err
	}
	return &request, nil
}

// ApproveAgentBudgetRequest 由用户批准加额申请：提高 Grant 的 Run 额度与每日池，
// 并递增版本。
//
// 旧 Run 会因为 GrantVersion 变化而在下一次预留时被拒绝；这是有意的——用户改了
// 授权，旧 Run 不能继续用旧边界。批准后 Agent 需要开新一轮继续创作。
func (r *Repository) ApproveAgentBudgetRequest(userID, requestID string) (*model.AgentGrant, error) {
	agentCreditReserveMu.Lock()
	defer agentCreditReserveMu.Unlock()
	var grant *model.AgentGrant
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var request model.AgentBudgetRequest
		if err := tx.First(&request, "id = ? AND user_id = ?", requestID, userID).Error; err != nil {
			return err
		}
		if request.Status != model.AgentBudgetRequestPending {
			return errors.New("repository: budget request already decided")
		}
		var current model.AgentGrant
		if err := tx.First(&current, "id = ? AND user_id = ?", request.GrantID, userID).Error; err != nil {
			return err
		}
		if current.Status != model.AgentGrantStatusActive {
			return ErrAgentGrantRevoked
		}
		now := time.Now().UTC()
		raisedRun := maxCreditsValue(current.MaxCreditsPerRun, request.RequestedCredits)
		raisedDay := current.MaxCreditsPerDay
		// 单轮额度提高多少，每日池就同步增加多少，保持批准前的跨 Run 余量。
		// 每日上限为 0 表示没有日封顶，不能因为一次批准反而给它套上一个小上限。
		if delta := raisedRun - current.MaxCreditsPerRun; delta > 0 && raisedDay > 0 {
			raisedDay += delta
		}
		if err := tx.Model(&model.AgentGrant{}).Where("id = ?", current.ID).Updates(map[string]any{
			"max_credits_per_run": raisedRun,
			"max_credits_per_day": raisedDay,
			"version":             gorm.Expr("version + 1"),
			"updated_at":          now,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.AgentBudgetRequest{}).Where("id = ?", request.ID).Updates(map[string]any{
			"status": "approved", "decided_at": now, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		var updated model.AgentGrant
		if err := tx.First(&updated, "id = ?", current.ID).Error; err != nil {
			return err
		}
		grant = &updated
		return nil
	})
	return grant, err
}

func maxCreditsValue(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// AgentBudgetRequests 返回用户最近的加额申请。
func (r *Repository) AgentBudgetRequests(userID string, limit int) ([]model.AgentBudgetRequest, error) {
	var requests []model.AgentBudgetRequest
	err := r.db.Where("user_id = ?", userID).Order("created_at desc").Limit(limit).Find(&requests).Error
	return requests, err
}
