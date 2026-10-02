import { withCloudMediaContent } from "@/lib/canvas/canvas-cloud-media";
import { isHostedBuild } from "@/lib/hosted-build";
import { getMediaBlob } from "@/services/file-storage";
import { getImageBlob } from "@/services/image-storage";
import { syncLocalCanvasProjectToBackend } from "@/services/local-workspace-repository";
import { resourceFileUrl, resourceIdFromStorageKey, resourceStorageKey, ResourceUploadError, uploadResourceFile } from "@/services/api/resources";
import { useAssetStore, type Asset } from "@/stores/use-asset-store";
import { useCanvasStore } from "@/stores/canvas/use-canvas-store";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

/**
 * 把只存在于浏览器 IndexedDB 的素材补传进云端资源库。
 *
 * 托管构建在早些版本里把上传直接写进了 IndexedDB（见 workspace-resource-storage），
 * 于是画布节点和素材库都只留下 `image:<scope>:<id>` / `media:<scope>:<id>` 这类本机
 * 键，配上一个刷新即失效的 blob: 地址。服务端从未收到这些字节，换机器就是空白。
 *
 * 修复写入路径只能保证之后不再产生这类数据；已经散落在各人浏览器里的素材必须补传，
 * 否则用户手里那张画布永远只有一半在云端。这个任务只做一件事：
 * 找到本机还能读到的本地键 → 上传 → 把引用改写成 resource:<id> → 回存画布。
 * 读不到的（换过机器、清过缓存）跳过并如实计数，不做任何猜测。
 */

export type CanvasMediaCloudBackfillResult = {
    /** 成功补传并改写引用的媒体数量。 */
    uploaded: number;
    /** 只在本机、但当前进程读不到字节的引用数量。 */
    unreadable: number;
    /** 上传失败（多为网络/配额）而保留原状的引用数量。 */
    failed: number;
    /** 被改写并回存到服务端的画布数量。 */
    projects: number;
    /** 被改写引用的素材数量。 */
    assets: number;
};

const EMPTY_RESULT: CanvasMediaCloudBackfillResult = { uploaded: 0, unreadable: 0, failed: 0, projects: 0, assets: 0 };

/** 单次运行的上传上限。补传是后台任务，不能占满用户的带宽和配额。 */
const MAX_UPLOADS_PER_RUN = 120;

const LOCAL_MEDIA_NODE_TYPES: ReadonlySet<string> = new Set([CanvasNodeType.Image, CanvasNodeType.Video, CanvasNodeType.Audio, CanvasNodeType.Panorama]);

let inflight: Promise<CanvasMediaCloudBackfillResult> | null = null;

/** 已上传的本地键 → 云端引用。跨次运行保留，避免同一份字节反复上传。 */
const promoted = new Map<string, PromotedMedia>();

type PromotedMedia = {
    storageKey: string;
    url: string;
    bytes: number;
    mimeType: string;
};

function isLocalOnlyKey(storageKey?: string) {
    return Boolean(storageKey) && !resourceIdFromStorageKey(storageKey);
}

function uploadKind(storageKey: string): "image" | "video" | "audio" | "file" {
    const prefix = storageKey.split(":", 1)[0];
    if (prefix === "image") return "image";
    if (prefix === "video") return "video";
    if (prefix === "audio") return "audio";
    return "file";
}

/** 图片和音视频在两套 IndexedDB 实例里，按 key 前缀选对读取入口。 */
async function readLocalBlob(storageKey: string) {
    return uploadKind(storageKey) === "image" ? getImageBlob(storageKey) : getMediaBlob(storageKey);
}

function collectCandidateKeys() {
    const keys = new Set<string>();
    for (const project of useCanvasStore.getState().projects) {
        for (const node of project.nodes) {
            if (!LOCAL_MEDIA_NODE_TYPES.has(node.type)) continue;
            const storageKey = node.metadata?.storageKey;
            if (isLocalOnlyKey(storageKey)) keys.add(storageKey!);
        }
    }
    for (const asset of useAssetStore.getState().assets) {
        if (!("storageKey" in asset.data)) continue;
        const storageKey = asset.data.storageKey;
        if (isLocalOnlyKey(storageKey)) keys.add(storageKey!);
    }
    return keys;
}

async function promoteOne(storageKey: string, result: CanvasMediaCloudBackfillResult) {
    const cached = promoted.get(storageKey);
    if (cached) return;
    const blob = await readLocalBlob(storageKey).catch(() => null);
    if (!blob) {
        // 引用还在，字节已经不在本机：无法凭空恢复，只能计数交给上层如实告知。
        result.unreadable += 1;
        return;
    }
    try {
        const resource = await uploadResourceFile(blob, uploadKind(storageKey), {
            fileName: storageKey.split(":").pop(),
            idempotencyKey: storageKey,
        });
        promoted.set(storageKey, {
            storageKey: resourceStorageKey(resource.id),
            url: resource.publicUrl || resourceFileUrl(resource.id),
            bytes: resource.size || blob.size,
            mimeType: resource.mimeType || blob.type || "application/octet-stream",
        });
        result.uploaded += 1;
    } catch (error) {
        // 鉴权/越权这类永久失败重试也没用，但补传任务不该把整个流程打断。
        result.failed += 1;
        if (!(error instanceof ResourceUploadError && error.permanent)) {
            console.warn("本地素材补传云端失败，稍后会再试", { storageKey, error });
        }
    }
}

function rewriteNode(node: CanvasNodeData): CanvasNodeData | null {
    if (!LOCAL_MEDIA_NODE_TYPES.has(node.type) || !node.metadata) return null;
    const storageKey = node.metadata.storageKey;
    if (!storageKey) return null;
    const promotedMedia = promoted.get(storageKey);
    if (!promotedMedia) return null;
    const metadata = {
        ...node.metadata,
        storageKey: promotedMedia.storageKey,
        content: promotedMedia.url,
        bytes: node.metadata.bytes || promotedMedia.bytes,
        mimeType: node.metadata.mimeType || promotedMedia.mimeType,
    };
    return { ...node, metadata };
}

function rewriteAssets(result: CanvasMediaCloudBackfillResult) {
    const store = useAssetStore.getState();
    for (const asset of store.assets) {
        if (!("storageKey" in asset.data)) continue;
        const promotedMedia = promoted.get(asset.data.storageKey || "");
        if (!promotedMedia) continue;
        store.updateAsset(asset.id, assetPatch(asset, promotedMedia));
        result.assets += 1;
    }
}

type AssetPatch = Partial<Omit<Asset, "id" | "createdAt">>;

function assetPatch(asset: Asset, promotedMedia: PromotedMedia): AssetPatch {
    const coverUrl = asset.coverUrl.startsWith("blob:") || asset.coverUrl.startsWith("data:") ? promotedMedia.url : asset.coverUrl;
    switch (asset.kind) {
        case "image":
            return { coverUrl, data: { ...asset.data, dataUrl: promotedMedia.url, storageKey: promotedMedia.storageKey } };
        case "video":
        case "audio":
        case "model":
            return { coverUrl, data: { ...asset.data, url: promotedMedia.url, storageKey: promotedMedia.storageKey } };
        default:
            return { coverUrl };
    }
}

async function rewriteProjects(result: CanvasMediaCloudBackfillResult) {
    const store = useCanvasStore.getState();
    const changedIds = store.projects
        .map((project) => {
            let changed = false;
            const nodes = project.nodes.map((node) => {
                const rewritten = rewriteNode(node);
                if (rewritten) changed = true;
                return rewritten || withCloudMediaContent(node);
            });
            return { id: project.id, nodes, changed };
        })
        .filter((entry) => entry.changed);

    for (const entry of changedIds) {
        store.updateProject(entry.id, { nodes: entry.nodes });
    }
    for (const entry of changedIds) {
        // 逐个回存：并发 PUT 会撞服务端乐观锁，反而制造出一堆 409。
        try {
            await syncLocalCanvasProjectToBackend(entry.id);
            result.projects += 1;
        } catch (error) {
            console.warn("补传素材后回存画布失败，本地已改写，等待下次保存", { id: entry.id, error });
        }
    }
}

async function run(): Promise<CanvasMediaCloudBackfillResult> {
    const result: CanvasMediaCloudBackfillResult = { ...EMPTY_RESULT };
    const keys = Array.from(collectCandidateKeys()).slice(0, MAX_UPLOADS_PER_RUN);
    for (const storageKey of keys) {
        await promoteOne(storageKey, result);
    }
    if (!promoted.size) return result;
    rewriteAssets(result);
    await rewriteProjects(result);
    return result;
}

/**
 * 幂等、单飞、失败不影响主流程。
 *
 * 桌面/本地构建没有云端资源库，直接跳过；托管构建在会话就绪后调用一次即可。
 */
export function backfillCanvasMediaToCloud(): Promise<CanvasMediaCloudBackfillResult> {
    if (!isHostedBuild()) return Promise.resolve({ ...EMPTY_RESULT });
    if (inflight) return inflight;
    inflight = run()
        .catch((error) => {
            console.warn("本地素材补传云端任务异常终止", error);
            return { ...EMPTY_RESULT };
        })
        .finally(() => {
            inflight = null;
        });
    return inflight;
}
