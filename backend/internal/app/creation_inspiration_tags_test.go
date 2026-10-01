package app

import (
	"reflect"
	"testing"
)

// 标签是展示字段，容错口径必须写死：重复、空白、超长、超量都只能被丢掉，
// 不能反过来让整条灵感存不进去。
func TestCreationInspirationTagsRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		want  []string
	}{
		{name: "逐项去空白", input: []string{" 科幻 ", "群像"}, want: []string{"科幻", "群像"}},
		{name: "丢掉空项", input: []string{"科幻", "   ", ""}, want: []string{"科幻"}},
		{name: "按原文去重", input: []string{"科幻", "科幻", " 科幻 "}, want: []string{"科幻"}},
		{name: "丢掉超长项", input: []string{"科幻", "这是一个远远超过标签长度的说明性文字"}, want: []string{"科幻"}},
		{name: "截到四个", input: []string{"一", "二", "三", "四", "五"}, want: []string{"一", "二", "三", "四"}},
		{name: "空输入得到空数组", input: nil, want: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			joined := joinCreationInspirationTags(tc.input)
			if got := splitCreationInspirationTags(joined); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("标签往返后 = %v, want %v（中间串 %q）", got, tc.want, joined)
			}
		})
	}
}

// 库里可能留着迁移前或手工写进去的脏串（空项、多余逗号），出口必须一样收干净。
func TestSplitCreationInspirationTagsCleansStoredRaw(t *testing.T) {
	if got := splitCreationInspirationTags("科幻, ,群像,"); !reflect.DeepEqual(got, []string{"科幻", "群像"}) {
		t.Fatalf("清洗后 = %v", got)
	}
	if got := splitCreationInspirationTags("   "); !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("全空白应得到空数组，实际 %v", got)
	}
}
