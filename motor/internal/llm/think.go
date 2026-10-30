package llm

import (
	"regexp"
	"strings"
)

var (
	thinkBlock  = regexp.MustCompile(`(?is)<think(?:ing)?>.*?</think(?:ing)?>`)
	thinkOpen   = regexp.MustCompile(`(?is)<think(?:ing)?>.*$`)
	thinkStray  = regexp.MustCompile(`(?is)^.*?</think(?:ing)?>`)
	thinkTagAny = regexp.MustCompile(`(?i)</?think(?:ing)?>`)
)

// StripThinking removes the reasoning some models (DeepSeek-R1, Qwen, etc.)
// emit inside <think>...</think>. It handles complete blocks, an unclosed
// block (everything after the opening tag is reasoning) and a stray closing
// tag (everything before it is reasoning), so it never reaches a customer.
func StripThinking(s string) string {
	s = thinkBlock.ReplaceAllString(s, "")
	if thinkStray.MatchString(s) && !thinkOpen.MatchString(s) {
		s = thinkStray.ReplaceAllString(s, "")
	}
	s = thinkOpen.ReplaceAllString(s, "")
	s = thinkTagAny.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}
