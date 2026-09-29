package startup

import (
	"fmt"
	"os"
	"strings"
)

// LoadPromptFile reads a file as the initial writing requirements.
func LoadPromptFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取 prompt 失败: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// PrepareQuick assembles the quick-start prompts.
func PrepareQuick(rawPrompt string) (string, error) {
	prompt := strings.TrimSpace(rawPrompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	return prompt, nil
}
