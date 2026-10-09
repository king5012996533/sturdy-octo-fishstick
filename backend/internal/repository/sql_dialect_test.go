package repository

import (
	"strings"
	"testing"
)

func TestNormalizeSQLDialect(t *testing.T) {
	cases := map[string]string{
		"sqlite":     "sqlite",
		"SQLite":     "sqlite",
		" mysql ":    "mysql",
		"postgres":   "postgres",
		"postgresql": "postgres",
		"pgx":        "postgres",
		"":           "sqlite",
		"unknown":    "sqlite",
	}
	for input, want := range cases {
		if got := normalizeSQLDialect(input); got != want {
			t.Errorf("normalizeSQLDialect(%q) = %q, want %q", input, got, want)
		}
	}
}

// 这三条表达式是本文件存在的理由：写死任何一条都会在另外两个数据库上直接语法错误。
func TestTextByteLengthPerDialect(t *testing.T) {
	cases := []struct {
		driver string
		want   string
	}{
		{"sqlite", "length(CAST(COALESCE(payload_json, '') AS BLOB))"},
		{"mysql", "LENGTH(COALESCE(payload_json, ''))"},
		{"postgres", "OCTET_LENGTH(COALESCE(payload_json, ''))"},
	}
	for _, c := range cases {
		if got := textByteLength(c.driver, "payload_json"); got != c.want {
			t.Errorf("%s: textByteLength = %q, want %q", c.driver, got, c.want)
		}
	}
}

func TestTextByteLengthNeverEmitsBlobCastOutsideSQLite(t *testing.T) {
	for _, driver := range []string{"mysql", "postgres"} {
		if strings.Contains(textByteLength(driver, "col"), "AS BLOB") {
			t.Fatalf("%s 不应出现 CAST(... AS BLOB)", driver)
		}
	}
}

func TestSumBytesBuildsSubquery(t *testing.T) {
	got := sumBytes("assets", "mysql", "payload_json")
	want := "(SELECT COALESCE(SUM(LENGTH(COALESCE(payload_json, ''))), 0) FROM assets WHERE user_id = ?)"
	if got != want {
		t.Fatalf("sumBytes = %q, want %q", got, want)
	}
	for _, column := range []string{"a", "b"} {
		if !strings.Contains(sumBytes("t", "sqlite", column), column) {
			t.Fatalf("多列时缺少 %s", column)
		}
	}
}

func TestSQLDialectFallsBackOnNilRepository(t *testing.T) {
	var repo *Repository
	if got := repo.sqlDialect(); got != "sqlite" {
		t.Fatalf("nil repository 应回落 sqlite，得到 %q", got)
	}
}
