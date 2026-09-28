import { getPublicAppearance, type PublicAppearance } from "@/services/api/appearance";
import { commitPublicAppearance, DEFAULT_PUBLIC_APPEARANCE } from "@/stores/use-appearance-store";

const APPEARANCE_BOOTSTRAP_TIMEOUT_MS = 4_000;

export async function resolvePublicAppearance(fetchAppearance: (signal: AbortSignal) => Promise<PublicAppearance> = getPublicAppearance) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), APPEARANCE_BOOTSTRAP_TIMEOUT_MS);
    try {
        return await fetchAppearance(controller.signal);
    } catch {
        return DEFAULT_PUBLIC_APPEARANCE;
    } finally {
        clearTimeout(timer);
    }
}

/**
 * 启动时应用平台外观。
 *
 * 必须真去取一次：品牌名、Logo、登录页文案都由后台配置，若这里只落内置默认值，
 * 运营在后台改完品牌后前台永远看不到变化（而登录页恰好与默认值相同，故障会被掩盖）。
 * 取不到就回落默认值，离线桌面形态照常启动。
 */
export async function bootstrapAppearance(fetchAppearance: (signal: AbortSignal) => Promise<PublicAppearance> = getPublicAppearance) {
    return commitPublicAppearance(await resolvePublicAppearance(fetchAppearance));
}
