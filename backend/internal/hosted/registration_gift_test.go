package hosted

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"infinite-canvas/backend/internal/auth"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// registrationGiftWallet 读一个账号的积分余额，只走用户端接口。
//
// 断言落在 HTTP 层而不是直接查表：注册礼包的价值是"用户注册完就能看到这笔积分"，
// 只查 credit_accounts 证明不了投影对了。
func registrationGiftWallet(t *testing.T, router *gin.Engine, cookie *http.Cookie) int64 {
	t.Helper()
	recorder := perform(router, http.MethodGet, "/api/finance/wallet", "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取积分余额失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Wallet struct {
				Balance int64 `json:"balance"`
			} `json:"wallet"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析余额响应失败: %v %s", err, recorder.Body.String())
	}
	return payload.Data.Wallet.Balance
}

// registerByEmailCode 走一遍「协议 → 下发验证码 → 注册」，返回账号 ID 与会话 Cookie。
func registerByEmailCode(t *testing.T, router *gin.Engine, authDB *gorm.DB, target string) (string, *http.Cookie) {
	t.Helper()
	recorder := perform(router, http.MethodGet, "/api/auth/agreements", "", nil)
	var agreements struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &agreements); err != nil || agreements.Data.Version == "" {
		t.Fatalf("解析协议响应失败: %v %s", err, recorder.Body.String())
	}
	if recorder = perform(router, http.MethodPost, "/api/auth/verification-code",
		`{"methodType":"EMAIL_CODE","target":"`+target+`"}`, nil); recorder.Code != http.StatusOK {
		t.Fatalf("下发验证码失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var record struct {
		Code string `gorm:"column:code"`
	}
	if err := authDB.Table("auth_verification_codes").
		Where("target = ? AND used_at IS NULL", target).
		Order("created_at desc").First(&record).Error; err != nil {
		t.Fatalf("账号库里没有验证码记录: %v", err)
	}
	recorder = perform(router, http.MethodPost, "/api/auth/register",
		`{"methodType":"EMAIL_CODE","target":"`+target+`","code":"`+record.Code+`","agreementVersion":"`+agreements.Data.Version+`"}`, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("注册失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var login struct {
		Data struct {
			User struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &login); err != nil || login.Data.User.ID == "" {
		t.Fatalf("解析注册响应失败: %v %s", err, recorder.Body.String())
	}
	return login.Data.User.ID, sessionCookie(t, recorder)
}

// TestRegistrationGiftCreditsNewAccount 覆盖「注册即到账」：新账号注册完成即可见 100 积分，
// 且流水能区分出这是赠送而不是充值。
func TestRegistrationGiftCreditsNewAccount(t *testing.T) {
	extension, authDB, _, service := newTestExtension(t)
	defer extension.Close()
	router := newTestRouter(extension, service)

	userID, cookie := registerByEmailCode(t, router, authDB, "gift@example.com")
	if balance := registrationGiftWallet(t, router, cookie); balance != registrationGiftCredits {
		t.Fatalf("注册礼包余额 = %d，期望 %d", balance, registrationGiftCredits)
	}

	recorder := perform(router, http.MethodGet, "/api/finance/ledger", "", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取积分流水失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var ledger struct {
		Data struct {
			Entries []struct {
				Kind    string `json:"kind"`
				Amount  int64  `json:"amount"`
				RefType string `json:"refType"`
				RefID   string `json:"refId"`
			} `json:"entries"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &ledger); err != nil {
		t.Fatalf("解析流水响应失败: %v %s", err, recorder.Body.String())
	}
	if ledger.Data.Total != 1 || len(ledger.Data.Entries) != 1 {
		t.Fatalf("注册后应只有一笔流水，实际 %d 条：%s", ledger.Data.Total, recorder.Body.String())
	}
	entry := ledger.Data.Entries[0]
	if entry.Kind != auth.CreditKindGift || entry.Amount != registrationGiftCredits || entry.RefType != "REGISTER" || entry.RefID != userID {
		t.Fatalf("注册礼包流水 = %+v", entry)
	}
}

// TestRegistrationGiftOnlyGrantsOnce 确认幂等：钩子被重复触发（第三方绑定、重放）不会重复发放。
func TestRegistrationGiftOnlyGrantsOnce(t *testing.T) {
	extension, authDB, _, service := newTestExtension(t)
	defer extension.Close()
	router := newTestRouter(extension, service)

	userID, cookie := registerByEmailCode(t, router, authDB, "gift-once@example.com")
	if balance := registrationGiftWallet(t, router, cookie); balance != registrationGiftCredits {
		t.Fatalf("注册礼包余额 = %d，期望 %d", balance, registrationGiftCredits)
	}

	authService, err := auth.NewService(auth.Options{Store: auth.NewStore(authDB)})
	if err != nil {
		t.Fatalf("装配账号服务失败: %v", err)
	}
	gift := &registrationGift{service: authService}
	for i := 0; i < 2; i++ {
		if err := gift.grant(context.Background(), userID); err != nil {
			t.Fatalf("第 %d 次补发失败: %v", i+1, err)
		}
	}
	if balance := registrationGiftWallet(t, router, cookie); balance != registrationGiftCredits {
		t.Fatalf("重复触发后余额 = %d，期望仍为 %d", balance, registrationGiftCredits)
	}
}
