#!/bin/sh
#
# Generate AI-summarized release notes from git commits.
# Usage: .github/scripts/gen-changelog.sh [previous_tag]
#
# Requires GEMINI_API_KEY (preferred), ANTHROPIC_API_KEY, or OPENAI_API_KEY.
# Falls back to raw commit list if no API key is set.
#
set -e

PREV_TAG="${1:-$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null || echo "")}"
CURR_TAG="$(git describe --tags --abbrev=0 HEAD 2>/dev/null || echo "HEAD")"

if [ -n "$PREV_TAG" ]; then
    COMMITS=$(git log "${PREV_TAG}..${CURR_TAG}" --pretty=format:"- %s" --no-merges)
    RANGE="${PREV_TAG}..${CURR_TAG}"
else
    COMMITS=$(git log --pretty=format:"- %s" --no-merges -50)
    RANGE="last 50 commits"
fi

if [ -z "$COMMITS" ]; then
    echo "No commits found in range ${RANGE}"
    exit 0
fi

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

cat > "$TMPDIR/prompt.txt" <<PROMPT_EOF
你是 Go 命令行工具 ainovel-cli（一款 AI 小说写作引擎）的发布说明撰写者。
请根据下面的 Git 提交记录，生成简洁、清晰、面向用户的中文 Markdown 发布说明。

规则：
- 使用中文输出
- 按以下分组组织内容：新功能、问题修复、性能优化、重构、其他；没有内容的分组不要输出
- 每条内容一行，保持简洁，不要包含 commit hash 或作者名
- 移除 conventional commit 前缀，例如 feat:、fix:、perf:、refactor: 等
- 合并相近或重复的提交，避免逐条机械复述 commit
- 使用面向用户的表达，突出实际变化和影响
- 重点关注用户可感知的变化，例如发布流程、二进制打包、CLI/TUI 行为、写作流程、模型支持和文档
- 只输出 Markdown 内容，不要输出开场白、解释或总结

提交记录（${RANGE}）：
${COMMITS}
PROMPT_EOF

# Build JSON body with jq (reads from file to handle special chars).
build_body() { jq -Rs "$1" < "$TMPDIR/prompt.txt" > "$TMPDIR/body.json"; }

# Extract text from JSON response (python3 handles control chars reliably).
extract() { python3 -c "import json,sys; d=json.load(open('$TMPDIR/result.json')); print($1)"; }

fallback() {
    echo "## What's Changed"
    echo ""
    echo "$COMMITS"
}

# Try Gemini first, then Anthropic, then OpenAI.
if [ -n "$GEMINI_API_KEY" ]; then
    API_URL="${GEMINI_BASE_URL:-https://generativelanguage.googleapis.com}/v1beta/models/gemini-2.5-flash:generateContent?key=${GEMINI_API_KEY}"
    build_body '{contents: [{parts: [{text: .}]}]}'
    if curl -fsSL "$API_URL" -H "content-type: application/json" -d @"$TMPDIR/body.json" -o "$TMPDIR/result.json"; then
        extract "d['candidates'][0]['content']['parts'][0]['text']"
    else
        fallback
    fi

elif [ -n "$ANTHROPIC_API_KEY" ]; then
    API_URL="${ANTHROPIC_BASE_URL:-https://api.anthropic.com}/v1/messages"
    build_body '{model: "claude-sonnet-4-5-20250514", max_tokens: 1024, messages: [{role: "user", content: .}]}'
    if curl -fsSL "$API_URL" -H "x-api-key: ${ANTHROPIC_API_KEY}" -H "anthropic-version: 2023-06-01" -H "content-type: application/json" -d @"$TMPDIR/body.json" -o "$TMPDIR/result.json"; then
        extract "d['content'][0]['text']"
    else
        fallback
    fi

elif [ -n "$OPENAI_API_KEY" ]; then
    API_URL="${OPENAI_BASE_URL:-https://api.openai.com}/v1/chat/completions"
    build_body '{model: "gpt-4o-mini", messages: [{role: "user", content: .}]}'
    if curl -fsSL "$API_URL" -H "Authorization: Bearer ${OPENAI_API_KEY}" -H "content-type: application/json" -d @"$TMPDIR/body.json" -o "$TMPDIR/result.json"; then
        extract "d['choices'][0]['message']['content']"
    else
        fallback
    fi

else
    fallback
fi
