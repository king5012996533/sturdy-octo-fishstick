package auth

import (
	"fmt"
	"strings"
	"time"

	"infinite-canvas/backend/internal/kernel"
)

// 协议的后台维护与重新同意。
//
// 这里处理的是「条款什么时候变、变了之后谁还要再同意一次」，与 agreements.go 里
// 的内置骨架正文相对：内置那份只在后台还没发过任何一版时兜底，保证新装的实例也能
// 打开注册页。

const (
	agreementTermTitleMaxRunes   = 64
	agreementTermBodyMinRunes    = 200
	agreementPrivacyBodyMinRunes = 200
	agreementSignatureDefault    = 20
	agreementSignatureMaxLimit   = 100
	agreementHistoryLimit        = 50
)

// AgreementVersionView 是历史版本列表里的一版。
type AgreementVersionView struct {
	Version     string              `json:"version"`
	Documents   []AgreementDocument `json:"documents"`
	PublishedAt time.Time           `json:"publishedAt"`
	PublishedBy string              `json:"publishedBy,omitempty"`
	Current     bool                `json:"current"`
}

// AgreementAdminView 是后台协议管理的读模型。
type AgreementAdminView struct {
	Version   string              `json:"version"`
	Documents []AgreementDocument `json:"documents"`
	/** 是否来自后台发布；false 表示用的还是内置骨架正文，上线前必须替换。 */
	Configured  bool       `json:"configured"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
	PublishedBy string     `json:"publishedBy,omitempty"`
	/** 尚未同意当前版本的账号数：发布新版本前用它提示影响面。 */
	PendingUsers int64                  `json:"pendingUsers"`
	History      []AgreementVersionView `json:"history"`
}

// PublishAgreementInput 是一次发布请求。
type PublishAgreementInput struct {
	TermsTitle   string
	TermsBody    string
	PrivacyTitle string
	PrivacyBody  string
	ActorUserID  string
}

// AgreementsPayload 返回当前生效的协议版本与正文（优先取后台发布的版本）。
func (s *Service) AgreementsPayload() (*AgreementsPayload, error) {
	writing, err := s.currentAgreementWriting()
	if err != nil {
		return nil, err
	}
	payload := writing.payload()
	return &payload, nil
}

// CurrentAgreementVersionString 返回当前生效的版本串。
func (s *Service) CurrentAgreementVersionString() (string, error) {
	writing, err := s.currentAgreementWriting()
	if err != nil {
		return "", err
	}
	return writing.Version, nil
}

// AgreementAdminView 汇总当前版本、历史版本与待重签账号数。
func (s *Service) AgreementAdminView() (*AgreementAdminView, error) {
	current, err := s.currentAgreementWriting()
	if err != nil {
		return nil, err
	}
	history, err := s.store.AgreementVersions(agreementHistoryLimit)
	if err != nil {
		return nil, internalFailure(err)
	}
	pending, err := s.store.CountUsersPendingAgreement(current.Version, AgreementTypeTerms)
	if err != nil {
		return nil, internalFailure(err)
	}
	view := &AgreementAdminView{
		Version:      current.Version,
		Documents:    current.payload().Documents,
		Configured:   current.Configured,
		PublishedBy:  current.PublishedBy,
		PendingUsers: pending,
	}
	if current.PublishedAt != nil {
		publishedAt := *current.PublishedAt
		view.PublishedAt = &publishedAt
	}
	view.History = make([]AgreementVersionView, 0, len(history))
	for _, record := range history {
		view.History = append(view.History, AgreementVersionView{
			Version:     record.Version,
			Documents:   documentsOf(record),
			PublishedAt: record.PublishedAt,
			PublishedBy: record.PublishedBy,
			Current:     record.Version == current.Version,
		})
	}
	return view, nil
}

// PublishAgreements 发布一版新的协议。
//
// 版本号由服务端生成而不是让运营填：手填的版本号既可能重复（覆盖历史留痕），也可能
// 倒着排（"当前版本"变成旧条款）。发布即强制重签——所有账号的已签版本都会与新版本
// 不一致，直到他们重新同意。
func (s *Service) PublishAgreements(input PublishAgreementInput) (*AgreementAdminView, error) {
	termsTitle, err := validateAgreementCopy(input.TermsTitle, "用户协议标题", agreementTermTitleMaxRunes)
	if err != nil {
		return nil, err
	}
	privacyTitle, err := validateAgreementCopy(input.PrivacyTitle, "隐私政策标题", agreementTermTitleMaxRunes)
	if err != nil {
		return nil, err
	}
	termsBody, err := validateAgreementBody(input.TermsBody, "用户协议正文", agreementTermBodyMinRunes)
	if err != nil {
		return nil, err
	}
	privacyBody, err := validateAgreementBody(input.PrivacyBody, "隐私政策正文", agreementPrivacyBodyMinRunes)
	if err != nil {
		return nil, err
	}
	version, err := s.nextAgreementVersion()
	if err != nil {
		return nil, err
	}
	record := &AgreementVersion{
		Version:      version,
		TermsTitle:   termsTitle,
		TermsBody:    termsBody,
		PrivacyTitle: privacyTitle,
		PrivacyBody:  privacyBody,
		PublishedAt:  s.now(),
		PublishedBy:  strings.TrimSpace(input.ActorUserID),
	}
	if err := s.store.CreateAgreementVersion(record); err != nil {
		return nil, internalFailure(err)
	}
	return s.AgreementAdminView()
}

// AgreementSignatures 分页返回签署记录。
func (s *Service) AgreementSignatures(filter AgreementSignatureFilter) ([]AgreementSignatureRow, int64, int, error) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	limit := filter.Limit
	if limit < 1 {
		limit = agreementSignatureDefault
	}
	if limit > agreementSignatureMaxLimit {
		limit = agreementSignatureMaxLimit
	}
	filter.Page = page
	filter.Limit = limit
	rows, total, err := s.store.AgreementSignaturePage(filter)
	if err != nil {
		return nil, 0, 0, internalFailure(err)
	}
	return rows, total, limit, nil
}

// AgreementStatus 返回「当前版本」与「该账号已接受的版本」。
//
// 两者不一致即表示需要重新同意：发布新版本后所有账号都会落到这个状态，直到他们
// 在前台确认一次。
func (s *Service) AgreementStatus(userID string) (string, string, error) {
	current, err := s.CurrentAgreementVersionString()
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(userID) == "" {
		return current, "", nil
	}
	accepted, err := s.store.AcceptedAgreementVersions(userID)
	if err != nil {
		return "", "", internalFailure(err)
	}
	record, ok := accepted[AgreementTypeTerms]
	if !ok {
		return current, "", nil
	}
	return current, record.Version, nil
}

// AcceptCurrentAgreements 记录一次重新同意。
//
// 版本号必须与当前版本一致：前端拿着旧页面上的版本号回来时应当先刷新，而不是把
// 一个已经不生效的版本写进留痕。
func (s *Service) AcceptCurrentAgreements(userID string, version string, ip string, userAgent string) error {
	if strings.TrimSpace(userID) == "" {
		return ErrNotAuthenticated
	}
	current, err := s.CurrentAgreementVersionString()
	if err != nil {
		return err
	}
	if strings.TrimSpace(version) != current {
		return invalidArgument("协议已更新，请刷新页面后重试")
	}
	return s.recordAgreements(userID, current, ip, userAgent)
}

// currentAgreementWriting 把「后台发布过的最新一版」与「内置骨架」统一成一种形态。
type agreementWriting struct {
	Version      string
	TermsTitle   string
	TermsBody    string
	PrivacyTitle string
	PrivacyBody  string
	PublishedAt  *time.Time
	PublishedBy  string
	Configured   bool
}

func (w agreementWriting) payload() AgreementsPayload {
	return AgreementsPayload{
		Version: w.Version,
		Documents: []AgreementDocument{
			{Type: AgreementTypeTerms, Title: w.TermsTitle, Body: w.TermsBody},
			{Type: AgreementTypePrivacy, Title: w.PrivacyTitle, Body: w.PrivacyBody},
		},
	}
}

func (s *Service) currentAgreementWriting() (agreementWriting, error) {
	record, err := s.store.CurrentAgreementVersion()
	if err != nil && err != ErrNotFound {
		return agreementWriting{}, internalFailure(err)
	}
	if record != nil {
		publishedAt := record.PublishedAt
		return agreementWriting{
			Version:      record.Version,
			TermsTitle:   record.TermsTitle,
			TermsBody:    record.TermsBody,
			PrivacyTitle: record.PrivacyTitle,
			PrivacyBody:  record.PrivacyBody,
			PublishedAt:  &publishedAt,
			PublishedBy:  record.PublishedBy,
			Configured:   true,
		}, nil
	}
	builtin := Agreements()
	return agreementWriting{
		Version:      builtin.Version,
		TermsTitle:   builtin.Documents[0].Title,
		TermsBody:    builtin.Documents[0].Body,
		PrivacyTitle: builtin.Documents[1].Title,
		PrivacyBody:  builtin.Documents[1].Body,
	}, nil
}

func documentsOf(record AgreementVersion) []AgreementDocument {
	return []AgreementDocument{
		{Type: AgreementTypeTerms, Title: record.TermsTitle, Body: record.TermsBody},
		{Type: AgreementTypePrivacy, Title: record.PrivacyTitle, Body: record.PrivacyBody},
	}
}

// nextAgreementVersion 以发布日期为基准生成版本串，同日多次发布时追加序号。
func (s *Service) nextAgreementVersion() (string, error) {
	base := s.now().Format("2006-01-02")
	for attempt := 1; attempt <= 50; attempt++ {
		candidate := base
		if attempt > 1 {
			candidate = fmt.Sprintf("%s.%d", base, attempt)
		}
		// 内置骨架版本号也曾被真实注册用户签过（注册时拿到的就是它），发布时不能复用：
		// 复用会让"发布新版"对这些账号变成无感的空操作，重新同意的留痕也就没了意义。
		if candidate == agreementVersion {
			continue
		}
		exists, err := s.store.AgreementVersionExists(candidate)
		if err != nil {
			return "", internalFailure(err)
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", kernel.NewAppError(409, "同一天发布的协议版本过多，请稍后再试")
}

func validateAgreementCopy(value string, label string, maxRunes int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", invalidArgument(label + "不能为空")
	}
	if len([]rune(trimmed)) > maxRunes {
		return "", invalidArgument(fmt.Sprintf("%s不能超过 %d 个字", label, maxRunes))
	}
	return trimmed, nil
}

func validateAgreementBody(value string, label string, minRunes int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len([]rune(trimmed)) < minRunes {
		return "", invalidArgument(fmt.Sprintf("%s至少需要 %d 个字，占位骨架不能上线", label, minRunes))
	}
	return trimmed, nil
}
