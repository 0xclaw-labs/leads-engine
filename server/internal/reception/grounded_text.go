package reception

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxModelReplyRunes caps a model draft that may replace a grounded FAQ answer.
const MaxModelReplyRunes = 400

// promiseWords are commitments a visitor could rely on. A model draft may
// carry one only when the grounded FAQ answer already states it.
var promiseWords = []string{"保证", "包退", "退款", "免费", "承诺", "最低价", "绝对", "一定"}

var numberRun = regexp.MustCompile(`[0-9]+(?:[:.][0-9]+)*`)

// GroundedReplyText decides whether a model draft may replace the grounded
// FAQ answer. It returns the trimmed draft and true only when every number
// run appears verbatim in the FAQ answer, no promise word appears unless the
// FAQ states it, and the draft has no placeholder and fits the length cap.
// Anything else returns "", false and the caller keeps the FAQ answer.
func GroundedReplyText(candidate, faqAnswer string) (string, bool) {
	text := strings.TrimSpace(candidate)
	if text == "" || utf8.RuneCountInString(text) > MaxModelReplyRunes {
		return "", false
	}
	if strings.Contains(text, "{{") || strings.Contains(text, "}}") || strings.Contains(text, "模板占位") {
		return "", false
	}
	for _, run := range numberRun.FindAllString(text, -1) {
		if !strings.Contains(faqAnswer, run) {
			return "", false
		}
	}
	for _, word := range promiseWords {
		if strings.Contains(text, word) && !strings.Contains(faqAnswer, word) {
			return "", false
		}
	}
	return text, true
}
