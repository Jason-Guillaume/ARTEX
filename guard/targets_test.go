package guard

import (
	"encoding/json"
	"testing"
)

func TestNetworkTargets(t *testing.T) {
	bash, _ := json.Marshal(map[string]string{"command": "curl -s https://A.Example.com/x && ping 203.0.113.5"})
	got := NetworkTargets("Bash", bash)
	want := map[string]bool{"a.example.com": true, "203.0.113.5": true}
	if len(got) != len(want) {
		t.Fatalf("hosts=%v", got)
	}
	for _, h := range got {
		if !want[h] {
			t.Errorf("unexpected host %q in %v", h, got)
		}
	}

	local, _ := json.Marshal(map[string]string{"command": "cat notes/example.com.txt && ls"})
	if hosts := NetworkTargets("Bash", local); len(hosts) != 0 {
		t.Fatalf("file path should not be a target, got %v", hosts)
	}

	fetch, _ := json.Marshal(map[string]string{"url": "https://api.example.com:8443/v1"})
	if hosts := NetworkTargets("WebFetch", fetch); len(hosts) != 1 || hosts[0] != "api.example.com" {
		t.Fatalf("webfetch hosts=%v", hosts)
	}

	read, _ := json.Marshal(map[string]string{"file_path": "https://evil.example/x"})
	if hosts := NetworkTargets("Read", read); hosts != nil {
		t.Fatalf("Read is not a network tool, got %v", hosts)
	}
}
