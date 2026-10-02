import { readFileSync } from "node:fs";
import { describe, expect, test } from "bun:test";

import { buildNodeConfig } from "@/components/canvas/canvas-node-prompt-panel";
import { audioDurationLabel, audioSettingsSummary, audioSpeechProfile, audioTextOptional, buildAudioSpeechRequest, normalizeAudioDurationValue, normalizeAudioFormatValue, normalizeAudioSpeedValue, normalizeAudioVoiceValue, resolveAudioSpeechSettings } from "@/lib/audio-generation";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";
import { createModelChannel, defaultConfig, type AiConfig } from "@/stores/use-config-store";

function audioNode(patch: Partial<CanvasNodeData> = {}): CanvasNodeData {
    return {
        id: "audio-1",
        type: CanvasNodeType.Audio,
        title: "旁白",
        position: { x: 0, y: 0 },
        width: 320,
        height: 120,
        metadata: {},
        ...patch,
    };
}

function enterpriseConfig(): AiConfig {
    const channel = createModelChannel({
        id: "beefapi",
        name: "BeefAPI",
        baseUrl: "https://enterprise.beefapi.com",
        interfaceType: "openai-audio",
        models: ["minimax-speech-2.8-hd", "minimax-music-v3.0", "gpt-4o-mini-tts"],
        modelProfiles: [
            { model: "minimax-speech-2.8-hd", capability: "audio", protocol: "openai-audio" },
            { model: "minimax-music-v3.0", capability: "audio", protocol: "openai-audio" },
            { model: "gpt-4o-mini-tts", capability: "audio", protocol: "openai-audio" },
        ],
    });
    return {
        ...defaultConfig,
        channels: [channel],
        audioModel: "beefapi::minimax-speech-2.8-hd",
        audioVoice: "alloy",
        audioFormat: "mp3",
        audioSpeed: "1",
    };
}

describe("enterprise MiniMax speech settings", () => {
    test('blank speed stays 1 instead of clamping Number("") to the floor', () => {
        expect(normalizeAudioSpeedValue("", "minimax-speech-2.8-hd")).toBe("1");
        expect(normalizeAudioSpeedValue("  ", "gpt-4o-mini-tts")).toBe("1");
        expect(resolveAudioSpeechSettings("minimax-speech-2.8-hd", {}).audioSpeed).toBe("1");
        expect(normalizeAudioSpeedValue("0", "minimax-speech-2.8-hd")).toBe("1");
        expect(normalizeAudioSpeedValue("1.25", "minimax-speech-2.8-hd")).toBe("1.25");
    });

    test("MiniMax formats follow the native adapter, not OpenAI-only opus", () => {
        expect(audioSpeechProfile("minimax-speech-2.8-hd").formats.map((item) => item.value)).toEqual(["mp3", "wav", "aac", "flac", "pcm"]);
        expect(audioSpeechProfile("minimax-music-v3.0").formats.map((item) => item.value)).toEqual(["mp3", "wav", "aac", "flac", "pcm"]);
        expect(audioSpeechProfile("gpt-4o-mini-tts").formats.map((item) => item.value)).toContain("opus");
        expect(normalizeAudioFormatValue("opus", "minimax-speech-2.8-hd")).toBe("mp3");
    });

    test("does not coerce MiniMax voices onto OpenAI alloy", () => {
        expect(normalizeAudioVoiceValue("alloy", "beefapi::minimax-speech-2.8-hd")).toBe("male-qn-qingse");
        expect(normalizeAudioVoiceValue("中文", "minimax-speech-2.8-hd")).toBe("male-qn-qingse");
        expect(normalizeAudioVoiceValue("male-qn-jingying", "minimax-speech-2.8-hd")).toBe("male-qn-jingying");
        expect(normalizeAudioVoiceValue("cloned-voice-abc", "minimax-speech-2.8-hd")).toBe("cloned-voice-abc");
        expect(normalizeAudioVoiceValue("alloy", "gpt-4o-mini-tts")).toBe("alloy");
    });

    test("summary and payload follow the speech contract instead of 中文 · 24k · wav", () => {
        const settings = resolveAudioSpeechSettings("beefapi::minimax-speech-2.8-hd", {
            audioVoice: "alloy",
            audioFormat: "mp3",
            audioSpeed: "1",
        });
        expect(audioSettingsSummary({ model: "beefapi::minimax-speech-2.8-hd", ...settings })).toBe("青涩青年 · MP3 · 1x");
        expect(audioSettingsSummary({ model: "beefapi::minimax-speech-2.8-hd", ...settings })).not.toContain("24k");
        expect(audioSettingsSummary({ model: "beefapi::minimax-speech-2.8-hd", ...settings })).not.toContain("中文");
        expect(buildAudioSpeechRequest({ model: "minimax-speech-2.8-hd", ...settings }, "大家好")).toEqual({
            model: "minimax-speech-2.8-hd",
            input: "大家好",
            voice: "male-qn-qingse",
            response_format: "mp3",
            speed: 1,
        });
    });

    test("helper music payload omits voice; OpenAI TTS keeps alloy and instructions", () => {
        expect(buildAudioSpeechRequest({ model: "minimax-music-v3.0", audioFormat: "mp3" }, "轻快的钢琴")).toEqual({
            model: "minimax-music-v3.0",
            input: "轻快的钢琴",
            response_format: "mp3",
        });
        expect(
            buildAudioSpeechRequest(
                {
                    model: "gpt-4o-mini-tts",
                    audioVoice: "alloy",
                    audioFormat: "wav",
                    audioSpeed: "1.25",
                    audioInstructions: "温暖旁白",
                },
                "hello",
            ),
        ).toEqual({
            model: "gpt-4o-mini-tts",
            input: "hello",
            voice: "alloy",
            response_format: "wav",
            speed: 1.25,
            instructions: "温暖旁白",
        });
    });

    test("canvas node config remaps enterprise speech defaults for the selected model", () => {
        const config = buildNodeConfig(enterpriseConfig(), audioNode(), "audio", { capability: "audio" });
        expect(config.model).toContain("minimax-speech-2.8-hd");
        expect(config.audioVoice).toBe("male-qn-qingse");
        expect(config.audioFormat).toBe("mp3");
        expect(audioSettingsSummary(config)).toBe("青涩青年 · MP3 · 1x");
    });

    test("prompt panel no longer overlays a fake Seed Audio summary", () => {
        const source = readFileSync(new URL("../src/components/canvas/canvas-node-prompt-panel.tsx", import.meta.url), "utf8");
        expect(source).not.toContain("中文 · 24k · wav");
        expect(source).not.toContain('summaryOverride={localOnly ? "中文');
    });
});

describe("Replicate MiniMax audio settings", () => {
    test("owner/name models get their own family instead of OpenAI alloys", () => {
        expect(audioSpeechProfile("minimax/speech-2.8-turbo").kind).toBe("replicate-minimax-speech");
        expect(audioSpeechProfile("minimax/music-2.5").kind).toBe("replicate-minimax-music");
        expect(audioSpeechProfile("beefapi::minimax-speech-2.8-hd").kind).toBe("minimax-speech");
        expect(audioSpeechProfile("gpt-4o-mini-tts").kind).toBe("openai");
    });

    test("Replicate speech voices never fall back to OpenAI names", () => {
        expect(normalizeAudioVoiceValue("alloy", "minimax/speech-2.8-turbo")).toBe("English_Wiselady");
        expect(normalizeAudioVoiceValue("Wise_Woman", "minimax/speech-2.8-turbo")).toBe("Wise_Woman");
        expect(normalizeAudioVoiceValue("cloned-voice-abc", "minimax/speech-2.8-turbo")).toBe("cloned-voice-abc");
        expect(normalizeAudioVoiceValue("alloy", "minimax/music-2.5")).toBe("");
    });

    test("formats follow each family's upstream enum", () => {
        expect(audioSpeechProfile("minimax/speech-2.8-turbo").formats.map((item) => item.value)).toEqual(["mp3", "wav", "flac", "pcm"]);
        expect(audioSpeechProfile("minimax/music-2.5").formats.map((item) => item.value)).toEqual(["mp3", "wav", "pcm"]);
        expect(normalizeAudioFormatValue("aac", "minimax/speech-2.8-turbo")).toBe("mp3");
        expect(normalizeAudioFormatValue("flac", "minimax/music-2.5")).toBe("mp3");
        expect(normalizeAudioFormatValue("wav", "minimax/music-2.5")).toBe("wav");
    });

    test("speech speed is clamped to the upstream 0.5–2.0 range", () => {
        expect(normalizeAudioSpeedValue("4", "minimax/speech-2.8-turbo")).toBe("2");
        expect(normalizeAudioSpeedValue("0.1", "minimax/speech-2.8-turbo")).toBe("0.5");
        expect(normalizeAudioSpeedValue("1.25", "minimax/speech-2.8-turbo")).toBe("1.25");
    });

    test("music reuses the instructions field as the style description", () => {
        const profile = audioSpeechProfile("minimax/music-2.5");
        expect(profile.showInstructions).toBe(true);
        expect(profile.instructionsTitle).toBe("风格描述");
        expect(profile.showVoice).toBe(false);
        expect(profile.showSpeed).toBe(false);
        const settings = resolveAudioSpeechSettings("minimax/music-2.5", { audioFormat: "wav", audioInstructions: "独立民谣，木吉他" });
        expect(settings.audioInstructions).toBe("独立民谣，木吉他");
        expect(audioSettingsSummary({ model: "minimax/music-2.5", ...settings })).toBe("WAV");
    });
});

// ACE-Step 是社区模型：风格标签必填、歌词可空（空 = 纯器乐）、时长按秒指定。
// MiniMax 音乐族的时长由上游抽卡决定，给它挂档位只会骗用户，所以档位只在这里出现。
describe("ACE-Step music settings", () => {
    function aceConfig(): AiConfig {
        const channel = createModelChannel({
            id: "replicate",
            name: "Replicate",
            baseUrl: "https://api.replicate.com",
            interfaceType: "replicate-prediction-audio",
            models: ["lucataco/ace-step", "minimax/music-2.5"],
            modelProfiles: [
                { model: "lucataco/ace-step", capability: "audio", protocol: "replicate-prediction-audio" },
                { model: "minimax/music-2.5", capability: "audio", protocol: "replicate-prediction-audio" },
            ],
        });
        return { ...defaultConfig, channels: [channel], audioModel: "replicate::lucataco/ace-step" };
    }

    test("only the model that takes seconds gets duration tiers", () => {
        const profile = audioSpeechProfile("lucataco/ace-step");
        expect(profile.kind).toBe("replicate-ace-step");
        expect(profile.durationOptions?.map((item) => item.value)).toEqual(["15", "30", "60", "90", "120", "180"]);
        expect(profile.showFormat).toBe(false);
        expect(profile.instructionsTitle).toBe("风格标签（必填）");
        expect(audioSpeechProfile("minimax/music-2.5").durationOptions || []).toEqual([]);
        expect(normalizeAudioDurationValue("30", "minimax/music-2.5")).toBe("");
    });

    test("duration falls back to the default tier and refuses values outside it", () => {
        expect(normalizeAudioDurationValue("", "lucataco/ace-step")).toBe("60");
        expect(normalizeAudioDurationValue("600", "lucataco/ace-step")).toBe("60");
        expect(normalizeAudioDurationValue("15", "lucataco/ace-step")).toBe("15");
        expect(audioDurationLabel("90", "lucataco/ace-step")).toBe("1.5分");
        expect(audioSettingsSummary({ model: "lucataco/ace-step", audioDuration: "30" })).toBe("30秒");
    });

    test("empty lyrics are legitimate only where the upstream takes an instrumental marker", () => {
        expect(audioTextOptional("lucataco/ace-step")).toBe(true);
        expect(audioTextOptional("minimax/music-2.5")).toBe(false);
        expect(audioTextOptional("minimax/speech-2.8-turbo")).toBe(false);
    });

    test("canvas node metadata carries the chosen duration into the request config", () => {
        const config = buildNodeConfig(aceConfig(), audioNode({ metadata: { model: "replicate::lucataco/ace-step", audioDuration: "30", audioInstructions: "lofi, rainy night" } }), "audio", { capability: "audio" });
        expect(config.audioDuration).toBe("30");
        expect(audioSettingsSummary(config)).toBe("30秒");
    });
});
