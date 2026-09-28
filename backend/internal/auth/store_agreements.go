package auth

import (
	"errors"
	"strings"

	"gorm.io/gorm"
)

// 协议版本与签署记录的读写。
//
// 与其它 store 一致：这里只做查询与持久化，"该不该允许多签一版""影响多少账号"
// 这类判断留在 Service。

// AgreementSignatureFilter 是签署记录的筛选条件。
type AgreementSignatureFilter struct {
	Version       string
	AgreementType string
	Keyword       string
	Page          int
	Limit         int
}

// CurrentAgreementVersion 返回当前生效的协议版本。
//
// 表为空时返回 ErrNotFound，由服务层回落到内置的骨架版本：全新装好的实例也要能
// 打开注册页，不能因为运营还没发过一版就谁也注册不了。
func (s *Store) CurrentAgreementVersion() (*AgreementVersion, error) {
	var record AgreementVersion
	err := s.db.Order("published_at DESC, version DESC").First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// AgreementVersions 按发布时间倒序返回历史版本。
func (s *Store) AgreementVersions(limit int) ([]AgreementVersion, error) {
	query := s.db.Order("published_at DESC, version DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	var records []AgreementVersion
	if err := query.Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}

// AgreementVersionByVersion 读取指定版本。
func (s *Store) AgreementVersionByVersion(version string) (*AgreementVersion, error) {
	var record AgreementVersion
	err := s.db.Where("version = ?", strings.TrimSpace(version)).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// CreateAgreementVersion 追加一版协议。
//
// 版本号由服务层生成并保证唯一，这里不做 upsert：覆盖历史版本会让已经落库的
// 签署记录指向一份用户从未见过的正文。
func (s *Store) CreateAgreementVersion(record *AgreementVersion) error {
	if record == nil {
		return errors.New("auth: 协议版本为空")
	}
	now := s.clock()
	if record.PublishedAt.IsZero() {
		record.PublishedAt = now
	}
	return s.db.Create(record).Error
}

// AgreementVersionExists 判断版本号是否已被占用。
func (s *Store) AgreementVersionExists(version string) (bool, error) {
	var count int64
	if err := s.db.Model(&AgreementVersion{}).Where("version = ?", strings.TrimSpace(version)).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// AcceptedAgreementVersions 返回某账号已接受的各协议的最新一条记录。
//
// 以 agreement_type 为键取最新一条：同一类型可能有多版签署记录，判断"是否还要
// 重新同意"只关心最近一次。
func (s *Store) AcceptedAgreementVersions(userID string) (map[string]UserAgreement, error) {
	var records []UserAgreement
	if err := s.db.Where("user_id = ?", strings.TrimSpace(userID)).
		Order("accepted_at DESC, created_at DESC").Find(&records).Error; err != nil {
		return nil, err
	}
	result := make(map[string]UserAgreement, len(records))
	for _, record := range records {
		if _, exists := result[record.AgreementType]; exists {
			continue
		}
		result[record.AgreementType] = record
	}
	return result, nil
}

// CountUsersPendingAgreement 统计还没有接受指定版本的账号数，用于发布前提示影响面。
func (s *Store) CountUsersPendingAgreement(version string, agreementType string) (int64, error) {
	var total int64
	if err := s.db.Model(&User{}).Count(&total).Error; err != nil {
		return 0, err
	}
	var signed int64
	if err := s.db.Model(&UserAgreement{}).
		Where("agreement_type = ? AND version = ?", agreementType, strings.TrimSpace(version)).
		Distinct("user_id").Count(&signed).Error; err != nil {
		return 0, err
	}
	if signed > total {
		return 0, nil
	}
	return total - signed, nil
}

// AgreementSignaturePage 分页返回签署记录（带账号标识）。
func (s *Store) AgreementSignaturePage(filter AgreementSignatureFilter) ([]AgreementSignatureRow, int64, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	limit := filter.Limit
	if limit < 1 {
		limit = 20
	}
	query := s.db.Table("app_user_agreements AS agreements").
		Joins("LEFT JOIN app_users AS users ON users.id = agreements.user_id")
	if version := strings.TrimSpace(filter.Version); version != "" {
		query = query.Where("agreements.version = ?", version)
	}
	if agreementType := strings.TrimSpace(filter.AgreementType); agreementType != "" {
		query = query.Where("agreements.agreement_type = ?", agreementType)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("users.email LIKE ? OR users.phone LIKE ? OR users.name LIKE ? OR agreements.user_id LIKE ?", like, like, like, like)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []AgreementSignatureRow
	err := query.Select(`agreements.id AS id, agreements.user_id AS user_id,
			COALESCE(users.email, '') AS email, COALESCE(users.phone, '') AS phone, COALESCE(users.name, '') AS name,
			agreements.agreement_type AS agreement_type, agreements.version AS version,
			agreements.accepted_at AS accepted_at, COALESCE(agreements.ip_address, '') AS ip_address,
			COALESCE(agreements.user_agent, '') AS user_agent`).
		Order("agreements.accepted_at DESC, agreements.id DESC").
		Offset((page - 1) * limit).Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// LatestAgreementVersionString 取当前版本的版本串，表为空时返回空串。
func (s *Store) LatestAgreementVersionString() (string, error) {
	record, err := s.CurrentAgreementVersion()
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return record.Version, nil
}
