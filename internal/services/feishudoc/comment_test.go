package feishudoc

import (
	"testing"

	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
)

func TestNormalizeCommentTokenType(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		err  bool
	}{
		{name: "default", in: "", want: "docx"},
		{name: "document alias", in: "document", want: "docx"},
		{name: "doc", in: "doc", want: "doc"},
		{name: "unsupported wiki", in: "wiki", err: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeCommentTokenType(tc.in)
			if tc.err {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("normalizeCommentTokenType() mismatch: got=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestExtractReplyContent(t *testing.T) {
	content := larkdrive.NewReplyContentBuilder().
		Elements([]*larkdrive.ReplyElement{
			larkdrive.NewReplyElementBuilder().
				Type("text_run").
				TextRun(larkdrive.NewTextRunBuilder().Text("hello ").Build()).
				Build(),
			larkdrive.NewReplyElementBuilder().
				Type("person").
				Person(larkdrive.NewPersonBuilder().UserId("ou_xxx").Build()).
				Build(),
			larkdrive.NewReplyElementBuilder().
				Type("text_run").
				TextRun(larkdrive.NewTextRunBuilder().Text(" 查看 ").Build()).
				Build(),
			larkdrive.NewReplyElementBuilder().
				Type("docs_link").
				DocsLink(larkdrive.NewDocsLinkBuilder().Url("https://feishu.cn/docx/abc").Build()).
				Build(),
		}).
		Build()

	got := extractReplyContent(content)
	want := "hello @ou_xxx 查看 https://feishu.cn/docx/abc"
	if got != want {
		t.Fatalf("extractReplyContent() mismatch: got=%q want=%q", got, want)
	}
}

func TestToComment(t *testing.T) {
	item := larkdrive.NewFileCommentBuilder().
		CommentId("cmt_1").
		Quote("quote text").
		IsSolved(true).
		CreateTime(1700000000).
		UpdateTime(1700000100).
		ReplyList(
			larkdrive.NewReplyListBuilder().
				Replies([]*larkdrive.FileCommentReply{
					larkdrive.NewFileCommentReplyBuilder().
						ReplyId("r_1").
						UserId("ou_xxx").
						CreateTime(1700000010).
						UpdateTime(1700000020).
						Content(
							larkdrive.NewReplyContentBuilder().
								Elements([]*larkdrive.ReplyElement{
									larkdrive.NewReplyElementBuilder().
										Type("text_run").
										TextRun(larkdrive.NewTextRunBuilder().Text("reply").Build()).
										Build(),
								}).
								Build(),
						).
						Build(),
				}).
				Build(),
		).
		Build()

	got := toComment(item)
	if got.CommentID != "cmt_1" {
		t.Fatalf("unexpected comment id: %q", got.CommentID)
	}
	if got.Quote != "quote text" {
		t.Fatalf("unexpected quote: %q", got.Quote)
	}
	if !got.IsSolved {
		t.Fatalf("expected solved=true")
	}
	if got.CreateTime != 1700000000 || got.UpdateTime != 1700000100 {
		t.Fatalf("unexpected time fields: create=%d update=%d", got.CreateTime, got.UpdateTime)
	}
	if len(got.Replies) != 1 {
		t.Fatalf("unexpected replies count: %d", len(got.Replies))
	}
	if got.Replies[0].ReplyID != "r_1" {
		t.Fatalf("unexpected reply id: %q", got.Replies[0].ReplyID)
	}
	if got.Replies[0].UserID != "ou_xxx" {
		t.Fatalf("unexpected reply user id: %q", got.Replies[0].UserID)
	}
	if got.Replies[0].Content != "reply" {
		t.Fatalf("unexpected reply content: %q", got.Replies[0].Content)
	}
}
