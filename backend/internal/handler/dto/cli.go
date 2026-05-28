package dto

// === PunkcodeAI 桌面端 CLI 模式专用 DTO ===
// 这些类型只服务于 /api/v1/cli/* 路由组，与 Web 前端的 DTO 解耦。

// CliRegisterRequest 桌面端注册请求：邮箱 + 密码 + 昵称。
// 企业内部使用，不需要 verify_code / invitation_code / turnstile_token / promo_code。
type CliRegisterRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	Nickname string `json:"nickname" binding:"required,min=1,max=64"`
}

// CliLoginRequest 桌面端登录请求
type CliLoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// CliRefreshRequest 刷新 token
type CliRefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// CliLogoutRequest 登出请求；refresh_token 可选（提供则服务端撤销）
type CliLogoutRequest struct {
	RefreshToken string `json:"refresh_token,omitempty"`
}

// CliAuthResponse 注册 / 登录成功响应
type CliAuthResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresIn    int     `json:"expires_in"`
	TokenType    string  `json:"token_type"`
	User         CliUser `json:"user"`
}

// CliTokenPair 仅 token，不含用户信息。/cli/refresh 用这个，客户端拿到后自行调 /cli/me 取最新用户信息。
type CliTokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// CliUser 桌面端可见的用户字段
type CliUser struct {
	ID         int64   `json:"id"`
	Email      string  `json:"email"`
	Nickname   string  `json:"nickname"`
	BalanceUSD float64 `json:"balance_usd"`
}

// CliMeResponse /cli/me 返回值，含余额和用量摘要
type CliMeResponse struct {
	ID           int64   `json:"id"`
	Email        string  `json:"email"`
	Nickname     string  `json:"nickname"`
	BalanceUSD   float64 `json:"balance_usd"`
	UsedTodayUSD float64 `json:"used_today_usd"`
	UsedMonthUSD float64 `json:"used_month_usd"`
}

// CliApiKeyResponse 返回明文 API Key（仅用于桌面端注入 LLM 调用，桌面端不持久化）
type CliApiKeyResponse struct {
	Key string `json:"key"`
}

// CliLlmResponse LLM 网关接入信息
type CliLlmResponse struct {
	BaseURL          string     `json:"base_url"`           // OpenAI-compatible /v1
	AnthropicBaseURL string     `json:"anthropic_base_url"` // Anthropic 原生
	GeminiBaseURL    string     `json:"gemini_base_url"`    // Gemini 原生
	Models           []CliModel `json:"models"`             // 用户可见模型列表
}

// CliModel 桌面端展示的单个模型
type CliModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"` // 后端内部识别字段，桌面端 UI 不展示
	ContextWindow int    `json:"context_window"`
}
