import React from "react";
import { createRoot } from "react-dom/client";
import "antd/dist/reset.css";
import "./styles/globals.css";
import "./styles/beeftv-local-overrides.css";
// 全局自举内置插件注册（editor-shell 等预设以模块副作用注册编辑器插槽）：
// 冷启动直达编辑器时素材/时间线等插槽不再为空。
import "@/lib/plugins/builtin";
import { RouterProvider } from "react-router";

import { AppProviders } from "@/components/layout/app-providers";
import { router } from "@/router";

// 上游在这里硬写了一条 SF Pro 内联字体，优先级高于任何样式表，KinoTV 的
// Geist / Space Grotesk 字系被整条盖掉。正文交给 --font-sans 令牌，
// antd 浮层走 ConfigProvider 的 fontFamily token，两边同一个字系。
document.body.style.fontFamily = "var(--font-sans)";

createRoot(document.getElementById("root")!).render(
    <React.StrictMode>
        <AppProviders>
            <RouterProvider router={router} />
        </AppProviders>
    </React.StrictMode>,
);
