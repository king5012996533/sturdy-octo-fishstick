package hosted

import (
	"context"
	"log"

	"infinite-canvas/backend/internal/auth"
)

// 新用户注册礼包：注册即到账的见面礼，让刚进站的账号能立刻跑通一次生成。
//
// 额度写死在代码里而不是做成后台配置：这是营销口径上的一个常数，改动应当走发版，
// 与内容审核词表同理——随手改成 10000 就是一次没有留痕的成本泄漏。
const registrationGiftCredits int64 = 100

// registrationGift 把"注册成功"翻译成一笔积分入账。
//
// 只发一次靠积分域自带的幂等键（user_id + kind + ref_type + ref_id），而不是靠这里
// 的判断：钩子在验证码、密码与第三方建号三条通道上共用，任何一条被重放都不该重复发放。
type registrationGift struct {
	service *auth.Service
}

// grant 是注入到 auth.Options.OnUserRegistered 的钩子。
//
// 失败一律不返回错误：账号此时已经建好，返回错误只会让注册接口变成 500，用户重试还会
// 撞上"邮箱已被注册"，而礼包依然没到账——那是最糟的组合。这里只留日志，补偿走后台调整。
func (g *registrationGift) grant(_ context.Context, userID string) error {
	if g == nil || g.service == nil || userID == "" {
		return nil
	}
	if _, _, err := g.service.ApplyCreditMutations([]auth.CreditMutation{{
		UserID:  userID,
		Kind:    auth.CreditKindGift,
		Amount:  registrationGiftCredits,
		RefType: "REGISTER",
		RefID:   userID,
		Note:    "新用户注册礼包",
	}}); err != nil {
		log.Printf("hosted: 注册礼包发放失败 user=%s: %v", userID, err)
	}
	return nil
}
