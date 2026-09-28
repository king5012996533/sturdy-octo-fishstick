package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"golang.org/x/crypto/scrypt"
)

// Node 侧 crypto.scrypt 的默认参数：N=16384、r=8、p=1、keyLen=64。
// 这些值必须与 CanvasMind 的 hashUserPassword 严格一致，否则存量用户密码无法验证。
const (
	scryptN      = 16384
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 64
	scryptPrefix = "scrypt:"
)

// generateSessionToken 生成不透明会话令牌。
//
// 用随机令牌而不是 JWT：会话必须能即时吊销，JWT 做不到。长度与编码沿用
// CanvasMind 的 randomBytes(24).toString('base64url')。
func generateSessionToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// hashSessionToken 只把摘要落库，数据库泄漏时无法直接复用会话。
func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// generateVerificationCode 生成 6 位数字验证码。
//
// 用 crypto/rand 而不是 math/rand：CanvasMind 用的 Math.random() 是可预测的
// xorshift128+，登录验证码不能用可预测随机源。
func generateVerificationCode() (string, error) {
	limit := big.NewInt(1000000)
	value, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

// HashPassword 生成与 Node 版兼容的密码哈希。
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	// CanvasMind 把随机 16 字节先转成 hex 字符串再当作 salt 传入，
	// 因此这里参与运算的是那 32 个 ASCII 字符本身。
	saltHex := hex.EncodeToString(salt)
	derived, err := scrypt.Key([]byte(password), []byte(saltHex), scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return "", err
	}
	return scryptPrefix + saltHex + ":" + hex.EncodeToString(derived), nil
}

// VerifyPassword 按 Node 版格式校验密码。
func VerifyPassword(password string, stored *string) bool {
	if stored == nil {
		return false
	}
	normalized := strings.TrimSpace(*stored)
	if !strings.HasPrefix(normalized, scryptPrefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(normalized, scryptPrefix), ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	expected, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	derived, err := scrypt.Key([]byte(password), []byte(parts[0]), scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(expected, derived) == 1
}

// ErrPasswordFormat 表示存量哈希格式不受支持（例如历史遗留的非 scrypt 记录）。
var ErrPasswordFormat = errors.New("密码哈希格式不受支持")
