package auth

import (
	"strings"
	"testing"
)

// 附加费要真的进总额、进流水备注，并且与"单价 × 用量"分开可查。
//
// H3 这类按秒计价的模型上，参考图超量是一笔与秒数无关的钱；把它折成秒会在不同档位上
// 算出不同的张单价，所以它必须是一条显式的附加费。
func TestChargeTaskAddsSurchargeBeyondUnitPrice(t *testing.T) {
	env := newCreditTaskEnv(t)
	sell := int64(15)
	if err := env.store.SaveModelPrice(&ModelPrice{
		ModelKey:      "CHANNEL_000008::MiniMax-H3",
		Capability:    string(CapabilityVideo),
		Unit:          string(UnitPerSecond),
		SellUnitPrice: &sell,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("写入单价失败: %v", err)
	}
	if _, _, err := env.service.GrantTopUpCredits("user-1", "order-1", 5000, 0, "测试充值"); err != nil {
		t.Fatalf("充值失败: %v", err)
	}

	input := TaskChargeInput{
		UserID:           "user-1",
		TaskID:           "task-1",
		ModelKey:         "CHANNEL_000008::MiniMax-H3",
		Capability:       "VIDEO",
		Quantity:         21,
		SurchargeCredits: 60,
		SurchargeNote:    "参考图 9 张，超出免费的 4 张按 15 分/张加收",
	}
	quote, err := env.service.QuoteTask(input)
	if err != nil {
		t.Fatalf("试算失败: %v", err)
	}
	if quote.Credits != 21*15+60 {
		t.Fatalf("总额应含附加费 375，实际 %d", quote.Credits)
	}
	if quote.SurchargeCredits != 60 {
		t.Fatalf("附加费应原样回传，实际 %d", quote.SurchargeCredits)
	}

	charged, entry, created, err := env.service.ChargeTask(input)
	if err != nil || !created || entry == nil {
		t.Fatalf("扣费失败: %v created=%v", err, created)
	}
	if charged.Credits != quote.Credits || -entry.Amount != quote.Credits {
		t.Fatalf("实扣应与试算一致：%d / %d / %d", charged.Credits, entry.Amount, quote.Credits)
	}
	if !strings.Contains(entry.Note, "参考图 9 张") {
		t.Fatalf("流水备注要写清附加费来源，实际 %q", entry.Note)
	}

	// 没有附加费时备注与金额都不该多出一个 0。
	plain := input
	plain.TaskID = "task-2"
	plain.SurchargeCredits = 0
	plain.SurchargeNote = ""
	plainQuote, err := env.service.QuoteTask(plain)
	if err != nil {
		t.Fatalf("无附加费试算失败: %v", err)
	}
	if plainQuote.Credits != 21*15 || plainQuote.SurchargeCredits != 0 {
		t.Fatalf("无附加费不该改变总额：%#v", plainQuote)
	}
}
