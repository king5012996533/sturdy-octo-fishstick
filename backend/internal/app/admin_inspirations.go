package app

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

const (
	// creationInspirationTitleMaxLen 是灵感标题上限（按字符计）：前台卡片按一行展示，
	// 超过 40 字在所有列表里都会被截断。
	creationInspirationTitleMaxLen = 40
	// creationInspirationDescriptionMaxLen 与提示词上限都按字符计，只用来挡住误粘贴的
	// 整篇小说：广场卡片的描述是一行摘要，提示词才是真正被套用的正文。
	creationInspirationDescriptionMaxLen = 200
	creationInspirationPromptMaxLen      = 8000
	// creationInspirationDefaultCategory 是分类缺省值。分类是自由文本，运营自己定；
	// 不填时归入「精选」，避免出现空分类导致前台筛选少一档。
	creationInspirationDefaultCategory = "精选"
)

// creationInspirationModes 是允许的创作模式（冻结）：与前台 CreationMode 一一对应，
// 多存一个前台不认识的值只会让卡片点开后落到默认分支。
var creationInspirationModes = map[string]struct{}{
	"video": {},
	"image": {},
	"text":  {},
}

// CreationInspirationView 是精选灵感的对外视图；后台与前台共用同一套字段。
//
// Source 与 SourceURL 原样回显：署名口径（示例素材 / 开源改编 / 原创）由前台按这两个
// 字段推导，服务端不在这里拼展示文案，否则同一句话会有中英文两处来源。
type CreationInspirationView struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CoverURL    string    `json:"coverUrl"`
	Prompt      string    `json:"prompt"`
	Mode        string    `json:"mode"`
	Category    string    `json:"category"`
	Author      string    `json:"author"`
	Likes       int       `json:"likes"`
	SourceURL   string    `json:"sourceUrl"`
	Source      string    `json:"source"`
	Status      string    `json:"status"`
	Featured    bool      `json:"featured"`
	SortOrder   int       `json:"sortOrder"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// CreationInspirationInput 是新建 / 编辑精选灵感的入参。
type CreationInspirationInput struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	CoverURL    string `json:"coverUrl"`
	Prompt      string `json:"prompt"`
	Mode        string `json:"mode"`
	Category    string `json:"category"`
	Author      string `json:"author"`
	Likes       int    `json:"likes"`
	SourceURL   string `json:"sourceUrl"`
	Source      string `json:"source"`
	Status      string `json:"status"`
	Featured    bool   `json:"featured"`
	SortOrder   int    `json:"sortOrder"`
}

// AdminCreationInspirations 返回后台灵感列表（含已下架）。
func (s *Service) AdminCreationInspirations() ([]CreationInspirationView, error) {
	records, err := s.repo.AdminCreationInspirations()
	if err != nil {
		return nil, err
	}
	return creationInspirationViews(records), nil
}

// CreationInspirationCatalog 返回前台可见的灵感目录，只含已上架条目。
func (s *Service) CreationInspirationCatalog() ([]CreationInspirationView, error) {
	records, err := s.repo.CreationInspirationCatalog()
	if err != nil {
		return nil, err
	}
	return creationInspirationViews(records), nil
}

// SaveCreationInspiration 新建或更新一条精选灵感。
//
// 校验在写库前一次做完：标题长度、模式与状态取值。ID 非空时必须命中已有行，否则按
// not found 处理——把「编辑一个不存在的 id」静默降级成新建，会让一次误操作凭空多出
// 一条灵感。
func (s *Service) SaveCreationInspiration(input CreationInspirationInput) (*CreationInspirationView, error) {
	title := strings.TrimSpace(input.Title)
	if runeCount := utf8.RuneCountInString(title); runeCount == 0 || runeCount > creationInspirationTitleMaxLen {
		return nil, BadAuthRequest("灵感标题需为 1 到 40 个字符")
	}
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if _, ok := creationInspirationModes[mode]; !ok {
		return nil, BadAuthRequest("创作模式取值无效")
	}
	status := model.CreationInspirationStatus(strings.ToUpper(strings.TrimSpace(input.Status)))
	if status != model.CreationInspirationOnline && status != model.CreationInspirationOffline {
		return nil, BadAuthRequest("灵感状态取值无效")
	}

	id := strings.TrimSpace(input.ID)
	var record *model.CreationInspiration
	if id != "" {
		existing, err := s.repo.CreationInspirationByID(id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("精选灵感不存在")
		}
		if err != nil {
			return nil, err
		}
		record = existing
	}

	now := time.Now()
	if record == nil {
		record = &model.CreationInspiration{ID: id, CreatedAt: now}
	}
	likes := input.Likes
	if likes < 0 {
		// 展示用的热度不允许为负：负值在卡片上会渲染成 "-12" 这种没人能解释的读数。
		likes = 0
	}
	record.Title = title
	record.Description = truncateRunes(strings.TrimSpace(input.Description), creationInspirationDescriptionMaxLen)
	record.CoverURL = truncateRunes(strings.TrimSpace(input.CoverURL), 1000)
	record.Prompt = truncateRunes(strings.TrimSpace(input.Prompt), creationInspirationPromptMaxLen)
	record.Mode = mode
	record.Category = creationInspirationCategory(input.Category)
	record.Author = truncateRunes(strings.TrimSpace(input.Author), 80)
	record.Likes = likes
	record.SourceURL = truncateRunes(strings.TrimSpace(input.SourceURL), 1000)
	record.Source = truncateRunes(strings.TrimSpace(input.Source), 120)
	record.Status = status
	record.Featured = input.Featured
	record.SortOrder = input.SortOrder
	record.UpdatedAt = now

	if err := s.repo.SaveCreationInspiration(record); err != nil {
		return nil, err
	}
	return creationInspirationView(record), nil
}

// DeleteCreationInspiration 删除一条精选灵感；目标不存在时按 not found 处理。
func (s *Service) DeleteCreationInspiration(id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return BadAuthRequest("缺少灵感标识")
	}
	if err := s.repo.DeleteCreationInspiration(trimmed); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NotFound("精选灵感不存在")
		}
		return err
	}
	return nil
}

// creationInspirationCategory 归一分类：空白回落到默认分类，并截断到列宽上限。
func creationInspirationCategory(raw string) string {
	category := strings.TrimSpace(raw)
	if category == "" {
		category = creationInspirationDefaultCategory
	}
	return truncateRunes(category, 80)
}

func creationInspirationView(record *model.CreationInspiration) *CreationInspirationView {
	if record == nil {
		return nil
	}
	return &CreationInspirationView{
		ID:          record.ID,
		Title:       record.Title,
		Description: record.Description,
		CoverURL:    record.CoverURL,
		Prompt:      record.Prompt,
		Mode:        record.Mode,
		Category:    record.Category,
		Author:      record.Author,
		Likes:       record.Likes,
		SourceURL:   record.SourceURL,
		Source:      record.Source,
		Status:      string(record.Status),
		Featured:    record.Featured,
		SortOrder:   record.SortOrder,
		CreatedAt:   record.CreatedAt,
		UpdatedAt:   record.UpdatedAt,
	}
}

// creationInspirationViews 保证列表位置恒为数组，前端可以直接遍历。
func creationInspirationViews(records []model.CreationInspiration) []CreationInspirationView {
	views := make([]CreationInspirationView, 0, len(records))
	for index := range records {
		views = append(views, *creationInspirationView(&records[index]))
	}
	return views
}
