package auth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"infinite-canvas/backend/internal/kernel"

	"gorm.io/gorm"
)

// 身份绑定域：看得见自己绑了什么，换得掉绑定用的邮箱与手机号。
//
// 托管形态下用户的登录方式只有两类：验证码（邮箱/手机）与第三方 OAuth，密码是
// 2014 年之后才补上的可选凭据。账号一旦丢了收件箱或手机号，就没有任何自救路径，
// 因此"换绑"不是锦上添花，而是账号可用性的一部分。
//
// 换绑的证明方式：**新地址收码 + 旧地址收通知**。要求旧地址也回一次确认码更严格，
// 但那种严格会惩罚最需要换绑的人（旧邮箱已经登不上去的正是来换绑的），因此取
// 通知而不是确认：真正被搬走账号的用户能在几分钟内看到告警并找客服冻结。

// errBindingTaken 表示目标标识在这一刻已经被占用。
//
// 由存储层在事务内抛出：查重与写入之间隔着一次网络往返，只有在同一个事务里检查到的
// 结果才算数，否则两个账号可以同时"通过"检查、再同时写入。
var errBindingTaken = errors.New("auth: 该标识已被占用")

// BindingView 是一条绑定关系。
type BindingView struct {
	MethodType string     `json:"methodType"`
	Label      string     `json:"label"`
	Identifier string     `json:"identifier"`
	Verified   bool       `json:"verified"`
	BoundAt    *time.Time `json:"boundAt,omitempty"`
	// Primary 表示这条标识同时是账号的联系地址（app_users 上的 email/phone 列），
	// 验证码登录与安全通知都走它。
	Primary bool `json:"primary"`
}

// BindingsView 是「身份绑定」整块页面的读模型。
type BindingsView struct {
	Email    string        `json:"email"`
	Phone    string        `json:"phone"`
	Bindings []BindingView `json:"bindings"`
}

// BindingCodeInput 是"给新地址发验证码"的入参。
type BindingCodeInput struct {
	UserID      string
	Channel     VerificationChannel
	Target      string
	RequesterIP string
	UserAgent   string
}

// BindingCodeResult 是发码结果。DevCode 只在本地投递通道下非空。
type BindingCodeResult struct {
	Channel   string    `json:"channel"`
	Target    string    `json:"target"`
	ExpiresAt time.Time `json:"expiresAt"`
	Cooldown  int       `json:"cooldown"`
	DevCode   string    `json:"devCode,omitempty"`
}

// BindingConfirmInput 是确认换绑的入参。
type BindingConfirmInput struct {
	UserID  string
	Channel VerificationChannel
	Target  string
	Code    string
}

// BindingsByUser 取该账号的全部身份绑定行。
func (s *Store) BindingsByUser(userID string) ([]AuthIdentity, error) {
	var identities []AuthIdentity
	err := s.db.
		Where("user_id = ?", strings.TrimSpace(userID)).
		Order("created_at asc").
		Find(&identities).Error
	if err != nil {
		return nil, err
	}
	return identities, nil
}

// ReplaceContact 在同一个事务里换掉账号的联系地址与对应的身份绑定。
//
// 四件事必须原子完成，任何一步单独失败都会留下"改了一半"的账号：
//  1. app_users 上的 email/phone 列（账号的联系地址）
//  2. 新地址上的验证码身份行（登录入口）
//  3. 旧地址上残留的身份行（(method_type, identifier) 是唯一索引，留着旧行会让
//     这个邮箱/手机号永远无法再被绑回任何账号）
//  4. 旧地址上的密码身份行（用旧邮箱注册密码的账号，改绑之后密码登录必须仍然可用）
func (s *Store) ReplaceContact(userID string, channel VerificationChannel, methodType MethodType, previous string, target string, at time.Time) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New("auth: 换绑缺少用户标识")
	}
	column := "email"
	if channel == ChannelPhone {
		column = "phone"
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).
			Where("id = ?", userID).
			Updates(map[string]any{column: target, "updated_at": at}).Error; err != nil {
			return err
		}
		if previous != "" && previous != target {
			if err := tx.Where("user_id = ? AND method_type = ? AND identifier = ?", userID, string(methodType), previous).
				Delete(&AuthIdentity{}).Error; err != nil {
				return err
			}
			if err := tx.Model(&AuthIdentity{}).
				Where("user_id = ? AND method_type = ? AND identifier = ?", userID, string(MethodPassword), previous).
				Update("identifier", target).Error; err != nil {
				return err
			}
		}
		var existing AuthIdentity
		err := tx.Where("method_type = ? AND identifier = ?", string(methodType), target).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&AuthIdentity{
				ID:         kernel.NewID(),
				UserID:     userID,
				MethodType: methodType,
				Identifier: target,
				IsVerified: true,
				VerifiedAt: &at,
				CreatedAt:  at,
				UpdatedAt:  at,
			}).Error
		}
		if err != nil {
			return err
		}
		if existing.UserID != userID {
			return errBindingTaken
		}
		return tx.Model(&AuthIdentity{}).
			Where("id = ?", existing.ID).
			Updates(map[string]any{"is_verified": true, "verified_at": at, "updated_at": at}).Error
	})
}

// Bindings 返回当前账号的绑定关系。
func (s *Service) Bindings(userID string) (*BindingsView, error) {
	user, err := s.accountForSelfService(userID)
	if err != nil {
		return nil, err
	}
	identities, err := s.store.BindingsByUser(user.ID)
	if err != nil {
		return nil, internalFailure(err)
	}
	return bindingsViewOf(user, identities), nil
}

// SendBindingCode 给"想换成的那个地址"下发验证码。
func (s *Service) SendBindingCode(ctx context.Context, in BindingCodeInput) (*BindingCodeResult, error) {
	user, err := s.accountForSelfService(in.UserID)
	if err != nil {
		return nil, err
	}
	methodType, err := bindingMethodFor(in.Channel)
	if err != nil {
		return nil, err
	}
	target, err := normalizeBindingTarget(in.Channel, in.Target)
	if err != nil {
		return nil, err
	}
	if current := currentContact(user, in.Channel); current == target {
		return nil, invalidArgument("新地址与当前绑定的地址相同")
	}
	if err := s.ensureBindingAvailable(user.ID, methodType, target); err != nil {
		return nil, err
	}
	output, err := s.SendCode(ctx, SendCodeInput{
		MethodType:  methodType,
		Target:      target,
		RequesterIP: in.RequesterIP,
		UserAgent:   in.UserAgent,
	})
	if err != nil {
		return nil, err
	}
	return &BindingCodeResult{
		Channel:   string(in.Channel),
		Target:    output.Target,
		ExpiresAt: output.ExpiresAt,
		Cooldown:  output.Cooldown,
		DevCode:   output.DevCode,
	}, nil
}

// ConfirmBinding 校验新地址的验证码并完成换绑。
func (s *Service) ConfirmBinding(ctx context.Context, in BindingConfirmInput) (*BindingsView, error) {
	user, err := s.accountForSelfService(in.UserID)
	if err != nil {
		return nil, err
	}
	methodType, err := bindingMethodFor(in.Channel)
	if err != nil {
		return nil, err
	}
	target, err := normalizeBindingTarget(in.Channel, in.Target)
	if err != nil {
		return nil, err
	}
	if current := currentContact(user, in.Channel); current == target {
		return nil, invalidArgument("新地址与当前绑定的地址相同")
	}
	if err := s.consumeAccountCode(methodType, target, in.Code); err != nil {
		return nil, err
	}
	// 发码与核销之间可能被别人抢注，事务里还会再判一次；这里先判一次是为了给出
	// 明确的冲突文案，而不是让用户在事务里拿到一个含糊的失败。
	if err := s.ensureBindingAvailable(user.ID, methodType, target); err != nil {
		return nil, err
	}
	previous := currentContact(user, in.Channel)
	if err := s.store.ReplaceContact(user.ID, in.Channel, methodType, previous, target, s.now()); err != nil {
		if errors.Is(err, errBindingTaken) {
			return nil, conflict("该地址刚刚被其他账号绑定，请换一个")
		}
		return nil, internalFailure(err)
	}
	s.notifyContactChange(ctx, in.Channel, previous, target)
	refreshed, err := s.accountForSelfService(user.ID)
	if err != nil {
		return nil, err
	}
	identities, err := s.store.BindingsByUser(user.ID)
	if err != nil {
		return nil, internalFailure(err)
	}
	return bindingsViewOf(refreshed, identities), nil
}

// ensureBindingAvailable 确认目标标识没有被别的账号占用。
//
// 两处都要看：app_users 的 email/phone 列是"联系地址"，app_user_auth_identities 是
// "登录入口"。只查其中一处会漏掉一种占用（老账号可能只有绑定行没有列值）。
func (s *Service) ensureBindingAvailable(userID string, methodType MethodType, target string) error {
	if identity, err := s.store.IdentityByIdentifier(methodType, target); err == nil {
		if identity.UserID != userID {
			return conflict("该地址已被其他账号使用")
		}
	} else if !errors.Is(err, ErrNotFound) {
		return internalFailure(err)
	}
	var owner *User
	var err error
	if methodType == MethodPhoneCode {
		owner, err = s.store.UserByPhone(target)
	} else {
		owner, err = s.store.UserByEmail(target)
	}
	if err == nil {
		if owner.ID != userID {
			return conflict("该地址已被其他账号使用")
		}
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return internalFailure(err)
	}
	return nil
}

// notifyContactChange 给旧地址发一条变更通知（尽力而为）。
//
// 通知失败不回滚换绑：换绑本身已经通过新地址的验证码完成，把一次投递故障升级成
// "换绑失败"会让用户在一个看起来永远失败的按钮上反复重试，而账号其实已经改好了。
func (s *Service) notifyContactChange(ctx context.Context, channel VerificationChannel, previous string, target string) {
	previous = strings.TrimSpace(previous)
	if previous == "" || previous == target {
		return
	}
	if channel != ChannelEmail {
		// 国内短信必须使用已报备的模板，通知类短信在模板审批下来之前发不出去；
		// 这里只留一条日志，等短信通道就绪后再补投递。
		log.Printf("auth: 手机号已更换为 %s，旧号码 %s 的提醒暂缺短信模板", maskIdentifier(target), maskIdentifier(previous))
		return
	}
	sender := s.resolveEmailSender()
	notifier, ok := sender.(securityNotifier)
	if !ok {
		log.Printf("auth: 邮箱已更换为 %s，但当前邮件通道不支持安全通知", maskIdentifier(target))
		return
	}
	body := fmt.Sprintf("你的账号绑定邮箱已从 %s 更换为 %s。如果这不是你本人的操作，请立即通过原邮箱联系客服冻结账号。", previous, target)
	if err := notifier.SendSecurityNotice(ctx, previous, "账号绑定信息已变更", body); err != nil {
		log.Printf("auth: 向旧邮箱 %s 投递变更通知失败: %v", maskIdentifier(previous), err)
	}
}

func bindingMethodFor(channel VerificationChannel) (MethodType, error) {
	switch channel {
	case ChannelEmail:
		return MethodEmailCode, nil
	case ChannelPhone:
		return MethodPhoneCode, nil
	default:
		return "", invalidArgument("请选择要更换的绑定方式")
	}
}

func normalizeBindingTarget(channel VerificationChannel, raw string) (string, error) {
	switch channel {
	case ChannelEmail:
		email := strings.ToLower(strings.TrimSpace(raw))
		if !validEmail(email) {
			return "", invalidArgument("请输入正确的邮箱地址")
		}
		return email, nil
	case ChannelPhone:
		phone := normalizePhone(raw)
		if !mainlandPhonePattern.MatchString(phone) {
			return "", invalidArgument("请输入正确的手机号")
		}
		return phone, nil
	default:
		return "", invalidArgument("请选择要更换的绑定方式")
	}
}

func currentContact(user *User, channel VerificationChannel) string {
	if channel == ChannelPhone {
		return phoneOf(user)
	}
	return strings.ToLower(emailOf(user))
}

// securityNotifier 是邮件通道可选实现的安全通知能力。
//
// 不在 EmailSender 接口上加这个方法：验证码投递是登录链路的硬依赖，而安全通知是
// 尽力而为的附加项，把它写进接口会强迫每个实现（含测试替身）都去实现一个多数场景
// 用不到的投递口。
type securityNotifier interface {
	SendSecurityNotice(ctx context.Context, to string, subject string, body string) error
}

func bindingsViewOf(user *User, identities []AuthIdentity) *BindingsView {
	email := emailOf(user)
	phone := phoneOf(user)
	view := &BindingsView{Email: email, Phone: phone, Bindings: make([]BindingView, 0, len(identities))}
	for index := range identities {
		identity := identities[index]
		view.Bindings = append(view.Bindings, BindingView{
			MethodType: string(identity.MethodType),
			Label:      bindingLabel(identity.MethodType),
			Identifier: identity.Identifier,
			Verified:   identity.IsVerified,
			BoundAt:    &identity.CreatedAt,
			Primary:    identity.Identifier == email || identity.Identifier == phone,
		})
	}
	return view
}

// bindingLabel 是登录方式的中文名。与前端 methodLabels 保持同一套口径，
// 两处都要改的情况只发生在新增登录方式时。
func bindingLabel(methodType MethodType) string {
	switch methodType {
	case MethodPassword:
		return "密码"
	case MethodEmailCode:
		return "邮箱验证码"
	case MethodPhoneCode:
		return "手机验证码"
	case MethodGithubOAuth:
		return "GitHub"
	case MethodGoogleOAuth:
		return "Google"
	case MethodWechatOAuth:
		return "微信"
	default:
		return string(methodType)
	}
}
