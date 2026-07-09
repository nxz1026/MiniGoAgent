package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"MiniGoAgent/tools/filter"
)

type TerminalInput struct {
	Command string `json:"command" jsonschema:"required" jsonschema_description:"要执行的 shell 命令"`
}

func RunTerminal(ctx context.Context, input TerminalInput) (string, error) {
	if isCacheable(input.Command) {
		if cached, ok := lookupCache(input.Command); ok {
			return cached, nil
		}
	}

	out, err := runCmd(ctx, "cmd", "/c", input.Command)
	if err == nil {
		filtered := filter.Run(out, input.Command)
		if isCacheable(input.Command) {
			storeCache(input.Command, filtered)
		}
		return filtered, nil
	}

	if isAccessDenied(err) {
		if !isAdmin() {
			return "", fmt.Errorf("命令需要管理员权限，请以管理员身份运行本程序后重试。当前无管理员权限。")
		}
		elevatedOut, elevErr := runElevated(ctx, input.Command)
		if elevErr == nil {
			return filter.Run(elevatedOut, input.Command), nil
		}
		return fmt.Sprintf("提权执行失败: %v\n输出: %s", elevErr, elevatedOut), nil
	}

	return fmt.Sprintf("执行失败: %v\n输出: %s", err, string(out)), nil
}

func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func isAccessDenied(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "access is denied") ||
		strings.Contains(msg, "拒绝访问") ||
		strings.Contains(msg, "permission denied")
}

func isAdmin() bool {
	_, err := os.Open("\\\\.\\PHYSICALDRIVE0")
	return err == nil
}

func runElevated(ctx context.Context, command string) (string, error) {
	tmpFile := filepath.Join(os.TempDir(), "elevated_out.txt")
	psCmd := fmt.Sprintf(
		`Start-Process cmd -Verb RunAs -ArgumentList '/c,%s > "%s" 2>&1' -Wait -WindowStyle Hidden`,
		strings.ReplaceAll(command, `"`, `\"`),
		tmpFile,
	)
	_, err := runCmd(ctx, "powershell", "-NoProfile", "-Command", psCmd)
	if err != nil {
		os.Remove(tmpFile)
		return "", err
	}
	out, err := os.ReadFile(tmpFile)
	os.Remove(tmpFile)
	if err != nil {
		return "", fmt.Errorf("提权成功但读取输出失败: %w", err)
	}
	return string(out), nil
}
