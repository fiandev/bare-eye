package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiandev/bare-eye/internal/config"
)

func TestReadOnly(t *testing.T) {
	cases := map[string]bool{
		"ls -la /var/log":                 true,
		"cat /etc/os-release":             true,
		"df -h":                           true,
		"systemctl status nginx":          true,
		"systemctl --no-pager status ssh": true,
		"ip -br addr":                     true,
		"ip link set eth0 down":           false,
		"apt list --installed":            true,
		"apt-get install -y nginx":        false,
		"docker ps":                       true,
		"docker rm -f web":                false,
		"journalctl --vacuum-size=1M":     false,
		"cat ~/.ssh/id_ed25519":           false,
		"cat /etc/shadow":                 false,
		"ls; rm -rf /":                    false,
		"echo hi > /etc/motd":             false,
		"hostname":                        true,
		"hostname new-name":               false,
		"sort -o out.txt in.txt":          false,
		"tail -f /var/log/syslog":         false,
		"sudo apt-get update":             false,
		"/tmp/ls":                         false,
		"":                                false,
	}
	for cmd, want := range cases {
		if got := ReadOnly(cmd); got != want {
			t.Errorf("ReadOnly(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestRunListsHostsAndFinishes(t *testing.T) {
	t.Setenv("BARE_EYE_API_KEY", "test-key")
	var requests []chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests = append(requests, req)
		w.Header().Set("Content-Type", "application/json")
		if len(requests) == 1 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Checking hosts.","tool_calls":[{"id":"call_1","type":"function","function":{"name":"list_hosts","arguments":"{}"}}]}}]}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"No hosts configured."}}]}`)
	}))
	defer srv.Close()

	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.AI.Enabled = true
	cfg.AI.BaseURL = srv.URL
	cfg.AI.Model = "test-model"
	cfg.AI.Timeout = config.Duration(10 * time.Second)

	r := New(cfg, nil, nil)
	if err := r.Ready(); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	var events []Event
	if err := r.Run(context.Background(), "set up nginx", "selected host = none", Callbacks{
		Emit: func(ev Event) { events = append(events, ev) },
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	if requests[0].Model != "test-model" {
		t.Errorf("model = %q", requests[0].Model)
	}
	if len(requests[0].Tools) != 2 {
		t.Errorf("tools = %d", len(requests[0].Tools))
	}
	if len(requests[1].Messages) < 4 {
		t.Fatalf("messages = %d", len(requests[1].Messages))
	}
	last := requests[1].Messages[len(requests[1].Messages)-1]
	if last.Role != "tool" || strings.TrimSpace(last.Content) != "[]" {
		t.Errorf("tool result = %+v", last)
	}
	if events[len(events)-1].Kind != EventAssistant || !strings.Contains(events[len(events)-1].Text, "No hosts") {
		t.Errorf("events = %+v", events)
	}
}

func TestRunDeclinedMutationIsReportedToModel(t *testing.T) {
	t.Setenv("BARE_EYE_API_KEY", "test-key")
	var requests []chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		requests = append(requests, req)
		w.Header().Set("Content-Type", "application/json")
		if len(requests) == 1 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"run_command","arguments":"{\"host\":\"web-01\",\"command\":\"apt-get install -y nginx\",\"purpose\":\"install nginx\"}"}}]}}]}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Okay, I did not change anything."}}]}`)
	}))
	defer srv.Close()

	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.AI.Enabled = true
	cfg.AI.BaseURL = srv.URL
	cfg.AI.Model = "test-model"
	cfg.Servers = append(cfg.Servers, config.ServerConfig{
		ID: "web-01", Name: "Web", Host: "192.0.2.10", Port: 22, User: "root",
		Auth: config.AuthConfig{Method: config.AuthAgent}, Enabled: true,
	})

	r := New(cfg, nil, nil)
	approvals := 0
	err = r.Run(context.Background(), "install nginx", "", Callbacks{
		Approve: func(_ context.Context, req Approval) bool {
			approvals++
			if req.Host != "Web" || !strings.Contains(req.Command, "apt-get") {
				t.Errorf("approval = %+v", req)
			}
			return false
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if approvals != 1 {
		t.Fatalf("approvals = %d", approvals)
	}
	last := requests[1].Messages[len(requests[1].Messages)-1]
	if last.Role != "tool" || !strings.Contains(last.Content, `"declined":true`) {
		t.Errorf("tool result = %+v", last)
	}
}

func TestRunRequiresKey(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.AI.Enabled = true
	t.Setenv("BARE_EYE_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	r := New(cfg, nil, nil)
	if err := r.Ready(); err != ErrNoAPIKey {
		t.Fatalf("Ready = %v, want ErrNoAPIKey", err)
	}
}

func TestRunnerUsesStoredKey(t *testing.T) {
	t.Setenv("BARE_EYE_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.AI.Enabled = true
	secrets := &config.Secrets{Passwords: map[string]string{config.SecretAIAPIKey: "stored-key"}}
	r := New(cfg, secrets, nil)
	if err := r.Ready(); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if r.client == nil || r.client.APIKey != "stored-key" {
		t.Fatalf("client = %+v", r.client)
	}
}
