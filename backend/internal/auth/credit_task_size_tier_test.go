package auth

import (
	"strings"
	"testing"
)

// TestChargeTaskFallsBackFromSizeTierToUntieredRow 覆盖"尺寸档没配价就按不区分档位结算"。
//
// 尺寸档是本平台自己加的轴，模型没配就是没有这条轴。少了这条回落，任何一个模型只要被
// 请求带上尺寸又会走尺寸档，就会从"按不区分档位结算"变成"未定价"——一次全量报价失败。
func TestChargeTaskFallsBackFromSizeTierToUntieredRow(t *testing.T) {
	env := newCreditTaskEnv(t)
	upstream := int64(45)
	if err := env.store.SaveModelPrice(&ModelPrice{
		ModelKey:          "mm::hy-image",
		Capability:        string(CapabilityImage),
		Unit:              string(UnitPerImage),
		UpstreamUnitPrice: &upstream,
		Enabled:           true,
	}); err != nil {
		t.Fatalf("写入空档价目失败: %v", err)
	}
	if err := env.store.SaveMarkupRule(&MarkupRule{ID: "rule-1", Scope: MarkupScopeGlobal, MultiplierBp: 20000}); err != nil {
		t.Fatalf("写入倍率失败: %v", err)
	}

	quote, err := env.service.QuoteTaskCharge(TaskChargeInput{
		ModelKey:   "mm::hy-image",
		Capability: "IMAGE",
		Tier:       string(PriceTierSize2K),
		Quantity:   1,
	})
	if err != nil {
		t.Fatalf("尺寸档缺行时应回落到空档，实际报错: %v", err)
	}
	if !quote.Priced || quote.SellUnitPrice == nil || *quote.SellUnitPrice != 90 {
		t.Fatalf("应回落到空档价 90，实际 %#v", quote)
	}
}

// TestChargeTaskDoesNotFallBackFromQualityTier 覆盖质量档缺行仍然报错。
//
// 与尺寸档相反：质量档是上游真实存在的维度，缺一行说明配置漏了。回落到别的行等于用一个
// 自己没验过的成本出货，正是"按最低价卖最高档"这类资损的来源。
func TestChargeTaskDoesNotFallBackFromQualityTier(t *testing.T) {
	env := newCreditTaskEnv(t)
	upstream := int64(45)
	if err := env.store.SaveModelPrice(&ModelPrice{
		ModelKey:          "mm::quality-image",
		Capability:        string(CapabilityImage),
		Unit:              string(UnitPerImage),
		UpstreamUnitPrice: &upstream,
		Enabled:           true,
	}); err != nil {
		t.Fatalf("写入空档价目失败: %v", err)
	}

	input := TaskChargeInput{ModelKey: "mm::quality-image", Capability: "IMAGE", Tier: string(PriceTierMax), Quantity: 1}
	quote, err := env.service.QuoteTaskCharge(input)
	if err != nil {
		t.Fatalf("试算不应因缺行报错，实际 %v", err)
	}
	if quote.Priced {
		t.Fatalf("质量档缺行不应给出售价，实际 %#v", quote)
	}
	input.UserID, input.TaskID = "user-1", "task-1"
	_, _, _, chargeErr := env.service.ChargeTask(input)
	if chargeErr == nil || !strings.Contains(chargeErr.Error(), string(PriceTierMax)) {
		t.Fatalf("提交时应报出缺哪一档，实际 %v", chargeErr)
	}
}
