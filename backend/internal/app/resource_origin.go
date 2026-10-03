package app

// 产物来路。
//
// 写成常量而不是散落的字符串：后台"没关联任务"的分诊按这几档走，拼错一个只会在页面上
// 多出一个看不懂的英文词，不会报错，等到有人对着后台发呆时才发现。
const (
	resourceSourceGeneration = "generation"
	resourceSourceUpload     = "upload"
	resourceSourceImport     = "import"
	resourceSourceRender     = "render"
	resourceSourceLegacy     = "legacy"
)

// resourceOrigin 说明一条产物是怎么来的：由哪次任务产出、走的是哪条链路。
//
// 上传没有任务，TaskID 为空是正确的取值，不是缺失——它正好说明这条产物不该出现在
// "上游有结果但没扣费"的对账里。
type resourceOrigin struct {
	Source string
	TaskID string
}
