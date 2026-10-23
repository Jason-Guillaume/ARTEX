// 厂商预设只提供 API Base URL；模型名、密钥和请求参数由用户按账号实际情况填写。
export type VendorPreset = {
  id: string;
  name: string;
  baseUrl: string;
};

export const VENDOR_PRESETS: VendorPreset[] = [
  { id: "deepseek", name: "DeepSeek", baseUrl: "https://api.deepseek.com" },
  {
    id: "qwen",
    name: "通义千问",
    baseUrl: "https://dashscope.aliyuncs.com/compatible-mode/v1",
  },
  { id: "glm", name: "智谱 GLM", baseUrl: "https://open.bigmodel.cn/api/paas/v4" },
  { id: "kimi", name: "Kimi", baseUrl: "https://api.moonshot.cn/v1" },
  { id: "minimax", name: "MiniMax", baseUrl: "https://api.minimaxi.com/v1" },
  { id: "sensenova", name: "商汤日日新", baseUrl: "https://token.sensenova.cn/v1" },
];
