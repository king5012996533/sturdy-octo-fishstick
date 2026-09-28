package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/protocol"

	"gorm.io/gorm"
)

const (
	// vendorNameMaxLen 与列表列宽一致：厂商名只在一行里展示，超过 80 字的输入
	// 在任何表格里都只会被截断，不如在写入前收敛。
	vendorNameMaxLen = 80
	// vendorDocsURLMaxLen 是文档地址上限，避免把超长 URL 当作备注字段使用。
	vendorDocsURLMaxLen = 1000
	// vendorCredentialNameMaxLen 是凭据名上限；它与厂商名拼成渠道名，两者都要
	// 留出空间，否则“厂商名很长的厂商”下的渠道名会互相看不出差别。
	vendorCredentialNameMaxLen = 80
	// vendorChannelNameMaxLen 与 model_channels.name 的列宽一致。
	vendorChannelNameMaxLen = 80
	// vendorCredentialDefaultName 是凭据名的缺省值：凭据本身可以不填名字，
	// 但渠道名必须可读，否则后台里会出现一串“厂商名 · ”。
	vendorCredentialDefaultName = "默认凭据"
	// vendorKeyHintLen 只保留密钥尾号：展示用，也足以让运营分辨“换没换过”。
	vendorKeyHintLen = 4
	// vendorCredentialDefaultWeight 是轮询权重缺省值：新建凭据默认等权参与轮询。
	vendorCredentialDefaultWeight = 100
	// vendorCredentialMinWeight / MaxWeight 是权重取值范围：0 会让一条凭据在轮询里
	// 永远拿不到流量，却仍在后台显示为“启用”，这种状态应该用 enabled 表达。
	vendorCredentialMinWeight = 1
	vendorCredentialMaxWeight = 1000
	// vendorManualModelLimit 是手动登记的单次上限：这是"补几条被目录漏掉的模型"的入口，
	// 不是批量导入通道，每条都要向上游发一次校验请求，放开会变成压测自己的上游。
	vendorManualModelLimit = 50
)

// vendorCodePattern 与契约一致：小写字母数字开头，后接小写字母数字或连字符。
// 不允许大写与下划线，是为了让标识可以直接作为配置键与路由片段使用。
var vendorCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,63}$`)

// vendorManualModelPattern 约束手动登记的模型标识：允许 owner/name 这类多段形式，
// 但不允许空格、问号、井号等会改变请求路径或查询串的字符。
var vendorManualModelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)*$`)

// VendorCatalogItem 是协议注册表里的一个可接入厂商，后台新增厂商时从这里选。
type VendorCatalogItem struct {
	Code         string   `json:"code"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	Protocols    []string `json:"protocols"`
	DocsURL      string   `json:"docsUrl"`
}

// VendorView 是厂商的对外视图。Capabilities / Protocols 由目录带出，运营不手填。
type VendorView struct {
	ID              string   `json:"id"`
	Code            string   `json:"code"`
	Name            string   `json:"name"`
	Kind            string   `json:"kind"`
	Enabled         bool     `json:"enabled"`
	SortOrder       int      `json:"sortOrder"`
	Capabilities    []string `json:"capabilities"`
	Protocols       []string `json:"protocols"`
	DocsURL         string   `json:"docsUrl"`
	CredentialCount int      `json:"credentialCount"`
	ModelCount      int      `json:"modelCount"`
	CreatedAt       string   `json:"createdAt"`
	UpdatedAt       string   `json:"updatedAt"`
}

// CredentialView 是凭据（厂商下的接入点）的对外视图。
//
// 不回显密钥本体，只回“有没有配”与尾号：后台要能看出密钥是否可用，但没有任何
// 场景需要把明文再送到浏览器一次。
type CredentialView struct {
	ID            string `json:"id"`
	VendorID      string `json:"vendorId"`
	Name          string `json:"name"`
	ChannelID     string `json:"channelId"`
	KeyHint       string `json:"keyHint"`
	BaseURL       string `json:"baseUrl"`
	APIFormat     string `json:"apiFormat"`
	Enabled       bool   `json:"enabled"`
	Weight        int    `json:"weight"`
	ModelCount    int    `json:"modelCount"`
	HasAPIKey     bool   `json:"hasApiKey"`
	HasSecretKey  bool   `json:"hasSecretKey"`
	LastError     string `json:"lastError"`
	LastCheckedAt string `json:"lastCheckedAt"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

// VendorInput 是新建 / 编辑厂商的入参。
type VendorInput struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	DocsURL   string `json:"docsUrl"`
	Enabled   *bool  `json:"enabled"`
	SortOrder *int   `json:"sortOrder"`
}

// CredentialInput 是新建 / 编辑凭据的入参。
//
// APIKey / SecretKey 用“留空表示不改”的口径：与渠道表单一致，避免后台只想改名称
// 却因为拿不到明文而被迫重新粘贴一次密钥。
type CredentialInput struct {
	Name             string   `json:"name"`
	BaseURL          string   `json:"baseUrl"`
	APIKey           string   `json:"apiKey"`
	SecretKey        string   `json:"secretKey"`
	APIFormat        string   `json:"apiFormat"`
	ConcurrencyLimit *int     `json:"concurrencyLimit"`
	Enabled          *bool    `json:"enabled"`
	Weight           *int     `json:"weight"`
	Models           []string `json:"models"`
}

// VendorCatalog 返回可接入的厂商目录。
//
// 目录完全来自协议注册表，不在这里维护第二份清单：厂商能不能接，取决于二进制里
// 有没有对应协议适配器，手写的清单迟早会和协议注册表脱节。
// VendorCatalog 返回"运营可以接哪些上游"。
//
// 数据源是运行时注册表（插件包 + 内置适配器的合并结果），不是 protocol.Builtins()：
// 官方厂商里有八十多家是声明式插件包（Replicate / fal.ai / SiliconFlow / Kling /
// Runway …），它们只有装进实例之后才存在。只读 Builtins 会让这整批厂商从目录里
// 消失——运营在后台挑不到 Replicate，代码里其实早就写着它的请求协议。
func (s *Service) VendorCatalog() ([]VendorCatalogItem, error) {
	builtin := vendorCatalogItems(protocol.Builtins())
	return mergeVendorCatalog(builtin, s.pluginVendorCatalog(builtin)), nil
}

// pluginVendorCodeAliases 收掉"模型家族名被当成厂商"的少数几个。
//
// gemini-image / grok-image 这类 provider ID 的首段是模型家族而不是厂商，按首段推导
// 会让同一个上游在后台裂成两行（Google 与 Gemini、xAI 与 Grok），运营往两边各配一条
// 凭据后账就对不上了。表刻意保持极小：只有当首段确实指向某个已有厂商的同一条上游时
// 才补一行，不要用它来给厂商改名。
var pluginVendorCodeAliases = map[string]string{
	"gemini": "google",
	"grok":   "xai",
}

// pluginVendorCatalog 把插件贡献的 provider 归到厂商名下。
//
// 插件包不提供厂商归属：全部 84 个包的 author 都是 "BeefTV Contributors"，直接拿
// manifest 作者当厂商会让所有上游塌成一行。归组规则有两条：
//
//  1. provider ID 命中内置协议（claude-api、gemini-image、grok-image、chat-completion
//     这些在内置协议表里已有）时，跟内置协议的厂商走——插件只是把同一个协议换成了
//     声明式实现，厂商仍是 Anthropic / Google / xAI，按 ID 首段猜会凭空多出
//     claude、gemini、grok、chat 四个假厂商。
//  2. 其余按 provider ID 首段推导（replicate-prediction-image → replicate、
//     fal-queue-video → fal），展示名取 label 首段品牌词。
func (s *Service) pluginVendorCatalog(builtin []VendorCatalogItem) []VendorCatalogItem {
	vendorByProtocol := make(map[string]string)
	for _, item := range builtin {
		for _, protocolID := range item.Protocols {
			if _, exists := vendorByProtocol[protocolID]; !exists {
				vendorByProtocol[protocolID] = item.Code
			}
		}
	}
	plugins := s.Plugins()
	entries := make(map[string]*VendorCatalogItem)
	capabilitySets := make(map[string]map[string]struct{})
	for _, plugin := range plugins {
		for _, provider := range plugin.Manifest.Contributes.Providers {
			providerID := strings.TrimSpace(provider.ID)
			code := vendorByProtocol[providerID]
			if code == "" {
				code = vendorCodeFromProviderID(providerID)
				if alias, aliased := pluginVendorCodeAliases[code]; aliased {
					code = alias
				}
			}
			if code == "" {
				continue
			}
			item, exists := entries[code]
			if !exists {
				item = &VendorCatalogItem{Code: code, Name: vendorNameFromProviderLabel(provider.Label, code), Capabilities: []string{}, Protocols: []string{}}
				entries[code] = item
				capabilitySets[code] = make(map[string]struct{})
			}
			for _, capability := range provider.Capabilities {
				key := strings.ToUpper(strings.TrimSpace(string(capability)))
				if key == "" {
					continue
				}
				if _, duplicate := capabilitySets[code][key]; duplicate {
					continue
				}
				capabilitySets[code][key] = struct{}{}
				item.Capabilities = append(item.Capabilities, key)
			}
			if providerID != "" {
				item.Protocols = append(item.Protocols, providerID)
			}
		}
	}
	items := make([]VendorCatalogItem, 0, len(entries))
	for _, item := range entries {
		sort.Strings(item.Capabilities)
		sort.Strings(item.Protocols)
		items = append(items, *item)
	}
	sort.Slice(items, func(left int, right int) bool { return items[left].Code < items[right].Code })
	return items
}

// mergeVendorCatalog 合并内置厂商与插件厂商，并把短标识折进长标识。
//
// volcengine（插件）与 volcengine-ark（内置）说的是同一个上游，agnes 与 agnes-ai
// 同理：不折叠的话后台会出现两行同一个厂商，运营会往两边各配一条凭据、再各自定价，
// 最后对不出账。折叠方向固定为"并进更长的那个"，因为更长的那个来自内置目录，
// 厂商名是权威的。
func mergeVendorCatalog(primary []VendorCatalogItem, extra []VendorCatalogItem) []VendorCatalogItem {
	merged := make([]VendorCatalogItem, 0, len(primary)+len(extra))
	index := make(map[string]int, len(primary)+len(extra))
	capabilitySets := make(map[string]map[string]struct{})
	appendItem := func(item VendorCatalogItem) {
		if position, exists := index[item.Code]; exists {
			target := &merged[position]
			set := capabilitySets[item.Code]
			for _, capability := range item.Capabilities {
				if _, duplicate := set[capability]; duplicate {
					continue
				}
				set[capability] = struct{}{}
				target.Capabilities = append(target.Capabilities, capability)
			}
			for _, protocolID := range item.Protocols {
				if !containsString(target.Protocols, protocolID) {
					target.Protocols = append(target.Protocols, protocolID)
				}
			}
			if target.DocsURL == "" {
				target.DocsURL = item.DocsURL
			}
			return
		}
		index[item.Code] = len(merged)
		capabilitySets[item.Code] = make(map[string]struct{}, len(item.Capabilities))
		for _, capability := range item.Capabilities {
			capabilitySets[item.Code][capability] = struct{}{}
		}
		merged = append(merged, item)
	}
	for _, item := range primary {
		appendItem(item)
	}
	for _, item := range extra {
		if target, folded := foldVendorCode(index, item.Code); folded {
			item.Code = target
		}
		appendItem(item)
	}
	for position := range merged {
		sort.Strings(merged[position].Capabilities)
		sort.Strings(merged[position].Protocols)
	}
	sort.Slice(merged, func(left int, right int) bool { return merged[left].Code < merged[right].Code })
	return merged
}

// foldVendorCode 找出 code 应该并进哪个已有厂商：只认"前缀 + 连字符"这一种关系，
// 避免把 openai 和 openrouter 这类只共享字符串前缀的两家误并。
func foldVendorCode(index map[string]int, code string) (string, bool) {
	for existing := range index {
		if strings.HasPrefix(existing, code+"-") || strings.HasPrefix(code, existing+"-") {
			target := code
			if len(existing) > len(code) {
				target = existing
			}
			if target != code {
				return target, true
			}
		}
	}
	return code, false
}

// vendorCodeFromProviderID 取 provider ID 的首段作为厂商标识。
func vendorCodeFromProviderID(id string) string {
	trimmed := strings.ToLower(strings.TrimSpace(id))
	if trimmed == "" {
		return ""
	}
	if index := strings.Index(trimmed, "-"); index > 0 {
		trimmed = trimmed[:index]
	}
	return vendorCodeFromName(trimmed)
}

// providerLabelShapeWords 是 label 里描述"接口形态"的词：厂商名到它们为止。
//
// 直接取首词会把 "Black Forest Labs FLUX" 压成 "Black"、"SiliconFlow Chat" 的
// "SiliconFlow" 倒是正好——用词表截断比按词数截断更稳，中文 label 没有空格时也
// 会原样保留。
var providerLabelShapeWords = map[string]struct{}{
	"queue": {}, "predictions": {}, "prediction": {}, "chat": {}, "completions": {},
	"completion": {}, "video": {}, "videos": {}, "image": {}, "images": {},
	"audio": {}, "audios": {}, "messages": {}, "workflow": {}, "generate": {},
	"generation": {}, "text": {}, "native": {}, "task": {}, "tasks": {},
	"compatible": {}, "api": {}, "model": {}, "models": {},
}

// vendorNameFromProviderLabel 取 label 里"厂商名"这一段作为展示名；拿不到就用标识兜底。
func vendorNameFromProviderLabel(label string, fallback string) string {
	fields := strings.Fields(strings.TrimSpace(label))
	kept := make([]string, 0, len(fields))
	for _, field := range fields {
		if _, isShape := providerLabelShapeWords[strings.ToLower(field)]; isShape {
			break
		}
		kept = append(kept, field)
	}
	if len(kept) == 0 {
		if len(fields) > 0 {
			return fields[0]
		}
		return fallback
	}
	return strings.Join(kept, " ")
}
func (s *Service) AdminModelVendors() ([]VendorView, error) {
	records, err := s.repo.ModelVendors()
	if err != nil {
		return nil, err
	}
	views := make([]VendorView, 0, len(records))
	for index := range records {
		view, viewErr := s.vendorView(&records[index])
		if viewErr != nil {
			return nil, viewErr
		}
		views = append(views, *view)
	}
	return views, nil
}

// SaveModelVendor 新建或更新一条厂商。
//
// 目录里命中 Code 的按 BUILTIN 处理，并把能力与协议清单带出来；自定义 Code 则留空，
// 由模型配置去描述它实际支持什么。改名改 Code 都会重新判定 Kind，避免出现
// “名字已经换成自定义厂商、却还挂着一份内置能力清单”的错位。
func (s *Service) SaveModelVendor(input VendorInput, id string) (*VendorView, error) {
	code := strings.TrimSpace(input.Code)
	if !vendorCodePattern.MatchString(code) {
		return nil, BadAuthRequest("厂商标识只允许小写字母、数字与连字符，长度 2-64 位")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, BadAuthRequest("请填写厂商名称")
	}
	name = truncateRunes(name, vendorNameMaxLen)

	targetID := strings.TrimSpace(id)
	var record *model.ModelVendor
	if targetID != "" {
		existing, err := s.repo.ModelVendorByID(targetID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("厂商不存在")
		}
		if err != nil {
			return nil, err
		}
		record = existing
	}

	// 唯一性检查在写入前给出可读提示；数据库唯一索引仍是并发下的最终防线。
	conflict, err := s.repo.ModelVendorByCode(code)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if conflict != nil && conflict.ID != targetID {
		return nil, NewAppError(http.StatusConflict, "厂商标识已存在")
	}

	catalogItem, hasCatalogItem := s.vendorCatalogIndex()[code]
	now := time.Now()
	if record == nil {
		record = &model.ModelVendor{ID: targetID, CreatedAt: now}
	}
	record.Code = code
	record.Name = name
	record.Kind = model.ModelVendorKindCustom
	record.CapabilitiesJSON = encodeVendorStringList(nil)
	record.ProtocolsJSON = encodeVendorStringList(nil)
	if hasCatalogItem {
		record.Kind = model.ModelVendorKindBuiltin
		record.CapabilitiesJSON = encodeVendorStringList(catalogItem.Capabilities)
		record.ProtocolsJSON = encodeVendorStringList(catalogItem.Protocols)
	}
	record.DocsURL = truncateRunes(firstNonEmpty(strings.TrimSpace(input.DocsURL), catalogItem.DocsURL), vendorDocsURLMaxLen)
	if input.Enabled != nil {
		record.Enabled = *input.Enabled
	} else if targetID == "" {
		record.Enabled = true
	}
	if input.SortOrder != nil {
		record.SortOrder = *input.SortOrder
	}
	record.UpdatedAt = now

	if err := s.repo.SaveModelVendor(record); err != nil {
		return nil, err
	}
	return s.vendorView(record)
}

// DeleteModelVendor 删除一条厂商；厂商下仍有凭据时拒绝。
//
// 不级联删除凭据：凭据背后是真实的接入点与计费流量，一次误点不应该连带停掉线上
// 供应；让运营显式先清凭据，等于多一道确认。
func (s *Service) DeleteModelVendor(id string) error {
	targetID := strings.TrimSpace(id)
	if targetID == "" {
		return BadAuthRequest("缺少厂商标识")
	}
	count, err := s.repo.CountCredentialsByVendor(targetID)
	if err != nil {
		return err
	}
	if count > 0 {
		return NewAppError(http.StatusConflict, "请先删除该厂商下的凭据")
	}
	if err := s.repo.DeleteModelVendor(targetID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NotFound("厂商不存在")
		}
		return err
	}
	return nil
}

// VendorCredentials 返回某厂商下的凭据列表（含模型数与密钥配置状态）。
func (s *Service) VendorCredentials(vendorID string) ([]CredentialView, error) {
	records, err := s.repo.VendorCredentials(vendorID)
	if err != nil {
		return nil, err
	}
	views := make([]CredentialView, 0, len(records))
	for index := range records {
		view, viewErr := s.credentialView(&records[index])
		if viewErr != nil {
			return nil, viewErr
		}
		views = append(views, *view)
	}
	return views, nil
}

// SaveVendorCredential 新建或更新一条凭据。
//
// 凭据本身不存密钥：新建时先走 CreateSystemChannel 建出 system 渠道（沿用加密与
// 脱敏），再把渠道主键与展示用信息写进 vendor_credentials。编辑时 API Key 留空
// 表示不改密钥，只有真的传了新 Key 才更新尾号。
func (s *Service) SaveVendorCredential(actor *model.User, vendorID string, input CredentialInput, id string) (*CredentialView, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	vendor, err := s.repo.ModelVendorByID(vendorID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("厂商不存在")
		}
		return nil, err
	}
	if input.Weight != nil && (*input.Weight < vendorCredentialMinWeight || *input.Weight > vendorCredentialMaxWeight) {
		return nil, BadAuthRequest("轮询权重必须是 1-1000 的整数")
	}
	name := truncateRunes(strings.TrimSpace(input.Name), vendorCredentialNameMaxLen)
	if name == "" {
		name = vendorCredentialDefaultName
	}
	// 渠道名带上厂商名：后台的渠道列表是平铺的，光看凭据名分不清它属于谁。
	channelName := truncateRunes(vendor.Name+" · "+name, vendorChannelNameMaxLen)

	credentialID := strings.TrimSpace(id)
	if credentialID == "" {
		weight := vendorCredentialDefaultWeight
		if input.Weight != nil {
			weight = *input.Weight
		}
		return s.createVendorCredential(actor, vendor, name, channelName, input, weight)
	}
	return s.updateVendorCredential(actor, vendor, credentialID, name, channelName, input)
}

func (s *Service) createVendorCredential(actor *model.User, vendor *model.ModelVendor, name string, channelName string, input CredentialInput, weight int) (*CredentialView, error) {
	if strings.TrimSpace(input.APIKey) == "" {
		return nil, BadAuthRequest("API Key 必填")
	}
	public, err := s.CreateSystemChannel(actor, ChannelRequest{
		Name:             channelName,
		BaseURL:          input.BaseURL,
		APIKey:           input.APIKey,
		SecretKey:        input.SecretKey,
		ConcurrencyLimit: input.ConcurrencyLimit,
		Enabled:          input.Enabled,
		Models:           input.Models,
	})
	if err != nil {
		return nil, err
	}
	credential := &model.VendorCredential{
		VendorID:  vendor.ID,
		Name:      name,
		ChannelID: public.ID,
		KeyHint:   vendorKeyHint(input.APIKey),
		BaseURL:   public.BaseURL,
		APIFormat: vendorAPIFormat(input.APIFormat, public.APIFormat),
		Enabled:   public.Enabled,
		Weight:    weight,
	}
	if err := s.repo.SaveVendorCredential(credential); err != nil {
		return nil, err
	}
	return s.credentialView(credential)
}

func (s *Service) updateVendorCredential(actor *model.User, vendor *model.ModelVendor, credentialID string, name string, channelName string, input CredentialInput) (*CredentialView, error) {
	credential, err := s.repo.VendorCredentialByID(credentialID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("凭据不存在")
		}
		return nil, err
	}
	if credential.VendorID != vendor.ID {
		return nil, NotFound("凭据不存在")
	}

	request := ChannelRequest{
		Name:             channelName,
		BaseURL:          input.BaseURL,
		APIKey:           input.APIKey,
		SecretKey:        input.SecretKey,
		ConcurrencyLimit: input.ConcurrencyLimit,
		Enabled:          input.Enabled,
	}
	// Models 只在显式传入时覆盖：模型清单由导入接口维护，普通编辑不该顺手重置它。
	if input.Models != nil {
		request.Models = input.Models
	}
	public, err := s.UpdateSystemChannel(actor, credential.ChannelID, request)
	if err != nil {
		return nil, err
	}

	credential.Name = name
	credential.BaseURL = public.BaseURL
	credential.Enabled = public.Enabled
	if input.Weight != nil {
		credential.Weight = *input.Weight
	} else if credential.Weight < vendorCredentialMinWeight || credential.Weight > vendorCredentialMaxWeight {
		credential.Weight = vendorCredentialDefaultWeight
	}
	if format := strings.TrimSpace(input.APIFormat); format != "" {
		credential.APIFormat = format
	}
	if strings.TrimSpace(input.APIKey) != "" {
		credential.KeyHint = vendorKeyHint(input.APIKey)
	}
	if err := s.repo.SaveVendorCredential(credential); err != nil {
		return nil, err
	}
	return s.credentialView(credential)
}

// DeleteVendorCredential 删除一条凭据：先校验归属，再软删凭据行并删除对应渠道。
//
// 渠道必须一起删：只删凭据会留下一条仍在参与路由的 system 渠道，后台却看不到它，
// 等于制造了一个无法管理的线上供应入口。
func (s *Service) DeleteVendorCredential(actor *model.User, vendorID string, id string) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	if _, err := s.repo.ModelVendorByID(vendorID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NotFound("厂商不存在")
		}
		return err
	}
	credential, err := s.vendorCredentialOwnedBy(vendorID, id)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteVendorCredential(credential.ID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NotFound("凭据不存在")
		}
		return err
	}
	return s.DeleteSystemChannel(actor, credential.ChannelID)
}

// VendorCredentialModels 返回凭据对应渠道下的模型列表。
func (s *Service) VendorCredentialModels(actor *model.User, vendorID string, credentialID string) ([]model.ChannelModel, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	credential, err := s.vendorCredentialOwnedBy(vendorID, credentialID)
	if err != nil {
		return nil, err
	}
	return s.AdminChannelModels(actor, credential.ChannelID)
}

// ImportVendorCredentialModels 从上游目录导入选定模型到凭据对应渠道。
func (s *Service) ImportVendorCredentialModels(ctx context.Context, actor *model.User, vendorID string, credentialID string, models []string) (*AdminChannelModelFetchResult, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	credential, err := s.vendorCredentialOwnedBy(vendorID, credentialID)
	if err != nil {
		return nil, err
	}
	return s.ImportAdminChannelModels(ctx, actor, credential.ChannelID, models)
}

// AddVendorCredentialModels 按模型标识逐个向上游核对后登记。
//
// 分页目录拉不全上游模型：Replicate 的公开目录按发布时间倒序、一页 25 条，要卖的
// 常用模型基本排在后面。这个入口让运营直接按 owner/name 登记，代价是每个标识都要先
// 向上游单模型接口核对——否则拼错的 ID 会先写进路由表，直到用户发起任务才报错。
func (s *Service) AddVendorCredentialModels(ctx context.Context, actor *model.User, vendorID string, credentialID string, models []string) (*AdminChannelModelFetchResult, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	credential, err := s.vendorCredentialOwnedBy(vendorID, credentialID)
	if err != nil {
		return nil, err
	}
	channel, err := s.adminSystemChannel(credential.ChannelID)
	if err != nil {
		return nil, err
	}
	headers, err := ParseOutboundHeadersJSON(channel.HeadersJSON)
	if err != nil {
		return nil, err
	}
	request := ChannelModelsRequest{BaseURL: channel.BaseURL, APIKey: channel.APIKey, APIFormat: channel.APIFormat, Headers: headers}

	chosen := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, raw := range models {
		name := strings.TrimPrefix(strings.TrimSpace(raw), "/")
		if name == "" {
			continue
		}
		if !vendorManualModelPattern.MatchString(name) {
			return nil, BadAuthRequest("模型标识只能包含字母、数字、点、下划线与连字符，多段用 / 分隔：" + name)
		}
		key := channelModelCatalogKey(name)
		if _, ok := seen[key]; ok {
			continue
		}
		if len(chosen) >= vendorManualModelLimit {
			return nil, BadAuthRequest(fmt.Sprintf("单次最多登记 %d 个模型，超过请分批", vendorManualModelLimit))
		}
		seen[key] = struct{}{}
		if err := s.VerifyUpstreamChannelModel(ctx, request, name); err != nil {
			return nil, err
		}
		chosen = append(chosen, name)
	}
	if len(chosen) == 0 {
		return nil, BadAuthRequest("请至少填写一个模型标识")
	}
	added, err := s.registerChannelModels(channel, chosen)
	if err != nil {
		return nil, err
	}
	return &AdminChannelModelFetchResult{Models: chosen, Added: added}, nil
}

// ProbeVendorCredentialModels 探测上游模型目录，并把结果写回凭据的最近检查记录。
//
// 探测失败也要落库：运营在后台看到的是“最近一次检查失败 + 原文”，而不是一个
// 只在响应里闪一下、刷新页面就消失的错误。
func (s *Service) ProbeVendorCredentialModels(ctx context.Context, actor *model.User, vendorID string, credentialID string) ([]string, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	credential, err := s.vendorCredentialOwnedBy(vendorID, credentialID)
	if err != nil {
		return nil, err
	}
	models, probeErr := s.PreviewAdminChannelModels(ctx, actor, credential.ChannelID)
	checkedAt := time.Now()
	credential.LastCheckedAt = &checkedAt
	credential.LastError = ""
	if probeErr != nil {
		credential.LastError = truncateRunes(probeErr.Error(), 500)
	}
	if saveErr := s.repo.SaveVendorCredential(credential); saveErr != nil {
		return nil, saveErr
	}
	if probeErr != nil {
		return nil, probeErr
	}
	return models, nil
}

// vendorCredentialOwnedBy 读取凭据并校验它确实属于该厂商。
//
// 归属校验不能省：凭据 ID 是路径参数，缺了这一层，换一个 ID 就能操作别的厂商的
// 接入点，而返回 404 而不是 403 可以避免暴露“这个 ID 在别处存在”。
func (s *Service) vendorCredentialOwnedBy(vendorID string, credentialID string) (*model.VendorCredential, error) {
	targetID := strings.TrimSpace(credentialID)
	if targetID == "" {
		return nil, NotFound("凭据不存在")
	}
	if _, err := s.repo.ModelVendorByID(vendorID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("厂商不存在")
		}
		return nil, err
	}
	credential, err := s.repo.VendorCredentialByID(targetID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("凭据不存在")
		}
		return nil, err
	}
	if credential.VendorID != strings.TrimSpace(vendorID) {
		return nil, NotFound("凭据不存在")
	}
	return credential, nil
}

func (s *Service) vendorView(record *model.ModelVendor) (*VendorView, error) {
	if record == nil {
		return nil, nil
	}
	credentialCount, err := s.repo.CountCredentialsByVendor(record.ID)
	if err != nil {
		return nil, err
	}
	modelCount, err := s.repo.CountModelsByVendor(record.ID)
	if err != nil {
		return nil, err
	}
	return &VendorView{
		ID:              record.ID,
		Code:            record.Code,
		Name:            record.Name,
		Kind:            record.Kind,
		Enabled:         record.Enabled,
		SortOrder:       record.SortOrder,
		Capabilities:    decodeVendorStringList(record.CapabilitiesJSON),
		Protocols:       decodeVendorStringList(record.ProtocolsJSON),
		DocsURL:         record.DocsURL,
		CredentialCount: int(credentialCount),
		ModelCount:      int(modelCount),
		CreatedAt:       vendorTimeString(record.CreatedAt),
		UpdatedAt:       vendorTimeString(record.UpdatedAt),
	}, nil
}

// credentialView 投影凭据视图。
//
// 密钥“有没有配”以渠道表为准而不是凭据行的 KeyHint：KeyHint 只记录过一次写入，
// 密钥被渠道侧清空（例如删除渠道）时它不会跟着变，只信它会把已失效的凭据显示成可用。
func (s *Service) credentialView(record *model.VendorCredential) (*CredentialView, error) {
	if record == nil {
		return nil, nil
	}
	modelCount, err := s.repo.CountChannelModels(record.ChannelID)
	if err != nil {
		return nil, err
	}
	hasAPIKey := strings.TrimSpace(record.KeyHint) != ""
	hasSecretKey := false
	if channel, channelErr := s.repo.AdminSystemChannel(record.ChannelID); channelErr == nil {
		hasAPIKey = strings.TrimSpace(channel.APIKey) != ""
		hasSecretKey = strings.TrimSpace(channel.SecretKey) != ""
	}
	return &CredentialView{
		ID:            record.ID,
		VendorID:      record.VendorID,
		Name:          record.Name,
		ChannelID:     record.ChannelID,
		KeyHint:       record.KeyHint,
		BaseURL:       record.BaseURL,
		APIFormat:     record.APIFormat,
		Enabled:       record.Enabled,
		Weight:        record.Weight,
		ModelCount:    int(modelCount),
		HasAPIKey:     hasAPIKey,
		HasSecretKey:  hasSecretKey,
		LastError:     record.LastError,
		LastCheckedAt: vendorTimeStringPtr(record.LastCheckedAt),
		CreatedAt:     vendorTimeString(record.CreatedAt),
		UpdatedAt:     vendorTimeString(record.UpdatedAt),
	}, nil
}

// vendorCatalogItems 把协议注册表聚合成厂商目录。
//
// 同一个厂商可能注册了多个协议（OpenAI 的对话与响应、新 API 的两条视频链路），
// 目录里合并成一条：后台要选的是“接哪家”，协议差异由模型配置决定。
func vendorCatalogItems(registry *protocol.Registry) []VendorCatalogItem {
	if registry == nil {
		return []VendorCatalogItem{}
	}
	type catalogEntry struct {
		item         VendorCatalogItem
		capabilities map[string]struct{}
	}
	entries := make(map[string]*catalogEntry)
	// includeUnavailable 为真：目录是给运营选“要不要接”的，协议在当前二进制里
	// 暂不可用不该从选择列表里消失，否则加装协议后还要靠记忆去重配一遍。
	for _, metadata := range registry.List("", "", true) {
		vendorName := strings.TrimSpace(metadata.Vendor)
		if vendorName == "" {
			continue
		}
		code := vendorCodeFromName(vendorName)
		if code == "" {
			continue
		}
		entry, exists := entries[code]
		if !exists {
			entry = &catalogEntry{
				item:         VendorCatalogItem{Code: code, Name: vendorName, Capabilities: []string{}, Protocols: []string{}},
				capabilities: make(map[string]struct{}),
			}
			entries[code] = entry
		}
		for _, capability := range metadata.Categories {
			key := strings.ToUpper(strings.TrimSpace(string(capability)))
			if key == "" {
				continue
			}
			if _, exists := entry.capabilities[key]; exists {
				continue
			}
			entry.capabilities[key] = struct{}{}
			entry.item.Capabilities = append(entry.item.Capabilities, key)
		}
		if protocolID := strings.TrimSpace(metadata.ID); protocolID != "" {
			entry.item.Protocols = append(entry.item.Protocols, protocolID)
		}
		if entry.item.DocsURL == "" {
			entry.item.DocsURL = strings.TrimSpace(metadata.Documentation)
		}
	}
	items := make([]VendorCatalogItem, 0, len(entries))
	for _, entry := range entries {
		sort.Strings(entry.item.Capabilities)
		sort.Strings(entry.item.Protocols)
		items = append(items, entry.item)
	}
	sort.Slice(items, func(left int, right int) bool { return items[left].Code < items[right].Code })
	return items
}

func (s *Service) vendorCatalogIndex() map[string]VendorCatalogItem {
	index := make(map[string]VendorCatalogItem)
	catalog, err := s.VendorCatalog()
	if err != nil {
		return index
	}
	for _, item := range catalog {
		index[item.Code] = item
	}
	return index
}

// vendorCodeFromName 把厂商展示名压成稳定标识：转小写、空白与其他分隔符统一成
// 连字符，并丢弃不合法字符，保证结果一定能通过 vendorCodePattern。
func vendorCodeFromName(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	var builder strings.Builder
	trailingDash := false
	for _, char := range normalized {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			builder.WriteRune(char)
			trailingDash = false
		case char == '-', char == '_', char == ' ', char == '\t', char == '/', char == '.', char == '·':
			if builder.Len() > 0 && !trailingDash {
				builder.WriteRune('-')
				trailingDash = true
			}
		}
	}
	return strings.Trim(builder.String(), "-")
}

// decodeVendorStringList 读 JSON 数组；坏数据回落到空数组而不是报错：一行历史
// 脏数据不该让整个厂商列表打不开。
func decodeVendorStringList(raw string) []string {
	values := []string{}
	if strings.TrimSpace(raw) == "" {
		return values
	}
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return []string{}
	}
	if values == nil {
		return []string{}
	}
	return values
}

func encodeVendorStringList(values []string) string {
	if values == nil {
		values = []string{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func vendorKeyHint(apiKey string) string {
	trimmed := strings.TrimSpace(apiKey)
	runes := []rune(trimmed)
	if len(runes) == 0 {
		return ""
	}
	if len(runes) <= vendorKeyHintLen {
		return string(runes)
	}
	return string(runes[len(runes)-vendorKeyHintLen:])
}

// vendorAPIFormat 决定凭据行记录的协议格式。
//
// 渠道侧当前固定写 "openai"（系统渠道的协议由所选模型决定），因此优先取运营
// 显式填写值，其次才回落到渠道现值，避免把渠道的缺省值当成运营的选择。
func vendorAPIFormat(inputFormat string, channelFormat string) string {
	if format := strings.TrimSpace(inputFormat); format != "" {
		return truncateRunes(format, 24)
	}
	if format := strings.TrimSpace(channelFormat); format != "" {
		return truncateRunes(format, 24)
	}
	return "openai"
}

func vendorTimeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func vendorTimeStringPtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return vendorTimeString(*value)
}
