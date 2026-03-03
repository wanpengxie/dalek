package feishudoc

import (
	"strings"
	"testing"

	larkdocx "github.com/larksuite/oapi-sdk-go/v3/service/docx/v1"
)

func TestDocMarkdownRenderer_RenderMainBlocks(t *testing.T) {
	blocks := []*larkdocx.Block{
		{
			BlockId:   strPtr("doc-1"),
			BlockType: intPtr(blockTypePage),
			Children:  []string{"h1", "p1", "list-1", "table-1", "image-1", "todo-1", "quote-1", "code-1", "divider-1"},
		},
		{
			BlockId:   strPtr("h1"),
			ParentId:  strPtr("doc-1"),
			BlockType: intPtr(blockTypeHeading1),
			Heading1:  textOf("标题"),
		},
		{
			BlockId:   strPtr("p1"),
			ParentId:  strPtr("doc-1"),
			BlockType: intPtr(blockTypeText),
			Text: &larkdocx.Text{Elements: []*larkdocx.TextElement{
				{TextRun: textRun("hello ", nil)},
				{TextRun: textRun("world", &larkdocx.TextElementStyle{Bold: boolPtr(true)})},
			}},
		},
		{
			BlockId:   strPtr("list-1"),
			ParentId:  strPtr("doc-1"),
			BlockType: intPtr(blockTypeBullet),
			Bullet:    textOf("列表父项"),
			Children:  []string{"list-2"},
		},
		{
			BlockId:   strPtr("list-2"),
			ParentId:  strPtr("list-1"),
			BlockType: intPtr(blockTypeOrdered),
			Ordered: &larkdocx.Text{
				Style:    &larkdocx.TextStyle{Sequence: strPtr("2")},
				Elements: []*larkdocx.TextElement{{TextRun: textRun("子项", nil)}},
			},
		},
		{
			BlockId:   strPtr("table-1"),
			ParentId:  strPtr("doc-1"),
			BlockType: intPtr(blockTypeTable),
			Table: &larkdocx.Table{
				Property: &larkdocx.TableProperty{RowSize: intPtr(2), ColumnSize: intPtr(2)},
				Cells:    []string{"cell-11", "cell-12", "cell-21", "cell-22"},
			},
		},
		{
			BlockId:   strPtr("cell-11"),
			ParentId:  strPtr("table-1"),
			BlockType: intPtr(blockTypeTableCell),
			Children:  []string{"cell-11-t"},
		},
		{
			BlockId:   strPtr("cell-11-t"),
			ParentId:  strPtr("cell-11"),
			BlockType: intPtr(blockTypeText),
			Text:      textOf("A"),
		},
		{
			BlockId:   strPtr("cell-12"),
			ParentId:  strPtr("table-1"),
			BlockType: intPtr(blockTypeTableCell),
			Children:  []string{"cell-12-t"},
		},
		{
			BlockId:   strPtr("cell-12-t"),
			ParentId:  strPtr("cell-12"),
			BlockType: intPtr(blockTypeText),
			Text:      textOf("B"),
		},
		{
			BlockId:   strPtr("cell-21"),
			ParentId:  strPtr("table-1"),
			BlockType: intPtr(blockTypeTableCell),
			Children:  []string{"cell-21-t"},
		},
		{
			BlockId:   strPtr("cell-21-t"),
			ParentId:  strPtr("cell-21"),
			BlockType: intPtr(blockTypeText),
			Text:      textOf("C"),
		},
		{
			BlockId:   strPtr("cell-22"),
			ParentId:  strPtr("table-1"),
			BlockType: intPtr(blockTypeTableCell),
			Children:  []string{"cell-22-t"},
		},
		{
			BlockId:   strPtr("cell-22-t"),
			ParentId:  strPtr("cell-22"),
			BlockType: intPtr(blockTypeText),
			Text:      textOf("D"),
		},
		{
			BlockId:   strPtr("image-1"),
			ParentId:  strPtr("doc-1"),
			BlockType: intPtr(blockTypeImage),
			Image: &larkdocx.Image{
				Token:   strPtr("img_token"),
				Caption: &larkdocx.Caption{Content: strPtr("封面")},
			},
		},
		{
			BlockId:   strPtr("todo-1"),
			ParentId:  strPtr("doc-1"),
			BlockType: intPtr(blockTypeTodo),
			Todo: &larkdocx.Text{
				Style:    &larkdocx.TextStyle{Done: boolPtr(true)},
				Elements: []*larkdocx.TextElement{{TextRun: textRun("完成事项", nil)}},
			},
		},
		{
			BlockId:   strPtr("quote-1"),
			ParentId:  strPtr("doc-1"),
			BlockType: intPtr(blockTypeQuote),
			Quote:     textOf("引用段落"),
		},
		{
			BlockId:   strPtr("code-1"),
			ParentId:  strPtr("doc-1"),
			BlockType: intPtr(blockTypeCode),
			Code:      textOf("fmt.Println(\"hi\")"),
		},
		{
			BlockId:   strPtr("divider-1"),
			ParentId:  strPtr("doc-1"),
			BlockType: intPtr(blockTypeDivider),
		},
	}

	roots, nodeByID := buildDocumentBlockTree("doc-1", blocks)
	renderer := newDocMarkdownRenderer(nodeByID, map[string]string{"img_token": "./images/img_token.png"})
	markdown := renderer.render(roots)

	checks := []string{
		"# 标题",
		"hello **world**",
		"- 列表父项",
		"  2. 子项",
		"| A | B |",
		"| C | D |",
		"![封面](./images/img_token.png)",
		"- [x] 完成事项",
		"> 引用段落",
		"```",
		"fmt.Println(\"hi\")",
		"---",
	}
	for _, want := range checks {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown missing %q\nactual:\n%s", want, markdown)
		}
	}
}

func TestDocMarkdownRenderer_LinkStyle(t *testing.T) {
	renderer := newDocMarkdownRenderer(nil, nil)
	text := &larkdocx.Text{Elements: []*larkdocx.TextElement{
		{TextRun: textRun("OpenAI", &larkdocx.TextElementStyle{Bold: boolPtr(true), Link: &larkdocx.Link{Url: strPtr("https://openai.com")}})},
	}}

	got := renderer.renderRichText(text)
	want := "[**OpenAI**](https://openai.com)"
	if got != want {
		t.Fatalf("renderRichText() mismatch: got=%q want=%q", got, want)
	}
}

func TestJoinMarkdownPath(t *testing.T) {
	if got := joinMarkdownPath("./images", "a.png"); got != "./images/a.png" {
		t.Fatalf("joinMarkdownPath() mismatch: got=%q", got)
	}
	if got := joinMarkdownPath("images/", "a.png"); got != "images/a.png" {
		t.Fatalf("joinMarkdownPath() normalize mismatch: got=%q", got)
	}
}

func textOf(value string) *larkdocx.Text {
	return &larkdocx.Text{Elements: []*larkdocx.TextElement{{TextRun: textRun(value, nil)}}}
}

func textRun(content string, style *larkdocx.TextElementStyle) *larkdocx.TextRun {
	return &larkdocx.TextRun{Content: strPtr(content), TextElementStyle: style}
}

func strPtr(value string) *string {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}
