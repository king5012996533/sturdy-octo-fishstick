import { useLayoutEffect, useRef, useState, type CSSProperties } from "react";

/**
 * 首页输入卡的双向对撞流光。
 *
 * 两束光都从卡"顶部正中"出发，一束顺时针、一束逆时针沿卡边环行：角速度相同、方向相反，
 * 起点又是同一个点，所以每圈只在顶部正中和底部正中各重合一次；重合处两层走加法混合
 * 叠起来，就是"撞"的那一下最亮。相位对齐完全靠共用起点，不需要额外的时序逻辑。
 *
 * 为什么是 SVG 描边而不是 conic-gradient：conic 的角度在中垂线附近变化极快，卡片又扁又宽，
 * 同样角宽的"高光"铺到上边缘会拉成一条几百像素的横带，读不出"一束光"。描边走的是周长，
 * 光斑长度按像素给，四条边一样长。
 *
 * 路径不从句首的顶部正中起笔，而是往回让出半个光斑，这样"光斑中心"在动画里正好压在
 * 顶/底正中，且关键帧可以写成纯数字：Chrome 对关键帧里带 var()/calc() 的值按离散处理，
 * 光斑会瞬移而不是游走（实测过），所以相位这件事必须用几何解决，不能塞进关键帧。
 *
 * 慢（5.5s）/快（2.6s）两条轨道一起跑、只切换透明度，而不是聚焦时改 animation-duration：
 * 改 duration 浏览器会按已跑过的进度重算相位，光会瞬移一段；叠一条同相的快轨道做
 * 交叉淡入，视觉上就只是"变快了"。
 *
 * 颜色、线宽、节奏都在 globals.css 的 composer-border-stream 段，组件只负责形状；
 * 系统"减少动态效果"和站内"无动效"降级成一条静态双色描边，不需要组件感知。
 */

// 光斑长度（px）：够读出"一束光"，又不至于长到像在描边。
const BEAM_LENGTH = 150;
// 周长归一化刻度：关键帧位移写死在这个刻度上，跟卡片实际尺寸解耦。
const PATH_LENGTH = 1000;
// 描边压在卡边往里 1px，2px 线宽正好盖住外壳那 1px 描边环再吃进卡里 1px。
const PATH_INSET = 1;

type BeamTone = "violet" | "indigo";
type BeamDirection = "forward" | "reverse";

type BeamGeometry = { path: string; dash: string; back: number };

function parseRadius(value: string): number {
    const parsed = Number.parseFloat(value);
    return Number.isFinite(parsed) ? parsed : 0;
}

/** 生成圆角矩形路径：起笔点让出半个光斑，绕一圈回到起笔点。 */
function buildBeamGeometry(width: number, height: number, cornerRadius: number): BeamGeometry | null {
    const w = width - PATH_INSET * 2;
    const h = height - PATH_INSET * 2;
    if (w <= 0 || h <= 0) return null;

    const r = Math.min(Math.max(cornerRadius - PATH_INSET, 0), w / 2, h / 2);
    const left = PATH_INSET;
    const top = PATH_INSET;
    const right = left + w;
    const bottom = top + h;
    // 起笔点只能退到上边第一个直段里，窄屏上退过头会掉进圆角。
    const back = Math.max(Math.min(BEAM_LENGTH / 2, w / 2 - r - 1), 1);
    const startX = left + w / 2 - back;
    const path = [
        `M ${startX} ${top}`,
        `H ${right - r}`,
        `A ${r} ${r} 0 0 1 ${right} ${top + r}`,
        `V ${bottom - r}`,
        `A ${r} ${r} 0 0 1 ${right - r} ${bottom}`,
        `H ${left + r}`,
        `A ${r} ${r} 0 0 1 ${left} ${bottom - r}`,
        `V ${top + r}`,
        `A ${r} ${r} 0 0 1 ${left + r} ${top}`,
        `H ${startX}`,
        "Z",
    ].join(" ");

    const perimeter = 2 * (w - 2 * r) + 2 * (h - 2 * r) + 2 * Math.PI * r;
    const beam = Math.min(back * 2, perimeter / 2);
    const dash = (beam / perimeter) * PATH_LENGTH;
    return { path, dash: `${dash} ${PATH_LENGTH - dash}`, back: (back / perimeter) * PATH_LENGTH };
}

function Beam({ tone, direction, geometry, length }: { tone: BeamTone; direction: BeamDirection; geometry: BeamGeometry; length: number }) {
    return (
        <path
            className={`composer-border-stream-beam is-${tone} is-${direction}`}
            d={geometry.path}
            pathLength={length}
            style={{ "--composer-stream-dash": geometry.dash, "--composer-stream-back": geometry.back } as CSSProperties}
        />
    );
}

const TONES: BeamTone[] = ["violet", "indigo"];
const DIRECTIONS: Record<BeamTone, BeamDirection> = { violet: "forward", indigo: "reverse" };

export function ComposerBorderStream() {
    const hostRef = useRef<HTMLSpanElement | null>(null);
    const [geometry, setGeometry] = useState<BeamGeometry | null>(null);

    useLayoutEffect(() => {
        const host = hostRef.current;
        const shell = host?.closest(".creation-composer-shell") ?? host;
        if (!host || !shell) return;
        const measure = () => {
            const rect = host.getBoundingClientRect();
            const radius = parseRadius(getComputedStyle(shell).borderTopLeftRadius);
            setGeometry(buildBeamGeometry(rect.width, rect.height, radius));
        };
        measure();
        const observer = new ResizeObserver(measure);
        observer.observe(host);
        return () => observer.disconnect();
    }, []);

    return (
        <span aria-hidden className="composer-border-stream" ref={hostRef}>
            <svg className="composer-border-stream-art" width="100%" height="100%">
                {geometry
                    ? (["cruise", "boost"] as const).map((pace) => (
                        <g className={`composer-border-stream-track is-${pace}`} key={pace}>
                            {TONES.map((tone) => (
                                <Beam key={tone} tone={tone} direction={DIRECTIONS[tone]} geometry={geometry} length={PATH_LENGTH} />
                            ))}
                        </g>
                    ))
                    : null}
            </svg>
        </span>
    );
}
