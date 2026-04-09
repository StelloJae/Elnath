package telegram

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPClientSendMessageAndGetUpdates(t *testing.T) {
	var gotSendBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bottoken/sendMessage":
			body, _ := io.ReadAll(r.Body)
			gotSendBody = string(body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/bottoken/getUpdates":
			if got := r.URL.Query().Get("offset"); got != "42" {
				t.Fatalf("offset = %q, want 42", got)
			}
			if got := r.URL.Query().Get("timeout"); got != "15" {
				t.Fatalf("timeout = %q, want 15", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"ok": true,
				"result": [
					{"update_id": 42, "message": {"text": "/status", "chat": {"id": 12345}}}
				]
			}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewHTTPClient("token", server.URL)
	if err := client.SendMessage(context.Background(), "12345", "hello world"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if !strings.Contains(gotSendBody, "chat_id=12345") || !strings.Contains(gotSendBody, "text=hello+world") {
		t.Fatalf("send body = %q", gotSendBody)
	}

	updates, err := client.GetUpdates(context.Background(), 42, 15)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("updates = %d, want 1", len(updates))
	}
	if updates[0].ID != 42 || updates[0].Message.ChatID != "12345" || updates[0].Message.Text != "/status" {
		t.Fatalf("update = %+v", updates[0])
	}
}
