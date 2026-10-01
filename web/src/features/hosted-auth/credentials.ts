/**
 * 标识与口令的纯规则，登录页与「忘记密码」共用。
 *
 * 抽出来不是为了复用几行代码，而是因为两处必须给出**同一套**判定：登录页放行的
 * 标识在重置页被判非法，用户会看到"刚才还能登，现在说格式不对"；反过来更糟，
 * 重置页接受了一个登录页不认的写法，用户设置完密码却登不进去。
 *
 * 这些函数只做即时提示，最终以服务端为准；前端规则与服务端不一致时，
 * 唯一的正当做法是把两边改齐，而不是在这里加一层宽容。
 */

const EMAIL_INPUT_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export function isValidEmailInput(value: string): boolean {
    const raw = String(value ?? "").trim();
    return EMAIL_INPUT_PATTERN.test(raw) && !raw.includes(" ");
}

/**
 * 与服务端 normalizePhone 对齐：先去掉分隔符和 +86 前缀再判断。
 *
 * 前端只做即时提示，最终以服务端为准；两边规则不一致会让用户看到「格式正确却
 * 被拒绝」，所以这里的归一逻辑必须和服务端保持一致。
 */
export function normalizePhoneInput(value: string): string {
    const compact = String(value ?? "").replace(/[\s\-()\t]/g, "");
    const withoutPrefix = compact.startsWith("+") ? compact.slice(1) : compact;
    return withoutPrefix.startsWith("86") && withoutPrefix.length > 11 ? withoutPrefix.slice(2) : withoutPrefix;
}

export function isValidPhoneInput(value: string): boolean {
    return /^1[3-9]\d{9}$/.test(normalizePhoneInput(value));
}

/**
 * 密码通道的标识既可以是邮箱也可以是手机号，服务端按形态分列存储。
 *
 * 规则必须和服务端 classifyPasswordTarget 对齐：只在一边放宽，用户就会看到
 * 「前端说格式没问题、提交后被拒」。
 */
export function isValidPasswordTargetInput(value: string): boolean {
    const raw = String(value ?? "").trim();
    if (!raw) return false;
    return isValidPhoneInput(raw) || isValidEmailInput(raw);
}

/**
 * 与服务端 validatePassword 对齐：8-64 位、不含空白、字母与数字都要有。
 *
 * 只做长度和字符类别，不强制大小写与符号 —— 服务端也是这个口径。
 */
export function isValidPasswordInput(value: string): boolean {
    const raw = String(value ?? "");
    if (raw.length < 8 || raw.length > 64) return false;
    if (/\s/.test(raw)) return false;
    return /[A-Za-z]/.test(raw) && /\d/.test(raw);
}

/** 标识形态：验证码通道按形态寻址，密码通道两种都收。 */
export type IdentityShape = "email" | "phone";

/**
 * 验证码发到哪：手机号走短信，其余走邮箱。
 *
 * 与登录页同一口径：形态由输入内容决定，不让用户先声明走哪条通道再填标识。
 */
export function identityShapeOf(value: string): IdentityShape | null {
    const raw = String(value ?? "").trim();
    if (isValidPhoneInput(raw)) return "phone";
    if (isValidEmailInput(raw)) return "email";
    return null;
}

/**
 * 本地联调时后端会回显验证码（没有真实投递通道才可能发生）。
 *
 * 抽成纯函数是为了让「回显」这件事在测试里可断言，而不是藏在点击回调内部。
 */
export function resolveDevCodeHint(challenge: { devCode?: string }): { code: string; message: string } | null {
    const code = String(challenge.devCode ?? "").trim();
    if (!code) return null;
    return { code, message: `本地投递：验证码 ${code} 已自动填入（未真实发送）` };
}
