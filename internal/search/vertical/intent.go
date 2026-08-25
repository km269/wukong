package vertical

import (
	"regexp"
	"strings"
)

// IntentDetector classifies a query into a search vertical
// using keyword and pattern matching. It is deliberately
// rule-based (no ML) for determinism and zero latency.
type IntentDetector struct {
	academic   []*regexp.Regexp
	code       []*regexp.Regexp
	encyclo    []*regexp.Regexp
	discussion []*regexp.Regexp
}

// NewIntentDetector builds a detector with default patterns.
func NewIntentDetector() *IntentDetector {
	return &IntentDetector{
		academic: compileAll(
			`\bpaper\b`, `\barxiv\b`, `\bresearch\b`, `\babstract\b`,
			`\bstudy\b`, `\bstudies\b`,
			`\bLLM\b.*\bpaper\b`, `\btransformer\b.*\bmodel\b`,
			// CJK patterns (no \b — word boundaries don't apply).
			`论文`, `論文`, `研究`, `学术`, `摘要`,
		),
		code: compileAll(
			`\bcode\b`, `\bcodes?\b`, `\bgithub\b`, `\brepo(?:sitory)?\b`,
			`\bimplementation\b`, `\bexample\s+code\b`, `\bsnippet\b`,
			`\bfunction\b`, `\bclass\b`, `\bapi\b.*\bendpoint\b`,
			// CJK patterns.
			`代码`, `源码`, `示例`, `实现`,
		),
		encyclo: compileAll(
			`^\s*what\s+(?:is|are)\b`, `^\s*who\s+(?:is|are|was)\b`,
			`^\s*define\b`, `^\s*definition\s+of\b`,
			`^\s*explain\b`, `\bwikipedia\b`,
			// CJK patterns.
			`什么是`, `是什么`, `是谁`, `解释`, `百科`,
		),
		discussion: compileAll(
			`\breddit\b`, `\bhacker\s*news\b`, `\b\bhn\b`,
			`\bdiscussion\b`, `\bopinion\b`, `\bthread\b`,
			`\bcommunity\b`, `\bforum\b`,
			// CJK patterns.
			`评论`, `讨论`, `看法`, `观点`,
		),
	}
}

func compileAll(patterns ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile("(?i)" + p)
		if err == nil {
			out = append(out, re)
		}
	}
	return out
}

// Detect returns the best-matching intent for the query.
// Precedence: academic > code > encyclopedia > discussion > general.
// Returns IntentGeneral when no pattern matches.
func (d *IntentDetector) Detect(query string) Intent {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return IntentGeneral
	}

	// Score each vertical by match count; ties broken by precedence.
	scores := map[Intent]int{
		IntentAcademic:     d.countMatches(q, d.academic),
		IntentCode:         d.countMatches(q, d.code),
		IntentEncyclopedia: d.countMatches(q, d.encyclo),
		IntentDiscussion:   d.countMatches(q, d.discussion),
	}

	// Precedence order when scores tie.
	precedence := []Intent{
		IntentAcademic, IntentCode, IntentEncyclopedia, IntentDiscussion,
	}
	best := IntentGeneral
	bestScore := 0
	for _, intent := range precedence {
		if scores[intent] > bestScore {
			bestScore = scores[intent]
			best = intent
		}
	}
	return best
}

func (d *IntentDetector) countMatches(
	q string, patterns []*regexp.Regexp,
) int {
	count := 0
	for _, re := range patterns {
		if re.MatchString(q) {
			count++
		}
	}
	return count
}
