package app

// 画布 Agent 的视觉能力。
//
// 图片内容不进读取工具的结果：工具结果只能是文本，而画布读取按设计只暴露媒体特征
// （mimeType/尺寸），不转发像素。所以要"看图"必须把图片作为消息内容送进模型上下文。
// 这里复用平台已有的资源占位机制（canonical 消息里的 image_url 用 resource: 占位，
// 由 resolveAgentResourcePlaceholders 在真正发请求时水合成数据或公网地址），
// 并按当前模型声明的图片理解能力收口，避免向纯文本模型发送图片。

import (
	"fmt"
	"strings"

	"infinite-canvas/backend/internal/repository"
)

// cloudAgentVisionMaxImages 是单次看图的上限：图片会按步计入上下文，多张会显著抬高成本。
const cloudAgentVisionMaxImages = 4

type cloudAgentVisionImage struct {
	NodeID     string `json:"nodeId"`
	StorageKey string `json:"storageKey"`
	MIMEType   string `json:"mimeType"`
	Bytes      int64  `json:"bytes"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

// cloudAgentInspectImages 只登记待看图节点，不读取像素；图片在下一次模型调用时随消息附上。
func cloudAgentInspectImages(repo *repository.Repository, userID string, state *cloudAgentRuntime, call cloudAgentCall) (any, error) {
	var args struct {
		NodeIDs []string `json:"nodeIds"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if len(args.NodeIDs) == 0 || len(args.NodeIDs) > cloudAgentVisionMaxImages {
		return nil, &cloudAgentArgumentError{BadAuthRequest(fmt.Sprintf("看图参数无效：nodeIds 需要 1 到 %d 个图片节点ID，先用 canvas_get_state 取真实节点ID", cloudAgentVisionMaxImages))}
	}
	if err := cloudAgentVisionAllowance(repo, state, len(args.NodeIDs)); err != nil {
		return nil, err
	}
	canvas, err := repo.CanvasProjectForUser(userID, state.Request.CanvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, BadAuthRequest("服务端画布内容无法解析，请先重新同步")
	}
	nodes, err := creationObjects(doc["nodes"])
	if err != nil {
		return nil, err
	}
	images := make([]cloudAgentVisionImage, 0, len(args.NodeIDs))
	seen := map[string]bool{}
	for _, nodeID := range args.NodeIDs {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" || seen[nodeID] {
			continue
		}
		seen[nodeID] = true
		node := nodes[nodeID]
		if node == nil {
			return nil, &cloudAgentArgumentError{BadAuthRequest(fmt.Sprintf("节点 %s 不在当前画布，请重新读取画布获取真实节点ID", nodeID))}
		}
		reference, _, err := cloudAgentReference(repo, userID, node)
		if err != nil {
			return nil, BadAuthRequest(fmt.Sprintf("节点 %s 不能作为图片查看：%v", nodeID, err))
		}
		mimeType := strings.ToLower(stringValue(reference["mimeType"]))
		if !strings.HasPrefix(mimeType, "image/") {
			return nil, BadAuthRequest(fmt.Sprintf("节点 %s 不是图片，当前只能查看图片节点", nodeID))
		}
		image := cloudAgentVisionImage{
			NodeID: nodeID, StorageKey: stringValue(reference["storageKey"]), MIMEType: stringValue(reference["mimeType"]),
			Bytes: int64(intValue(reference["bytes"])), Width: intValue(reference["width"]), Height: intValue(reference["height"]),
		}
		images = append(images, image)
	}
	if len(images) == 0 {
		return nil, &cloudAgentArgumentError{BadAuthRequest("看图参数无效：没有可用的图片节点ID，先用 canvas_get_state 取真实节点ID")}
	}
	state.InspectImages = images
	payload := make([]any, 0, len(images))
	for _, image := range images {
		payload = append(payload, map[string]any{"nodeId": image.NodeID, "mimeType": image.MIMEType, "width": image.Width, "height": image.Height})
	}
	return map[string]any{"images": payload, "attached": len(images), "nextStep": "图片已排入你下一次模型调用，请直接描述实际看到的内容，并把它写进回复或计划；同一节点不要反复查看"}, nil
}

// cloudAgentVisionAllowance 确认当前 Agent 模型能接收 want 张图片。
// 能力的权威来源是后台为这个渠道模型声明的文本参考配置，不是模型名字或上游默认值。
func cloudAgentVisionAllowance(repo *repository.Repository, state *cloudAgentRuntime, want int) error {
	if state == nil {
		return BadAuthRequest("Agent 状态无效")
	}
	if strings.TrimSpace(state.Request.CanvasID) == "" {
		return BadAuthRequest("看图需要当前画布上下文，请先打开画布再对话")
	}
	channelID := strings.TrimSpace(state.Request.ChannelID)
	modelKey := firstNonEmpty(state.Request.ChannelModelKey, state.Request.Model)
	if channelID == "" || modelKey == "" {
		return BadAuthRequest("当前 Agent 未绑定具体渠道模型，无法确认图片理解能力")
	}
	channelModel, err := repo.ChannelModelByKey(channelID, modelKey)
	if err != nil {
		return BadAuthRequest("读取 Agent 模型能力失败，请确认模型仍可用")
	}
	profile, err := DecodeModelCapabilityConfig(channelModel.CapabilityConfigJSON)
	if err != nil || profile == nil || profile.Text == nil {
		return BadAuthRequest("当前 Agent 模型未声明文本能力配置，无法看图")
	}
	maxImages := profile.Text.References.MaxImages
	if maxImages <= 0 {
		// 纯文本模型收到图片会被上游拒绝，必须在这里拦住并给出可执行的下一步。
		return BadAuthRequest("当前 Agent 模型未开启图片理解：请在后台为该模型开启（需上游本身支持视觉），或把本轮 Agent 切换到支持视觉的模型")
	}
	if want > maxImages {
		return &cloudAgentArgumentError{BadAuthRequest(fmt.Sprintf("当前 Agent 模型一次最多查看 %d 张图片，请减少 nodeIds 后重试", maxImages))}
	}
	return nil
}

// cloudAgentAttachInspectionImages 把待看图片接到这一步请求上；返回需要水合的参考素材。
// 图片只随本次请求发出，不写回持久化会话，避免每一步都重复计费。
func cloudAgentAttachInspectionImages(state *cloudAgentRuntime, canonical *canonicalAgentRequest) []providerMedia {
	if state == nil || canonical == nil || len(state.InspectImages) == 0 {
		return nil
	}
	images := state.InspectImages
	state.InspectImages = nil
	parts := make([]any, 0, len(images)+1)
	parts = append(parts, map[string]any{"type": "text", "text": "以下是你上一步请求查看的画布图片，请直接根据画面内容回答或继续操作："})
	references := make([]providerMedia, 0, len(images))
	for _, image := range images {
		if !strings.HasPrefix(image.StorageKey, "resource:") {
			continue
		}
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": image.StorageKey}})
		references = append(references, providerMedia{
			ID: image.NodeID, Type: "image", MimeType: image.MIMEType, Bytes: image.Bytes,
			Width: image.Width, Height: image.Height, StorageKey: image.StorageKey,
		})
	}
	if len(references) == 0 {
		return nil
	}
	// 显式复制消息切片：这一步的图片不能因 append 别名而写回持久化会话。
	messages := make([]map[string]any, 0, len(canonical.Messages)+1)
	messages = append(messages, canonical.Messages...)
	canonical.Messages = append(messages, map[string]any{"role": "user", "content": parts})
	return references
}
