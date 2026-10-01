package auth

import (
	"errors"
	"strings"
)

// 账号自助操作的公共前置。
//
// 用户中心里的每个写入口（资料、密码、设备、绑定）都以「会话账号」为主体：请求体里
// 不接受任何用户标识，目标账号只能来自会话。这几个前置被所有入口共用，因此写在这里
// 而不是各自复制一份——复制出来的版本迟早会漏掉其中一条，而漏掉的那条就是越权入口。

// accountForSelfService 取当前账号，并确认它还能进行自助操作。
//
// DISABLED 一律拒绝：注销执行与后台封禁都会把状态改成 DISABLED，此后再允许改密码
// 或换绑，等于给一个已经被停用的账号留下继续搬运账号资料的口子。
func (s *Service) accountForSelfService(userID string) (*User, error) {
	if s == nil || s.store == nil {
		return nil, internalFailure(errors.New("账号库未装配"))
	}
	user, err := s.store.UserByID(strings.TrimSpace(userID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, unauthorized("当前未登录或登录已失效")
		}
		return nil, internalFailure(err)
	}
	if user.Status != StatusActive {
		return nil, forbidden("账号当前状态不允许此操作，请联系客服处理")
	}
	return user, nil
}

// accountVerificationTarget 取该账号在指定验证渠道上的标识。
//
// 只认账号自己绑定的邮箱/手机号：请求里带什么目标都不参与校验，否则用一个自己
// 收得到的号码，就能把别人的账号操作走。
func accountVerificationTarget(user *User, methodType MethodType) (string, error) {
	if user == nil {
		return "", unauthorized("当前未登录或登录已失效")
	}
	switch methodType {
	case MethodEmailCode:
		if user.Email == nil || strings.TrimSpace(*user.Email) == "" {
			return "", invalidArgument("该账号未绑定邮箱，无法用邮箱验证码确认身份")
		}
		return strings.ToLower(strings.TrimSpace(*user.Email)), nil
	case MethodPhoneCode:
		if user.Phone == nil || strings.TrimSpace(*user.Phone) == "" {
			return "", invalidArgument("该账号未绑定手机号，无法用短信验证码确认身份")
		}
		return strings.TrimSpace(*user.Phone), nil
	default:
		return "", invalidArgument("请选择邮箱或手机号接收验证码")
	}
}

// consumeAccountCode 核销一次账号级验证码。
//
// 校验失败一律折叠成同一句文案：区分「码错了」与「码过期了」会让攻击者知道自己的
// 猜测落在哪个窗口里，而用户能做的动作完全相同——重新获取。
func (s *Service) consumeAccountCode(methodType MethodType, target string, code string) error {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return invalidArgument("请输入验证码")
	}
	if _, err := s.store.ConsumeCode(methodType, target, trimmed, codeScene); err != nil {
		if errors.Is(err, ErrNotFound) {
			return invalidArgument("验证码不正确或已过期")
		}
		return internalFailure(err)
	}
	return nil
}

// emailOf / phoneOf 把可空列读成稳定的字符串，供视图层直接使用。
func emailOf(user *User) string {
	if user == nil || user.Email == nil {
		return ""
	}
	return strings.TrimSpace(*user.Email)
}

func phoneOf(user *User) string {
	if user == nil || user.Phone == nil {
		return ""
	}
	return strings.TrimSpace(*user.Phone)
}

func hasPasswordSet(user *User) bool {
	return user != nil && user.PasswordHash != nil && strings.TrimSpace(*user.PasswordHash) != ""
}

// displayNameOf 与 User.DisplayName 同源，但对可空字段做一次归一，避免视图层到处判空。
func displayNameOf(user *User) string {
	if user == nil {
		return ""
	}
	return user.DisplayName()
}
