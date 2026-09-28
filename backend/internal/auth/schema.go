package auth

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// tableNamer 让校验逻辑拿到模型对应的真实表名。
type tableNamer interface{ TableName() string }

// VerifySchema 校验账号表在生产库里已经存在。
//
// 本模块不建表：结构与数据都归 CanvasMind 的 Prisma 迁移所有。缺表时必须在
// 启动阶段就失败，否则用户要等到第一次点「登录」才会看到一个 500。
func VerifySchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("auth: 数据库连接为空")
	}
	models := []tableNamer{User{}, Session{}, VerificationCode{}, AuthIdentity{}, MethodConfig{}, UserAgreement{}}
	var missing []string
	for _, model := range models {
		if !db.Migrator().HasTable(model) {
			missing = append(missing, model.TableName())
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("auth: 账号表缺失（%s）；请先在 CanvasMind 侧执行 Prisma 迁移", strings.Join(missing, "、"))
	}
	return nil
}
