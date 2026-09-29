import { listCreationInspirations } from "@/services/api/creation-inspirations";

import { inspirationFromRecord, type CreationInspiration } from "./creation-inspirations";

/**
 * 精选灵感广场的数据来源（平台后台目录）。
 *
 * 单独成一个文件，是为了让 creation-inspirations.ts 保持纯数据：那份列表被测试与
 * 兜底逻辑共用，把网络层引进去会让一份常量文件在离线环境下也要求 axios 就绪。
 *
 * 取不到（桌面端、离线预览、接口尚未部署）时返回空数组而不是抛出：广场是首页的
 * 主要内容，调用方需要的是一个"能不能用"的判断，不是一个要渲染的错误。
 */
export async function loadCreationInspirations(): Promise<CreationInspiration[]> {
    const { inspirations } = await listCreationInspirations();
    return inspirations.map(inspirationFromRecord).filter((item): item is CreationInspiration => item !== null);
}
