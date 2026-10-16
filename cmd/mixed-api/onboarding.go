package main

import (
	"fmt"
	"strings"
)

// The onboarding guidance is deliberately independent of the Windows UI.
// UI and status text are driven by observed backend state, never by a click alone.
type onboardingGuide struct {
	Name, Stage, StageLabel, Detail, Prepare, Authorize, Verify, Next, Recovery string
	NeedsKey                                                                    bool
}

type onboardingProvider struct {
	Name, Prepare, Authorize, Verify, Recovery string
	NeedsKey                                   bool
}

var onboardingProviders = map[string]onboardingProvider{
	"codex": {
		Name: "Codex · ChatGPT", Prepare: "自动安装 Codex CLI 并连接官方 app-server，不需要 SillyTavern。",
		Authorize: "首次会打开 Codex 官方登录窗口，用 ChatGPT 账号本人完成授权；不要在这里填写 Token。",
		Verify:    "登录成功后自动刷新模型，看到 codex/ 开头的模型才可使用。",
		Recovery:  "登录窗口未出现？再次点一键接入；若刚安装 Node.js，请重启 MIXED-API 后重试。",
	},
	"vertex": {
		Name: "Vertex · Gemini", Prepare: "软件自动准备 Python、代理和本地访问密钥。",
		Authorize: "将你自己的 Google Vertex Express API Key 粘贴到下方输入框，点击『保存并继续』；没有 Key 就先申请。",
		Verify:    "读取 vertex/ 开头的模型后，即可选择它开始聊天。",
		Recovery:  "没有模型或 401/403？检查 Express Key 对应项目的权限、计费与 API 额度。",
		NeedsKey:  true,
	},
	"opencode": {
		Name: "OpenCode", Prepare: "自动下载安装 OpenCode 代理，并生成私有 server key。",
		Authorize: "可先尝试匿名模式；需要 Zen 服务时只填你的 OpenCode Zen Key，不是本地 server key。",
		Verify:    "出现 opencode/ 开头的模型，即接入成功。匿名模式可用性取决于上游。",
		Recovery:  "没有模型？尝试填写有效 Zen Key，再点『保存并继续』；若报限流，请检查上游额度。",
		NeedsKey:  true,
	},
	"workbuddy": {
		Name: "WorkBuddy", Prepare: "MIXED-API 自动安装并启动 WorkBuddy 代理后端。",
		Authorize: "需要电脑已安装官方 WorkBuddy 客户端，并在客户端内本人登录账号。代理不会替你登录。",
		Verify:    "保持 WorkBuddy 客户端运行，再检查 workbuddy/ 模型列表。",
		Recovery:  "显示无法连接？先确认 WorkBuddy 客户端已打开并登录，再点击重新接入。",
	},
	"aps": {
		Name: "APS2api", Prepare: "自动下载安装固定的 Windows 后端、生成本地访问 Key 和配置。",
		Authorize: "请确认符合原项目的非商业许可证与服务使用规则；无需自行生成本地 Key。",
		Verify:    "检测到 aps/ 模型即成功。原项目部分接口可能依赖匿名服务。",
		Recovery:  "模型为空或 403？查看日志确认上游限制、网络与许可证；请勿绕过服务限制。",
	},
	"aistudio": {
		Name: "AI Studio Build", Prepare: "自动安装 Node 依赖、准备 Camoufox 浏览器、代理和随机本地 Key。",
		Authorize: "首次需要在弹出的 Google AI Studio 浏览器窗口中本人登录。完成后软件复用有效会话。",
		Verify:    "登录后检查 aistudio/ 模型；浏览器首次初始化可能稍慢。",
		Recovery:  "没有弹出登录窗口？点击『打开登录』；已过期时重新进行 Google 授权。",
	},
}

func guideFor(id, state string, models []string) onboardingGuide {
	p, ok := onboardingProviders[id]
	if !ok {
		return onboardingGuide{Stage: "failed", StageLabel: "不支持的来源", Detail: "请选择已内置的六个来源。"}
	}
	g := onboardingGuide{Name: p.Name, Prepare: p.Prepare, Authorize: p.Authorize, Verify: p.Verify, Recovery: p.Recovery, NeedsKey: p.NeedsKey}
	switch {
	case strings.HasPrefix(state, "failed:") || strings.HasPrefix(state, "exited:"):
		g.Stage = "failed"
		g.StageLabel = "需要处理"
		g.Detail = "连接失败。查看下面的错误和解决办法，然后再次点击一键接入。"
	case strings.HasPrefix(state, "needs_runtime:"):
		g.Stage = "runtime"
		g.StageLabel = "待重启软件"
		g.Detail = "已尝试安装缺失的运行环境。等安装结束后关闭并重开 MIXED-API，再点一次一键接入。"
	case strings.HasPrefix(state, "auth_pending:") || strings.HasPrefix(state, "needs_auth:"):
		g.Stage = "auth"
		g.StageLabel = "等待账号授权"
		g.Detail = "程序已完成能代办的准备。现在只需要你进行下面的官方授权步骤。"
	case strings.HasPrefix(state, "installing") || strings.HasPrefix(state, "quickstart") || strings.HasPrefix(state, "checking:") || strings.HasPrefix(state, "stopping"):
		g.Stage = "working"
		g.StageLabel = "正在自动处理"
		g.Detail = "软件正在下载、安装、启动或检测模型。请等待；不用重复点击。"
	case strings.HasPrefix(state, "ready:") && len(models) > 0:
		g.Stage = "ready"
		g.StageLabel = "模型已就绪"
		g.Detail = fmt.Sprintf("已发现 %d 个模型。下一步：复制 URL、客户端 Key 和下方模型 ID 到你的 AI 应用。", len(models))
	case strings.HasPrefix(state, "ready:"):
		g.Stage = "checking"
		g.StageLabel = "已授权，待检查模型"
		g.Detail = "后台报告已就绪，但还没有从统一接口验证到模型。点『检查模型』，不要直接把它当成可聊天。"
	case strings.HasPrefix(state, "running"):
		g.Stage = "checking"
		g.StageLabel = "进程已启动"
		g.Detail = "代理已启动，正在等待 API 模型检查；启动进程不等于已经能聊天。"
	default:
		g.Stage = "idle"
		g.StageLabel = "尚未接入"
		g.Detail = "先点左侧『一键接入』，软件负责安装与配置。只在需要官方授权时让你操作。"
	}
	if strings.HasPrefix(state, "failed:") || strings.HasPrefix(state, "needs_auth:") || strings.HasPrefix(state, "needs_runtime:") {
		if _, detail, ok := strings.Cut(state, ":"); ok && strings.TrimSpace(detail) != "" {
			g.Detail += "\r\n当前原因：" + strings.TrimSpace(detail)
		}
	}
	if len(models) > 0 {
		g.Next = "客户端选择：" + models[0] + "\r\n统一 URL：http://127.0.0.1:8765/v1 ；Key：点顶部『复制客户端 Key』。"
	} else {
		g.Next = "模型尚未确认可用。授权后点击『检查模型』；成功时可复制完整模型 ID。"
	}
	return g
}

func guideSteps(g onboardingGuide) string {
	return "① 自动准备　" + g.Prepare + "\r\n\r\n" +
		"② 需要你做　" + g.Authorize + "\r\n\r\n" +
		"③ 验证与使用　" + g.Verify
}


 // Wizard stages are UI-level gates, not backend statuses. A successful
 // background download or API probe is NEVER permission to skip the user's
 // explicit "我已完成，下一步" confirmation.
 type wizardPhase string

 const (
     wizardIdle      wizardPhase = "idle"
     wizardPreparing wizardPhase = "preparing"
     wizardAwaitUser wizardPhase = "await_user"
     wizardVerifying wizardPhase = "verifying"
     wizardComplete wizardPhase = "complete"
     wizardBlocked  wizardPhase = "blocked"
 )

 func wizardAfterStatus(phase wizardPhase, state string) wizardPhase {
     if phase == wizardIdle || phase == wizardVerifying {
         return phase
     }
     if strings.HasPrefix(state, "failed:") || strings.HasPrefix(state, "exited:") || strings.HasPrefix(state, "needs_runtime:") {
         return wizardBlocked
     }
     if phase == wizardPreparing && (strings.HasPrefix(state, "ready:") ||
         strings.HasPrefix(state, "checking:") || strings.HasPrefix(state, "auth_pending:") ||
         strings.HasPrefix(state, "needs_auth:") || strings.HasPrefix(state, "running") ||
         strings.HasPrefix(state, "installed:")) {
         return wizardAwaitUser
     }
     return phase
 }

 func wizardCanConfirm(phase wizardPhase) bool { return phase == wizardAwaitUser }

 func wizardStageText(phase wizardPhase, g onboardingGuide, status string, models []string, validationError string) (string, string, string) {
     switch phase {
     case wizardPreparing:
         return "①/③ 由软件自动准备", "正在自动下载、安装、生成本地配置并启动服务。请等待完成；此时不能跳过到下一步。\r\n\r\n" + g.Prepare +
             "\r\n\r\n当前进度：" + status, "准备中…"
     case wizardAwaitUser:
         detail := "②/③ 请完成授权或确认"
         next := "请先按下面的说明操作，操作完成后再点击『我已完成，下一步』。\r\n\r\n" + g.Authorize
         if g.Name == "APS2api" {
             next += "\r\n\r\n请确认你遵守原项目的非商业许可证与使用规则。"
         }
         if validationError != "" {
             next += "\r\n\r\n上次验证未通过：" + validationError + "\r\n请解决后再点『我已完成，下一步』，不会自动跳过。"
         }
         next += "\r\n\r\n当前后端状态：" + status + "\r\n\r\n遇到问题：" + g.Recovery
         return detail, next, "我已完成，下一步 →"
     case wizardVerifying:
         return "③/③ 软件正在验证模型", "已收到你的确认。正在向统一网关检查该来源的模型接口及授权状态；验证通过才能进入完成页。\r\n\r\n" + g.Verify, "正在验证…"
     case wizardComplete:
         id := ""
         if len(models) > 0 { id = models[0] }
         return "✓ 已验证模型列表", fmt.Sprintf("当前来源已读取到 %d 个模型。\r\n\r\n接下来在你使用的 AI 软件中填写：\r\nBase URL：http://127.0.0.1:8765/v1\r\nAPI Key：点击顶部「复制客户端 Key」\r\n模型：在下方选择并点击「复制模型 ID」\r\n\r\n例如：%s\r\n\r\n注意：模型列表验证通过不等于已完成实际对话测试。", len(models), id), "重新检查模型"
     case wizardBlocked:
         return "需要你处理后重试", "自动准备未完成，不能继续到验证步骤。\r\n\r\n当前错误：" + status +
             "\r\n\r\n建议：" + g.Recovery + "\r\n\r\n处理好后点「重新接入」。", "请先解决问题"
     default:
         return "①/③ 先选择来源", "选择左侧的「一键接入」，软件会先自动准备运行环境；遇到首次授权时自动打开可用的官方登录入口，然后停下等待你点击「我已完成，下一步」。", "尚未开始"
     }
 }
