package main

import (
    "strings"
    "testing"
)

func TestWizardRequiresUserConfirmationEvenAfterBackendReady(t *testing.T) {
    cases := []struct{ current wizardPhase; status string; expected wizardPhase }{
        {wizardIdle, "ready: 5 models", wizardIdle},
        {wizardPreparing, "quickstart: preparing", wizardPreparing},
        {wizardPreparing, "installed: backend installed", wizardAwaitUser},
        {wizardPreparing, "auth_pending: complete official login", wizardAwaitUser},
        {wizardPreparing, "ready: 5 models", wizardAwaitUser},
        {wizardAwaitUser, "ready: 5 models", wizardAwaitUser},
        {wizardVerifying, "ready: 5 models", wizardVerifying},
        {wizardComplete, "ready: 5 models", wizardComplete},
        {wizardPreparing, "failed: port in use", wizardBlocked},
        {wizardAwaitUser, "needs_runtime: restart app", wizardBlocked},
        {wizardComplete, "exited: process terminated", wizardBlocked},
    }
    for _, c := range cases {
        actual := wizardAfterStatus(c.current,c.status)
        if actual != c.expected { t.Errorf("phase=%s status=%q => %s, want %s",c.current,c.status,actual,c.expected) }
    }
}
func TestWizardAcknowledgementOnlyAtUserStep(t *testing.T) {
    for _, p := range []wizardPhase{wizardIdle,wizardPreparing,wizardVerifying,wizardBlocked,wizardComplete} {
        if wizardCanConfirm(p) { t.Errorf("wizard must not confirm from %s",p) }
    }
    if !wizardCanConfirm(wizardAwaitUser) { t.Fatal("user acknowledgement stage must enable Next") }
}
func TestWizardMessagesAreActionable(t *testing.T) {
    g := guideFor("vertex", "needs_auth: Google Express key missing", nil)
    _, text, action := wizardStageText(wizardAwaitUser,g,"needs_auth: Google Express key missing",nil,"previous 401")
    for _, want := range []string{"我已完成","Express","previous 401","401"} {
        if !strings.Contains(text+" "+action,want) { t.Errorf("guide is missing %q",want) }
    }
    label, text, _ := wizardStageText(wizardComplete,g,"ready:",[]string{"vertex/gemini-foo"},"")
    if !strings.Contains(label,"已验证")||!strings.Contains(text,"http://127.0.0.1:8765/v1")||!strings.Contains(text,"vertex/gemini-foo") { t.Fatal("missing complete-stage connection instructions") }
}
