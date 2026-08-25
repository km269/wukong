package sanitize

import (
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
)

type MarkdownOptions struct {
	StripComments    bool
	StripDataURLs    bool
	KeepFormatting   bool
	HeadingLevel     int
	NoFollowLinks    bool
	NoImages         bool
	SkipTables       bool
	SkipCodeBlocks   bool
	PreserveCodeLang bool
}

func DefaultMarkdownOptions() MarkdownOptions {
	return MarkdownOptions{
		StripComments:    true,
		StripDataURLs:    true,
		KeepFormatting:   true,
		HeadingLevel:     0,
		NoFollowLinks:    false,
		NoImages:         false,
		SkipTables:       false,
		SkipCodeBlocks:   false,
		PreserveCodeLang: true,
	}
}

func HTMLToMarkdown(htmlContent string) (string, error) {
	opts := DefaultMarkdownOptions()
	return HTMLToMarkdownWithOptions(htmlContent, opts)
}

func HTMLToMarkdownWithOptions(htmlContent string, opts MarkdownOptions) (string, error) {
	conv := converter.NewConverter()

	conv.Register.Plugin(base.NewBasePlugin())
	conv.Register.Plugin(commonmark.NewCommonmarkPlugin())

	if opts.SkipTables {
		conv.Register.TagType("table", converter.TagTypeRemove, converter.PriorityStandard)
		conv.Register.TagType("thead", converter.TagTypeRemove, converter.PriorityStandard)
		conv.Register.TagType("tbody", converter.TagTypeRemove, converter.PriorityStandard)
		conv.Register.TagType("tr", converter.TagTypeRemove, converter.PriorityStandard)
		conv.Register.TagType("td", converter.TagTypeRemove, converter.PriorityStandard)
		conv.Register.TagType("th", converter.TagTypeRemove, converter.PriorityStandard)
	}

	if opts.SkipCodeBlocks {
		conv.Register.TagType("pre", converter.TagTypeRemove, converter.PriorityStandard)
		conv.Register.TagType("code", converter.TagTypeRemove, converter.PriorityStandard)
	}

	if opts.NoImages {
		conv.Register.TagType("img", converter.TagTypeRemove, converter.PriorityStandard)
		conv.Register.TagType("picture", converter.TagTypeRemove, converter.PriorityStandard)
		conv.Register.TagType("figure", converter.TagTypeRemove, converter.PriorityStandard)
	}

	if opts.HeadingLevel > 0 {
		for i := 1; i <= 6; i++ {
			if i > opts.HeadingLevel {
				tagName := "h" + string(rune('0'+i))
				conv.Register.TagType(tagName, converter.TagTypeBlock, converter.PriorityStandard)
			}
		}
	}

	markdown, err := conv.ConvertString(htmlContent)
	if err != nil {
		return "", err
	}

	markdown = strings.TrimSpace(markdown)

	return markdown, nil
}

func ExtractMainContentMarkdown(htmlContent string) (string, error) {
	cleanHTML, _ := CleanHTMLWithOptions(htmlContent, CleanOptions{
		KeepNoscript:    false,
		KeepMetaRefresh: false,
		MobileReadable:  false,
	})

	markdown, err := HTMLToMarkdown(cleanHTML)
	if err != nil {
		return "", err
	}

	markdown = removeEmptyLines(markdown)
	markdown = collapseMultipleBlankLines(markdown)

	return markdown, nil
}

func removeEmptyLines(s string) string {
	lines := strings.Split(s, "\n")
	var result []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

func collapseMultipleBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

func HTMLToMarkdownWithDomain(htmlContent, baseURL string) (string, error) {
	conv := converter.NewConverter()

	conv.Register.Plugin(base.NewBasePlugin())
	conv.Register.Plugin(commonmark.NewCommonmarkPlugin())

	markdown, err := conv.ConvertString(htmlContent)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(markdown), nil
}

func CleanAndConvertToMarkdown(htmlContent string, opts MarkdownOptions) (string, CleanReport, error) {
	cleanHTML, report := CleanHTMLWithOptions(htmlContent, CleanOptions{
		KeepNoscript:    false,
		KeepMetaRefresh: false,
		MobileReadable:  false,
	})

	markdown, err := HTMLToMarkdownWithOptions(cleanHTML, opts)
	if err != nil {
		return "", report, err
	}

	markdown = removeEmptyLines(markdown)
	markdown = collapseMultipleBlankLines(markdown)

	return markdown, report, nil
}
