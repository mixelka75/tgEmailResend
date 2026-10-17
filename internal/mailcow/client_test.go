package mailcow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(Config{BaseURL: srv.URL + "/", APIKey: "key", Domain: "Example.com"})
}

func TestCreateMailboxRequestAndSuccess(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/add/mailbox" || r.Header.Get("X-API-Key") != "key" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		// Response format from openapi.yaml
		w.Write([]byte(`[{"type":"success","log":["mailbox","add","mailbox",{},null],"msg":["mailbox_added","info@example.com"]}]`))
	})

	mb, err := c.CreateMailbox(context.Background(), "info", "Info", "")
	if err != nil {
		t.Fatal(err)
	}
	if mb.Email() != "info@example.com" {
		t.Errorf("email = %s", mb.Email())
	}
	if got["password"] != mb.Password || got["password2"] != mb.Password || got["imap_access"] != "1" {
		t.Errorf("unexpected request body: %v", got)
	}
	// Server defaults must apply: no forced TLS for inbound mail, domain quota
	for _, key := range []string{"tls_enforce_in", "tls_enforce_out", "quota"} {
		if _, ok := got[key]; ok {
			t.Errorf("request must not set %s", key)
		}
	}
}

func TestAPIErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"exists", 200, `[{"type":"danger","log":[],"msg":["object_exists","info@example.com"]}]`, "ящик info@example.com уже существует"},
		{"policy", 200, `[{"type":"danger","msg":"password_complexity"}]`, "политике сложности"},
		{"second entry fails", 200, `[{"type":"success","msg":"ok"},{"type":"danger","msg":["max_mailbox_exceeded",10,10]}]`, "лимит ящиков домена (10 из 10)"},
		{"bad key", 401, `{"type":"error","msg":"authentication failed"}`, "MAILCOW_API_KEY"},
		{"ip", 401, `{"type":"error","msg":"api access denied for ip 1.2.3.4"}`, "IP бота (1.2.3.4)"},
		{"read only", 200, `{"type":"error","msg":"API read/write access denied"}`, "только права на чтение"},
		{"html page", 200, `<html><body>Login</body></html>`, "проверьте MAILCOW_URL"},
		{"server error", 502, `Bad Gateway`, "ответил 502"},
		{"unknown key", 200, `[{"type":"danger","msg":["something_new","x"]}]`, "something_new: x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			})
			_, err := c.CreateMailbox(context.Background(), "info", "Info", "Secret1!")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/get/domain/example.com" {
			w.Write([]byte(`{}`))
			return
		}
		w.Write([]byte(`{"active":"1","domain_name":"example.com","max_quota_for_mbox":10737418240}`))
	})
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}

	missing := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	if err := missing.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "не найден") {
		t.Errorf("missing domain: err = %v", err)
	}
}

func TestGenerateSecurePasswordPolicy(t *testing.T) {
	for i := 0; i < 500; i++ {
		p, err := GenerateSecurePassword(20)
		if err != nil {
			t.Fatal(err)
		}
		// Mailcow password_check(): length, letters, digits, special, lower+upper
		for _, re := range []string{`[a-z]`, `[A-Z]`, `\d`, `[^a-zA-Z\d]`} {
			if !regexp.MustCompile(re).MatchString(p) || len(p) != 20 {
				t.Fatalf("password %q violates %s", p, re)
			}
		}
	}
}

func TestValidateLocalPartAndIMAPServer(t *testing.T) {
	for _, ok := range []string{"info", "john.doe", "a", "x_y-z+tag"} {
		if err := ValidateLocalPart(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Иван", "a b", ".a", "a.", "a..b", "a@b", strings.Repeat("a", 65)} {
		if ValidateLocalPart(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}

	if got := NewClient(Config{BaseURL: "https://mail.example.com:8443/path/"}).GetIMAPServer(); got != "mail.example.com:993" {
		t.Errorf("GetIMAPServer = %s", got)
	}
	if got := NewClient(Config{BaseURL: "https://x", IMAPServer: "imap.example.com"}).GetIMAPServer(); got != "imap.example.com:993" {
		t.Errorf("GetIMAPServer override = %s", got)
	}
}
