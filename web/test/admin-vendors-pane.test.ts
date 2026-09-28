import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");
const read = (path: string) => readFileSync(resolve(root, path), "utf8");

const apiPath = "src/features/admin-console/api-vendors.ts";
const panePath = "src/features/admin-console/vendors-pane.tsx";

/**
 * 「模型厂商」分区：源码级契约。
 *
 * 面板与接线（admin-console.tsx 的分区 key、import、联合类型）由合并方负责，这里只钉住
 * 本分区自己的两件事：导出名必须与合并方的 import 一致，以及接口路径与「密钥只写不读」
 * 「两步接入不回滚」这两条口径不能在改代码时被悄悄改掉。
 */
describe("后台模型厂商面板", () => {
    test("文件存在，导出名与合并方 import 的一致", () => {
        expect(existsSync(resolve(root, apiPath))).toBe(true);
        expect(existsSync(resolve(root, panePath))).toBe(true);
        expect(read(panePath)).toContain("export function VendorsPane()");
        expect(read(apiPath)).toContain("export function listAdminVendors()");
    });

    test("全局 antd message 在项目里是关闭的，反馈必须落在页面上", () => {
        const pane = read(panePath);
        expect(pane).not.toContain("message.success");
        expect(pane).not.toContain("message.error");
        expect(pane).not.toContain("notification.");
        // 反馈统一走页内提示条；读失败要能当场重试。
        expect(pane).toContain("admin-notice is-error");
        expect(pane).toContain("admin-notice is-ok");
        expect(pane).toContain("重试");
        // 空态与加载态沿用既有的 admin-empty 块，不引 antd Empty。
        expect(pane).toContain("admin-empty");
        expect(pane).not.toMatch(/import\s*\{[^}]*\bEmpty\b/);
    });

    test("取数走 useCallback + useEffect，并覆盖加载态", () => {
        const pane = read(panePath);
        expect(pane).toContain("const load = useCallback(async () => {");
        expect(pane).toContain("useEffect(() => {");
        expect(pane).toContain("void load();");
        expect(pane).toContain("setLoading(true)");
        expect(pane).toContain("正在加载厂商");
        expect(pane).toContain("还没有接入厂商");
    });

    test("厂商卡片：类型、能力中文映射与凭证/模型计数", () => {
        const pane = read(panePath);
        expect(pane).toContain("export function capabilityLabel(");
        expect(pane).toContain("export function vendorKindLabel(");
        // 能力枚举 → 中文，四种能力都要在映射表里。
        expect(pane).toContain('{ TEXT: "文本", IMAGE: "图片", VIDEO: "视频", AUDIO: "音频" }');
        expect(pane).toContain('return kind === "BUILTIN" ? "内置" : "自建";');
        expect(pane).toContain("credentialCount");
        expect(pane).toContain("modelCount");
    });

    test("启用开关是乐观更新 + 失败回滚，不是半吊子实现", () => {
        const pane = read(panePath);
        // 先本地翻转，再发请求。
        expect(pane).toContain("const toggleVendor = useCallback(");
        expect(pane).toMatch(/setVendors\(\(current\) => current\.map\(\(item\) => \(item\.id === vendor\.id \? \{ \.\.\.item, enabled \} : item\)\)\)/);
        // 失败回滚到请求前的值，并把原因落到页面反馈条。
        expect(pane).toMatch(/enabled: vendor\.enabled/);
        expect(pane).toContain("失败：");
        expect(pane).toContain("updateAdminVendor(vendor.id, { enabled })");
    });

    test("密钥只写不读：Input.Password + 编辑留空表示不修改", () => {
        const pane = read(panePath);
        expect(pane).toContain("Input.Password");
        expect(pane).toContain("留空表示不修改");
        expect(pane).toContain("已设置，留空表示不修改");
        expect(pane).toContain("未设置");
        // 空串必须收成 undefined，否则后端会把密钥覆盖成空。
        expect(pane).toMatch(/function optionalText\(value\?: string\) \{[\s\S]*return trimmed \? trimmed : undefined;/);
        // 密钥原文不回显：编辑表单里 apiKey/secretKey 一律从空串起步。
        expect(pane).toContain('apiKey: "",');
        expect(pane).toContain('secretKey: "",');
    });

    test("接入厂商是两步：第二步失败要说明「厂商已建、凭据未建」并停在补凭据状态", () => {
        const pane = read(panePath);
        expect(pane).toContain("厂商已建、凭据未建");
        expect(pane).toContain("pendingVendor");
        expect(pane).toContain("重试保存凭据");
        // 顺序不能反：先 POST /admin/vendors，再用返回的 id 建凭证。
        const vendorCreate = pane.indexOf("await createAdminVendor(input)");
        const credentialCreate = pane.indexOf("await createAdminVendorCredential(created.id");
        expect(vendorCreate).toBeGreaterThan(-1);
        expect(credentialCreate).toBeGreaterThan(vendorCreate);
    });

    test("详情抽屉：凭证列表 + 测连通性 + 模型列表 + 从上游拉取", () => {
        const pane = read(panePath);
        expect(pane).toContain("<Drawer");
        expect(pane).toContain("测连通性");
        expect(pane).toContain("probeAdminVendorCredential");
        expect(pane).toContain("listAdminVendorCredentialModels");
        expect(pane).toContain("从上游拉取");
        expect(pane).toContain("importAdminVendorCredentialModels");
        // 探测结果必须说出"拉到多少个模型"，否则运营不知道这条路通不通。
        expect(pane).toContain("上游返回");
        expect(pane).toContain("formatCount(probed.length)");
        // 最近错误要露出来，否则只能靠猜哪条凭证坏了。
        expect(pane).toContain("lastError");
    });

    test("凭证与厂商删除都有二次确认，并如实展示服务端拒绝原因", () => {
        const pane = read(panePath);
        expect(pane).toContain("确认删除");
        expect(pane).toContain("取消");
        expect(pane).toContain("删除凭证？");
        expect(pane).toContain("删除厂商？");
        // 内置厂商不可删。
        expect(pane).toContain('disabled={detail.kind === "BUILTIN"}');
        // 服务端拒绝（例如仍被套餐引用）时引导改用停用，而不是硬报错。
        expect(pane).toContain("改用");
        expect(pane).toContain("停用");
    });

    test("接口路径与动词都拼在 /admin/vendors 下", () => {
        const api = read(apiPath);
        expect(api).toContain('http.get<{ vendors: Vendor[] }>("/admin/vendors")');
        expect(api).toContain('http.get<{ catalog: VendorCatalogItem[] }>("/admin/vendors/catalog")');
        expect(api).toContain('http.post<{ vendor: Vendor }>("/admin/vendors", input)');
        expect(api).toContain("http.put<{ vendor: Vendor }>(`/admin/vendors/${encodeURIComponent(id)}`, input)");
        expect(api).toContain("http.delete<{ deleted: true }>(`/admin/vendors/${encodeURIComponent(id)}`)");
        // 凭证：GET/POST 列表、PUT/DELETE 单条。
        expect(api).toContain("http.get<{ credentials: Credential[] }>(`/admin/vendors/${encodeURIComponent(vendorId)}/credentials`)");
        expect(api).toContain("http.post<{ credential: Credential }>(`/admin/vendors/${encodeURIComponent(vendorId)}/credentials`");
        expect(api).toContain(
            "http.put<{ credential: Credential }>(\n        `/admin/vendors/${encodeURIComponent(vendorId)}/credentials/${encodeURIComponent(credentialId)}`",
        );
        expect(api).toContain("http.delete<{ deleted: true }>(`/admin/vendors/${encodeURIComponent(vendorId)}/credentials/${encodeURIComponent(credentialId)}`)");
    });

    test("凭证探测与模型导入的路径带厂商标识和凭证标识，并放宽超时", () => {
        const api = read(apiPath);
        expect(api).toContain("http.get<{ models: VendorModel[] }>(");
        expect(api).toContain("`/admin/vendors/${encodeURIComponent(vendorId)}/credentials/${encodeURIComponent(credentialId)}/models`");
        expect(api).toContain(
            "`/admin/vendors/${encodeURIComponent(vendorId)}/credentials/${encodeURIComponent(credentialId)}/models/import`",
        );
        expect(api).toContain("`/admin/vendors/${encodeURIComponent(vendorId)}/credentials/${encodeURIComponent(credentialId)}/probe`");
        // 导入要提交勾选后的模型清单。
        expect(api).toContain("{ models }");
        expect(api).toContain("http.post<{ models: string[] }>(");
        expect(api).toContain("http.post<{ added: number; models: VendorModel[] }>(");
        // 上游目录拉取与探测是同步阻塞的，不能用默认 4s 超时。
        expect(api).toContain("const remoteProbeTimeoutMs = 45_000;");
        expect(api).toContain("timeout: remoteProbeTimeoutMs");
    });

    test("模型类型直接复用既有的 AdminChannelModel，不新造投影", () => {
        const api = read(apiPath);
        expect(api).toContain('import type { AdminChannelModel } from "./api";');
        expect(api).toContain("export type VendorModel = AdminChannelModel;");
    });
});
