package app

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

const (
	// canvasTemplateCodeMaxLen 限制业务标识长度：它既是对外稳定的引用键，
	// 也参与唯一索引，放任超长值只会把一次误粘贴变成难排查的写入失败。
	canvasTemplateCodeMaxLen = 64
	// canvasTemplateNameMaxLen 是模板名称上限（按字符计）：后台表格与前台卡片
	// 都按一行展示，超过 40 字的名字在所有列表里都会被截断。
	canvasTemplateNameMaxLen = 40
	// canvasTemplateDefaultCategory 是分类缺省值。分类是自由文本，运营自己定；
	// 不填时归入「通用」，避免出现空分类导致前台筛选少一档。
	canvasTemplateDefaultCategory = "通用"
)

// canvasTemplateCodePattern 限定标识字符集：只允许小写字母、数字与连字符，
// 这样它可以直接作为前台路由片段或文件名片段使用。
var canvasTemplateCodePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// CanvasTemplateView 是画布模板的对外视图；后台与用户端共用同一套字段。
//
// PayloadJSON 原样回显：模板内容由画布客户端解析，服务端不引入第二份解析规则，
// 否则画布协议一升级，后台读到的结构就会与用户实际加载的不一致。
type CanvasTemplateView struct {
	ID          string    `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	CoverURL    string    `json:"coverUrl"`
	PayloadJSON string    `json:"payloadJson"`
	Status      string    `json:"status"`
	Featured    bool      `json:"featured"`
	SortOrder   int       `json:"sortOrder"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// CanvasTemplateInput 是新建 / 编辑画布模板的入参。
type CanvasTemplateInput struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	CoverURL    string `json:"coverUrl"`
	PayloadJSON string `json:"payloadJson"`
	Status      string `json:"status"`
	Featured    bool   `json:"featured"`
	SortOrder   int    `json:"sortOrder"`
}

// AdminCanvasTemplates 返回后台模板列表（含已下架）。
//
// 不带 actor 参数：路由挂在管理员守卫之下，鉴权在 HTTP 层收敛，服务层只负责
// 读取与校验，避免同一处判断在两套入口里出现不同结果。
func (s *Service) AdminCanvasTemplates() ([]CanvasTemplateView, error) {
	records, err := s.repo.AdminCanvasTemplates()
	if err != nil {
		return nil, err
	}
	return canvasTemplateViews(records), nil
}

// CanvasTemplateCatalog 返回用户端可见的模板目录，只含已上架模板。
func (s *Service) CanvasTemplateCatalog() ([]CanvasTemplateView, error) {
	records, err := s.repo.CanvasTemplateCatalog()
	if err != nil {
		return nil, err
	}
	return canvasTemplateViews(records), nil
}

// SaveCanvasTemplate 新建或更新一条画布模板。
//
// 校验在写库前一次做完：标识字符集与长度、名称长度、上下架状态取值。ID 非空时
// 必须命中已有行，否则按 not found 处理——把「编辑一个不存在的 id」静默降级成
// 新建，会让一次误操作凭空多出一条模板。
func (s *Service) SaveCanvasTemplate(input CanvasTemplateInput) (*CanvasTemplateView, error) {
	code := strings.TrimSpace(input.Code)
	if !canvasTemplateCodePattern.MatchString(code) {
		return nil, BadAuthRequest("模板标识只允许小写字母、数字与连字符")
	}
	if len(code) > canvasTemplateCodeMaxLen {
		return nil, BadAuthRequest("模板标识过长，请控制在 64 个字符以内")
	}
	name := strings.TrimSpace(input.Name)
	if runeCount := utf8.RuneCountInString(name); runeCount == 0 || runeCount > canvasTemplateNameMaxLen {
		return nil, BadAuthRequest("模板名称需为 1 到 40 个字符")
	}
	status := model.CanvasTemplateStatus(strings.ToUpper(strings.TrimSpace(input.Status)))
	if status != model.CanvasTemplateOnline && status != model.CanvasTemplateOffline {
		return nil, BadAuthRequest("模板状态取值无效")
	}

	id := strings.TrimSpace(input.ID)
	var record *model.CanvasTemplate
	if id != "" {
		existing, err := s.repo.CanvasTemplateByID(id)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("画布模板不存在")
		}
		if err != nil {
			return nil, err
		}
		record = existing
	}

	// 唯一性检查放在写入前给出可读提示；数据库唯一索引仍是最终防线（并发下两次
	// 同码写入只会有一条成功，失败方落成 5xx 由上层记录，不会产生重复行）。
	conflict, err := s.repo.CanvasTemplateByCode(code)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if conflict != nil && conflict.ID != id {
		return nil, NewAppError(http.StatusConflict, "该模板标识已被占用，请换一个")
	}

	now := time.Now()
	if record == nil {
		record = &model.CanvasTemplate{ID: id, CreatedAt: now}
	}
	record.Code = code
	record.Name = name
	record.Description = strings.TrimSpace(input.Description)
	record.Category = canvasTemplateCategory(input.Category)
	record.CoverURL = truncateRunes(strings.TrimSpace(input.CoverURL), 1000)
	record.PayloadJSON = input.PayloadJSON
	record.Status = status
	record.Featured = input.Featured
	record.SortOrder = input.SortOrder
	record.UpdatedAt = now

	if err := s.repo.SaveCanvasTemplate(record); err != nil {
		return nil, err
	}
	return canvasTemplateView(record), nil
}

// DeleteCanvasTemplate 删除一条画布模板；目标不存在时按 not found 处理。
func (s *Service) DeleteCanvasTemplate(id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return BadAuthRequest("缺少模板标识")
	}
	if err := s.repo.DeleteCanvasTemplate(trimmed); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NotFound("画布模板不存在")
		}
		return err
	}
	return nil
}

// canvasTemplateCategory 归一分类：空白回落到默认分类，并截断到列宽上限。
func canvasTemplateCategory(raw string) string {
	category := strings.TrimSpace(raw)
	if category == "" {
		category = canvasTemplateDefaultCategory
	}
	return truncateRunes(category, 80)
}

func canvasTemplateView(record *model.CanvasTemplate) *CanvasTemplateView {
	if record == nil {
		return nil
	}
	return &CanvasTemplateView{
		ID:          record.ID,
		Code:        record.Code,
		Name:        record.Name,
		Description: record.Description,
		Category:    record.Category,
		CoverURL:    record.CoverURL,
		PayloadJSON: record.PayloadJSON,
		Status:      string(record.Status),
		Featured:    record.Featured,
		SortOrder:   record.SortOrder,
		CreatedAt:   record.CreatedAt,
		UpdatedAt:   record.UpdatedAt,
	}
}

// canvasTemplateViews 保证列表位置恒为数组，前端可以直接遍历。
func canvasTemplateViews(records []model.CanvasTemplate) []CanvasTemplateView {
	views := make([]CanvasTemplateView, 0, len(records))
	for index := range records {
		views = append(views, *canvasTemplateView(&records[index]))
	}
	return views
}
