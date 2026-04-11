package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
					{"update_id": 42, "message": {"text": "/status", "chat": {"id": 12345}, "from": {"id": 777}}}
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
	if updates[0].ID != 42 || updates[0].Message.ChatID != "12345" || updates[0].Message.UserID != "777" || updates[0].Message.Text != "/status" {
		t.Fatalf("update = %+v", updates[0])
	}
}

func TestHTTPClientGetUpdatesReturnsPollingConflictError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/getUpdates" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		http.Error(w, "terminated by other getUpdates request", http.StatusConflict)
	}))
	defer server.Close()

	client := NewHTTPClient("token", server.URL)
	_, err := client.GetUpdates(context.Background(), 0, 15)
	if err == nil {
		t.Fatal("GetUpdates error = nil, want conflict error")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("GetUpdates error type = %T, want *APIError", err)
	}
	if apiErr.Method != "getUpdates" || apiErr.StatusCode != http.StatusConflict {
		t.Fatalf("apiErr = %+v", apiErr)
	}
	if !IsPollingConflict(err) {
		t.Fatalf("IsPollingConflict(%v) = false, want true", err)
	}
}

func TestHTTPClientRetryOnFloodControlSend(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/sendMessage" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		n := attempts.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":99}}`))
	}))
	defer server.Close()

	client := NewHTTPClient("token", server.URL)
	id, err := client.SendMessageReturningID(context.Background(), "123", "hello")
	if err != nil {
		t.Fatalf("SendMessageReturningID: %v", err)
	}
	if id != 99 {
		t.Fatalf("message_id = %d, want 99", id)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestHTTPClientRetryOnFloodControlEdit(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/editMessageText" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		n := attempts.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewHTTPClient("token", server.URL)
	err := client.EditMessage(context.Background(), "123", 42, "updated")
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestHTTPClientFloodControlMaxRetries(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bottoken/sendMessage" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1"}`))
	}))
	defer server.Close()

	client := NewHTTPClient("token", server.URL)
	_, err := client.SendMessageReturningID(context.Background(), "123", "hello")
	if err == nil {
		t.Fatal("expected error after max retries, got nil")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("error = %q, want 429 error", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}
