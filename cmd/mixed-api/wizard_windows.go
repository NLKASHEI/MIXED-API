//go:build windows

package main

import (
    "encoding/json"
    "fmt"
    "os/exec"
    "strings"

    "github.com/NLKASHEI/MIXED-API/internal/gateway"
)

// beginProvider deliberately does not move to the last step. The user must
// acknowledge any required authorization before model verification happens.
func (v *nativeView) beginProvider(id string) {
    v.mu.Lock()
    phase := v.wizard[id]
    if phase == wizardPreparing || phase == wizardVerifying || phase == wizardAwaitUser || phase == wizardComplete {
        v.mu.Unlock()
        v.notify("已选中 " + id + "；请按右侧当前步骤操作。需要重新安装时点击「重新接入」。")
        return
    }
    v.wizard[id] = wizardPreparing
    v.wizardError[id] = ""
    v.autoAuthOpened[id] = false
    v.mu.Unlock()
    v.notify("① 软件正在自动准备 " + id + "。请等待，随后会提示你下一步。")
    v.connectProvider(id)
}

func (v *nativeView) retryProvider(id string) {
    v.mu.Lock()
    v.wizard[id] = wizardPreparing
    v.wizardError[id] = ""
    v.autoAuthOpened[id] = false
    v.modelCache[id] = nil
    if v.selected == id {
        v.modelIDs = nil
        v.modelError = ""
    }
    v.mu.Unlock()
    v.notify("正在重新准备 "+id+"；已有的个人账号授权不会被删除。")
    v.connectProvider(id)
}

func (v *nativeView) modelCheckState(id string) string {
    raw, err := v.request("GET", "/api/status")
    if err != nil { return "" }
    var report struct { Processes map[string]string `json:"processes"` }
    if json.Unmarshal(raw, &report) != nil { return "" }
    state := report.Processes["setup:"+id]
    if state == "" { state = report.Processes[id] }
    return state
}

// This is the only transition to wizardComplete. A timer or provider state
// update can never silently mark an unacknowledged wizard completed.
func (v *nativeView) confirmStep() {
    v.mu.Lock()
    id := v.selected
    phase := v.wizard[id]
    if !wizardCanConfirm(phase) && phase != wizardComplete {
        v.mu.Unlock()
        v.notify("当前步骤还不能跳过。请等待软件准备完成，再点击『我已完成，下一步』。")
        return
    }
    v.wizard[id] = wizardVerifying
    v.wizardError[id] = ""
    v.mu.Unlock()
    v.notify("③ 已收到确认。正在检查 "+id+" 的登录状态与模型 API……")
    v.refresh()
    // Re-read the server state to avoid trusting a stale UI status.
    state := v.modelCheckState(id)
    if strings.HasPrefix(state, "auth_pending:") || strings.HasPrefix(state, "needs_auth:") {
        v.finishVerification(id, nil, "后台仍在等待授权：" + state)
        return
    }
    if strings.HasPrefix(state, "needs_runtime:") || strings.HasPrefix(state, "failed:") {
        v.finishVerification(id, nil, "后端尚未准备好：" + state)
        return
    }
    ids, err := v.requestModelsFor(id)
    if len(ids) == 0 {
        if err == "" { err = "还没找到可用模型，请确认官方账号完成授权；如果刚登录成功，请稍等几秒重试。" }
        v.finishVerification(id, nil, err)
        return
    }
    v.finishVerification(id, ids, "")
}

func (v *nativeView) finishVerification(id string, ids []string, problem string) {
    v.mu.Lock()
    if problem != "" {
        v.wizard[id] = wizardAwaitUser
        v.wizardError[id] = problem
        v.modelCache[id] = nil
        if v.selected == id {
            v.modelIDs = nil
            v.modelError = problem
        }
    } else {
        v.wizard[id] = wizardComplete
        v.wizardError[id] = ""
        v.modelCache[id] = append([]string(nil), ids...)
        if v.selected == id {
            v.modelIDs = append([]string(nil), ids...)
            v.modelError = ""
        }
    }
    v.mu.Unlock()
    if problem != "" {
        v.notify("验证未通过，仍停留在第 ② 步："+problem)
    } else {
        v.notify(fmt.Sprintf("验证通过：%s 已发现 %d 个模型。接下来复制 URL、Key 和模型 ID 到客户端。", id, len(ids)))
    }
    v.refresh()
}

// Open the necessary official authorization UI just once when the managed
// backend reaches the human-confirmation step. Never guess credentials.
func (v *nativeView) autoAuthFor(id, state string) {
    switch id {
    case "codex":
        // The managed Codex quickstart has already initiated the official
        // Codex CLI OAuth flow when required; don't launch it twice.
        return
    case "aistudio":
        if strings.HasPrefix(state, "needs_auth:") || strings.HasPrefix(state, "checking:") {
            v.openLogin(id) // Camoufox / Google AI Studio interactive window
        }
    case "vertex":
        raw, err := v.request("GET", "/api/config")
        if err != nil { return }
        var cfg gateway.Config
        if json.Unmarshal(raw, &cfg) != nil { return }
        for _, b := range cfg.Backends {
            if b.ID == id && b.ProviderKey == "" {
                v.openOfficialURL("https://console.cloud.google.com/expressmode")
                v.notify("已打开 Google 官方 Express Key 入口；申请后粘贴到右侧输入框，点保存。")
            }
        }
    case "workbuddy":
        if strings.HasPrefix(state, "needs_auth:") {
            v.openOfficialURL("https://copilot.tencent.com")
            v.notify("已打开 WorkBuddy 官方入口。请先在 WorkBuddy 桌面客户端登录，再点『我已完成，下一步』。")
        }
    case "opencode":
        if strings.HasPrefix(state, "needs_auth:") {
            v.openOfficialURL("https://opencode.ai/zen")
            v.notify("已打开 OpenCode Zen 官方入口。需要 Key 时填写；也可以按提示尝试匿名模式。")
        }
    }
}

func (v *nativeView) openOfficialURL(address string) {
    if err := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", address).Start(); err != nil {
        v.notify("无法自动打开浏览器，请点击右侧授权入口："+err.Error())
    }
}
