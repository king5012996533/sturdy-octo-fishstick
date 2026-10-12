package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// providerConfigScalarKeys 是"调用方直觉上会写 JSON 数字或布尔，但内部统一用字符串传递"的配置项。
//
// 这份名单只放标量语义的字段：数量、时长、档位名、开关。像 baseUrl / apiKey 这种就是文本，
// 收下数字只会掩盖错误，不在名单里。
var providerConfigScalarKeys = []string{
	"count",
	"videoSeconds",
	"vquality",
	"size",
	"quality",
	"transparentBackground",
	"videoGenerateAudio",
	"videoWatermark",
	"audioSpeed",
	"audioDuration",
}

// UnmarshalJSON 把标量配置项归一化成字符串再解析。
//
// 这些字段内部统一用字符串（沿用既有协议），但外部调用方按语义写 "videoSeconds": 15 或
// "videoGenerateAudio": true 很常见。直接解到 string 会以 Go 内部错误原文失败并暴露给用户：
//
//	json: cannot unmarshal number into Go struct field canvasGenerationInput.config.videoSeconds of type string
//
// （线上真实反馈）。归一化之后，取值合不合法仍由后面的能力校验说话，报的是人能看懂的话。
func (c *providerConfig) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, key := range providerConfigScalarKeys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		normalized, changed := normalizeJSONScalarString(value)
		if changed {
			raw[key] = normalized
		}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	// plain 没有 UnmarshalJSON，避免递归回自己。
	type plain providerConfig
	if err := json.Unmarshal(encoded, (*plain)(c)); err != nil {
		// 这一层是内层解析，报出来的字段名缺了 config 前缀（调用方看到的是裸 "videoSeconds"）。
		// 配置块在输入里始终挂在 config 下，补回去，提示才能直接对上参数路径。
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) && typeErr.Field != "" && !strings.Contains(typeErr.Field, ".") {
			typeErr.Field = "config." + typeErr.Field
		}
		return err
	}
	return nil
}

// normalizeJSONScalarString 把 JSON 数字/布尔字面量改写成等值字符串字面量。
//
// 字符串、null、对象、数组原样返回：前两者本来就该按原样解析，后两者不是标量，
// 让接下来的解析如实报类型错误，比在这里悄悄改成 "map[...]" 好。
func normalizeJSONScalarString(value json.RawMessage) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" || trimmed == "null" || trimmed[0] == '"' {
		return value, false
	}
	if !json.Valid(value) {
		return value, false
	}
	// 只看首字符就能认出字面量类别：数字（含正负号、小数点、科学计数法）与 true/false。
	// 字符串、null、对象、数组在上面已经提前返回。
	if !strings.ContainsRune("0123456789+-.eEtf", rune(trimmed[0])) {
		return value, false
	}
	quoted, err := json.Marshal(trimmed)
	if err != nil {
		return value, false
	}
	return quoted, true
}

// taskInputDecodeError 把任务输入解码失败翻成用户能照着改的说法。
//
// 直接回 %w 会把 Go 类型名和结构体细节丢给用户（"cannot unmarshal number into Go struct
// field ... of type string"，线上真实反馈）。参数名要留下——它是唯一能指路的信息，
// Go 类型名和结构体路径要去掉。
func taskInputDecodeError(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := strings.TrimSpace(typeErr.Field)
		if field != "" {
			return fmt.Errorf("任务参数 %s 格式不正确（收到 %s），请检查该参数后重新提交", field, typeErr.Value)
		}
	}
	return errors.New("任务参数格式不正确，请检查参数后重新提交")
}
