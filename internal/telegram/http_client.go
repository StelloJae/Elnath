package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type APIError struct {
	Method     string
	StatusCode int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: status %d", e.Method, e.StatusCode)
}

func IsPollingConflict(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Method == "getUpdates" && apiErr.StatusCode == http.StatusConflict
}

type HTTPClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewHTTPClient(token, baseURL string) *HTTPClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	return &HTTPClient{
		baseURL: baseURL,
		token:   strings.TrimSpace(token),
		client:  &http.Client{},
	}
}

func (c *HTTPClient) SendMessage(ctx context.Context, chatID, text string) error {
	_, err := c.SendMessageReturningID(ctx, chatID, text)
	return err
}

func (c *HTTPClient) SendMessageReturningID(ctx context.Context, chatID, text string) (int64, error) {
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", text)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("sendMessage"), strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, &APIError{Method: "sendMessage", StatusCode: resp.StatusCode}
	}
	var decoded struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return 0, nil
	}
	return decoded.Result.MessageID, nil
}

func (c *HTTPClient) EditMessage(ctx context.Context, chatID string, messageID int64, text string) error {
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("message_id", strconv.FormatInt(messageID, 10))
	form.Set("text", text)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("editMessageText"), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{Method: "editMessageText", StatusCode: resp.StatusCode}
	}
	return nil
}

func (c *HTTPClient) GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]Update, error) {
	query := url.Values{}
	if offset > 0 {
		query.Set("offset", strconv.FormatInt(offset, 10))
	}
	if timeoutSeconds > 0 {
		query.Set("timeout", strconv.Itoa(timeoutSeconds))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("getUpdates")+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Method: "getUpdates", StatusCode: resp.StatusCode}
	}

	var decoded struct {
		OK     bool `json:"ok"`
		Result []struct {
			UpdateID int64 `json:"update_id"`
			Message  struct {
				Text string `json:"text"`
				Chat struct {
					ID int64 `json:"id"`
				} `json:"chat"`
			} `json:"message"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	updates := make([]Update, 0, len(decoded.Result))
	for _, item := range decoded.Result {
		updates = append(updates, Update{
			ID: item.UpdateID,
			Message: Message{
				ChatID: strconv.FormatInt(item.Message.Chat.ID, 10),
				Text:   item.Message.Text,
			},
		})
	}
	return updates, nil
}

func (c *HTTPClient) endpoint(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", c.baseURL, c.token, method)
}
