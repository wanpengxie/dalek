package app

// ApplyAgentProviderModelOverride 按“provider/model”覆盖 worker_agent 和 pm_agent。
// 仅做结构化覆盖，不做 provider 合法性校验（由调用方负责）。
func ApplyAgentProviderModelOverride(cfg *ProjectConfig, provider, model string) {
	if cfg == nil {
		return
	}
	*cfg = applyAgentProviderModel(*cfg, provider, model)
}
