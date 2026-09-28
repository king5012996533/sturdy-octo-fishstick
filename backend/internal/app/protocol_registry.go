package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/protocol"
)

type PluginProviderCatalogItem struct {
	ID                string                      `json:"id"`
	Version           string                      `json:"version"`
	Name              string                      `json:"name"`
	Vendor            string                      `json:"vendor"`
	Categories        []protocol.Capability       `json:"categories"`
	Scopes            []protocol.Surface          `json:"scopes"`
	Create            string                      `json:"create,omitempty"`
	Poll              string                      `json:"poll,omitempty"`
	ContentType       string                      `json:"contentType,omitempty"`
	BaseURL           string                      `json:"baseUrl,omitempty"`
	Enabled           bool                        `json:"enabled"`
	UnavailableReason string                      `json:"unavailableReason,omitempty"`
	Workflows         []protocol.ManifestWorkflow `json:"workflows,omitempty"`
}

// AdminProtocolCatalog 列出管理员可以为系统渠道模型选择的请求协议。
//
// 数据源必须是通道保存时做校验的那份合并注册表（s.protocolRegistry()），而不是
// 插件包视图：管理端另取一份"官方协议清单"，就会重新出现前端能选中、后端却报
// "请选择有效的模型请求协议"的分裂——上一轮 save 失败正是这么来的。
func (s *Service) AdminProtocolCatalog(actor *model.User, capability string) ([]PluginProviderCatalogItem, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	wantCapability := protocol.Capability(strings.TrimSpace(capability))
	metadataList := s.protocolRegistry().List(protocol.SurfaceAdminSystemChannel, wantCapability, false)
	items := make([]PluginProviderCatalogItem, 0, len(metadataList))
	for _, metadata := range metadataList {
		items = append(items, PluginProviderCatalogItem{
			ID:          metadata.ID,
			Version:     metadata.Version,
			Name:        metadata.Name,
			Vendor:      metadata.Vendor,
			Categories:  metadata.Categories,
			Scopes:      metadata.Scopes,
			Create:      metadata.Create,
			Poll:        metadata.Poll,
			ContentType: metadata.ContentType,
			Enabled:     metadata.Enabled,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

// PluginProviderCatalog projects provider and workflow contributions from the
// unified plugin registry for channel and creation settings.
func (s *Service) PluginProviderCatalog(scope, capability string, includeUnavailable bool) []PluginProviderCatalogItem {
	wantScope := protocol.Surface(strings.TrimSpace(scope))
	wantCapability := protocol.Capability(strings.TrimSpace(capability))
	items := make([]PluginProviderCatalogItem, 0)
	for _, plugin := range s.Plugins() {
		for _, provider := range plugin.Manifest.Contributes.Providers {
			if !containsPluginSurface(provider.Scopes, wantScope) || (wantCapability != "" && !containsPluginCapability(provider.Capabilities, wantCapability)) {
				continue
			}
			item := PluginProviderCatalogItem{ID: provider.ID, Version: plugin.Manifest.Version, Name: provider.Label, Vendor: plugin.Manifest.Author, Categories: provider.Capabilities, Scopes: provider.Scopes, BaseURL: provider.BaseURL, Enabled: plugin.Status == "enabled", UnavailableReason: plugin.Error, Workflows: workflowsForProvider(plugin.Manifest.Contributes.Workflows, provider.ID)}
			item.Create, item.Poll, item.ContentType = operationSummary(provider.Create), operationSummaryPtr(provider.Poll), provider.Create.ContentType
			// The registry metadata is the canonical provider projection. This keeps
			// host-backed dispatch paths out of every user-facing catalog consumer.
			if adapter, ok := canonicalProviderAdapter(s.protocolRegistry(), provider.ID); ok {
				metadata := adapter.Metadata()
				item.Create, item.Poll, item.ContentType = metadata.Create, metadata.Poll, metadata.ContentType
			}
			if includeUnavailable || item.Enabled {
				items = append(items, item)
			}
		}
	}
	return items
}

func canonicalProviderAdapter(registry *protocol.Registry, id string) (protocol.Adapter, bool) {
	return registry.Resolve(id)
}

func containsPluginSurface(items []protocol.Surface, want protocol.Surface) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
func containsPluginCapability(items []protocol.Capability, want protocol.Capability) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
func workflowsForProvider(items []protocol.ManifestWorkflow, providerID string) []protocol.ManifestWorkflow {
	result := make([]protocol.ManifestWorkflow, 0)
	for _, item := range items {
		if item.ProviderID == providerID {
			result = append(result, item)
		}
	}
	return result
}
func operationSummary(operation protocol.ManifestOperation) string {
	path := strings.ReplaceAll(operation.Path, "{{model}}", "{model}")
	path = strings.ReplaceAll(path, "{{taskId}}", "{task_id}")
	return strings.ToUpper(operation.Method) + " " + path
}

func operationSummaryPtr(operation *protocol.ManifestOperation) string {
	if operation == nil {
		return ""
	}
	return operationSummary(*operation)
}

// protocolRegistry 返回当前可用的协议注册表：运行时插件优先，其次官方插件包，最后内置协议。
//
// 三层合并而不是单取运行时快照：执行路径本来就会在 protocol.Builtins() 里解析协议
// （provider_protocol.go），管理端若只认插件注册表，未安装插件包的部署就会出现
// 「模型跑得起来，却保存不了」的死角。同名协议按上面的优先级取第一个（插件可覆盖内置）。
func (s *Service) protocolRegistry() *protocol.Registry {
	var snapshot *protocol.Registry
	if s.pluginRuntime != nil {
		snapshot = s.pluginRuntime.registrySnapshot()
	}
	return mergedProtocolRegistry(snapshot, loadOfficialFallbackRegistry())
}

type protocolRegistryCacheEntry struct {
	snapshot *protocol.Registry
	official *protocol.Registry
	merged   *protocol.Registry
}

var (
	protocolRegistryCacheMu sync.Mutex
	protocolRegistryCache   protocolRegistryCacheEntry
)

// mergedProtocolRegistry 按来源优先级合并注册表并缓存结果。
// 插件快照指针变化（安装/卸载/重载）时自动重建，避免每次解析都重新拼装。
func mergedProtocolRegistry(snapshot *protocol.Registry, official *protocol.Registry) *protocol.Registry {
	protocolRegistryCacheMu.Lock()
	defer protocolRegistryCacheMu.Unlock()
	if protocolRegistryCache.merged != nil && protocolRegistryCache.snapshot == snapshot && protocolRegistryCache.official == official {
		return protocolRegistryCache.merged
	}
	adapters := make([]protocol.Adapter, 0, 64)
	seen := make(map[string]bool, 64)
	for _, source := range []*protocol.Registry{snapshot, official, protocol.Builtins()} {
		if source == nil {
			continue
		}
		for _, metadata := range source.List("", "", true) {
			id := strings.TrimSpace(metadata.ID)
			if id == "" || seen[id] {
				continue
			}
			adapter, ok := source.Get(id)
			if !ok {
				continue
			}
			seen[id] = true
			adapters = append(adapters, adapter)
		}
	}
	merged, err := protocol.NewRegistry(adapters...)
	if err != nil {
		merged = protocol.Builtins()
	}
	protocolRegistryCache = protocolRegistryCacheEntry{snapshot: snapshot, official: official, merged: merged}
	return merged
}

func (s *Service) protocolMetadata(id string) (protocol.Metadata, bool) {
	adapter, ok := s.protocolRegistry().Resolve(strings.TrimSpace(id))
	if !ok {
		return protocol.Metadata{}, false
	}
	return adapter.Metadata(), true
}

func (s *Service) channelProtocolMetadata(id string) (protocol.Metadata, bool) {
	return s.protocolMetadata(id)
}

func (s *Service) canonicalProtocolID(id string) (string, bool) {
	adapter, ok := s.protocolRegistry().Resolve(strings.TrimSpace(id))
	if !ok {
		return "", false
	}
	return adapter.Metadata().ID, true
}

func (s *Service) protocolIsSelectable(id string) bool {
	metadata, ok := s.channelProtocolMetadata(id)
	return ok && metadata.Enabled && metadata.UnavailableReason == ""
}

func (s *Service) Plugins() []PluginView {
	if s.pluginRuntime == nil {
		return []PluginView{}
	}
	items := s.pluginRuntime.list()
	for index := range items {
		items[index].Management = pluginManagementFromView(items[index])
	}
	return items
}

// PluginsForUser keeps the plugin center response aligned with the public
// feature switch. Administrators must still be able to inspect and recover
// bundled plugins even when ordinary users cannot see them.
func (s *Service) PluginsForUser(actor *model.User) ([]PluginView, error) {
	items := s.Plugins()
	if actor != nil && actor.Role == model.UserRoleAdmin {
		return items, nil
	}
	visible, err := s.FeatureEnabled(FeatureSystemPlugins)
	if err != nil {
		return nil, err
	}
	if visible {
		return items, nil
	}
	filtered := make([]PluginView, 0, len(items))
	for _, item := range items {
		if item.Management.ActivationScope == PluginScopeUser {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *Service) InstallPlugin(data []byte, fileName string) (PluginView, error) {
	if s.pluginRuntime == nil {
		return PluginView{}, fmt.Errorf("插件运行时未初始化")
	}
	plugin, err := s.pluginRuntime.install(data, fileName)
	if err == nil {
	}
	return plugin, err
}

func (s *Service) InstallPluginForAdmin(actor *model.User, data []byte, fileName string) (PluginView, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return PluginView{}, err
	}
	parsed, err := protocol.ParsePluginPackage(data)
	if err != nil {
		return PluginView{}, err
	}
	if _, reserved := officialApplicationPolicies[parsed.Manifest.Metadata.ID]; reserved {
		return PluginView{}, fmt.Errorf("插件 ID %q 由官方应用保留", parsed.Manifest.Metadata.ID)
	}
	plugin, err := s.InstallPlugin(data, fileName)
	if err != nil {
		return PluginView{}, err
	}
	plugin.Management = pluginManagementFromView(plugin)
	now := time.Now()
	state := &model.PluginPlatformState{PluginID: plugin.Manifest.ID, Available: plugin.Status == "enabled", UpdatedBy: actor.ID, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.SavePluginPlatformState(state); err != nil {
		return PluginView{}, fmt.Errorf("保存插件平台状态：%w", err)
	}
	if err := s.appendAdminAudit(actor, "plugin.install", "plugin", plugin.Manifest.ID, "安装自定义插件", map[string]any{"fileName": fileName, "sha256": plugin.SHA256}); err != nil {
		return PluginView{}, err
	}
	return plugin, nil
}

func (s *Service) PluginPackage(id string) ([]byte, string, error) {
	if s.pluginRuntime == nil {
		return nil, "", fmt.Errorf("插件运行时未初始化")
	}
	s.pluginRuntime.mu.RLock()
	record, ok := s.pluginRuntime.plugins[strings.TrimSpace(id)]
	s.pluginRuntime.mu.RUnlock()
	if !ok {
		return nil, "", fmt.Errorf("插件 %q 不存在", id)
	}
	if record.PackagePath == "" {
		return nil, "", fmt.Errorf("插件 %q 没有可下载的包文件", id)
	}
	data, err := os.ReadFile(filepath.Join(s.pluginRuntime.packageDir, filepath.Base(record.PackagePath)))
	if err != nil {
		return nil, "", fmt.Errorf("读取插件包失败：%w", err)
	}
	return data, record.FileName, nil
}

func (s *Service) SetPluginEnabled(id string, enabled bool) (PluginView, error) {
	if s.pluginRuntime == nil {
		return PluginView{}, fmt.Errorf("插件运行时未初始化")
	}
	plugin, err := s.pluginRuntime.setEnabled(id, enabled)
	if err == nil {
	}
	return plugin, err
}

func (s *Service) UninstallPlugin(id string) error {
	if s.pluginRuntime == nil {
		return fmt.Errorf("插件运行时未初始化")
	}
	err := s.pluginRuntime.uninstall(id)
	if err == nil {
	}
	return err
}

func (s *Service) UninstallPluginForAdmin(actor *model.User, id string) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	if err := s.UninstallPlugin(id); err != nil {
		return err
	}
	if err := s.repo.DeleteUserPluginStates(id); err != nil {
		return fmt.Errorf("清理用户插件状态：%w", err)
	}
	if err := s.repo.DeletePluginPlatformState(id); err != nil {
		return fmt.Errorf("清理插件平台状态：%w", err)
	}
	return s.appendAdminAudit(actor, "plugin.uninstall", "plugin", id, "卸载自定义插件", nil)
}
