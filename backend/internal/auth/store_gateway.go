package auth

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 网关配置的读写。密文进出，明文只在服务层短暂存在。

// GatewayConfig 读取一个通道的配置，未配置时返回 ErrNotFound。
func (s *Store) GatewayConfig(channel string) (*GatewayConfig, error) {
	var record GatewayConfig
	err := s.db.Where("channel = ?", channel).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// SaveGatewayConfig 写入（或覆盖）一个通道的配置。
//
// 覆盖而不是追加：网关是"当前生效的那一份"，历史密钥没有留档价值，留着反而是
// 一份多余的凭据副本。
func (s *Store) SaveGatewayConfig(record *GatewayConfig) error {
	if record == nil {
		return errors.New("auth: 网关配置为空")
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = s.clock()
	}
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "channel"}},
		DoUpdates: clause.AssignmentColumns([]string{"config_json", "enabled", "updated_at", "updated_by"}),
	}).Create(record).Error
}
