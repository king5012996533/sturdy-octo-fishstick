package repository

import "strings"

// 本文件集中处理 SQLite / MySQL / PostgreSQL 的方言差异，避免方言专有写法散落在查询里。

// normalizeSQLDialect 把驱动名收成内部判断用的三种形态；未知驱动按 SQLite 处理。
func normalizeSQLDialect(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "mysql":
		return "mysql"
	case "postgres", "postgresql", "pgx":
		return "postgres"
	default:
		return "sqlite"
	}
}

// sqlDialect 返回当前连接实际使用的方言。
func (r *Repository) sqlDialect() string {
	if r == nil || r.db == nil || r.db.Dialector == nil {
		return "sqlite"
	}
	return normalizeSQLDialect(r.db.Dialector.Name())
}

// textByteLength 返回「按字节统计文本长度」的 SQL 表达式。
//
// 三家写法互不通用：SQLite 的 length() 对文本返回字符数，必须先 CAST 成 BLOB 才是字节；
// MySQL 的 LENGTH() 本身按字节计；PostgreSQL 要用 OCTET_LENGTH()。
// COALESCE 保留原语义——NULL 记 0 字节，而不是让整条 SUM 变成 NULL。
func textByteLength(driver, column string) string {
	coalesced := "COALESCE(" + column + ", '')"
	switch normalizeSQLDialect(driver) {
	case "mysql":
		return "LENGTH(" + coalesced + ")"
	case "postgres":
		return "OCTET_LENGTH(" + coalesced + ")"
	default:
		return "length(CAST(" + coalesced + " AS BLOB))"
	}
}

// sumBytes 生成「按 user_id 汇总若干列的字节数」的子查询。
func sumBytes(table string, driver string, columns ...string) string {
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		parts = append(parts, textByteLength(driver, column))
	}
	return "(SELECT COALESCE(SUM(" + strings.Join(parts, " + ") + "), 0) FROM " + table + " WHERE user_id = ?)"
}
