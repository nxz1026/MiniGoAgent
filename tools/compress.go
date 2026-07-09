package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type CompressInput struct {
	Content      string `json:"content" jsonschema:"required" jsonschema_description:"需要压缩的长文本内容"`
	Instruction  string `json:"instruction" jsonschema_description:"自定义压缩要求，留空则默认保留关键信息"`
}

func RunCompress(ctx context.Context, input CompressInput) (string, error) {
	apiKey := os.Getenv("COMPRESS_API_KEY")
	baseURL := os.Getenv("COMPRESS_BASE_URL")
	modelID := os.Getenv("COMPRESS_MODEL")
	if apiKey == "" || baseURL == "" {
		return "", fmt.Errorf("请设置 COMPRESS_API_KEY 和 COMPRESS_BASE_URL")
	}
	if modelID == "" {
		modelID = "gpt-4o-mini"
	}

	prompt := input.Instruction
	if prompt == "" {
		prompt = "请压缩以下内容，保留所有关键信息，去除冗余，语言保持不变"
	}
	prompt += "\n\n" + input.Content

	apiURL := strings.TrimRight(baseURL, "/") + "/chat/completions"

	reqBody := map[string]any{
		"model": modelID,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	}
	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 60 * time.Second}
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
