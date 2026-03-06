package channel

import (
	"context"
	"testing"
	"time"

	gemini "github.com/wanpengxie/go-gemini-sdk"
)

type fakeRecordedConnectGeminiClient struct {
	connectCtx   context.Context
	connectCount int
	turnCount    int
	messages     chan gemini.Message
	errs         chan error
}

func (f *fakeRecordedConnectGeminiClient) Connect(ctx context.Context) error {
	f.connectCtx = ctx
	f.connectCount++
	return nil
}

func (f *fakeRecordedConnectGeminiClient) Query(ctx context.Context, prompt string) (geminiTurn, error) {
	_ = ctx
	f.turnCount++
	f.messages = make(chan gemini.Message, 2)
	f.errs = make(chan error, 1)
	f.messages <- &gemini.AssistantMessage{
		SessionID: "gemini-sess-1",
		Content: []gemini.ContentBlock{
			&gemini.TextBlock{Text: "reply:" + prompt},
		},
	}
	f.messages <- &gemini.ResultMessage{
		SessionID:  "gemini-sess-1",
		StopReason: "completed",
	}
	close(f.messages)
	close(f.errs)
	return fakeGeminiTurn{messages: f.messages, errs: f.errs}, nil
}

func (f *fakeRecordedConnectGeminiClient) Interrupt(ctx context.Context) error {
	_ = ctx
	return nil
}

func (f *fakeRecordedConnectGeminiClient) Close() error { return nil }

func (f *fakeRecordedConnectGeminiClient) SessionID() string { return "gemini-sess-1" }

func (f *fakeRecordedConnectGeminiClient) Err() error { return nil }

type fakeGeminiTurn struct {
	messages <-chan gemini.Message
	errs     <-chan error
}

func (f fakeGeminiTurn) Messages() <-chan gemini.Message { return f.messages }

func (f fakeGeminiTurn) Errors() <-chan error { return f.errs }

func TestGeminiChatRunner_ConnectUsesTurnContextAndReusesClient(t *testing.T) {
	origFactory := createGeminiClient
	t.Cleanup(func() { createGeminiClient = origFactory })

	fakeClient := &fakeRecordedConnectGeminiClient{}
	createGeminiClient = func(opts ...gemini.Option) geminiClient {
		_ = opts
		return fakeClient
	}

	r := &geminiChatRunner{}

	firstCtx, firstCancel := context.WithCancel(context.Background())
	res1, err := r.RunTurn(firstCtx, ChatRunRequest{
		Prompt: "hello-1",
	}, nil)
	if err != nil {
		t.Fatalf("first turn failed: %v", err)
	}
	if res1.Text != "reply:hello-1" {
		t.Fatalf("first turn text mismatch: %q", res1.Text)
	}
	if fakeClient.connectCtx != firstCtx {
		t.Fatalf("connect should use turn context")
	}
	firstCancel()

	time.Sleep(10 * time.Millisecond)

	secondCtx, secondCancel := context.WithCancel(context.Background())
	defer secondCancel()
	res2, err := r.RunTurn(secondCtx, ChatRunRequest{
		Prompt: "hello-2",
	}, nil)
	if err != nil {
		t.Fatalf("second turn failed: %v", err)
	}
	if res2.Text != "reply:hello-2" {
		t.Fatalf("second turn text mismatch: %q", res2.Text)
	}
	if fakeClient.connectCount != 1 {
		t.Fatalf("client should be reused across turns, got connectCount=%d", fakeClient.connectCount)
	}
	if fakeClient.turnCount != 2 {
		t.Fatalf("expected 2 turns on reused client, got=%d", fakeClient.turnCount)
	}
}
