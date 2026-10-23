package readmes

import (
	"regexp"
	"strings"
)

// MaxContentRunes 限制缓存与下发的 README 长度，超出部分截断并标记。
const MaxContentRunes = 60000

var (
	dangerousBlocks = regexp.MustCompile(`(?is)<\s*(script|style|iframe|object|embed)\b[^>]*>.*?<\s*/\s*(script|style|iframe|object|embed)\s*>`)
	dangerousTags   = regexp.MustCompile(`(?is)<\s*/?\s*(script|style|iframe|object|embed)\b[^>]*>`)
	eventHandlers   = regexp.MustCompile(`(?is)\son[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	unsafeScheme    = regexp.MustCompile(`(?is)(\]\(\s*|\b(?:href|src)\s*=\s*["']?)\s*(javascript|data|vbscript)\s*:`)
)

// Sanitize 对不可信 README 做保守净化：删除可执行块与事件属性，
// 禁止 javascript/data/vbscript 链接，并限制长度。不做 Markdown 渲染。
func Sanitize(markdown string) (string, bool) {
	clean := dangerousBlocks.ReplaceAllString(markdown, "")
	clean = dangerousTags.ReplaceAllString(clean, "")
	clean = eventHandlers.ReplaceAllString(clean, "")
	clean = unsafeScheme.ReplaceAllString(clean, "${1}#blocked-")
	clean = strings.ReplaceAll(clean, "\x00", "")
	return truncateRunes(clean, MaxContentRunes)
}

func truncateRunes(value string, limit int) (string, bool) {
	if limit <= 0 {
		return value, false
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value, false
	}
	return string(runes[:limit]), true
}
