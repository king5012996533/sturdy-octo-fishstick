package app

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// 用户投稿：把「自己生成出来的产物 + 当时用的提示词」变成广场条目，供别人一键复用。
//
// 它与运营自录的平台条目共享一张表（见 model.CreationInspiration），但这三点必须在
// 服务层落实，否则"共享一张表"就会变成"投稿直接上广场"：
//  1. 内容要过审才能上广场——平台条目由运营署名并负责，投稿是陌生人写的；
//  2. 封面必须由平台自己签发——存外链等于把广场的可用性押在别人的域名上；
//  3. 有配额——没有配额时一个人可以把广场刷成自己的作品集。
//
// 审核分两步走：关键词预筛（ScreenCreationPost）先拦掉没有讨论空间的内容，
// 余下的一律进人工队列由运营拍板。词表只是减负工具，不是防线。

const (
	// creationPostDailyLimit 是单个用户 24 小时内的投稿上限。
	//
	// 5 条这个量级是"分享作品够用、刷屏不够用"的折中：正常作者一天发不出 5 件作品，
	// 而想用广场做推广的人一天也只能发 5 条。
	creationPostDailyLimit = 5
	// creationPostWindow 是上面那条配额的时间窗。
	creationPostWindow = 24 * time.Hour
	// creationInspirationCoverTTL 是投稿封面签名地址的有效期。
	//
	// 12 小时是按"用户打开广场页到看完这一屏"的量级取的：太短会在用户浏览中途失效，
	// 太长则等于把资源出口变成一条长期有效的外链。
	creationInspirationCoverTTL = 12 * time.Hour
	// creationPostReviewNoteMaxLen 是审核备注上限，会原文回显给投稿人。
	creationPostReviewNoteMaxLen = 500
)

// CreationPostInput 是投稿入参。
//
// 不接收 CoverURL：封面只能是用户自己的生成产物，允许客户端传地址等于给广场开了一个
// 外链注入的口子（图片可以指向任意域名，也可以事后换成别的内容）。
type CreationPostInput struct {
	ResourceID  string `json:"resourceId"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Mode        string `json:"mode"`
	Category    string `json:"category"`
}

// SubmitCreationPost 受理一条投稿，返回落库后的条目（含审核结论）。
//
// 命中硬拦词仍然落库，但直接判为 REJECTED：不落库的话用户端"提交了却什么都没发生"，
// 既看不到记录也拿不到理由，最容易被当成故障反复提交。被驳回的条目状态恒为 OFFLINE，
// 而前台目录另外还要求 review_status = APPROVED，两道条件都不满足，不可能漏出去。
func (s *Service) SubmitCreationPost(userID string, displayName string, input CreationPostInput) (*CreationInspirationView, error) {
	author := strings.TrimSpace(userID)
	if author == "" {
		return nil, BadAuthRequest("缺少投稿人")
	}
	title := strings.TrimSpace(input.Title)
	if count := utf8.RuneCountInString(title); count == 0 || count > creationInspirationTitleMaxLen {
		return nil, BadAuthRequest("作品标题需为 1 到 40 个字符")
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return nil, BadAuthRequest("请填写可复用的提示词")
	}
	if utf8.RuneCountInString(prompt) > creationInspirationPromptMaxLen {
		return nil, BadAuthRequest("提示词过长，请精简后再提交")
	}
	resourceID := strings.TrimSpace(input.ResourceID)
	if resourceID == "" {
		return nil, BadAuthRequest("缺少要发布的生成产物")
	}

	// 只能发布自己的产物：产物归属用 ResourceForUser 一次判掉，不在这里再做判断，
	// 否则"别人的资源 ID + 我的投稿"会变成一条指向他人私有素材的广场封面。
	resource, err := s.repo.ResourceForUser(author, resourceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("生成产物不存在")
		}
		return nil, err
	}
	if resource.Status != model.ResourceStatusReady {
		return nil, BadAuthRequest("生成产物尚未就绪，暂时不能发布")
	}
	mode, err := creationPostMode(resource, input.Mode)
	if err != nil {
		return nil, err
	}
	// 转码中的视频不能上广场：封面与播放都指向同一个资源，此时打开只会转圈。
	// failed 不拦——原片往往仍可播放，且拦下会让用户永远发不出这条（转码失败不会自愈）。
	if mode == "video" && resource.PlaybackStatus == model.PlaybackStatusProcessing {
		return nil, BadAuthRequest("视频正在转码，请稍后再发布")
	}

	count, err := s.repo.CountCreationInspirationsByAuthorSince(author, time.Now().Add(-creationPostWindow))
	if err != nil {
		return nil, err
	}
	if count >= creationPostDailyLimit {
		return nil, BadAuthRequest("投稿太频繁，24 小时内最多发布 5 条，请稍后再试")
	}

	now := time.Now()
	record := &model.CreationInspiration{
		Origin:       model.CreationInspirationOriginUser,
		AuthorUserID: author,
		Author:       truncateRunes(creationPostAuthorName(displayName), 80),
		ResourceID:   resource.ID,
		Title:        title,
		Description:  truncateRunes(strings.TrimSpace(input.Description), creationInspirationDescriptionMaxLen),
		Prompt:       prompt,
		Mode:         mode,
		Category:     creationInspirationCategory(input.Category),
		// 未过审的条目一律不上架：上架与否由审核动作决定，不由投稿动作决定。
		Status:       model.CreationInspirationOffline,
		ReviewStatus: model.CreationInspirationReviewPending,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	// 预筛只写结论，不改可见性：命中的内容同样进库，供运营复盘"被判掉的是什么"。
	switch screen := ScreenCreationPost(record.Title, record.Description, record.Prompt, record.Category); screen.Level {
	case ContentScreenLevelBlock:
		record.ReviewStatus = model.CreationInspirationReviewRejected
		record.ReviewNote = screen.RejectReason()
		record.ReviewedAt = &now
	case ContentScreenLevelFlag:
		record.ReviewNote = screen.FlagNote()
	}

	if err := s.repo.SaveCreationInspiration(record); err != nil {
		return nil, err
	}
	return s.creationInspirationView(record), nil
}

// MyCreationPosts 返回某个用户自己的全部投稿（含待审、被驳回），最新在前。
func (s *Service) MyCreationPosts(userID string) ([]CreationInspirationView, error) {
	author := strings.TrimSpace(userID)
	if author == "" {
		return nil, BadAuthRequest("缺少投稿人")
	}
	records, err := s.repo.CreationInspirationsByAuthor(author)
	if err != nil {
		return nil, err
	}
	return s.creationInspirationViews(records), nil
}

// WithdrawCreationPost 撤回一条自己的投稿。
//
// 撤回直接删除而不是改成"下架"：用户说不要了就该真的不要了，留一条只有运营看得见的
// 记录属于自作主张。删除条件带上作者，避免"删自己那条"变成"删任何人的那条"。
func (s *Service) WithdrawCreationPost(userID string, id string) error {
	author := strings.TrimSpace(userID)
	trimmed := strings.TrimSpace(id)
	if author == "" {
		return BadAuthRequest("缺少投稿人")
	}
	if trimmed == "" {
		return BadAuthRequest("缺少投稿标识")
	}
	if _, err := s.repo.CreationInspirationByAuthorAndID(author, trimmed); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NotFound("投稿不存在")
		}
		return err
	}
	if err := s.repo.DeleteCreationInspiration(trimmed); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NotFound("投稿不存在")
		}
		return err
	}
	return nil
}

// AdminCreationPostQueue 返回待人工审核的投稿，先到先审。
func (s *Service) AdminCreationPostQueue() ([]CreationInspirationView, error) {
	records, err := s.repo.CreationInspirationReviewQueue()
	if err != nil {
		return nil, err
	}
	return s.creationInspirationViews(records), nil
}

// ReviewCreationPost 给出人工审核结论：通过即上架，驳回则留理由并保持下架。
//
// 通过时顺带把状态改成 ONLINE——运营的"批准"在用户看来就是"它出现在广场上了"，
// 拆成"审核通过"和"手动上架"两步只会让队列里堆一堆已通过但没上架的条目。
func (s *Service) ReviewCreationPost(id string, approve bool, note string) (*CreationInspirationView, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return nil, BadAuthRequest("缺少投稿标识")
	}
	record, err := s.repo.CreationInspirationByID(trimmed)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("投稿不存在")
		}
		return nil, err
	}
	if record.Origin != model.CreationInspirationOriginUser {
		return nil, BadAuthRequest("平台内容不需要审核")
	}

	now := time.Now()
	reviewNote := truncateRunes(strings.TrimSpace(note), creationPostReviewNoteMaxLen)
	if approve {
		record.ReviewStatus = model.CreationInspirationReviewApproved
		record.Status = model.CreationInspirationOnline
	} else {
		if reviewNote == "" {
			reviewNote = "内容不符合广场规范，无法通过审核"
		}
		record.ReviewStatus = model.CreationInspirationReviewRejected
		record.Status = model.CreationInspirationOffline
	}
	record.ReviewNote = reviewNote
	record.ReviewedAt = &now
	record.UpdatedAt = now
	if err := s.repo.SaveCreationInspiration(record); err != nil {
		return nil, err
	}
	return s.creationInspirationView(record), nil
}

// creationPostMode 归一创作模式，并与产物类型核对。
//
// 模式决定前台点开卡片跳到哪个创作页，因此不能只信客户端：把一张图标成 video，
// 复用的人会落到视频页，套上提示词后得到的结果与该卡片完全对不上。
func creationPostMode(resource *model.Resource, raw string) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(resource.Kind))
	if kind != "image" && kind != "video" {
		return "", BadAuthRequest("只有图片或视频产物可以发布到广场")
	}
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		mode = kind
	}
	if _, ok := creationInspirationModes[mode]; !ok {
		return "", BadAuthRequest("创作模式取值无效")
	}
	if mode != kind {
		return "", BadAuthRequest("创作模式与产物类型不一致")
	}
	return mode, nil
}

// creationPostAuthorName 决定投稿在广场上的署名。
func creationPostAuthorName(displayName string) string {
	name := strings.TrimSpace(displayName)
	if name == "" {
		return "匿名用户"
	}
	return name
}
