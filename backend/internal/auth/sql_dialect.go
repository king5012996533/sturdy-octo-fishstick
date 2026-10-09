package auth

import "strings"

// normalizeAuthDialect 把驱动名收成内部判断用的形态；未知驱动按 SQLite 处理。
func normalizeAuthDialect(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "mysql":
		return "mysql"
	case "postgres", "postgresql", "pgx":
		return "postgres"
	default:
		return "sqlite"
	}
}

// scalarMaxExpr 返回「取两者较大值」的 SQL 表达式。
//
// SQLite 的双参数 MAX() 是标量函数，可以直接用；MySQL 与 PostgreSQL 的 MAX() 是聚合函数，
// 双参数写法会直接报错，必须换成 GREATEST()。写死 MAX(a, b) 会让文本任务结算缺口在
// MySQL 账号库上记不下来，欠款静默丢失。
func scalarMaxExpr(driver, left, rightExpr string) string {
	if normalizeAuthDialect(driver) == "sqlite" {
		return "MAX(" + left + ", " + rightExpr + ")"
	}
	return "GREATEST(" + left + ", " + rightExpr + ")"
}

func (s *Store) sqlDialect() string {
	if s == nil || s.db == nil || s.db.Dialector == nil {
		return "sqlite"
	}
	return normalizeAuthDialect(s.db.Dialector.Name())
}
