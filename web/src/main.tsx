import "@fontsource-variable/geist";
import "@fontsource-variable/geist-mono";
import "@fontsource-variable/space-grotesk";
import { bootstrapAppearance } from "@/services/appearance-bootstrap";
import { bootstrapDesktopRuntime } from "@/services/desktop-runtime";
import { hydrateLocalCanvasProjectsFromBackend } from "@/services/local-workspace-repository";

async function startApplication() {
    await bootstrapDesktopRuntime();
    await hydrateLocalCanvasProjectsFromBackend();
    await bootstrapAppearance().finally(() => import("./application"));
}

void startApplication();
