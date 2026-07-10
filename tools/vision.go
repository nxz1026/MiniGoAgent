package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"MiniGoAgent/protocol"
)

type VisionInput struct {
	ImageURL string `json:"image_url" jsonschema:"required" jsonschema_description:"图片URL或base64 data URI"`
	Prompt   string `json:"prompt" jsonschema_description:"对图片的提问，留空则默认描述图片"`
}

func RunVision(ctx context.Context, input VisionInput) (string, error) {
	apiKey := os.Getenv("VISION_API_KEY")
	baseURL := os.Getenv("VISION_BASE_URL")
	modelID := os.Getenv("VISION_MODEL")
	if apiKey == "" || baseURL == "" {
		return "", fmt.Errorf("请设置 VISION_API_KEY 和 VISION_BASE_URL")
	}
	if modelID == "" {
		modelID = "gpt-4o"
	}

	prompt := input.Prompt
	if prompt == "" {
		prompt = "请详细描述这张图片的内容"
	}

	imgURL := input.ImageURL
	if !strings.HasPrefix(imgURL, "data:") {
		data, err := fetchImage(ctx, imgURL)
		if err != nil {
			return "", fmt.Errorf("下载图片失败: %w", err)
		}
		imgURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	}

	apiURL := strings.TrimRight(baseURL, "/") + "/chat/completions"

	reqBody := map[string]any{
		"model": modelID,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": prompt},
					{"type": "image_url", "image_url": map[string]string{"url": imgURL}},
				},
			},
		},
	}
	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := protocol.NewHTTPClient(60 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("API请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var apiErr struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &apiErr); err == nil && apiErr.Error != nil {
		return "", fmt.Errorf("API错误: %s", apiErr.Error.Message)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("API返回空结果")
	}
	return result.Choices[0].Message.Content, nil
}

func fetchImage(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid image URL: %w", err)
	}
	host := u.Hostname()
	if host != "" && protocol.IsPrivateHost(host) && !strings.EqualFold(os.Getenv("ALLOW_PRIVATE_IMAGE_URLS"), "true") {
		return nil, fmt.Errorf("image URL points to private/internal address (set ALLOW_PRIVATE_IMAGE_URLS=true to override)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	client := protocol.NewHTTPClient(30 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
