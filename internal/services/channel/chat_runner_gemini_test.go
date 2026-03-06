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
	blocks       chan gemini.StreamBlock
	errs         chan error
}

func (f *fakeRecordedConnectGeminiClient) Connect(ctx context.Context) error {
	f.connectCtx = ctx
	f.connectCount++
	return nil
}

func (f *fakeRecordedConnectGeminiClient) Send(ctx context.Context, prompt string) error {
	_ = ctx
	f.turnCount++
	f.blocks = make(chan gemini.StreamBlock, 2)
	f.errs = make(chan error, 1)
	f.blocks <- gemini.StreamBlock{
		Kind:      gemini.BlockKindText,
		RawType:   "agent_message_chunk",
		SessionID: "gemini-sess-1",
		Text:      "reply:" + prompt,
	}
	f.blocks <- gemini.StreamBlock{
		Kind:      gemini.BlockKindDone,
		RawType:   "completed",
		SessionID: "gemini-sess-1",
		Done:      true,
	}
	close(f.blocks)
	close(f.errs)
	return nil
}

func (f *fakeRecordedConnectGeminiClient) ReceiveMessagesWithErrors() (<-chan gemini.StreamBlock, <-chan error) {
	return f.blocks, f.errs
}

func (f *fakeRecordedConnectGeminiClient) Interrupt(ctx context.Context) error {
	_ = ctx
	return nil
}

func (f *fakeRecordedConnectGeminiClient) Close() error { return nil }

func (f *fakeRecordedConnectGeminiClient) SessionID() string { return "gemini-sess-1" }

func (f *fakeRecordedConnectGeminiClient) Err() error { return nil }

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
