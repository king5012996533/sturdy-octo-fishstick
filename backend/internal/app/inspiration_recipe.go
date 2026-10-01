package app

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// inspirationRecipeMaxImages 是一条作品最多带走的参考图数量。
//
// 上游一条作品里参考图可以很多（角色三视图、服装、场景各一张），全带走会让"使用这个
// 创意"变成一次几十张图的上传；而真正决定画面的是排在最前面的那几张主体参考。
const inspirationRecipeMaxImages = 4

// inspirationRecipe 是一条作品的复刻配方：把"照着做"需要、但提示词本身不包含的东西
// 从上游画布快照里摘出来。
//
// 为什么会需要它：上游的模板是整张画布（nodes + edges），提示词只是其中一个文本节点。
// 同一条提示词配上不同的参考图与模型，出来的是两个作品；只搬提示词，用户拿到的是一个
// 看起来一样、实际生成不出同样东西的输入框。
type inspirationRecipe struct {
	VideoModel      string
	VideoMode       string
	Ratio           string
	Resolution      string
	DurationSeconds int
	ImageURLs       []string
}

// liblibTemplateDetailResponse 是上游模板详情接口的信封。
//
// 走这个接口而不是抓作品页 HTML：一次请求同时拿到成片地址与整张画布快照，体积减半，
// 也不必再对转义过的页面载荷做正则。
type liblibTemplateDetailResponse struct {
	Code int `json:"code"`
	Data struct {
		Detail struct {
			FinalOutput  string `json:"finalOutput"`
			SnapshotData string `json:"snapshotData"`
		} `json:"detail"`
	} `json:"data"`
}

// 下面这几个结构只声明我们真正要读的字段：上游快照里还有坐标、尺寸、样式、连线等一大
// 堆东西，它们对"复刻配方"没有意义。只解需要的部分，上游加字段不会影响我们。
type liblibSnapshot struct {
	// Nodes 收成 RawMessage：快照是用户画布的自由结构，一条 129 个节点的作品里
	// 什么形状都可能有。整份文档一次性解码的话，任何一个节点的一个字段换了形态，
	// 赔上的是整条作品的配方——实测就栽在这里。逐个节点解码，坏的跳过，好的留下。
	Nodes []json.RawMessage `json:"nodes"`
}

type liblibSnapshotNode struct {
	Type string                   `json:"type"`
	Data liblibSnapshotNodeRecord `json:"data"`
}

type liblibSnapshotNodeRecord struct {
	Action string                   `json:"action"`
	URL    liblibSnapshotMediaURL   `json:"url"`
	Params liblibSnapshotNodeParams `json:"params"`
}

type liblibSnapshotNodeParams struct {
	Model     string                   `json:"model"`
	ModeType  string                   `json:"modeType"`
	ImageList []liblibSnapshotMediaRef `json:"imageList"`
	Settings  liblibSnapshotSettings   `json:"settings"`
}

type liblibSnapshotMediaRef struct {
	URL liblibSnapshotMediaURL `json:"url"`
}

// liblibSnapshotMediaURL 是上游快照里所有"资源地址"字段的形态：有时是一个字符串，
// 有时是字符串数组。同一条作品里两种混着出现——实测 129 节点的画布上，视频节点的
// url 全是数组，参考图的 url 也是数组，而另外一些节点给的是字符串。声明成 string
// 会让整份快照反序列化失败，所以两种都收下，取值时按顺序看待。
type liblibSnapshotMediaURL []string

func (u *liblibSnapshotMediaURL) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] == '[' {
		var list []string
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return err
		}
		*u = list
		return nil
	}
	var single string
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return err
	}
	*u = []string{single}
	return nil
}

// first 取第一个地址。数组形态在上游是同一份产物的多个镜像/多档清晰度，
// 复刻用哪一个都行，取头一个最稳定。
func (u liblibSnapshotMediaURL) first() string {
	for _, raw := range u {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// liblibSnapshotSettings 里 duration 既出现过数字也出现过字符串，还可能是 null，
// 因此先按原始 JSON 收下再单独解析。
type liblibSnapshotSettings struct {
	Ratio      string          `json:"ratio"`
	Resolution string          `json:"resolution"`
	Duration   json.RawMessage `json:"duration"`
}

// inspirationRecipeFromSnapshot 从画布快照里摘出复刻配方。
//
// 快照里一条作品会有很多视频节点：作者反复试的每一稿都留在画布上。先用成片地址
// （finalOutput）去锚"这一条作品的成片"，锚不到再按 pickLibraryRecipeNode 的优先级
// 挑一个最像成片输入的节点。
//
// 为什么锚点只是兜底：finalOutput 是导出产物，它的地址不出现在任何节点上——
// 池子里 58 条实测只有 1 条能对上。所以真正的选择逻辑在那个优先级函数里，
// 锚点留着是因为它对"单节点工作流"依然精确，而优先级排序对多镜头作品只是近似。
func inspirationRecipeFromSnapshot(finalOutput string, snapshotData string) inspirationRecipe {
	nodes := decodeLibrarySnapshotNodes(snapshotData)
	if len(nodes) == 0 {
		return inspirationRecipe{}
	}
	if target := strings.TrimSuffix(strings.TrimSpace(finalOutput), ".mp4"); target != "" {
		for index := range nodes {
			node := &nodes[index]
			if !isGenerativeVideoNode(node) {
				continue
			}
			for _, raw := range node.Data.URL {
				if strings.HasPrefix(strings.TrimSpace(raw), target) {
					return recipeFromSnapshotNode(node)
				}
			}
		}
	}
	if node := pickLibraryRecipeNode(nodes); node != nil {
		return recipeFromSnapshotNode(node)
	}
	return inspirationRecipe{}
}

// decodeLibrarySnapshotNodes 把快照解成节点列表，解不出来的单个节点直接跳过。
//
// 上游是用户自由编织的画布，节点形状千奇百怪。一个节点解码失败不该让整条作品的
// 配方归零：跳过它，其余节点仍然能被选中。
func decodeLibrarySnapshotNodes(snapshotData string) []liblibSnapshotNode {
	trimmed := strings.TrimSpace(snapshotData)
	if trimmed == "" {
		return nil
	}
	var snapshot liblibSnapshot
	if err := json.Unmarshal([]byte(trimmed), &snapshot); err != nil {
		return nil
	}
	nodes := make([]liblibSnapshotNode, 0, len(snapshot.Nodes))
	for _, raw := range snapshot.Nodes {
		var node liblibSnapshotNode
		if err := json.Unmarshal(raw, &node); err != nil {
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// isGenerativeVideoNode 判断一个节点是不是"能生成视频的那个"。
//
// 视频类节点里混着导出节点（video_resource，没有参数）和后处理节点（放大、补帧，
// 有 model 但没有 modeType）。它们都是节点，但把它们的参数当成复刻配方，用户拿到的
// 就是一个"用 topaz 放大"的配方——提示词、参考图、时长全都不在里面。
func isGenerativeVideoNode(node *liblibSnapshotNode) bool {
	if node.Type != "video" || node.Data.Action != "video_generate" {
		return false
	}
	return strings.TrimSpace(node.Data.Params.Model) != ""
}

// pickLibraryRecipeNode 在没有成片锚点时挑一个最像成片输入的节点。
//
// 优先级是"有参考图的生成节点 > 有参考图的任意节点 > 任意生成节点"，同一档取最后一个：
// 作者迭代时旧稿留在前面，末位是最接近成片的那一稿。把"有参考图"排在前面，是因为
// 参考图正是提示词补不回来的那一半——实测 58 条里 6 条的最后一个是纯文生视频节点，
// 按"取最后一个"会给出一个 0 张参考图的配方。
func pickLibraryRecipeNode(nodes []liblibSnapshotNode) *liblibSnapshotNode {
	var withImagesGenerative, withImages, anyGenerative *liblibSnapshotNode
	for index := range nodes {
		node := &nodes[index]
		if !isGenerativeVideoNode(node) {
			continue
		}
		anyGenerative = node
		if len(recipeImageURLs(node.Data.Params)) == 0 {
			continue
		}
		withImages = node
		if strings.TrimSpace(node.Data.Params.ModeType) != "" {
			withImagesGenerative = node
		}
	}
	switch {
	case withImagesGenerative != nil:
		return withImagesGenerative
	case withImages != nil:
		return withImages
	default:
		return anyGenerative
	}
}

// recipeImageURLs 取出可用的参考图地址：只认 http(s)，本地路径和空地址交给前端
// 就是一个必然裂的图，不如不带。
func recipeImageURLs(params liblibSnapshotNodeParams) []string {
	urls := make([]string, 0, len(params.ImageList))
	for _, image := range params.ImageList {
		url := image.URL.first()
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			continue
		}
		urls = append(urls, url)
		if len(urls) >= inspirationRecipeMaxImages {
			break
		}
	}
	return urls
}

// recipeFromSnapshotNode 把一个视频节点的参数读成配方。
func recipeFromSnapshotNode(node *liblibSnapshotNode) inspirationRecipe {
	params := node.Data.Params
	recipe := inspirationRecipe{
		VideoModel:      truncateRunes(strings.TrimSpace(params.Model), 80),
		VideoMode:       truncateRunes(strings.TrimSpace(params.ModeType), 32),
		Ratio:           truncateRunes(strings.TrimSpace(params.Settings.Ratio), 16),
		Resolution:      truncateRunes(strings.TrimSpace(params.Settings.Resolution), 16),
		DurationSeconds: snapshotDurationSeconds(params.Settings.Duration),
	}
	recipe.ImageURLs = recipeImageURLs(params)
	return recipe
}

// snapshotDurationSeconds 读时长：解不出来时返回 0，在条目上就是"没记录"，
// 比因为一个字段换了类型就丢掉整份配方好。
func snapshotDurationSeconds(raw json.RawMessage) int {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if text == "" || text == "null" {
		return 0
	}
	seconds, err := strconv.Atoi(text)
	if err != nil || seconds < 0 {
		return 0
	}
	return seconds
}

// joinInspirationRecipeImageIDs / splitInspirationRecipeImageIDs 负责配方图 ID 在列上
// 的存取（逗号分隔），与标签那一对同构：存储格式只在这一层出现，视图拿到的是数组。
func joinInspirationRecipeImageIDs(ids []string) string {
	cleaned := make([]string, 0, len(ids))
	for _, id := range ids {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.Join(cleaned, ",")
}

func splitInspirationRecipeImageIDs(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, ",")
	ids := make([]string, 0, len(parts))
	for _, part := range parts {
		if id := strings.TrimSpace(part); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
